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
)

var createSnapshotFile = func() (*os.File, error) {
	return os.CreateTemp("", "miniapp-bridge-capture-*.snapshot")
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

func MetadataPath(path string) string { return path + ".meta.jsonl" }

type Recorder struct {
	mu             sync.Mutex
	f              *os.File
	w              *bufio.Writer
	metadataFile   *os.File
	metadataWriter *bufio.Writer
	nextIndex      uint64
}

func Start(path string) (*Recorder, error) {
	f, e := os.Create(path)
	if e != nil {
		return nil, e
	}
	metadataFile, e := os.Create(MetadataPath(path))
	if e != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, e
	}
	return &Recorder{
		f:              f,
		w:              bufio.NewWriter(f),
		metadataFile:   metadataFile,
		metadataWriter: bufio.NewWriter(metadataFile),
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
	if len(frame) > MaxFrameSize {
		return fmt.Errorf("%w: capture frame length %d exceeds limit %d", ErrFrameTooLarge, len(frame), MaxFrameSize)
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
		return e
	}
	if _, e := r.w.Write(frame); e != nil {
		return e
	}
	if e := r.w.Flush(); e != nil {
		return e
	}
	metadata := FrameMetadata{Index: r.nextIndex, Direction: direction, Timestamp: timestamp.UTC(), Size: uint32(len(frame))}
	r.nextIndex++
	if r.metadataWriter != nil {
		if e := json.NewEncoder(r.metadataWriter).Encode(metadata); e != nil {
			return e
		}
		if e := r.metadataWriter.Flush(); e != nil {
			return e
		}
	}
	return nil
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
			out = err
		}
	}
	if r.f != nil {
		if err := r.f.Close(); err != nil && out == nil {
			out = err
		}
	}
	if r.metadataWriter != nil {
		if err := r.metadataWriter.Flush(); err != nil && out == nil {
			out = err
		}
	}
	if r.metadataFile != nil {
		if err := r.metadataFile.Close(); err != nil && out == nil {
			out = err
		}
	}
	r.w = nil
	r.f = nil
	r.metadataWriter = nil
	r.metadataFile = nil
	return out
}

func ReplayMetadata(path string) ([]FrameMetadata, error) {
	f, err := os.Open(MetadataPath(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	var out []FrameMetadata
	for {
		var metadata FrameMetadata
		if err := decoder.Decode(&metadata); err == io.EOF {
			return out, nil
		} else if err != nil {
			return nil, err
		}
		out = append(out, metadata)
	}
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
	if _, _, err := validateReader(ctx, source, 0, 0, snapshot); err != nil {
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
