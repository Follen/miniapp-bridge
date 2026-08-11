package capture

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	MaxFrameSize     = 64 << 20
	MaxCaptureSize   = 256 << 20
	MaxCaptureFrames = 1 << 20
)

var (
	ErrRecorderClosed  = errors.New("capture recorder is closed")
	ErrFrameTooLarge   = errors.New("capture frame exceeds size limit")
	ErrCaptureTooLarge = errors.New("capture exceeds aggregate size limit")
	ErrTooManyFrames   = errors.New("capture exceeds frame count limit")
	ErrRecorderFailed  = errors.New("capture recorder entered failed state")
	ErrCaptureInUse    = errors.New("capture path is already owned by another recorder")
)

var createSnapshotFile = func() (*os.File, error) {
	return os.CreateTemp("", "miniapp-bridge-capture-*.snapshot")
}

var createRecorderTemp = os.CreateTemp
var renameRecorderPath = os.Rename

// syncRecorderFile is kept injectable so durability failures can be tested
// without changing the on-disk capture format. A successful WriteFrame has
// reached the filesystem, rather than only a userspace bufio buffer.
var syncRecorderFile = func(f *os.File) error {
	if f == nil {
		return nil
	}
	return f.Sync()
}

type Direction string

const (
	DirectionUnknown    Direction = "unknown"
	DirectionUpstream   Direction = "upstream"
	DirectionDownstream Direction = "downstream"
)

type FrameMetadata struct {
	Index     uint64    `json:"index"`
	Direction Direction `json:"direction"`
	Timestamp time.Time `json:"timestamp"`
	Size      uint32    `json:"size"`
}

type FrameRecorder interface {
	WriteFrame(Direction, time.Time, []byte) error
	Close() error
}

func MetadataPath(path string) string { return path + ".meta.jsonl" }

type Recorder struct {
	mu             sync.Mutex
	f              *os.File
	w              *bufio.Writer
	metadataFile   *os.File
	metadataWriter *bufio.Writer
	nextIndex      uint64
	path           string
	tempPath       string
	metadataPath   string
	metadataTemp   string
	lockPath       string
	lockFile       *os.File
	bytesWritten   uint64
	failed         bool
	failure        error
}

func Start(path string) (*Recorder, error) {
	metadataPath := MetadataPath(path)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, fmt.Errorf("capture path is a directory: %s", path)
	}
	if info, err := os.Stat(metadataPath); err == nil && info.IsDir() {
		return nil, fmt.Errorf("capture metadata path is a directory: %s", metadataPath)
	}
	lockPath := path + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrCaptureInUse
		}
		return nil, err
	}
	dir, base := filepath.Dir(path), filepath.Base(path)
	f, err := createRecorderTemp(dir, "."+base+".capture-*")
	if err != nil {
		_ = lockFile.Close()
		_ = os.Remove(lockPath)
		return nil, err
	}
	metadataFile, err := createRecorderTemp(dir, "."+base+".meta-*")
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		_ = lockFile.Close()
		_ = os.Remove(lockPath)
		return nil, err
	}
	return &Recorder{
		f:              f,
		w:              bufio.NewWriter(f),
		metadataFile:   metadataFile,
		metadataWriter: bufio.NewWriter(metadataFile),
		path:           path,
		tempPath:       f.Name(),
		metadataPath:   metadataPath,
		metadataTemp:   metadataFile.Name(),
		lockPath:       lockPath,
		lockFile:       lockFile,
	}, nil
}
func (r *Recorder) Write(frame []byte) error {
	return r.WriteFrame(DirectionUnknown, time.Now().UTC(), frame)
}
func (r *Recorder) WriteFrame(direction Direction, timestamp time.Time, frame []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.w == nil {
		return ErrRecorderClosed
	}
	if r.failed {
		return errors.Join(ErrRecorderFailed, r.failure)
	}
	if len(frame) > MaxFrameSize {
		return fmt.Errorf("%w: capture frame length %d exceeds limit %d", ErrFrameTooLarge, len(frame), MaxFrameSize)
	}
	if r.bytesWritten+uint64(len(frame)) > MaxCaptureSize {
		return r.failLocked(fmt.Errorf("%w: capture payload exceeds %d bytes", ErrCaptureTooLarge, MaxCaptureSize))
	}
	if r.nextIndex >= MaxCaptureFrames {
		return r.failLocked(fmt.Errorf("%w: capture has more than %d frames", ErrTooManyFrames, MaxCaptureFrames))
	}
	if direction == "" {
		direction = DirectionUnknown
	}
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(frame)))
	if _, e := r.w.Write(n[:]); e != nil {
		return r.failLocked(e)
	}
	if _, e := r.w.Write(frame); e != nil {
		return r.failLocked(e)
	}
	if e := r.w.Flush(); e != nil {
		return r.failLocked(e)
	}
	if e := syncRecorderFile(r.f); e != nil {
		return r.failLocked(e)
	}
	metadata := FrameMetadata{Index: r.nextIndex, Direction: direction, Timestamp: timestamp.UTC(), Size: uint32(len(frame))}
	r.nextIndex++
	if r.metadataWriter != nil {
		if e := json.NewEncoder(r.metadataWriter).Encode(metadata); e != nil {
			return r.failLocked(e)
		}
		if e := r.metadataWriter.Flush(); e != nil {
			return r.failLocked(e)
		}
		if e := syncRecorderFile(r.metadataFile); e != nil {
			return r.failLocked(e)
		}
	}
	r.bytesWritten += uint64(len(frame))
	return nil
}

