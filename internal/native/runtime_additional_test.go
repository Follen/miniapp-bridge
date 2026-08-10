package native

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type nativeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f nativeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type nativeErrorReader struct{ sent bool }

func (r *nativeErrorReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, errors.New("body read failed")
	}
	r.sent = true
	copy(p, []byte("partial"))
	return len([]byte("partial")), nil
}
func (r *nativeErrorReader) Close() error { return nil }

type nativeCloseErrorWriter struct{ bytes.Buffer }

func (w *nativeCloseErrorWriter) Close() error { return errors.New("partial close failed") }

func TestNativeErrorFormattingAndWrappers(t *testing.T) {
	inner := errors.New("inner")
	err := &Error{Code: ErrNativeCache, Operation: "op", Path: "path", Expected: "want", Actual: "got", Err: inner}
	if got := err.Error(); got != "native: op: path: expected=want actual=got: inner" {
		t.Fatalf("error=%q", got)
	}
	if !errors.Is(err, ErrNativeCache) || !errors.Is(err, inner) {
		t.Fatal("expected code and wrapped error matches")
	}
	if errors.Is(err, errors.New("other")) {
		t.Fatal("unrelated error matched")
	}
	plain := &Error{Operation: "plain"}
	if plain.Error() != "native: plain" || plain.Unwrap() != nil {
		t.Fatalf("plain=%q unwrap=%v", plain.Error(), plain.Unwrap())
	}
}

func TestNativeDefaultAndExplicitRuntimeChecks(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	m.Size = int64(len(data))
	m.SHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))
	if err := CheckNativeRuntime(exe, m); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(exe, m); err != nil {
		t.Fatal(err)
	}
	withoutDLL := m
	withoutDLL.DLL = ""
	if err := VerifyManifest(exe, withoutDLL); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("empty DLL manifest check=%v", err)
	}
	if err := VerifyManifest("bad\x00path", m); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("invalid stat err=%v", err)
	}
	if got := DefaultManifest(); got.NativeVersion != NativeVersion || got.DLL != NativeDLLFileName {
		t.Fatalf("default manifest=%+v", got)
	}
}

func TestNativePrepareDefaultsAndExtractInstallFailures(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Prepare(canceled, PrepareOptions{Offline: true, Manifest: currentPlatformManifest()}); !errors.Is(err, ErrNativeOffline) {
		t.Fatalf("default cache cancellation=%v", err)
	}
	withoutDLL := DefaultManifest()
	withoutDLL.DLL = ""
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), Offline: true, Manifest: withoutDLL}); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("default DLL prepare=%v", err)
	}

	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dll, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	manifestForDLL(&m, dll)
	archive := makeArchive(t, m, dll, "")
	zipPath := filepath.Join(t.TempDir(), "native.zip")
	if err := os.WriteFile(zipPath, archive, 0o600); err != nil {
		t.Fatal(err)
	}

	cacheFile := filepath.Join(t.TempDir(), "cache-file")
	if err := os.WriteFile(cacheFile, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(zipPath, cacheFile, m); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("staging failure=%v", err)
	}
	cache := t.TempDir()
	if err := os.Mkdir(filepath.Join(cache, m.DLL), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(zipPath, cache, m); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("rename failure=%v", err)
	}
	cache = t.TempDir()
	if err := os.Mkdir(filepath.Join(cache, "manifest.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(zipPath, cache, m); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("manifest install failure=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, m.DLL)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest failure published DLL: %v", err)
	}
}

func TestNativeCacheRequiresManifestSidecar(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	m.Size = int64(len(data))
	sum := sha256.Sum256(data)
	m.SHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))
	cache := t.TempDir()
	dll := filepath.Join(cache, m.DLL)
	if err := os.WriteFile(dll, data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyCachedRuntime(dll, m); !errors.Is(err, ErrNativeMissing) {
		t.Fatalf("missing sidecar accepted: %v", err)
	}
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: cache, Manifest: m, Offline: true}); !errors.Is(err, ErrNativeOffline) {
		t.Fatalf("half-installed offline cache accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cache, "manifest.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyCachedRuntime(dll, m); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("malformed sidecar accepted: %v", err)
	}
	wrong := m
	wrong.NativeVersion = "wrong"
	encoded, err := json.Marshal(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "manifest.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyCachedRuntime(dll, m); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("mismatched sidecar accepted: %v", err)
	}
}

