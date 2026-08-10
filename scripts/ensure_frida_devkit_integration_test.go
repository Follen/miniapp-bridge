//go:build windows

package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fridaArchiveFixture struct {
	data        []byte
	archiveHash string
	headerHash  string
	libraryHash string
}

func TestFridaBootstrapCacheStateMachine(t *testing.T) {
	fixture := makeFridaArchiveFixture(t)
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		_, _ = w.Write(fixture.data)
	}))
	defer server.Close()

	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	devkit := filepath.Join(root, "devkit")
	args := fridaBootstrapArgs(cache, devkit, server.URL+"/fixture.tar.xz", fixture)

	output, err := runFridaBootstrap(args...)
	if err != nil {
		t.Fatalf("cold bootstrap failed: %v\n%s", err, output)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("cold bootstrap requests = %d, want 1", got)
	}
	assertFridaFixtureInstalled(t, devkit, fixture)
	assertNoFridaBootstrapTemps(t, cache)

	archive := filepath.Join(cache, "fixture.tar.xz")
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	output, err = runFridaBootstrap(append(args, "-Offline")...)
	if err != nil {
		t.Fatalf("verified devkit-only offline bootstrap failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "archive_cache=not-present") {
		t.Fatalf("offline output does not report absent archive:\n%s", output)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("offline bootstrap made a network request; total = %d", got)
	}

	if err := os.WriteFile(filepath.Join(devkit, "frida-core.h"), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err = runFridaBootstrap(append(args, "-Offline")...)
	if err == nil || !strings.Contains(output, "unavailable or invalid in offline mode") {
		t.Fatalf("invalid offline cache did not fail diagnostically: err=%v\n%s", err, output)
	}
	assertNoFridaBootstrapTemps(t, cache)

	output, err = runFridaBootstrap(args...)
	if err != nil {
		t.Fatalf("online cache repair failed: %v\n%s", err, output)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("online repair requests = %d, want 2 total", got)
	}
	assertFridaFixtureInstalled(t, devkit, fixture)

	if err := os.WriteFile(archive, []byte("corrupt archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devkit, "frida-core.lib"), []byte("corrupt library"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err = runFridaBootstrap(args...)
	if err != nil {
		t.Fatalf("combined archive/devkit repair failed: %v\n%s", err, output)
	}
	if got := atomic.LoadInt32(&requests); got != 3 {
		t.Fatalf("combined repair requests = %d, want 3 total", got)
	}
	assertFridaFixtureInstalled(t, devkit, fixture)
	assertNoFridaBootstrapTemps(t, cache)
}

func TestFridaBootstrapCleansFailedDownloadAndExtraction(t *testing.T) {
	fixture := makeFridaArchiveFixture(t)

	t.Run("download hash mismatch", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("wrong archive"))
		}))
		defer server.Close()
		root := t.TempDir()
		cache := filepath.Join(root, "cache")
		args := fridaBootstrapArgs(cache, filepath.Join(root, "devkit"), server.URL, fixture)
		output, err := runFridaBootstrap(args...)
		if err == nil || !strings.Contains(output, "download SHA-256 mismatch") {
			t.Fatalf("bad download did not fail hash verification: err=%v\n%s", err, output)
		}
		assertNoFridaBootstrapTemps(t, cache)
	})

	t.Run("invalid archive extraction", func(t *testing.T) {
		invalid := fridaArchiveFixture{
			data:        []byte("not an xz archive"),
			headerHash:  fixture.headerHash,
			libraryHash: fixture.libraryHash,
		}
		invalid.archiveHash = sha256Hex(invalid.data)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(invalid.data)
		}))
		defer server.Close()
		root := t.TempDir()
		cache := filepath.Join(root, "cache")
		args := fridaBootstrapArgs(cache, filepath.Join(root, "devkit"), server.URL, invalid)
		output, err := runFridaBootstrap(args...)
		if err == nil || !strings.Contains(output, "extraction failed") {
			t.Fatalf("invalid archive did not fail extraction: err=%v\n%s", err, output)
		}
		assertNoFridaBootstrapTemps(t, cache)
	})

	t.Run("failed extraction preserves existing install", func(t *testing.T) {
		root := t.TempDir()
		cache := filepath.Join(root, "cache")
		devkit := filepath.Join(root, "devkit")
		if err := os.MkdirAll(devkit, 0o755); err != nil {
			t.Fatal(err)
		}
		oldHeader := []byte("existing header that must survive\n")
		oldLibrary := []byte("existing library that must survive\n")
		if err := os.WriteFile(filepath.Join(devkit, "frida-core.h"), oldHeader, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(devkit, "frida-core.lib"), oldLibrary, 0o644); err != nil {
			t.Fatal(err)
		}
		invalid := []byte("not an xz archive")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(invalid)
		}))
		defer server.Close()
		fixture := fridaArchiveFixture{
			data:        invalid,
			archiveHash: sha256Hex(invalid),
			headerHash:  sha256Hex([]byte("expected header")),
			libraryHash: sha256Hex([]byte("expected library")),
		}
		output, err := runFridaBootstrap(fridaBootstrapArgs(cache, devkit, server.URL, fixture)...)
		if err == nil || !strings.Contains(output, "extraction failed") {
			t.Fatalf("invalid archive did not fail diagnostically: err=%v\n%s", err, output)
		}
		for path, want := range map[string][]byte{
			filepath.Join(devkit, "frida-core.h"):   oldHeader,
			filepath.Join(devkit, "frida-core.lib"): oldLibrary,
		} {
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("existing install file %s was removed: %v", path, readErr)
			}
			if string(got) != string(want) {
				t.Fatalf("existing install file %s changed: got %q, want %q", path, got, want)
			}
		}
		assertNoFridaBootstrapTemps(t, cache)
	})
}

