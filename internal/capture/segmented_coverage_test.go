package capture

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func preserveSegmentHooks(t *testing.T) {
	t.Helper()
	free, write := diskFreeBytes, segmentWrite
	mkdir, stat, glob := segmentMkdir, segmentStat, segmentGlob
	create, rename, remove := segmentCreate, segmentRename, segmentRemove
	openLock := segmentOpenLock
	syncFile := syncRecorderFile
	t.Cleanup(func() {
		diskFreeBytes, segmentWrite = free, write
		segmentMkdir, segmentStat, segmentGlob = mkdir, stat, glob
		segmentCreate, segmentRename, segmentRemove = create, rename, remove
		segmentOpenLock = openLock
		syncRecorderFile = syncFile
	})
}

func TestSegmentedStartFailures(t *testing.T) {
	preserveSegmentHooks(t)
	want := errors.New("injected")
	if _, err := StartSegmented("x", SegmentOptions{SegmentMaxBytes: 1}); err == nil {
		t.Fatal("small segment accepted")
	}
	segmentMkdir = func(string, os.FileMode) error { return want }
	if _, err := StartSegmented("x", SegmentOptions{}); !errors.Is(err, want) {
		t.Fatalf("mkdir error=%v", err)
	}
	segmentMkdir = os.MkdirAll
	diskFreeBytes = func(string) (uint64, error) { return 0, want }
	if _, err := StartSegmented("x", SegmentOptions{}); !errors.Is(err, want) {
		t.Fatalf("disk error=%v", err)
	}
	diskFreeBytes = func(string) (uint64, error) { return ^uint64(0), nil }
	segmentGlob = func(string) ([]string, error) { return nil, want }
	if _, err := StartSegmented("x", SegmentOptions{}); !errors.Is(err, want) {
		t.Fatalf("glob error=%v", err)
	}
}