func TestNativeExtractRejectsExpandedDLLOverLimit(t *testing.T) {
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	manifestForDLL(&m, []byte("oversized"))
	archive := makeArchive(t, m, []byte("oversized"), "")
	zipPath := filepath.Join(t.TempDir(), "native.zip")
	if err := os.WriteFile(zipPath, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	original := nativeDLLLimit
	nativeDLLLimit = 4
	defer func() { nativeDLLLimit = original }()
	if _, err := extractArchive(zipPath, t.TempDir(), m); !errors.Is(err, ErrNativeArchive) {
		t.Fatalf("expanded DLL limit err=%v", err)
	}
}

func TestNativeExtractManifestStagingFailureDoesNotPublishDLL(t *testing.T) {
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dll, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	manifestForDLL(&m, dll)
	archive := makeArchive(t, m, dll, "")
	zipPath := filepath.Join(t.TempDir(), "native.zip")
	if err := os.WriteFile(zipPath, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	original := nativeWriteFile
	nativeWriteFile = func(string, []byte, os.FileMode) error { return errors.New("manifest staging failed") }
	defer func() { nativeWriteFile = original }()
	if _, err := extractArchive(zipPath, cache, m); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("manifest staging error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, m.DLL)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest staging failure published DLL: %v", err)
	}
}

func TestNativeVerifyManifestHashReaderError(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	m.Size = info.Size()
	m.SHA256 = strings.Repeat("0", 64)
	original := nativeHashFile
	defer func() { nativeHashFile = original }()
	nativeHashFile = func(string) (string, error) { return "", errors.New("hash read failed") }
	if err := VerifyManifest(path, m); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("verify hash reader=%v", err)
	}
}

func TestNativeExtractManifestAndEntryErrors(t *testing.T) {
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dll, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	manifestForDLL(&m, dll)
	manifest := m
	manifest.DLL = ""
	manifest.Size = int64(len(dll))
	sum := sha256.Sum256(dll)
	manifest.SHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))
	archive := zipEntries(t, map[string][]byte{"manifest.json": mustJSON(t, manifest), m.DLL: dll})
	path := filepath.Join(t.TempDir(), "empty-dll.zip")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(path, t.TempDir(), m); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("empty manifest DLL=%v", err)
	}
	badHash := manifest
	badHash.DLL = m.DLL
	badHash.Size = int64(len(dll))
	badHash.SHA256 = strings.Repeat("0", 64)
	archive = zipEntries(t, map[string][]byte{"manifest.json": mustJSON(t, badHash), m.DLL: dll})
	path = filepath.Join(t.TempDir(), "bad-hash.zip")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(path, t.TempDir(), m); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("archive verify err=%v", err)
	}
	path = filepath.Join(t.TempDir(), "bad-entry.zip")
	archive = zipEntries(t, map[string][]byte{"manifest.json": mustJSON(t, m), "dir/" + m.DLL: dll})
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	badExpected := m
	badExpected.DLL = "dir/miniapp-frida.dll"
	if _, err := extractArchive(path, t.TempDir(), badExpected); !errors.Is(err, ErrNativeArchive) {
		t.Fatalf("entry output err=%v", err)
	}
}

