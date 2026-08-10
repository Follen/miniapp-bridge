//go:build windows

package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestZlibBuildRetriesTrickleDownloadAndCleansPartial(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	fixture := makeZlibArchiveFixture(t, root)
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(fixture.data)))
			_, _ = w.Write(fixture.data[:1])
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(2 * time.Second)
			return
		}
		_, _ = w.Write(fixture.data)
	}))
	defer server.Close()

	tmp := t.TempDir()
	args := []string{
		"-SourceURL", server.URL + "/zlib.tar.gz",
		"-CacheDirectory", filepath.Join(tmp, "cache"),
		"-SourceDirectory", filepath.Join(tmp, "source"),
		"-OutputDirectory", filepath.Join(tmp, "output"),
		"-ExpectedArchiveSHA256", fixture.hash,
		"-DownloadAttempts", "2",
		"-DownloadTimeoutSeconds", "1",
		"-DownloadTotalTimeoutSeconds", "10",
		"-DownloadRetrySeconds", "0",
	}
	output, err := runZlibBuild(root, args...)
	if err != nil {
		t.Fatalf("trickle retry zlib build failed: %v\n%s", err, output)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("trickle retry requests = %d, want 2", got)
	}
	if !strings.Contains(output, "Downloading zlib-1.3.1.tar.gz attempt=2/2") {
		t.Fatalf("retry attempt was not reported:\n%s", output)
	}
	if !strings.Contains(output, "zlib_version=1.3.1") {
		t.Fatalf("successful retry did not build zlib:\n%s", output)
	}
	cache := filepath.Join(tmp, "cache")
	if _, err := os.Stat(filepath.Join(cache, zlibOfflineArchiveName)); err != nil {
		t.Fatalf("verified archive was not promoted into cache: %v", err)
	}
	assertNoZlibDownloadTemps(t, cache)
}

func TestZlibBuildBadHashCleansPartial(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not the pinned archive"))
	}))
	defer server.Close()
	tmp := t.TempDir()
	output, err := runZlibBuild(root,
		"-SourceURL", server.URL+"/zlib.tar.gz",
		"-CacheDirectory", filepath.Join(tmp, "cache"),
		"-SourceDirectory", filepath.Join(tmp, "source"),
		"-OutputDirectory", filepath.Join(tmp, "output"),
		"-ExpectedArchiveSHA256", strings.Repeat("A", 64),
		"-DownloadAttempts", "1",
		"-DownloadTimeoutSeconds", "5",
		"-DownloadTotalTimeoutSeconds", "5",
	)
	if err == nil || !strings.Contains(output, "downloaded zlib 1.3.1 archive hash mismatch") {
		t.Fatalf("bad archive hash was not rejected: err=%v\n%s", err, output)
	}
	cache := filepath.Join(tmp, "cache")
	if _, statErr := os.Stat(filepath.Join(cache, zlibOfflineArchiveName)); !os.IsNotExist(statErr) {
		t.Fatalf("untrusted archive entered cache: err=%v", statErr)
	}
	assertNoZlibDownloadTemps(t, cache)
}

type zlibArchiveFixture struct {
	data []byte
	hash string
}

func makeZlibArchiveFixture(t *testing.T, root string) zlibArchiveFixture {
	t.Helper()
	source := filepath.Join(root, "third_party", "zlib", "src-1.3.1")
	archive := filepath.Join(t.TempDir(), "zlib-1.3.1.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	if err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(filepath.Dir(source), path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		_, err = io.Copy(tw, input)
		return err
	}); err != nil {
		_ = tw.Close()
		_ = gz.Close()
		_ = file.Close()
		t.Fatalf("create zlib fixture archive: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	return zlibArchiveFixture{data: data, hash: fmt.Sprintf("%X", sha256.Sum256(data))}
}

func assertNoZlibDownloadTemps(t *testing.T, cache string) {
	t.Helper()
	partial := filepath.Join(cache, zlibOfflineArchiveName+".partial")
	if _, err := os.Lstat(partial); !os.IsNotExist(err) {
		t.Fatalf("zlib partial download remains: %s (err=%v)", partial, err)
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
