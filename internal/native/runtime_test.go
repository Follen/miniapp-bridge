package native

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVerifyManifestMissingAndHash(t *testing.T) {
	dir := t.TempDir()
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	path := filepath.Join(dir, "miniapp-frida.dll")
	if err := VerifyManifest(path, m); !errors.Is(err, ErrNativeMissing) {
		t.Fatalf("missing err=%v", err)
	}
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Size = 99
	if err := VerifyManifest(path, m); !errors.Is(err, ErrNativeHashMismatch) {
		t.Fatalf("size err=%v", err)
	}
	m.Size = 0
	m.SHA256 = strings.Repeat("0", 64)
	m.Size = int64(len("fixture"))
	if err := VerifyManifest(path, m); !errors.Is(err, ErrNativeHashMismatch) {
		t.Fatalf("hash err=%v", err)
	}
}

func TestVerifyManifestVersionAndPlatform(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, NativeDLLFileName)
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	m.NativeVersion = "wrong"
	if err := VerifyManifest(path, m); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("version err=%v", err)
	}
	m = DefaultManifest()
	m.OS, m.Arch = "other", "amd64"
	if err := VerifyManifest(path, m); !errors.Is(err, ErrNativeWrongArch) {
		t.Fatalf("platform err=%v", err)
	}
}

func TestPrepareDownloadCacheOfflineAndHash(t *testing.T) {
	dll := []byte("fixture native dll")
	if runtime.GOOS == "windows" {
		var err error
		dll, err = os.ReadFile(os.Args[0])
		if err != nil {
			t.Fatal(err)
		}
	}
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	m.Size = int64(len(dll))
	sum := sha256.Sum256(dll)
	m.SHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))
	archive := makeArchive(t, m, dll, "LICENSE")
	archiveSum := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	cache := t.TempDir()
	path, err := Prepare(context.Background(), PrepareOptions{CacheDir: cache, SourceURL: server.URL, ExpectedArchiveSHA: strings.ToUpper(hex.EncodeToString(archiveSum[:])), Manifest: m})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, dll) {
		t.Fatalf("installed bytes=%q", got)
	}
	path2, err := Prepare(context.Background(), PrepareOptions{CacheDir: cache, Offline: true, Manifest: m})
	if err != nil || path2 != path {
		t.Fatalf("offline cache path=%q err=%v", path2, err)
	}
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), Offline: true, Manifest: m}); !errors.Is(err, ErrNativeOffline) {
		t.Fatalf("offline missing err=%v", err)
	}
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), SourceURL: server.URL, ExpectedArchiveSHA: strings.Repeat("0", 64), Manifest: m}); !errors.Is(err, ErrNativeHashMismatch) {
		t.Fatalf("archive hash err=%v", err)
	}
}

func TestPrepareRejectsZipSlip(t *testing.T) {
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	archive := makeArchive(t, m, []byte("x"), "../escape")
	archiveHash := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), SourceURL: server.URL, ExpectedArchiveSHA: hex.EncodeToString(archiveHash[:]), Manifest: m}); !errors.Is(err, ErrNativeArchive) {
		t.Fatalf("zip slip err=%v", err)
	}
}

func TestPrepareRejectsMalformedArchiveAndManifest(t *testing.T) {
	m := DefaultManifest()
	malformed := []byte("not a zip")
	malformedHash := sha256.Sum256(malformed)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(malformed)
	}))
	_, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), SourceURL: server.URL, ExpectedArchiveSHA: hex.EncodeToString(malformedHash[:]), Manifest: m})
	server.Close()
	if !errors.Is(err, ErrNativeArchive) {
		t.Fatalf("malformed archive err=%v", err)
	}

	dll := []byte("fixture")
	bad := m
	bad.NativeVersion = "17.3.1-abi1"
	archive := makeArchive(t, bad, dll, "")
	archiveHash := sha256.Sum256(archive)
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	_, err = Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), SourceURL: server.URL, ExpectedArchiveSHA: hex.EncodeToString(archiveHash[:]), Manifest: m})
	if !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("manifest mismatch err=%v", err)
	}
}

func TestPrepareUsesPinnedArchiveHashByDefault(t *testing.T) {
	archive := []byte("not the pinned native archive")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	cache := t.TempDir()
	_, err := Prepare(context.Background(), PrepareOptions{CacheDir: cache, SourceURL: server.URL, Manifest: DefaultManifest()})
	if !errors.Is(err, ErrNativeHashMismatch) {
		t.Fatalf("default trust anchor error=%v", err)
	}
	var nativeErr *Error
	if !errors.As(err, &nativeErr) || nativeErr.Expected != NativeArchiveSHA256 {
		t.Fatalf("default trust anchor typed error=%#v", err)
	}
	for _, path := range []string{
		filepath.Join(cache, NativeDLLFileName),
		filepath.Join(cache, NativeDLLFileName+".partial"),
	} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("untrusted download left cache file %q: %v", path, statErr)
		}
	}
}

func TestPrepareLockCancellationAndManifestPath(t *testing.T) {
	dir := t.TempDir()
	m := DefaultManifest()
	if err := os.WriteFile(filepath.Join(dir, "miniapp-frida.dll.lock"), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Prepare(ctx, PrepareOptions{CacheDir: dir, Offline: true, Manifest: m})
	if !errors.Is(err, ErrNativeCache) {
		t.Fatalf("cancelled lock err=%v", err)
	}
	m.DLL = "../escape.dll"
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), Offline: true, Manifest: m}); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("manifest path err=%v", err)
	}
}

func TestVerifyManifestNativeErrorFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, NativeDLLFileName)
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := DefaultManifest()
	m.DLL = "../bad.dll"
	err := VerifyManifest(path, m)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != ErrNativeManifest || typed.Operation != "manifest dll" {
		t.Fatalf("typed error=%#v", err)
	}
}

func makeArchive(t *testing.T, m Manifest, dll []byte, extra string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	manifest := m
	manifest.Size = int64(len(dll))
	sum := sha256.Sum256(dll)
	manifest.SHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))
	w, err := zw.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(data)
	w, err = zw.Create("miniapp-frida.dll")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(dll)
	if extra != "" {
		w, err = zw.Create(extra)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("x"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