func (r *Recorder) failLocked(err error) error {
	if err == nil {
		return nil
	}
	r.failed = true
	if r.failure == nil {
		r.failure = err
	}
	return errors.Join(ErrRecorderFailed, r.failure)
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil && r.w == nil && r.metadataFile == nil && r.metadataWriter == nil {
		return nil
	}
	var out error
	if r.w != nil {
		if err := r.w.Flush(); err != nil {
			out = joinCaptureErrors(out, err)
		}
	}
	if r.f != nil {
		if !r.failed {
			if err := r.f.Sync(); err != nil {
				out = joinCaptureErrors(out, err)
			}
		}
		if err := r.f.Close(); err != nil {
			out = joinCaptureErrors(out, err)
		}
	}
	if r.metadataWriter != nil {
		if err := r.metadataWriter.Flush(); err != nil {
			out = joinCaptureErrors(out, err)
		}
	}
	if r.metadataFile != nil {
		if !r.failed {
			if err := r.metadataFile.Sync(); err != nil {
				out = joinCaptureErrors(out, err)
			}
		}
		if err := r.metadataFile.Close(); err != nil {
			out = joinCaptureErrors(out, err)
		}
	}
	if r.failure != nil {
		out = joinCaptureErrors(out, r.failure)
	}
	if r.path != "" {
		if r.failed || out != nil {
			_ = os.Remove(r.tempPath)
			_ = os.Remove(r.metadataTemp)
		} else {
			out = joinCaptureErrors(out, publishGeneration(r.tempPath, r.path, r.metadataTemp, r.metadataPath))
		}
	}
	if r.lockFile != nil {
		out = joinCaptureErrors(out, r.lockFile.Close())
		_ = os.Remove(r.lockPath)
	}
	r.w = nil
	r.f = nil
	r.metadataWriter = nil
	r.metadataFile = nil
	return out
}

func joinCaptureErrors(first, second error) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return errors.Join(first, second)
}

