package capture

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type replaySeeker struct {
	data       []byte
	pos        int
	pass       int
	seekErr    error
	secondData []byte
}

func (r *replaySeeker) Read(p []byte) (int, error) {
	data := r.data
	if r.pass > 0 && r.secondData != nil {
		data = r.secondData
	}
	if r.pos >= len(data) {
		return 0, io.EOF
	}
	n := copy(p, data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *replaySeeker) Seek(offset int64, whence int) (int64, error) {
	if r.seekErr != nil {
		return 0, r.seekErr
	}
	if whence != io.SeekStart || offset != 0 {
		return 0, errors.New("unsupported seek")
	}
	r.pass++
	r.pos = 0
	return 0, nil
}

type shortWriter struct{ mode string }

func (w shortWriter) Write(p []byte) (int, error) {
	switch w.mode {
	case "error":
		return 0, io.ErrClosedPipe
	case "zero":
		return 0, nil
	case "short":
		if len(p) > 1 {
			return 1, nil
		}
	}
	return len(p), nil
}

type cancelOnRead struct {
	bytes.Reader
	cancel context.CancelFunc
}

type aggregateSeeker struct {
	pass  int
	frame int
	off   uint64
}

func (r *aggregateSeeker) Read(p []byte) (int, error) {
	original := len(p)
	if r.pass == 0 || r.frame >= 5 {
		return 0, io.EOF
	}
	for len(p) > 0 && r.frame < 5 {
		frameSize := uint64(4) + uint64(MaxFrameSize)
		if r.off < 4 {
			var h [4]byte
			binary.BigEndian.PutUint32(h[:], MaxFrameSize)
			n := copy(p, h[r.off:])
			r.off += uint64(n)
			p = p[n:]
			continue
		}
		remaining := frameSize - r.off
		n := len(p)
		if uint64(n) > remaining {
			n = int(remaining)
		}
		for i := 0; i < n; i++ {
			p[i] = 0
		}
		r.off += uint64(n)
		p = p[n:]
		if r.off == frameSize {
			r.frame++
			r.off = 0
		}
	}
	return original - len(p), nil
}

func (r *aggregateSeeker) Seek(offset int64, whence int) (int64, error) {
	if whence != io.SeekStart || offset != 0 {
		return 0, errors.New("unsupported seek")
	}
	r.pass++
	r.frame = 0
	r.off = 0
	return 0, nil
}

func (r *cancelOnRead) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

type cancelAfterReader struct {
	bytes.Reader
	cancel context.CancelFunc
	limit  int
}

func (r *cancelAfterReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if r.Size()-int64(r.Len()) >= int64(r.limit) {
		r.cancel()
	}
	return n, err
}

type cancelOnSeek struct {
	*replaySeeker
	cancel context.CancelFunc
}

func (r *cancelOnSeek) Seek(offset int64, whence int) (int64, error) {
	n, err := r.replaySeeker.Seek(offset, whence)
	if err == nil && r.pass > 0 {
		r.cancel()
	}
	return n, err
}

func TestCoverageCapturePublicValidationAndNilContexts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "valid.capture")
	if err := os.WriteFile(path, encodedFrames([]byte("ok")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Validate(path); err != nil {
		t.Fatal(err)
	}
	if err := ValidateContext(nil, path); err != nil {
		t.Fatal(err)
	}
	if frames, err := ReplayContext(nil, path); err != nil || len(frames) != 1 {
		t.Fatalf("nil-context replay frames=%v err=%v", frames, err)
	}
	if err := ValidateContext(nil, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing validation succeeded")
	}
	if err := ReplayEachContext(nil, path, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := ReplayEachContext(context.Background(), filepath.Join(t.TempDir(), "missing.capture"), func([]byte) error { return nil }); err == nil {
		t.Fatal("missing replay source succeeded")
	}
}

func TestCoverageReplayFramesDefensiveBranches(t *testing.T) {
	seekErr := errors.New("seek failed")
	valid := encodedFrames([]byte("ok"))
	if _, err := replayFramesContext(context.Background(), &replaySeeker{data: valid, seekErr: seekErr}); !errors.Is(err, seekErr) {
		t.Fatalf("seek error=%v", err)
	}
	truncatedHeader := &replaySeeker{data: valid, secondData: []byte{0, 0}}
	if _, err := replayFramesContext(context.Background(), truncatedHeader); err == nil {
		t.Fatal("second-pass header error missing")
	}
	truncatedBody := &replaySeeker{data: valid, secondData: encodedFrames([]byte{1, 2})[:5]}
	if _, err := replayFramesContext(context.Background(), truncatedBody); err == nil {
		t.Fatal("second-pass body error missing")
	}
	var oversized [4]byte
	binary.BigEndian.PutUint32(oversized[:], MaxFrameSize+1)
	if _, err := replayFramesContext(context.Background(), &replaySeeker{data: nil, secondData: oversized[:]}); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("second-pass oversized error=%v", err)
	}
	var tooMany bytes.Buffer
	for i := uint64(0); i < MaxCaptureFrames; i++ {
		_, _ = tooMany.Write(encodedFrames(nil))
	}
	_, _ = tooMany.Write(encodedFrames(nil))
	if _, err := replayFramesContext(context.Background(), &replaySeeker{data: nil, secondData: tooMany.Bytes()}); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("second-pass frame limit error=%v", err)
	}
	if _, err := replayFramesContext(context.Background(), &aggregateSeeker{}); !errors.Is(err, ErrCaptureTooLarge) {
		t.Fatalf("second-pass byte limit error=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := replayFramesContext(ctx, &cancelOnSeek{replaySeeker: &replaySeeker{data: valid}, cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-loop cancellation error=%v", err)
	}
}

