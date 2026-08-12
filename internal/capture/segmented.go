package capture

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	segmentHeaderSize = 25
	segmentMarkerSize = 4
	segmentCommit     = uint32(0x434d4954) // "CMIT"
)

var (
	ErrCaptureBackpressure = errors.New("capture writer queue is full")
	ErrInsufficientDisk    = errors.New("capture minimum free disk threshold reached")
	ErrCloseDrainTimeout   = errors.New("capture writer close drain budget exceeded")
	ErrCorruptRecord       = errors.New("capture record failed integrity validation")

	diskFreeBytes   = platformDiskFreeBytes
	segmentWrite    = writeAll
	segmentMkdir    = os.MkdirAll
	segmentStat     = os.Stat
	segmentGlob     = filepath.Glob
	segmentCreate   = os.CreateTemp
	segmentRename   = os.Rename
	segmentRemove   = os.Remove
	segmentOpenLock = func(path string) (*os.File, error) {
		return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
)

// SegmentOptions controls the bounded, crash-detectable capture writer.
type SegmentOptions struct {
	SegmentMaxBytes uint64
	RetainSegments  int
	MinFreeBytes    uint64
	QueueDepth      int
	CloseDrain      time.Duration
}

func (o SegmentOptions) normalized() SegmentOptions {
	if o.SegmentMaxBytes == 0 {
		o.SegmentMaxBytes = 64 << 20
	}
	if o.RetainSegments <= 0 {
		o.RetainSegments = 4
	}
	if o.QueueDepth <= 0 {
		o.QueueDepth = 128
	}
	if o.CloseDrain <= 0 {
		o.CloseDrain = 5 * time.Second
	}
	return o
}

type segmentRequest struct {
	payload   []byte
	direction Direction
	timestamp time.Time
}

// SegmentedRecorder accepts owned copies of frames and serializes them on a
// bounded background writer. Segment files are published only after sync.
type SegmentedRecorder struct {
	path      string
	opts      SegmentOptions
	queue     chan segmentRequest
	done      chan struct{}
	closing   atomic.Bool
	once      sync.Once
	queueMu   sync.RWMutex
	mu        sync.Mutex
	err       error
	index     uint64
	file      *os.File
	writer    *bufio.Writer
	temp      string
	size      uint64
	nextFrame uint64
	lockPath  string
	lockFile  *os.File
}

func StartSegmented(path string, options SegmentOptions) (*SegmentedRecorder, error) {
	opts := options.normalized()
	if opts.SegmentMaxBytes < segmentHeaderSize+segmentMarkerSize {
		return nil, fmt.Errorf("segment size %d is too small", opts.SegmentMaxBytes)
	}
	directory := filepath.Dir(path)
	info, err := segmentStat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("capture parent path is not a directory: %s", directory)
	}
	if err := segmentMkdir(directory, 0o700); err != nil {
		return nil, err
	}
	lockPath := path + ".lock"
	lockFile, err := segmentOpenLock(lockPath)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrCaptureInUse
		}
		return nil, err
	}
	releaseLock := func() {
		_ = lockFile.Close()
		_ = os.Remove(lockPath)
	}
	if _, err := diskFreeBytes(directory); err != nil {
		releaseLock()
		return nil, err
	}
	index, err := latestSegmentIndex(path)
	if err != nil {
		releaseLock()
		return nil, err
	}
	r := &SegmentedRecorder{path: path, opts: opts, queue: make(chan segmentRequest, opts.QueueDepth), done: make(chan struct{}), index: index, lockPath: lockPath, lockFile: lockFile}
	go r.run()
	return r, nil
}

func (r *SegmentedRecorder) Write(frame []byte) error {
	return r.WriteFrame(DirectionUnknown, time.Now().UTC(), frame)
}

func (r *SegmentedRecorder) WriteFrame(direction Direction, timestamp time.Time, frame []byte) error {
	if len(frame) > MaxFrameSize {
		return fmt.Errorf("%w: capture frame length %d exceeds limit %d", ErrFrameTooLarge, len(frame), MaxFrameSize)
	}
	r.queueMu.RLock()
	defer r.queueMu.RUnlock()
	if r.closing.Load() {
		return ErrRecorderClosed
	}
	if err := r.failure(); err != nil {
		return errors.Join(ErrRecorderFailed, err)
	}
	if direction == "" {
		direction = DirectionUnknown
	}
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	request := segmentRequest{payload: append([]byte(nil), frame...), direction: direction, timestamp: timestamp.UTC()}
	select {
	case r.queue <- request:
		return nil
	default:
		return ErrCaptureBackpressure
	}
}

func (r *SegmentedRecorder) Close() error {
	r.once.Do(func() {
		r.queueMu.Lock()
		r.closing.Store(true)
		close(r.queue)
		r.queueMu.Unlock()
	})
	timer := time.NewTimer(r.opts.CloseDrain)
	defer timer.Stop()
	select {
	case <-r.done:
		return r.failure()
	case <-timer.C:
		return ErrCloseDrainTimeout
	}
}