func TestSegmentedWriteStateAndFailureHelpers(t *testing.T) {
	r := &SegmentedRecorder{}
	if err := r.Write(make([]byte, MaxFrameSize+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversize=%v", err)
	}
	r.closing.Store(true)
	if err := r.Write(nil); !errors.Is(err, ErrRecorderClosed) {
		t.Fatalf("closed=%v", err)
	}
	r.closing.Store(false)
	r.err = io.ErrUnexpectedEOF
	if err := r.Write(nil); !errors.Is(err, ErrRecorderFailed) {
		t.Fatalf("failed=%v", err)
	}
	r.setFailure(nil)
	r.setFailure(io.EOF)
	if !errors.Is(r.failure(), io.ErrUnexpectedEOF) {
		t.Fatalf("sticky=%v", r.failure())
	}
}

func TestSegmentedRunSkipsAfterFailureAndEmptyAbort(t *testing.T) {
	r := &SegmentedRecorder{queue: make(chan segmentRequest, 2), done: make(chan struct{}), err: io.EOF}
	r.queue <- segmentRequest{payload: []byte("ignored")}
	close(r.queue)
	r.run()
	if !errors.Is(r.failure(), io.EOF) {
		t.Fatalf("failure=%v", r.failure())
	}
	r.abortSegment()
}

func TestSegmentedWriteRecordFailures(t *testing.T) {
	preserveSegmentHooks(t)
	diskFreeBytes = func(string) (uint64, error) { return ^uint64(0), nil }
	want := errors.New("injected")

	r := &SegmentedRecorder{opts: SegmentOptions{SegmentMaxBytes: segmentHeaderSize + segmentMarkerSize}}
	if err := r.writeRecord([]byte("x")); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("encoded oversize=%v", err)
	}

	temp, err := os.CreateTemp(t.TempDir(), "rotation")
	if err != nil {
		t.Fatal(err)
	}
	if err := temp.Close(); err != nil {
		t.Fatal(err)
	}
	r = &SegmentedRecorder{path: filepath.Join(t.TempDir(), "capture"), opts: SegmentOptions{SegmentMaxBytes: segmentHeaderSize + segmentMarkerSize + 1, RetainSegments: 1}, file: temp, writer: bufio.NewWriter(io.Discard), temp: temp.Name(), size: segmentHeaderSize + segmentMarkerSize + 1}
	if err := r.writeRecord([]byte("x")); err == nil {
		t.Fatal("rotation finish error missing")
	}

	segmentCreate = func(string, string) (*os.File, error) { return nil, want }
	r = &SegmentedRecorder{path: filepath.Join(t.TempDir(), "capture"), opts: SegmentOptions{SegmentMaxBytes: 64}}
	if err := r.writeRecord(nil); !errors.Is(err, want) {
		t.Fatalf("open error=%v", err)
	}
	segmentCreate = os.CreateTemp

	diskFreeBytes = func(string) (uint64, error) { return 0, want }
	r = &SegmentedRecorder{path: filepath.Join(t.TempDir(), "capture"), opts: SegmentOptions{SegmentMaxBytes: 64}}
	if err := r.writeRecord(nil); !errors.Is(err, want) {
		t.Fatalf("disk error=%v", err)
	}
	r.abortSegment()

	diskFreeBytes = func(string) (uint64, error) { return ^uint64(0), nil }
	segmentWrite = func(io.Writer, []byte) error { return want }
	r = &SegmentedRecorder{path: filepath.Join(t.TempDir(), "capture"), opts: SegmentOptions{SegmentMaxBytes: 64}}
	if err := r.writeRecord(nil); !errors.Is(err, want) {
		t.Fatalf("write error=%v", err)
	}
	r.abortSegment()
	segmentWrite = writeAll

	r = recorderWithWriter(t, &errorWriter{err: want})
	if err := r.writeRecord(nil); !errors.Is(err, want) {
		t.Fatalf("flush error=%v", err)
	}
	r.abortSegment()

	syncRecorderFile = func(*os.File) error { return want }
	r = &SegmentedRecorder{path: filepath.Join(t.TempDir(), "capture"), opts: SegmentOptions{SegmentMaxBytes: 64}}
	if err := r.writeRecord(nil); !errors.Is(err, want) {
		t.Fatalf("sync error=%v", err)
	}
	r.abortSegment()
}

type errorWriter struct{ err error }

func (w *errorWriter) Write([]byte) (int, error) { return 0, w.err }

func recorderWithWriter(t *testing.T, writer io.Writer) *SegmentedRecorder {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "segment")
	if err != nil {
		t.Fatal(err)
	}
	return &SegmentedRecorder{path: filepath.Join(t.TempDir(), "capture"), opts: SegmentOptions{SegmentMaxBytes: 64}, file: f, writer: bufio.NewWriterSize(writer, 64), temp: f.Name()}
}