func TestNativeZipReadAndHashErrors(t *testing.T) {
	if _, err := fileSHA256(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing hash unexpectedly succeeded")
	}
	dir := t.TempDir()
	if _, err := fileSHA256(dir); err == nil {
		t.Fatal("directory hash unexpectedly succeeded")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	contents := []byte("native-manifest-content")
	_, _ = w.Write(contents)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	archive := buf.Bytes()
	index := bytes.Index(archive, contents)
	if index < 0 {
		t.Fatal("zip entry data not found")
	}
	archive[index] ^= 0xff
	path := filepath.Join(t.TempDir(), "unsupported.zip")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := readZipFile(r.File[0]); err == nil {
		t.Fatal("unsupported zip method unexpectedly read")
	}
	unsupportedManifest := zipEntries(t, map[string][]byte{"manifest.json": []byte("{}")})
	setZipMethod(t, unsupportedManifest, "manifest.json", 99)
	path = filepath.Join(t.TempDir(), "bad-manifest-method.zip")
	if err := os.WriteFile(path, unsupportedManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err = zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := readZipFile(r.File[0]); err == nil {
		t.Fatal("unsupported manifest method unexpectedly read")
	}

	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dll, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := zipEntries(t, map[string][]byte{"manifest.json": mustJSON(t, m), m.DLL: dll})
	corruptManifestData(t, corrupt, "manifest.json")
	path = filepath.Join(t.TempDir(), "bad-manifest-crc.zip")
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(path, t.TempDir(), m); !errors.Is(err, ErrNativeArchive) {
		t.Fatalf("manifest read err=%v", err)
	}

	unsupported := zipEntries(t, map[string][]byte{"manifest.json": mustJSON(t, m), m.DLL: dll})
	setZipMethod(t, unsupported, m.DLL, 99)
	path = filepath.Join(t.TempDir(), "bad-dll-method.zip")
	if err := os.WriteFile(path, unsupported, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(path, t.TempDir(), m); err == nil {
		t.Fatal("unsupported DLL method unexpectedly extracted")
	}

	nested := m
	nested.DLL = filepath.Join("missing", NativeDLLFileName)
	path = filepath.Join(t.TempDir(), "missing-parent.zip")
	if err := os.WriteFile(path, zipEntries(t, map[string][]byte{"manifest.json": mustJSON(t, nested), nested.DLL: dll}), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(path, t.TempDir(), nested); !errors.Is(err, ErrNativeArchive) {
		t.Fatalf("nested target err=%v", err)
	}
}

func TestNativePlatformFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-pe.dll")
	if err := os.WriteFile(path, []byte("not a PE"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := verifyPlatformFile(path)
	if runtime.GOOS != "windows" {
		if err != nil {
			t.Fatalf("non-Windows platform file check=%v", err)
		}
		return
	}
	if !errors.Is(err, ErrNativeWrongArch) {
		t.Fatalf("parse PE err=%v", err)
	}
	path = filepath.Join(t.TempDir(), "x86.dll")
	if err := os.WriteFile(path, minimalPE(0x14c), 0o600); err != nil {
		t.Fatal(err)
	}
	err = verifyPlatformFile(path)
	var nativeErr *Error
	if !errors.As(err, &nativeErr) || nativeErr.Operation != "PE architecture" {
		t.Fatalf("x86 PE err=%v typed=%#v", err, nativeErr)
	}
}

func TestNativeLockWaitsBeforeCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.lock")
	unlock, err := acquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := acquireLock(ctx, path); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("wait cancellation err=%v", err)
	}
}

func TestNativePrepareHTTPAndCacheErrors(t *testing.T) {
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dll, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	manifestForDLL(&m, dll)
	archive := makeArchive(t, m, dll, "")

	statusClient := &http.Client{Transport: nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway", Body: io.NopCloser(strings.NewReader("bad"))}, nil
	})}
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), SourceURL: "https://fixture.invalid", HTTPClient: statusClient, Manifest: m}); !errors.Is(err, ErrNativeDownload) {
		t.Fatalf("status err=%v", err)
	}
	transportErr := errors.New("transport failed")
	errorClient := &http.Client{Transport: nativeRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, transportErr })}
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), SourceURL: "https://fixture.invalid", HTTPClient: errorClient, Manifest: m}); !errors.Is(err, ErrNativeDownload) {
		t.Fatalf("transport err=%v", err)
	}
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), SourceURL: "://invalid", Manifest: m}); !errors.Is(err, ErrNativeDownload) {
		t.Fatalf("request err=%v", err)
	}

	readErrClient := &http.Client{Transport: nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: &nativeErrorReader{}}, nil
	})}
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), SourceURL: "https://fixture.invalid", HTTPClient: readErrClient, Manifest: m}); !errors.Is(err, ErrNativeDownload) {
		t.Fatalf("read err=%v", err)
	}

	cacheFile := filepath.Join(t.TempDir(), "cache-file")
	if err := os.WriteFile(cacheFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: cacheFile, Offline: true, Manifest: m}); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("cache path err=%v", err)
	}

	partialDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(partialDir, NativeDLLFileName+".partial"), 0o700); err != nil {
		t.Fatal(err)
	}
	server := &http.Client{Transport: nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}
	archiveSum := sha256.Sum256(archive)
	archiveHash := strings.ToUpper(hex.EncodeToString(archiveSum[:]))
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: partialDir, HTTPClient: server, ExpectedArchiveSHA: archiveHash, Manifest: m}); err != nil {
		t.Fatalf("stale partial recovery err=%v", err)
	}
	if info, err := os.Stat(filepath.Join(partialDir, NativeDLLFileName+".partial")); err != nil || !info.IsDir() {
		t.Fatalf("unrelated stale partial changed: info=%v err=%v", info, err)
	}

	originalOpen, originalHash, originalLimit := nativeOpenPartial, nativeHashFile, nativeArchiveLimit
	defer func() {
		nativeOpenPartial, nativeHashFile, nativeArchiveLimit = originalOpen, originalHash, originalLimit
	}()
	nativeOpenPartial = func(dir, pattern string) (string, io.WriteCloser, error) {
		return filepath.Join(dir, pattern), &nativeCloseErrorWriter{}, nil
	}
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), HTTPClient: server, Manifest: m}); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("partial close err=%v", err)
	}
	nativeOpenPartial = func(dir, pattern string) (string, io.WriteCloser, error) {
		file, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return "", nil, err
		}
		return file.Name(), file, nil
	}
	nativeArchiveLimit = 3
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), HTTPClient: server, Manifest: m}); !errors.Is(err, ErrNativeDownload) {
		t.Fatalf("size limit err=%v", err)
	}
	nativeArchiveLimit = originalLimit
	nativeHashFile = func(string) (string, error) { return "", errors.New("hash failed") }
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), HTTPClient: server, ExpectedArchiveSHA: strings.Repeat("0", 64), Manifest: m}); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("archive hash read err=%v", err)
	}
}