func TestFridaBootstrapPromotionFailurePreservesOldCache(t *testing.T) {
	fixture := makeFridaArchiveFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture.data)
	}))
	defer server.Close()

	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	devkit := filepath.Join(root, "devkit")
	if err := os.MkdirAll(devkit, 0o755); err != nil {
		t.Fatal(err)
	}
	oldHeader := []byte("old verified header\n")
	oldLibrary := []byte("old verified library\n")
	if err := os.WriteFile(filepath.Join(devkit, "frida-core.h"), oldHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devkit, "frida-core.lib"), oldLibrary, 0o644); err != nil {
		t.Fatal(err)
	}

	// Replace only the staging-to-final rename in a temporary copy to force the
	// real publication catch/rollback path deterministically.
	_, source, _, _ := runtime.Caller(0)
	script, err := os.ReadFile(filepath.Join(filepath.Dir(source), "ensure-frida-devkit.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	failingScript := strings.Replace(string(script),
		"Move-DirectoryAtomically -Source $stagingDevkit -Destination $devkit",
		"throw 'injected promotion failure'", 1)
	failingPath := filepath.Join(root, "ensure-frida-devkit-failing.ps1")
	if err := os.WriteFile(failingPath, []byte(failingScript), 0o644); err != nil {
		t.Fatal(err)
	}

	output, runErr := runFridaBootstrapScript(failingPath, fridaBootstrapArgs(cache, devkit, server.URL, fixture)...)
	if runErr == nil || !strings.Contains(output, "Frida SDK publication failed: injected promotion failure") {
		t.Fatalf("promotion failure was not reported: err=%v\n%s", runErr, output)
	}
	for path, want := range map[string][]byte{
		filepath.Join(devkit, "frida-core.h"):   oldHeader,
		filepath.Join(devkit, "frida-core.lib"): oldLibrary,
	} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("old cache file %s was removed: %v", path, readErr)
		}
		if string(got) != string(want) {
			t.Fatalf("old cache file %s changed: got %q, want %q", path, got, want)
		}
	}
	assertNoFridaBootstrapTemps(t, cache)
}

func TestFridaBootstrapSuccessfulPublicationCleansAllTemps(t *testing.T) {
	fixture := makeFridaArchiveFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture.data)
	}))
	defer server.Close()

	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	devkit := filepath.Join(root, "devkit")
	output, err := runFridaBootstrap(fridaBootstrapArgs(cache, devkit, server.URL, fixture)...)
	if err != nil {
		t.Fatalf("successful publication failed: %v\n%s", err, output)
	}
	assertFridaFixtureInstalled(t, devkit, fixture)
	assertNoFridaBootstrapTemps(t, cache)
}

func TestFridaBootstrapSerializesConcurrentColdCache(t *testing.T) {
	fixture := makeFridaArchiveFixture(t)
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write(fixture.data)
	}))
	defer server.Close()

	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	devkit := filepath.Join(root, "devkit")
	args := fridaBootstrapArgs(cache, devkit, server.URL, fixture)
	start := make(chan struct{})
	results := make(chan string, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			output, err := runFridaBootstrap(args...)
			results <- fmt.Sprintf("err=%v\n%s", err, output)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for result := range results {
		if !strings.Contains(result, "err=<nil>") {
			t.Fatalf("concurrent bootstrap failed:\n%s", result)
		}
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("concurrent cold bootstrap downloads = %d, want 1", got)
	}
	assertFridaFixtureInstalled(t, devkit, fixture)
	assertNoFridaBootstrapTemps(t, cache)
}

