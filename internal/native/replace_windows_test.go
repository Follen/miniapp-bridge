//go:build windows

package native

import (
	"os"
	"path/filepath"
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
