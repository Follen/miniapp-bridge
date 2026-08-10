//go:build windows

package native

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReplaceFileAtomicWindows(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "destination")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceFileAtomic(source, destination); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "new" {
		t.Fatalf("destination=%q err=%v", got, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if err := replaceFileAtomic("bad\x00source", destination); err == nil {
		t.Fatal("NUL source accepted")
	}
	if err := replaceFileAtomic(destination, "bad\x00destination"); err == nil {
		t.Fatal("NUL destination accepted")
	}
	if err := replaceFileAtomic(filepath.Join(dir, "missing"), destination); err == nil {
		t.Fatal("missing source replaced destination")
	}
}

func TestWindowsFileLockErrors(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "lock-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	originalLock := lockFileExCall
	originalUnlock := unlockFileExCall
	defer func() {
		lockFileExCall = originalLock
		unlockFileExCall = originalUnlock
	}()

	lockFileExCall = func(...uintptr) (uintptr, uintptr, error) {
		return 0, 0, errorLockViolation
	}
	if locked, err := tryLockFile(file); err != nil || locked {
		t.Fatalf("lock violation locked=%v err=%v", locked, err)
	}

	want := syscall.ERROR_ACCESS_DENIED
	lockFileExCall = func(...uintptr) (uintptr, uintptr, error) {
		return 0, 0, want
	}
	if locked, err := tryLockFile(file); locked || !errors.Is(err, want) {
		t.Fatalf("lock error locked=%v err=%v", locked, err)
	}

	unlockFileExCall = func(...uintptr) (uintptr, uintptr, error) {
		return 0, 0, want
	}
	if err := unlockFile(file); !errors.Is(err, want) {
		t.Fatalf("unlock error=%v", err)
	}
}