func TestCoverageReplaySnapshotBranches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "valid.capture")
	if err := os.WriteFile(path, encodedFrames([]byte("ok")), 0o600); err != nil {
		t.Fatal(err)
	}
	old := createSnapshotFile
	createSnapshotFile = func() (*os.File, error) { return nil, errors.New("snapshot create failed") }
	if err := ReplayEachContext(context.Background(), path, func([]byte) error { return nil }); err == nil {
		t.Fatal("snapshot creation succeeded")
	}
	createSnapshotFile = old
	seekErr := errors.New("snapshot seek failed")
	if err := replaySnapshotContext(context.Background(), &replaySeeker{data: encodedFrames([]byte("ok")), seekErr: seekErr}, func([]byte) error { return nil }); !errors.Is(err, seekErr) {
		t.Fatalf("snapshot seek error=%v", err)
	}
	if err := replaySnapshotContext(context.Background(), &replaySeeker{data: []byte{0, 0}}, func([]byte) error { return nil }); err == nil {
		t.Fatal("snapshot header error missing")
	}
	truncated := encodedFrames([]byte("ok"))
	truncated = truncated[:len(truncated)-1]
	if err := replaySnapshotContext(context.Background(), &replaySeeker{data: truncated}, func([]byte) error { return nil }); err == nil {
		t.Fatal("snapshot body error missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterReader{Reader: *bytes.NewReader(encodedFrames([]byte("ok"))), cancel: cancel, limit: 6}
	if err := replaySnapshotContext(ctx, reader, func([]byte) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-read cancellation error=%v", err)
	}
}

func TestCoverageReaderAndWriterHelpers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := readFullContext(ctx, bytes.NewReader([]byte("x")), []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation=%v", err)
	}
	if err := readFullContext(context.Background(), bytes.NewReader([]byte("x")), make([]byte, 2)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read short=%v", err)
	}
	if err := readFullContext(context.Background(), bytes.NewReader(bytes.Repeat([]byte("x"), 32<<10+1)), make([]byte, 32<<10+1)); err != nil {
		t.Fatalf("large read=%v", err)
	}
	if _, _, err := validateReader(context.Background(), bytes.NewReader(encodedFrames([]byte("x"))), 0, 0, shortWriter{mode: "error"}); err == nil {
		t.Fatal("snapshot header write error missing")
	}
	if _, _, err := validateReader(context.Background(), bytes.NewReader(encodedFrames([]byte("x"))), 0, 0, shortWriter{mode: "zero"}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("snapshot zero write=%v", err)
	}
	if _, _, err := validateReader(context.Background(), bytes.NewReader(encodedFrames([]byte("x"))), 0, 0, shortWriter{mode: "short"}); err != nil {
		t.Fatalf("partial snapshot write=%v", err)
	}
	frameWriteFail := &failWriter{failAt: 2}
	if _, _, err := validateReader(context.Background(), bytes.NewReader(encodedFrames([]byte("x"))), 0, 0, frameWriteFail); err == nil {
		t.Fatal("snapshot frame write error missing")
	}
	if err := writeAll(shortWriter{mode: "error"}, []byte("x")); err == nil {
		t.Fatal("write error missing")
	}
	if err := writeAll(shortWriter{mode: "zero"}, []byte("x")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("zero write=%v", err)
	}
	if err := writeAll(shortWriter{mode: "short"}, []byte("xyz")); err != nil {
		t.Fatalf("partial write=%v", err)
	}
	innerCtx, innerCancel := context.WithCancel(context.Background())
	if _, _, err := validateReader(innerCtx, &cancelOnRead{Reader: *bytes.NewReader(encodedFrames([]byte("x"))), cancel: innerCancel}, 0, 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("inner validation cancellation=%v", err)
	}
}