func publishGeneration(tempPath, path, metadataTemp, metadataPath string) error {
	backup := path + ".previous"
	metadataBackup := metadataPath + ".previous"
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(backup)
		if err := renameRecorderPath(path, backup); err != nil {
			return err
		}
	}
	if _, err := os.Stat(metadataPath); err == nil {
		_ = os.Remove(metadataBackup)
		if err := renameRecorderPath(metadataPath, metadataBackup); err != nil {
			_ = renameRecorderPath(backup, path)
			return err
		}
	}
	if err := renameRecorderPath(tempPath, path); err != nil {
		_ = renameRecorderPath(backup, path)
		_ = renameRecorderPath(metadataBackup, metadataPath)
		return err
	}
	if err := renameRecorderPath(metadataTemp, metadataPath); err != nil {
		_ = os.Remove(path)
		_ = renameRecorderPath(backup, path)
		_ = renameRecorderPath(metadataBackup, metadataPath)
		return err
	}
	_ = os.Remove(backup)
	_ = os.Remove(metadataBackup)
	return nil
}

func ReplayMetadata(path string) ([]FrameMetadata, error) {
	f, err := os.Open(MetadataPath(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			exists, segmentErr := segmentedCaptureExists(path)
			if segmentErr != nil {
				return nil, segmentErr
			}
			if exists {
				return replaySegmentMetadata(path)
			}
		}
		return nil, err
	}
	defer f.Close()
	var out []FrameMetadata
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4<<10), 1<<20)
	for scanner.Scan() {
		if len(out) >= MaxCaptureFrames {
			return nil, fmt.Errorf("%w: metadata has more than %d frames", ErrTooManyFrames, MaxCaptureFrames)
		}
		var metadata FrameMetadata
		if err := json.Unmarshal(scanner.Bytes(), &metadata); err != nil {
			return nil, err
		}
		out = append(out, metadata)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func Replay(path string) ([][]byte, error) {
	return ReplayContext(context.Background(), path)
}

// ReplayContext validates the complete capture before allocating or returning
// frames. The aggregate limits bound the memory retained by the legacy Replay
// API; callers that need to process larger captures should use ReplayEachContext.
func ReplayContext(ctx context.Context, path string) ([][]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	f, e := os.Open(path)
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			exists, segmentErr := segmentedCaptureExists(path)
			if segmentErr != nil {
				return nil, segmentErr
			}
			if exists {
				snapshot, createErr := createSnapshotFile()
				if createErr != nil {
					return nil, createErr
				}
				snapshotPath := snapshot.Name()
				defer func() {
					_ = snapshot.Close()
					_ = os.Remove(snapshotPath)
				}()
				if segmentErr := snapshotSegmentsContext(ctx, path, snapshot); segmentErr != nil {
					return nil, segmentErr
				}
				return replayFramesContext(ctx, snapshot)
			}
		}
		return nil, e
	}
	defer f.Close()

	return replayFramesContext(ctx, f)
}

func replayFramesContext(ctx context.Context, f io.ReadSeeker) ([][]byte, error) {
	frameCount, _, err := validateReader(ctx, f, MaxCaptureSize, MaxCaptureFrames, nil)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	out := make([][]byte, 0, frameCount)
	var totalBytes uint64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := readHeader(f)
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if n > MaxFrameSize {
			return nil, frameSizeError(n)
		}
		if uint64(len(out)) >= MaxCaptureFrames {
			return nil, fmt.Errorf("%w: capture has more than %d frames", ErrTooManyFrames, MaxCaptureFrames)
		}
		totalBytes += uint64(n)
		if totalBytes > MaxCaptureSize {
			return nil, fmt.Errorf("%w: capture payload exceeds %d bytes", ErrCaptureTooLarge, MaxCaptureSize)
		}
		b := make([]byte, n)
		if err := readFullContext(ctx, f, b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// Validate checks a capture without retaining its frames in memory.
func Validate(path string) error {
	return ValidateContext(context.Background(), path)
}

// ValidateContext checks a capture without retaining its frames in memory.
func ValidateContext(ctx context.Context, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			exists, segmentErr := segmentedCaptureExists(path)
			if segmentErr != nil {
				return segmentErr
			}
			if exists {
				return replaySegmentsContext(ctx, path, func([]byte, FrameMetadata) error { return nil })
			}
		}
		return err
	}
	defer f.Close()
	_, _, err = validateReader(ctx, f, 0, 0, nil)
	return err
}

