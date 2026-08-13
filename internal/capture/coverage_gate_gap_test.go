package capture

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverageGateCaptureDirectoryAndStickyClose(t *testing.T) {
	root := t.TempDir()
	if _, err := Start(root); err == nil {
		t.Fatal("directory capture path was accepted")
	}

	metadataTarget := filepath.Join(root, "metadata-dir.capture")
	if err := os.Mkdir(MetadataPath(metadataTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(metadataTarget); err == nil {
		t.Fatal("directory metadata path was accepted")
	}

	zero := &Recorder{}
	if err := zero.failLocked(nil); err != nil || zero.failed {
		t.Fatalf("nil failure changed recorder: err=%v failed=%v", err, zero.failed)
	}

	path := filepath.Join(root, "failed.capture")
	recorder, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("injected recorder failure")
	recorder.failed = true
	recorder.failure = sentinel
	if err := recorder.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("close failure=%v, want sentinel", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed capture still published: %v", err)
	}
}

func TestCoverageGateCapturePublishRollbackBranches(t *testing.T) {
	originalRename := renameRecorderPath
	t.Cleanup(func() { renameRecorderPath = originalRename })

	makeGeneration := func(t *testing.T, name string) (string, string, string, string) {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, name+".capture")
		metadataPath := MetadataPath(path)
		tempPath := filepath.Join(dir, name+".temp")
		metadataTemp := filepath.Join(dir, name+".meta-temp")
		for file, contents := range map[string]string{
			path:         "old",
			metadataPath: "old-meta\n",
			tempPath:     "new",
			metadataTemp: "new-meta\n",
		} {
			if err := os.WriteFile(file, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return tempPath, path, metadataTemp, metadataPath
	}

	t.Run("metadata backup rollback", func(t *testing.T) {
		tempPath, path, metadataTemp, metadataPath := makeGeneration(t, "metadata-backup")
		calls := 0
		renameRecorderPath = func(from, to string) error {
			calls++
			if calls == 2 {
				return errors.New("metadata backup rename failed")
			}
			return os.Rename(from, to)
		}
		if err := publishGeneration(tempPath, path, metadataTemp, metadataPath); err == nil {
			t.Fatal("metadata backup failure was not returned")
		}
	})

	t.Run("capture backup rollback", func(t *testing.T) {
		tempPath, path, metadataTemp, metadataPath := makeGeneration(t, "capture-backup")
		calls := 0
		renameRecorderPath = func(from, to string) error {
			calls++
			if calls == 3 {
				return errors.New("capture rename failed")
			}
			return os.Rename(from, to)
		}
		if err := publishGeneration(tempPath, path, metadataTemp, metadataPath); err == nil {
			t.Fatal("capture rename failure was not returned")
		}
	})
}

func TestCoverageGateReplayMetadataFrameBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata-budget.capture")
	line := "{}\n"
	if err := os.WriteFile(MetadataPath(path), []byte(strings.Repeat(line, MaxCaptureFrames+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReplayMetadata(path); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("metadata frame budget error=%v", err)
	}
}