func (r *SegmentedRecorder) failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *SegmentedRecorder) setFailure(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	if r.err == nil {
		r.err = err
	}
	r.mu.Unlock()
}

func (r *SegmentedRecorder) run() {
	defer func() {
		if r.lockFile != nil {
			if err := r.lockFile.Close(); err != nil {
				r.setFailure(err)
			}
			_ = os.Remove(r.lockPath)
		}
		close(r.done)
	}()
	for request := range r.queue {
		if r.failure() != nil {
			continue
		}
		if err := r.writeRequest(request); err != nil {
			r.setFailure(err)
		}
	}
	if r.failure() != nil {
		r.abortSegment()
	} else if err := r.finishSegment(); err != nil {
		r.setFailure(err)
	}
}

func (r *SegmentedRecorder) abortSegment() {
	if r.file == nil {
		return
	}
	_ = r.file.Close()
	_ = segmentRemove(r.temp)
	r.file, r.writer, r.temp, r.size = nil, nil, "", 0
}

func (r *SegmentedRecorder) writeRecord(payload []byte) error {
	return r.writeRequest(segmentRequest{payload: payload, direction: DirectionUnknown, timestamp: time.Now().UTC()})
}

func (r *SegmentedRecorder) writeRequest(request segmentRequest) error {
	payload := request.payload
	recordSize := uint64(segmentHeaderSize + len(payload) + segmentMarkerSize)
	if recordSize > r.opts.SegmentMaxBytes {
		return fmt.Errorf("%w: encoded frame requires %d bytes", ErrFrameTooLarge, recordSize)
	}
	if r.file != nil && r.size+recordSize > r.opts.SegmentMaxBytes {
		if err := r.finishSegment(); err != nil {
			return err
		}
	}
	if r.file == nil {
		if err := r.openSegment(); err != nil {
			return err
		}
	}
	free, err := diskFreeBytes(filepath.Dir(r.path))
	if err != nil {
		return err
	}
	if free < r.opts.MinFreeBytes || recordSize > free-r.opts.MinFreeBytes {
		return fmt.Errorf("%w: free=%d required=%d record=%d", ErrInsufficientDisk, free, r.opts.MinFreeBytes, recordSize)
	}
	var header [segmentHeaderSize]byte
	binary.BigEndian.PutUint32(header[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(header[4:8], crc32.ChecksumIEEE(payload))
	binary.BigEndian.PutUint64(header[8:16], r.nextFrame)
	binary.BigEndian.PutUint64(header[16:24], uint64(request.timestamp.UnixNano()))
	header[24] = encodeDirection(request.direction)
	var marker [segmentMarkerSize]byte
	binary.BigEndian.PutUint32(marker[:], segmentCommit)
	for _, part := range [][]byte{header[:], payload, marker[:]} {
		if err := segmentWrite(r.writer, part); err != nil {
			return err
		}
	}
	if err := r.writer.Flush(); err != nil {
		return err
	}
	if err := syncRecorderFile(r.file); err != nil {
		return err
	}
	r.size += recordSize
	r.nextFrame++
	return nil
}

func encodeDirection(direction Direction) byte {
	switch direction {
	case DirectionUpstream:
		return 1
	case DirectionDownstream:
		return 2
	default:
		return 0
	}
}

func decodeDirection(value byte) (Direction, error) {
	switch value {
	case 0:
		return DirectionUnknown, nil
	case 1:
		return DirectionUpstream, nil
	case 2:
		return DirectionDownstream, nil
	default:
		return "", ErrCorruptRecord
	}
}

func (r *SegmentedRecorder) openSegment() error {
	r.index++
	file, err := segmentCreate(filepath.Dir(r.path), "."+filepath.Base(r.path)+".segment-*")
	if err != nil {
		return err
	}
	r.file, r.writer, r.temp, r.size = file, bufio.NewWriter(file), file.Name(), 0
	return nil
}

func (r *SegmentedRecorder) finishSegment() error {
	if r.file == nil {
		return nil
	}
	file, writer, temp := r.file, r.writer, r.temp
	r.file, r.writer, r.temp, r.size = nil, nil, "", 0
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		_ = segmentRemove(temp)
		return err
	}
	if err := syncRecorderFile(file); err != nil {
		_ = file.Close()
		_ = segmentRemove(temp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = segmentRemove(temp)
		return err
	}
	final := fmt.Sprintf("%s.%06d", r.path, r.index)
	if err := segmentRename(temp, final); err != nil {
		_ = segmentRemove(temp)
		return err
	}
	return retainSegments(r.path, r.opts.RetainSegments)
}

func retainSegments(path string, retain int) error {
	matches, err := segmentGlob(path + ".[0-9][0-9][0-9][0-9][0-9][0-9]")
	if err != nil {
		return err
	}
	sort.Strings(matches)
	for len(matches) > retain {
		if err := segmentRemove(matches[0]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		matches = matches[1:]
	}
	return nil
}

func latestSegmentIndex(path string) (uint64, error) {
	matches, err := segmentGlob(path + ".[0-9][0-9][0-9][0-9][0-9][0-9]")
	if err != nil {
		return 0, err
	}
	var latest uint64
	for _, match := range matches {
		suffix := strings.TrimPrefix(match, path+".")
		index, err := strconv.ParseUint(suffix, 10, 64)
		if err != nil {
			return 0, err
		}
		if index > latest {
			latest = index
		}
	}
	return latest, nil
}

// ReplaySegments validates every record before submitting it.
func ReplaySegments(path string, submit func([]byte) error) error {
	if submit == nil {
		return errors.New("capture replay submit callback is nil")
	}
	segments, err := segmentFiles(path)
	if err != nil {
		return err
	}
	for _, segment := range segments {
		if err := replaySegment(segment, submit); err != nil {
			return err
		}
	}
	return nil
}

func segmentFiles(path string) ([]string, error) {
	segments, err := segmentGlob(path + ".[0-9][0-9][0-9][0-9][0-9][0-9]")
	if err != nil {
		return nil, err
	}
	sort.Strings(segments)
	return segments, nil
}

func segmentedCaptureExists(path string) (bool, error) {
	segments, err := segmentFiles(path)
	return len(segments) > 0, err
}

func replaySegmentsContext(ctx context.Context, path string, submit func([]byte, FrameMetadata) error) error {
	segments, err := segmentFiles(path)
	if err != nil {
		return err
	}
	for _, segment := range segments {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := replaySegmentRecords(segment, func(frame []byte, metadata FrameMetadata) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return submit(frame, metadata)
		}); err != nil {
			return err
		}
	}
	return nil
}

func replaySegmentMetadata(path string) ([]FrameMetadata, error) {
	var metadata []FrameMetadata
	err := replaySegmentsContext(context.Background(), path, func(_ []byte, item FrameMetadata) error {
		metadata = append(metadata, item)
		return nil
	})
	return metadata, err
}

func snapshotSegmentsContext(ctx context.Context, path string, snapshot io.Writer) error {
	var frames, bytes uint64
	return replaySegmentsContext(ctx, path, func(frame []byte, _ FrameMetadata) error {
		frames++
		bytes += uint64(len(frame))
		return writeSegmentSnapshotRecord(snapshot, frames, bytes, frame)
	})
}

func writeSegmentSnapshotRecord(snapshot io.Writer, frames, bytes uint64, frame []byte) error {
	if err := segmentSnapshotBudgetError(frames, bytes); err != nil {
		return err
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
	if err := writeAll(snapshot, header[:]); err != nil {
		return err
	}
	return writeAll(snapshot, frame)
}

func segmentSnapshotBudgetError(frames, bytes uint64) error {
	if frames > MaxCaptureFrames {
		return fmt.Errorf("%w: capture has more than %d frames", ErrTooManyFrames, MaxCaptureFrames)
	}
	if bytes > MaxCaptureSize {
		return fmt.Errorf("%w: capture payload exceeds %d bytes", ErrCaptureTooLarge, MaxCaptureSize)
	}
	return nil
}

func replaySegment(path string, submit func([]byte) error) error {
	return replaySegmentRecords(path, func(frame []byte, _ FrameMetadata) error { return submit(frame) })
}

func replaySegmentRecords(path string, submit func([]byte, FrameMetadata) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		var header [segmentHeaderSize]byte
		if _, err := io.ReadFull(f, header[:]); err == io.EOF {
			return nil
		} else if err != nil {
			return errors.Join(ErrCorruptRecord, err)
		}
		n := binary.BigEndian.Uint32(header[0:4])
		if n > MaxFrameSize {
			return errors.Join(ErrCorruptRecord, frameSizeError(n))
		}
		payload := make([]byte, n)
		var marker [segmentMarkerSize]byte
		if _, err := io.ReadFull(f, payload); err != nil {
			return errors.Join(ErrCorruptRecord, err)
		}
		if _, err := io.ReadFull(f, marker[:]); err != nil {
			return errors.Join(ErrCorruptRecord, err)
		}
		if crc32.ChecksumIEEE(payload) != binary.BigEndian.Uint32(header[4:8]) || binary.BigEndian.Uint32(marker[:]) != segmentCommit {
			return ErrCorruptRecord
		}
		direction, err := decodeDirection(header[24])
		if err != nil {
			return err
		}
		metadata := FrameMetadata{Index: binary.BigEndian.Uint64(header[8:16]), Direction: direction, Timestamp: time.Unix(0, int64(binary.BigEndian.Uint64(header[16:24]))).UTC(), Size: n}
		if err := submit(payload, metadata); err != nil {
			return err
		}
	}
}
