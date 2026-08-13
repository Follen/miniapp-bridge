package capture

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionCaptureStartAndPublishFailures(t *testing.T) {
	originalCreate := createRecorderTemp
	t.Cleanup(func() { createRecorderTemp = originalCreate })
	createRecorderTemp = func(string, string) (*os.File, error) {
		return nil, errors.New("capture temp create failed")
	}
	if _, err := Start(filepath.Join(t.TempDir(), "capture.bin")); err == nil {
		t.Fatal("temp creation failure was not returned")
	}

	call := 0
	createRecorderTemp = func(dir, pattern string) (*os.File, error) {
		call++
		if call == 2 {
			return nil, errors.New("metadata temp create failed")
		}
		return os.CreateTemp(dir, pattern)
	}
	if _, err := Start(filepath.Join(t.TempDir(), "capture.bin")); err == nil {
		t.Fatal("metadata temp creation failure was not returned")
	}

	path := filepath.Join(t.TempDir(), "capture.bin")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(MetadataPath(path), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	createRecorderTemp = os.CreateTemp
	recorder, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	originalRename := renameRecorderPath
	t.Cleanup(func() { renameRecorderPath = originalRename })
	renameRecorderPath = func(string, string) error { return errors.New("publish rename failed") }
	if err := recorder.Close(); err == nil {
		t.Fatal("publish failure was not returned")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "old" {
		t.Fatalf("old capture was not preserved: %q %v", got, err)
	}

	createRecorderTemp = os.CreateTemp
	recorder, err = Start(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	count := 0
	renameRecorderPath = func(from, to string) error {
		count++
		if count == 4 {
			return errors.New("metadata publish rename failed")
		}
		return os.Rename(from, to)
	}
	if err := recorder.Close(); err == nil {
		t.Fatal("metadata publish failure was not returned")
	}
}

func TestProductionCaptureErrorHelpersAndMetadataBudget(t *testing.T) {
	if joinCaptureErrors(nil, nil) != nil || joinCaptureErrors(errors.New("a"), nil) == nil || joinCaptureErrors(nil, errors.New("b")) == nil || joinCaptureErrors(errors.New("a"), errors.New("b")) == nil {
		t.Fatal("joinCaptureErrors branches not covered")
	}
	recorder := &Recorder{failure: errors.New("first")}
	if err := recorder.failLocked(errors.New("second")); !errors.Is(err, ErrRecorderFailed) || !errors.Is(err, recorder.failure) {
		t.Fatalf("sticky failure=%v", err)
	}

	path := filepath.Join(t.TempDir(), "metadata.capture")
	line := `{"index":0,"direction":"unknown","timestamp":"2026-01-01T00:00:00Z","size":1,"padding":"` + strings.Repeat("x", 1<<20) + `"}`
	if err := os.WriteFile(MetadataPath(path), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReplayMetadata(path); err == nil {
		t.Fatal("oversized metadata line was accepted")
	}
}

func TestProductionCaptureWriterLeaseAndGenerationPublish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.bin")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(MetadataPath(path), []byte("old-meta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(path); !errors.Is(err, ErrCaptureInUse) {
		t.Fatalf("second writer error=%v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "old" {
		t.Fatalf("old capture changed during start: %q %v", got, err)
	}
	if err := first.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	frames, err := Replay(path)
	if err != nil || len(frames) != 1 || !bytes.Equal(frames[0], []byte("new")) {
		t.Fatalf("published frames=%q err=%v", frames, err)
	}
	second, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionCaptureStickyFailureAndBudgets(t *testing.T) {
	failing := &failWriter{failAt: 1}
	recorder := &Recorder{w: bufio.NewWriterSize(failing, 8), metadataWriter: bufio.NewWriter(ioDiscard{})}
	if err := recorder.Write([]byte("x")); !errors.Is(err, ErrRecorderFailed) {
		t.Fatalf("first write=%v", err)
	}
	if err := recorder.Write([]byte("x")); !errors.Is(err, ErrRecorderFailed) {
		t.Fatalf("sticky write=%v", err)
	}

	recorder = &Recorder{w: bufio.NewWriter(ioDiscard{}), metadataWriter: bufio.NewWriter(ioDiscard{}), bytesWritten: MaxCaptureSize}
	if err := recorder.Write([]byte("x")); !errors.Is(err, ErrCaptureTooLarge) || !errors.Is(err, ErrRecorderFailed) {
		t.Fatalf("size budget=%v", err)
	}

	recorder = &Recorder{w: bufio.NewWriter(ioDiscard{}), metadataWriter: bufio.NewWriter(ioDiscard{}), nextIndex: MaxCaptureFrames}
	if err := recorder.Write([]byte("x")); !errors.Is(err, ErrTooManyFrames) || !errors.Is(err, ErrRecorderFailed) {
		t.Fatalf("frame budget=%v", err)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
