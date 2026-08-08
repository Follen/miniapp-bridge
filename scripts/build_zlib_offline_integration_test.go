//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const zlibOfflineArchiveName = "zlib-1.3.1.tar.gz"

func TestZlibBuildOfflineCacheIntegration(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	archive := filepath.Join(root, "third_party", "downloads", "cache", zlibOfflineArchiveName)
	sourceHeader := filepath.Join(root, "third_party", "zlib", "src-1.3.1", "zlib.h")
	if _, err := os.Stat(archive); err != nil {
		t.Skipf("pinned zlib archive cache is not present: %v", err)
	}
	if _, err := os.Stat(sourceHeader); err != nil {
		t.Skipf("pinned zlib source cache is not present: %v", err)
	}
	for _, tool := range []string{"tar.exe", "gcc.exe", "ar.exe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("required Windows build tool %s is not installed: %v", tool, err)
		}
	}

	output, err := runZlibBuild(root, "-Offline")
	if err != nil {
		t.Fatalf("offline zlib build failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "zlib_version=1.3.1") {
		t.Fatalf("offline build did not report zlib 1.3.1:\n%s", output)
	}
	if _, err := os.Stat(archive + ".partial"); !os.IsNotExist(err) {
		t.Fatalf("offline build left temporary archive %s (err=%v)", archive+".partial", err)
	}

	moved := fmt.Sprintf("%s.offline-test-backup-%d", archive, os.Getpid())
	if _, err := os.Lstat(moved); err == nil {
		t.Fatalf("temporary archive path already exists: %s", moved)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Rename(archive, moved); err != nil {
		t.Fatalf("temporarily move archive for invalid-cache test: %v", err)
	}
	defer func() {
		if err := os.Rename(moved, archive); err != nil {
			t.Errorf("restore pinned zlib archive: %v", err)
		}
	}()

	output, err = runZlibBuild(root, "-Offline")
	if err == nil {
		t.Fatalf("offline build unexpectedly succeeded without archive cache:\n%s", output)
	}
	if !strings.Contains(strings.ToLower(output), "offline mode") {
		t.Fatalf("invalid offline cache error lacks offline-mode diagnostic: %v\n%s", err, output)
	}
	if _, statErr := os.Stat(archive + ".partial"); !os.IsNotExist(statErr) {
		t.Fatalf("failed offline build left temporary archive %s (err=%v)", archive+".partial", statErr)
	}
}

func runZlibBuild(root string, args ...string) (string, error) {
	script := filepath.Join(root, "scripts", "build-zlib.ps1")
	shell := "pwsh.exe"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "powershell.exe"
	}
	command := exec.Command(shell, append([]string{
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script,
	}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%w", err)
	}
	return string(output), nil
}
