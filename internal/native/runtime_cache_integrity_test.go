package native

import (
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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultManifestPinsOfficialDLL(t *testing.T) {
	manifest := DefaultManifest()
	if manifest.Size != NativeDLLSize {
		t.Fatalf("default size=%d constant=%d", manifest.Size, NativeDLLSize)
	}
	if NativeDLLSize != 67788800 {
		t.Fatalf("default size=%d", manifest.Size)
	}
	if manifest.SHA256 != NativeDLLSHA256 {
		t.Fatalf("default sha256=%q constant=%q", manifest.SHA256, NativeDLLSHA256)
	}
	if NativeDLLSHA256 != "700D4DACD175D3E8B212EAD6C38FE151CB80B855660CAB24DD87C5AEDB13EBBD" {
		t.Fatalf("default sha256=%q", manifest.SHA256)
	}
}

func TestPrepareRejectsUntrustedManifest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "size", mutate: func(m *Manifest) { m.Size = 0 }},
		{name: "short hash", mutate: func(m *Manifest) { m.SHA256 = "0" }},
		{name: "nonhex hash", mutate: func(m *Manifest) { m.SHA256 = strings.Repeat("z", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := currentPlatformManifest()
			test.mutate(&manifest)
			_, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), Manifest: manifest, Offline: true})
			if !errors.Is(err, ErrNativeManifest) {
				t.Fatalf("Prepare() error=%v", err)
			}
			var nativeErr *Error
			if !errors.As(err, &nativeErr) || nativeErr.Operation != "manifest trust" {
				t.Fatalf("Prepare() typed error=%#v", err)
			}
		})
	}
}

func TestPrepareOfflineRejectsTamperedCache(t *testing.T) {
	manifest, dll, archive, archiveHash := nativeCacheFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	cache := t.TempDir()
	path, err := Prepare(context.Background(), PrepareOptions{
		CacheDir: cache, SourceURL: server.URL, ExpectedArchiveSHA: archiveHash, Manifest: manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), dll...)
	tampered[len(tampered)-1] ^= 0xff
	if err := os.WriteFile(path, tampered, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = Prepare(context.Background(), PrepareOptions{CacheDir: cache, Manifest: manifest, Offline: true})
	if !errors.Is(err, ErrNativeOffline) || !errors.Is(err, ErrNativeHashMismatch) {
		t.Fatalf("tampered offline cache error=%v", err)
	}
	var nativeErr *Error
	if !errors.As(err, &nativeErr) || nativeErr.Operation != "offline cache" {
		t.Fatalf("tampered cache typed error=%#v", err)
	}
}

func TestPrepareAtomicallyReplacesExistingInvalidDLL(t *testing.T) {
	manifest, dll, archive, archiveHash := nativeCacheFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	cache := t.TempDir()
	bad := append([]byte(nil), dll...)
	bad[len(bad)-1] ^= 0xff
	if err := os.WriteFile(filepath.Join(cache, manifest.DLL), bad, 0o700); err != nil {
		t.Fatal(err)
	}
	writeInstalledManifest(t, cache, manifest)

	path, err := Prepare(context.Background(), PrepareOptions{
		CacheDir: cache, SourceURL: server.URL, ExpectedArchiveSHA: archiveHash, Manifest: manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, dll) || requests.Load() != 1 {
		t.Fatalf("replacement bytes match=%v requests=%d", bytes.Equal(installed, dll), requests.Load())
	}
	if err := verifyCachedRuntime(path, manifest); err != nil {
		t.Fatalf("replacement cache verification=%v", err)
	}
}

func TestPrepareRecoversAfterDLLCommitFailure(t *testing.T) {
	manifest, dll, archive, archiveHash := nativeCacheFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	cache := t.TempDir()
	bad := append([]byte(nil), dll...)
	bad[len(bad)-1] ^= 0xff
	finalDLL := filepath.Join(cache, manifest.DLL)
	if err := os.WriteFile(finalDLL, bad, 0o700); err != nil {
		t.Fatal(err)
	}
	writeInstalledManifest(t, cache, manifest)

	original := nativeReplaceFile
	failCommit := true
	nativeReplaceFile = func(source, destination string) error {
		if destination == finalDLL && failCommit {
			failCommit = false
			return errors.New("injected DLL commit failure")
		}
		return replaceFileAtomic(source, destination)
	}
	defer func() { nativeReplaceFile = original }()

	options := PrepareOptions{CacheDir: cache, SourceURL: server.URL, ExpectedArchiveSHA: archiveHash, Manifest: manifest}
	_, err := Prepare(context.Background(), options)
	if !errors.Is(err, ErrNativeCache) {
		t.Fatalf("commit failure error=%v", err)
	}
	var nativeErr *Error
	if !errors.As(err, &nativeErr) || nativeErr.Operation != "install dll" {
		t.Fatalf("commit failure typed error=%#v", err)
	}
	unchanged, readErr := os.ReadFile(finalDLL)
	if readErr != nil || !bytes.Equal(unchanged, bad) {
		t.Fatalf("failed commit changed readiness marker: match=%v err=%v", bytes.Equal(unchanged, bad), readErr)
	}

	path, err := Prepare(context.Background(), options)
	if err != nil {
		t.Fatalf("recovery Prepare()=%v", err)
	}
	if err := verifyCachedRuntime(path, manifest); err != nil {
		t.Fatalf("recovered cache=%v", err)
	}
}

func TestPrepareConcurrentInstallUsesSingleDownload(t *testing.T) {
	manifest, _, archive, archiveHash := nativeCacheFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	cache := t.TempDir()
	options := PrepareOptions{CacheDir: cache, SourceURL: server.URL, ExpectedArchiveSHA: archiveHash, Manifest: manifest}

	const workers = 6
	var wg sync.WaitGroup
	errorsByWorker := make(chan error, workers)
	paths := make(chan string, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := Prepare(context.Background(), options)
			paths <- path
			errorsByWorker <- err
		}()
	}
	wg.Wait()
	close(errorsByWorker)
	close(paths)
	for err := range errorsByWorker {
		if err != nil {
			t.Fatalf("concurrent Prepare()=%v", err)
		}
	}
	var installedPath string
	for path := range paths {
		if installedPath == "" {
			installedPath = path
		}
		if path != installedPath {
			t.Fatalf("concurrent paths %q and %q", installedPath, path)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("download requests=%d", requests.Load())
	}
	if err := verifyCachedRuntime(installedPath, manifest); err != nil {
		t.Fatalf("concurrent cache=%v", err)
	}
}

func nativeCacheFixture(t *testing.T) (Manifest, []byte, []byte, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dll, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	manifest := DefaultManifest()
	manifest.OS, manifest.Arch = runtime.GOOS, runtime.GOARCH
	manifest.Size = int64(len(dll))
	dllSum := sha256.Sum256(dll)
	manifest.SHA256 = strings.ToUpper(hex.EncodeToString(dllSum[:]))
	archive := makeArchive(t, manifest, dll, "")
	archiveSum := sha256.Sum256(archive)
	return manifest, dll, archive, strings.ToUpper(hex.EncodeToString(archiveSum[:]))
}

func writeInstalledManifest(t *testing.T, cache string, manifest Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