// ReplayEachContext snapshots and validates the entire source before invoking
// submit. This guarantees that a corrupt source never results in partial frame
// submission while keeping memory use bounded to one frame.
func ReplayEachContext(ctx context.Context, path string, submit func([]byte) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if submit == nil {
		return errors.New("capture replay submit callback is nil")
	}
	source, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			exists, segmentErr := segmentedCaptureExists(path)
			if segmentErr != nil {
				return segmentErr
			}
			if exists {
				snapshot, createErr := createSnapshotFile()
				if createErr != nil {
					return createErr
				}
				snapshotPath := snapshot.Name()
				defer func() {
					_ = snapshot.Close()
					_ = os.Remove(snapshotPath)
				}()
				if segmentErr := snapshotSegmentsContext(ctx, path, snapshot); segmentErr != nil {
					return segmentErr
				}
				return replaySnapshotContext(ctx, snapshot, submit)
			}
		}
		return err
	}
	defer source.Close()

	snapshot, err := createSnapshotFile()
	if err != nil {
		return err
	}
	snapshotPath := snapshot.Name()
	defer func() {
		_ = snapshot.Close()
		_ = os.Remove(snapshotPath)
	}()
	// The snapshot is the immutable replay source, so validation and replay
	// must use the same aggregate limits as the regular Replay API.
	if _, _, err := validateReader(ctx, source, MaxCaptureSize, MaxCaptureFrames, snapshot); err != nil {
		return err
	}
	return replaySnapshotContext(ctx, snapshot, submit)
}

func replaySnapshotContext(ctx context.Context, snapshot io.ReadSeeker, submit func([]byte) error) error {
	if _, err := snapshot.Seek(0, io.SeekStart); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := readHeader(snapshot)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		frame := make([]byte, n)
		if err := readFullContext(ctx, snapshot, frame); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := submit(frame); err != nil {
			return err
		}
	}
}

func frameSizeError(n uint32) error {
	return fmt.Errorf("%w: capture frame length %d exceeds limit %d", ErrFrameTooLarge, n, MaxFrameSize)
}

func readHeader(r io.Reader) (uint32, error) {
	var header [4]byte
	_, err := io.ReadFull(r, header[:])
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(header[:]), nil
}

func validateReader(ctx context.Context, r io.Reader, maxBytes uint64, maxFrames uint64, snapshot io.Writer) (uint64, uint64, error) {
	var frameCount uint64
	var totalBytes uint64
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		var header [4]byte
		_, err := io.ReadFull(r, header[:])
		if err == io.EOF {
			return frameCount, totalBytes, nil
		}
		if err != nil {
			return 0, 0, err
		}
		n := binary.BigEndian.Uint32(header[:])
		if n > MaxFrameSize {
			return 0, 0, frameSizeError(n)
		}
		frameCount++
		if maxFrames != 0 && frameCount > maxFrames {
			return 0, 0, fmt.Errorf("%w: capture has more than %d frames", ErrTooManyFrames, maxFrames)
		}
		totalBytes += uint64(n)
		if maxBytes != 0 && totalBytes > maxBytes {
			return 0, 0, fmt.Errorf("%w: capture payload exceeds %d bytes", ErrCaptureTooLarge, maxBytes)
		}
		if snapshot != nil {
			if err := writeAll(snapshot, header[:]); err != nil {
				return 0, 0, err
			}
		}
		remaining := uint64(n)
		for remaining > 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
			chunk := uint64(len(buffer))
			if remaining < chunk {
				chunk = remaining
			}
			part := buffer[:int(chunk)]
			if _, err := io.ReadFull(r, part); err != nil {
				return 0, 0, err
			}
			if snapshot != nil {
				if err := writeAll(snapshot, part); err != nil {
					return 0, 0, err
				}
			}
			remaining -= chunk
		}
	}
}

func readFullContext(ctx context.Context, r io.Reader, data []byte) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := data
		if len(chunk) > 32<<10 {
			chunk = chunk[:32<<10]
		}
		n, err := io.ReadFull(r, chunk)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