func TestNativeUserCacheDirectoryError(t *testing.T) {
	original := nativeUserCacheDir
	defer func() { nativeUserCacheDir = original }()
	nativeUserCacheDir = func() (string, error) { return "", errors.New("cache unavailable") }
	if _, err := Prepare(context.Background(), PrepareOptions{Offline: true, Manifest: currentPlatformManifest()}); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("user cache err=%v", err)
	}
}

func TestNativePrepareStaleLockAndWrapper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-11 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if _, err := PrepareNativeRuntime(context.Background(), PrepareOptions{CacheDir: t.TempDir(), Offline: true, Manifest: currentPlatformManifest()}); !errors.Is(err, ErrNativeOffline) {
		t.Fatalf("wrapper err=%v", err)
	}
	if _, err := acquireLock(context.Background(), filepath.Join(t.TempDir(), "missing", "lock")); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("parent lock err=%v", err)
	}
}

func TestNativeOldActiveLockIsNotStolen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.lock")
	unlock, err := acquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := acquireLock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("old active lock was stolen: %v", err)
	}
}

func TestNativePartialNamesAreUnique(t *testing.T) {
	dir := t.TempDir()
	const workers = 16
	paths := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			partialPath, partial, err := nativeOpenPartial(dir, ".native.*.partial")
			if err == nil {
				err = partial.Close()
			}
			paths <- partialPath
			errs <- err
		}()
	}
	wg.Wait()
	close(paths)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[string]struct{}, workers)
	for partialPath := range paths {
		defer os.Remove(partialPath)
		if _, duplicate := seen[partialPath]; duplicate {
			t.Fatalf("partial path is shared: %q", partialPath)
		}
		seen[partialPath] = struct{}{}
	}
	if len(seen) != workers {
		t.Fatalf("partial paths=%d, want %d", len(seen), workers)
	}
}

func TestNativeLockOperationError(t *testing.T) {
	original := nativeTryLockFile
	defer func() { nativeTryLockFile = original }()
	nativeTryLockFile = func(*os.File) (bool, error) {
		return false, errors.New("lock syscall failed")
	}
	if _, err := acquireLock(context.Background(), filepath.Join(t.TempDir(), "error.lock")); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("lock operation error=%v", err)
	}
}

