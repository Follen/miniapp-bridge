//go:build windows

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
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
	pinnedArchive := filepath.Join(root, "third_party", "downloads", "cache", zlibOfflineArchiveName)
	if _, err := os.Stat(pinnedArchive); err != nil {
		t.Skipf("pinned zlib archive cache is not present: %v", err)
	}
	for _, tool := range []string{"tar.exe", "gcc.exe", "ar.exe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("required Windows build tool %s is not installed: %v", tool, err)
		}
	}

	tmp := t.TempDir()
	cache := filepath.Join(tmp, "cache")
	sourceDir := filepath.Join(tmp, "source")
	outputDir := filepath.Join(tmp, "output")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatalf("create isolated zlib cache: %v", err)
	}
	archive := filepath.Join(cache, zlibOfflineArchiveName)
	if err := copyFile(pinnedArchive, archive); err != nil {
		t.Fatalf("copy pinned zlib archive into isolated cache: %v", err)
	}
	buildArgs := []string{
		"-Offline",
		"-CacheDirectory", cache,
		"-SourceDirectory", sourceDir,
		"-OutputDirectory", outputDir,
	}

	output, err := runZlibBuild(root, buildArgs...)
	if err != nil {
		t.Fatalf("offline zlib build failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "zlib_version=1.3.1") {
		t.Fatalf("offline build did not report zlib 1.3.1:\n%s", output)
	}
	if _, err := os.Stat(archive + ".partial"); !os.IsNotExist(err) {
		t.Fatalf("offline build left temporary archive %s (err=%v)", archive+".partial", err)
	}

	if err := os.Remove(archive); err != nil {
		t.Fatalf("remove isolated archive for invalid-cache test: %v", err)
	}

	output, err = runZlibBuild(root, buildArgs...)
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
	fixture := makeZlibArchiveFixture(t)
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

func makeZlibArchiveFixture(t *testing.T) zlibArchiveFixture {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	files := []struct {
		name string
		data string
	}{{"zlib.h", "#define ZLIB_VERSION \"1.3.1\"\n"}}
	for _, name := range []string{
		"adler32.c", "crc32.c", "deflate.c", "infback.c", "inffast.c",
		"inflate.c", "inftrees.c", "trees.c", "zutil.c", "compress.c",
		"uncompr.c", "gzclose.c", "gzlib.c", "gzread.c", "gzwrite.c",
	} {
		symbol := strings.NewReplacer(".", "_", "-", "_").Replace(name)
		files = append(files, struct {
			name string
			data string
		}{name, fmt.Sprintf("int fixture_%s(void) { return 0; }\n", symbol)})
	}
	var writeErrors []string
	for _, file := range files {
		data := []byte(file.data)
		header := &tar.Header{
			Name: "zlib-1.3.1/" + file.name,
			Mode: 0o644,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(header); err != nil {
			writeErrors = append(writeErrors, file.name+": "+err.Error())
			break
		}
		if _, err := tw.Write(data); err != nil {
			writeErrors = append(writeErrors, file.name+": "+err.Error())
			break
		}
	}
	var closeErrors []string
	if err := tw.Close(); err != nil {
		closeErrors = append(closeErrors, "tar: "+err.Error())
	}
	if err := gz.Close(); err != nil {
		closeErrors = append(closeErrors, "gzip: "+err.Error())
	}
	if len(writeErrors) != 0 {
		t.Fatalf("create zlib fixture archive: %s (close errors: %s)", strings.Join(writeErrors, "; "), strings.Join(closeErrors, "; "))
	}
	if len(closeErrors) != 0 {
		t.Fatalf("close zlib fixture archive: %s", strings.Join(closeErrors, "; "))
	}
	data := archive.Bytes()
	return zlibArchiveFixture{data: data, hash: fmt.Sprintf("%X", sha256.Sum256(data))}
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o644)
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
