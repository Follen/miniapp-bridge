package capture

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecorderSyncsEachCommittedFrame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "durable.capture")
	original := syncRecorderFile
	t.Cleanup(func() { syncRecorderFile = original })
	var calls int
	syncRecorderFile = func(f *os.File) error {
		calls++
		return f.Sync()
	}

	recorder, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Write([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatalf("expected capture and metadata syncs, got %d", calls)
	}
	if err := Validate(path); err != nil {
		t.Fatalf("published capture failed validation: %v", err)
	}
}

func TestRecorderSyncFailureIsStickyAndDoesNotPublish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "durable-failure.capture")
	original := syncRecorderFile
	t.Cleanup(func() { syncRecorderFile = original })
	want := errors.New("disk sync failed")
	syncRecorderFile = func(*os.File) error { return want }

	recorder, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Write([]byte("frame")); !errors.Is(err, ErrRecorderFailed) || !errors.Is(err, want) {
		t.Fatalf("write error = %v, want recorder and sync errors", err)
	}
	if err := recorder.Write([]byte("again")); !errors.Is(err, ErrRecorderFailed) {
		t.Fatalf("second write error = %v, want sticky recorder failure", err)
	}
	if err := recorder.Close(); !errors.Is(err, want) {
		t.Fatalf("close error = %v, want sync error", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed generation was published, stat error = %v", err)
	}
}

func TestRecorderMetadataSyncFailureIsSticky(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata-sync-failure.capture")
	original := syncRecorderFile
	t.Cleanup(func() { syncRecorderFile = original })
	want := errors.New("metadata sync failed")
	calls := 0
	syncRecorderFile = func(f *os.File) error {
		calls++
		if calls == 2 {
			return want
		}
		return f.Sync()
	}
	recorder, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Write([]byte("frame")); !errors.Is(err, want) || !errors.Is(err, ErrRecorderFailed) {
		t.Fatalf("metadata sync error=%v", err)
	}
	if err := recorder.Close(); !errors.Is(err, want) {
		t.Fatalf("close error=%v", err)
	}
}