func TestSegmentedFinishAndIndexFailures(t *testing.T) {
	preserveSegmentHooks(t)
	want := errors.New("injected")
	if err := (&SegmentedRecorder{}).finishSegment(); err != nil {
		t.Fatal(err)
	}

	r := recorderWithWriter(t, &errorWriter{err: want})
	if err := r.writer.WriteByte(1); err != nil {
		t.Fatal(err)
	}
	if err := r.finishSegment(); !errors.Is(err, want) {
		t.Fatalf("flush=%v", err)
	}

	r = recorderWithWriter(t, io.Discard)
	syncRecorderFile = func(*os.File) error { return want }
	if err := r.finishSegment(); !errors.Is(err, want) {
		t.Fatalf("sync=%v", err)
	}
	syncRecorderFile = func(f *os.File) error { return f.Sync() }

	r = recorderWithWriter(t, io.Discard)
	if err := r.file.Close(); err != nil {
		t.Fatal(err)
	}
	syncRecorderFile = func(*os.File) error { return nil }
	if err := r.finishSegment(); err == nil {
		t.Fatal("close error missing")
	}
	syncRecorderFile = func(f *os.File) error { return f.Sync() }

	r = recorderWithWriter(t, io.Discard)
	segmentRename = func(string, string) error { return want }
	if err := r.finishSegment(); !errors.Is(err, want) {
		t.Fatalf("rename=%v", err)
	}
	segmentRename = os.Rename

	segmentGlob = func(string) ([]string, error) { return nil, want }
	if err := retainSegments("x", 1); !errors.Is(err, want) {
		t.Fatalf("retain glob=%v", err)
	}
	if _, err := latestSegmentIndex("x"); !errors.Is(err, want) {
		t.Fatalf("latest glob=%v", err)
	}
	if err := ReplaySegments("x", func([]byte) error { return nil }); !errors.Is(err, want) {
		t.Fatalf("replay glob=%v", err)
	}

	segmentGlob = func(string) ([]string, error) { return []string{"x.bad"}, nil }
	if _, err := latestSegmentIndex("x"); err == nil {
		t.Fatal("bad index accepted")
	}

	segmentGlob = func(string) ([]string, error) { return []string{"first", "second"}, nil }
	segmentRemove = func(string) error { return os.ErrNotExist }
	if err := retainSegments("x", 1); err != nil {
		t.Fatalf("not-exist remove=%v", err)
	}
	segmentRemove = func(string) error { return want }
	if err := retainSegments("x", 1); !errors.Is(err, want) {
		t.Fatalf("remove=%v", err)
	}
}

func TestSegmentedReplayFailures(t *testing.T) {
	if err := ReplaySegments("x", nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	if err := replaySegment(filepath.Join(t.TempDir(), "missing"), func([]byte) error { return nil }); err == nil {
		t.Fatal("missing segment accepted")
	}
	want := errors.New("submit")

	cases := map[string][]byte{
		"partial header": {1},
		"oversize": func() []byte {
			b := make([]byte, segmentHeaderSize)
			binary.BigEndian.PutUint32(b, MaxFrameSize+1)
			return b
		}(),
		"partial payload": func() []byte {
			b := make([]byte, segmentHeaderSize)
			binary.BigEndian.PutUint32(b, 2)
			return append(b, 1)
		}(),
		"partial marker": func() []byte {
			b := make([]byte, segmentHeaderSize)
			binary.BigEndian.PutUint32(b, 1)
			return append(b, 1, 2)
		}(),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "segment")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := replaySegment(path, func([]byte) error { return nil }); !errors.Is(err, ErrCorruptRecord) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "segment")
	payload := []byte("x")
	record := make([]byte, segmentHeaderSize+len(payload)+segmentMarkerSize)
	binary.BigEndian.PutUint32(record[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(record[4:8], crc32Checksum(payload))
	copy(record[segmentHeaderSize:], payload)
	binary.BigEndian.PutUint32(record[segmentHeaderSize+len(payload):], segmentCommit)
	if err := os.WriteFile(path, record, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaySegment(path, func([]byte) error { return want }); !errors.Is(err, want) {
		t.Fatalf("submit=%v", err)
	}
}

func crc32Checksum(payload []byte) uint32 {
	return crc32.ChecksumIEEE(payload)
}

func TestSegmentedRunFinishFailure(t *testing.T) {
	preserveSegmentHooks(t)
	want := errors.New("finish")
	r := recorderWithWriter(t, io.Discard)
	r.queue, r.done = make(chan segmentRequest), make(chan struct{})
	segmentRename = func(string, string) error { return want }
	close(r.queue)
	r.run()
	if !errors.Is(r.failure(), want) {
		t.Fatalf("failure=%v", r.failure())
	}
}

func TestSegmentedWriteSeesAsyncFailure(t *testing.T) {
	r := &SegmentedRecorder{queue: make(chan segmentRequest, 1), err: io.EOF}
	if err := r.Write(nil); !errors.Is(err, ErrRecorderFailed) {
		t.Fatalf("error=%v", err)
	}
}
