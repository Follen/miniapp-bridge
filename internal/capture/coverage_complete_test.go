package capture

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type failWriter struct {
	calls  int
	failAt int
}

func (w *failWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.failAt {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestCoverageRecorderLifecycleAndWriteFailures(t *testing.T) {
	if _, err := Start(filepath.Join(t.TempDir(), "missing", "capture.bin")); err == nil {
		t.Fatal("expected create failure")
	}
	path := filepath.Join(t.TempDir(), "capture.bin")
	r, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(r.Write([]byte("after-close")), ErrRecorderClosed) {
		t.Fatal("closed recorder write did not fail")
	}

	headerFail := &failWriter{failAt: 1}
	r = &Recorder{w: bufio.NewWriterSize(headerFail, 1)}
	if err := r.Write([]byte("x")); err == nil {
		t.Fatal("header write failure was not reported")
	}
	frameFail := &failWriter{failAt: 2}
	w := bufio.NewWriterSize(frameFail, 4)
	_, _ = w.Write([]byte("abc"))
	r = &Recorder{w: w}
	if err := r.Write([]byte("xy")); err == nil {
		t.Fatal("frame write failure was not reported")
	}
	flushFail := &failWriter{failAt: 1}
	r = &Recorder{w: bufio.NewWriterSize(flushFail, 8)}
	if err := r.Write([]byte("x")); err == nil {
		t.Fatal("flush failure was not reported")
	}
	closeFlushFail := &failWriter{failAt: 1}
	r = &Recorder{w: bufio.NewWriter(closeFlushFail)}
	_, _ = r.w.Write([]byte("pending"))
	if err := r.Close(); err == nil {
		t.Fatal("close flush failure was not reported")
	}

	closedFile, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := closedFile.Close(); err != nil {
		t.Fatal(err)
	}
	r = &Recorder{f: closedFile, w: bufio.NewWriter(io.Discard)}
	if err := r.Close(); err == nil {
		t.Fatal("closed file close error was not returned")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageReplayOpenFailure(t *testing.T) {
	if _, err := Replay(filepath.Join(t.TempDir(), "missing", "capture.bin")); err == nil {
		t.Fatal("expected open failure")
	}
}