func TestNativeExtractArchiveShapeErrors(t *testing.T) {
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dll, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	for name, archive := range map[string][]byte{
		"missing-manifest": zipEntries(t, map[string][]byte{m.DLL: dll}),
		"missing-dll":      zipEntries(t, map[string][]byte{"manifest.json": mustJSON(t, m)}),
		"bad-manifest":     zipEntries(t, map[string][]byte{"manifest.json": []byte("{"), m.DLL: dll}),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.zip")
			if err := os.WriteFile(path, archive, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := extractArchive(path, t.TempDir(), m)
			if !errors.Is(err, ErrNativeArchive) && !errors.Is(err, ErrNativeManifest) {
				t.Fatalf("extract err=%v", err)
			}
		})
	}
	manifestMismatch := m
	manifestMismatch.DLL = "other.dll"
	archive := makeArchive(t, manifestMismatch, dll, "")
	path := filepath.Join(t.TempDir(), "fixture.zip")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(path, t.TempDir(), m); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("dll mismatch err=%v", err)
	}

	badArchive := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(badArchive, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(badArchive, t.TempDir(), m); !errors.Is(err, ErrNativeArchive) {
		t.Fatalf("open err=%v", err)
	}
}

func zipEntries(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func corruptManifestData(t *testing.T, archive []byte, name string) {
	t.Helper()
	for i := 0; i+30 <= len(archive); i++ {
		if !bytes.Equal(archive[i:i+4], []byte("PK\x03\x04")) {
			continue
		}
		nameLen := int(archive[i+26]) | int(archive[i+27])<<8
		extraLen := int(archive[i+28]) | int(archive[i+29])<<8
		start := i + 30
		end := start + nameLen
		if end > len(archive) || string(archive[start:end]) != name {
			continue
		}
		data := end + extraLen
		if data >= len(archive) {
			t.Fatal("zip data missing")
		}
		archive[data] ^= 0xff
		return
	}
	t.Fatalf("local ZIP entry %q not found", name)
}

func setZipMethod(t *testing.T, archive []byte, name string, method uint16) {
	t.Helper()
	local, central := false, false
	for i := 0; i+30 <= len(archive); i++ {
		if bytes.Equal(archive[i:i+4], []byte("PK\x03\x04")) {
			nameLen := int(archive[i+26]) | int(archive[i+27])<<8
			extraLen := int(archive[i+28]) | int(archive[i+29])<<8
			start, end := i+30, i+30+nameLen
			if end <= len(archive) && string(archive[start:end]) == name {
				archive[i+8], archive[i+9] = byte(method), byte(method>>8)
				local = true
				i = end + extraLen
			}
		}
		if i+46 <= len(archive) && bytes.Equal(archive[i:i+4], []byte("PK\x01\x02")) {
			nameLen := int(archive[i+28]) | int(archive[i+29])<<8
			extraLen := int(archive[i+30]) | int(archive[i+31])<<8
			commentLen := int(archive[i+32]) | int(archive[i+33])<<8
			start, end := i+46, i+46+nameLen
			if end <= len(archive) && string(archive[start:end]) == name {
				archive[i+10], archive[i+11] = byte(method), byte(method>>8)
				central = true
				i = end + extraLen + commentLen
			}
		}
	}
	if !local || !central {
		t.Fatalf("ZIP method headers for %q not found (local=%v central=%v)", name, local, central)
	}
}

func minimalPE(machine uint16) []byte {
	data := make([]byte, 0x40+4+20+0xe0)
	data[0], data[1] = 'M', 'Z'
	data[0x3c] = 0x40
	data[0x40], data[0x41], data[0x42], data[0x43] = 'P', 'E', 0, 0
	data[0x44], data[0x45] = byte(machine), byte(machine>>8)
	data[0x54], data[0x55] = 0xe0, 0
	data[0x58], data[0x59] = 0x0b, 0x01
	data[0xb4] = 16
	return data
}

func manifestForDLL(m *Manifest, dll []byte) {
	m.Size = int64(len(dll))
	sum := sha256.Sum256(dll)
	m.SHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))
}