func TestFridaBootstrapRetriesTimedOutDownload(t *testing.T) {
	fixture := makeFridaArchiveFixture(t)
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			time.Sleep(2 * time.Second)
			return
		}
		_, _ = w.Write(fixture.data)
	}))
	defer server.Close()

	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	devkit := filepath.Join(root, "devkit")
	args := append(fridaBootstrapArgs(cache, devkit, server.URL, fixture),
		"-DownloadAttempts", "2",
		"-DownloadTimeoutSeconds", "1",
		"-DownloadRetrySeconds", "0",
	)
	output, err := runFridaBootstrap(args...)
	if err != nil {
		t.Fatalf("retry bootstrap failed: %v\n%s", err, output)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("retry bootstrap requests = %d, want 2", got)
	}
	if !strings.Contains(output, "Downloading fixture.tar.xz attempt=2/2") {
		t.Fatalf("retry attempt was not reported:\n%s", output)
	}
	assertFridaFixtureInstalled(t, devkit, fixture)
	assertNoFridaBootstrapTemps(t, cache)
}

func makeFridaArchiveFixture(t *testing.T) fridaArchiveFixture {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	header := []byte("fixture frida-core header\n")
	library := []byte("fixture frida-core library\x00\x01\x02")
	if err := os.WriteFile(filepath.Join(source, "frida-core.h"), header, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "frida-core.lib"), library, 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "fixture.tar.xz")
	command := exec.Command("tar.exe", "-cJf", archive, "-C", source, "frida-core.h", "frida-core.lib")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create fixture archive: %v\n%s", err, output)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	return fridaArchiveFixture{
		data:        data,
		archiveHash: sha256Hex(data),
		headerHash:  sha256Hex(header),
		libraryHash: sha256Hex(library),
	}
}

func fridaBootstrapArgs(cache, devkit, sourceURL string, fixture fridaArchiveFixture) []string {
	return []string{
		"-ArchiveFileName", "fixture.tar.xz",
		"-SourceURL", sourceURL,
		"-CacheDirectory", cache,
		"-DevkitDirectory", devkit,
		"-ExpectedArchiveSHA256", fixture.archiveHash,
		"-ExpectedHeaderSHA256", fixture.headerHash,
		"-ExpectedLibrarySHA256", fixture.libraryHash,
		"-LockTimeoutSeconds", "10",
	}
}

func runFridaBootstrap(args ...string) (string, error) {
	_, source, _, _ := runtime.Caller(0)
	return runFridaBootstrapScript(filepath.Join(filepath.Dir(source), "ensure-frida-devkit.ps1"), args...)
}

func runFridaBootstrapScript(scriptPath string, args ...string) (string, error) {
	commandArgs := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath}
	shell := "pwsh.exe"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "powershell.exe"
	}
	command := exec.Command(shell, append(commandArgs, args...)...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func assertFridaFixtureInstalled(t *testing.T, devkit string, fixture fridaArchiveFixture) {
	t.Helper()
	for path, want := range map[string]string{
		filepath.Join(devkit, "frida-core.h"):   fixture.headerHash,
		filepath.Join(devkit, "frida-core.lib"): fixture.libraryHash,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := sha256Hex(data); got != want {
			t.Fatalf("%s hash = %s, want %s", path, got, want)
		}
	}
}

func assertNoFridaBootstrapTemps(t *testing.T, cache string) {
	t.Helper()
	for _, name := range []string{"fixture.tar.xz.partial", "fixture.tar.xz.extracting"} {
		if _, err := os.Stat(filepath.Join(cache, name)); !os.IsNotExist(err) {
			t.Fatalf("temporary bootstrap artifact remains: %s (err=%v)", name, err)
		}
	}
	for _, pattern := range []string{
		filepath.Join(filepath.Dir(cache), "*.extracting-*"),
		filepath.Join(filepath.Dir(cache), "*.backup-*"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Fatalf("temporary bootstrap artifacts remain: %v", matches)
		}
	}
}

func sha256Hex(data []byte) string {
	return strings.ToUpper(fmt.Sprintf("%x", sha256.Sum256(data)))
}
