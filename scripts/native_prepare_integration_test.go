//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
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
	"syscall"
	"testing"
	"time"
)

const nativePrepareAsset = "miniapp-frida-native-17.3.2-abi1-windows-amd64.zip"

var nativePrepareExports = []string{
	"mb_abi_version", "mb_native_version", "mb_frida_core_version", "mb_zlib_version",
	"mb_zlib_compress", "mb_zlib_decompress", "mb_bytes_free", "mb_device_open",
	"mb_device_enumerate", "mb_processes_free", "mb_device_attach", "mb_device_close",
	"mb_runtime_shutdown", "mb_session_load_script", "mb_session_detach", "mb_script_post",
	"mb_script_unload", "mb_error_free",
}

type nativePrepareEntry struct {
	name string
	data []byte
}

type nativePrepareArchiveOptions struct {
	dll               []byte
	archiveComment    string
	mutateManifest    func(map[string]any)
	manifestBytes     []byte
	omitManifest      bool
	omitDLL           bool
	duplicateManifest bool
	extraEntries      []nativePrepareEntry
}

type nativePrepareFixture struct {
	archive []byte
	hash    string
	dll     []byte
}

func TestNativePrepareRequiresExplicitArchiveHashForEveryCacheMode(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(fixture.archive)
	}))
	defer server.Close()

	for _, tc := range []struct {
		name    string
		offline bool
		hot     bool
	}{
		{name: "online-cold"},
		{name: "online-hot", hot: true},
		{name: "offline-cold", offline: true},
		{name: "offline-hot", offline: true, hot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "cache")
			if tc.hot {
				writeNativePrepareCache(t, cache, fixture.archive)
			}
			args := nativePrepareArgs(cache, filepath.Join(root, "destination"), server.URL, "")
			if tc.offline {
				args = append(args, "-Offline")
			}
			output, err := runNativePrepare(args...)
			if err == nil || !strings.Contains(output, "ExpectedArchiveSHA256 is required") {
				t.Fatalf("empty SHA was not rejected: err=%v\n%s", err, output)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("empty SHA caused %d network requests, want 0", got)
	}

	root := t.TempDir()
	output, err := runNativePrepare(nativePrepareArgs(
		filepath.Join(root, "cache"), filepath.Join(root, "destination"), server.URL, "abc",
	)...)
	if err == nil || !strings.Contains(output, "exactly 64 hexadecimal characters") {
		t.Fatalf("malformed SHA was not rejected: err=%v\n%s", err, output)
	}
}

func TestNativePrepareDownloadWarmCacheAndOfflineCache(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(fixture.archive)
	}))
	defer server.Close()

	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	args := nativePrepareArgs(cache, destination, server.URL, fixture.hash)

	output, err := runNativePrepare(args...)
	if err != nil {
		t.Fatalf("cold prepare failed: %v\n%s", err, output)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("cold prepare requests = %d, want 1", got)
	}
	if !strings.Contains(output, "native_version=17.3.2-abi1") ||
		!strings.Contains(output, "native_manifest=") ||
		!strings.Contains(output, "native_dll_sha256="+nativePrepareSHA(fixture.dll)) {
		t.Fatalf("compatible output fields missing:\n%s", output)
	}
	assertNativePrepareInstalled(t, destination, fixture.dll)
	assertNoNativePrepareTemps(t, cache, destination)

	if err := os.WriteFile(filepath.Join(destination, "miniapp-frida.dll"), []byte("old marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err = runNativePrepare(args...)
	if err != nil {
		t.Fatalf("warm prepare failed: %v\n%s", err, output)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("warm cache made another request; total = %d", got)
	}
	assertNativePrepareInstalled(t, destination, fixture.dll)
	assertNoNativePrepareTemps(t, cache, destination)

	offlineArgs := nativePrepareArgs(cache, destination, "http://127.0.0.1:1/unreachable", fixture.hash)
	output, err = runNativePrepare(append(offlineArgs, "-Offline")...)
	if err != nil {
		t.Fatalf("offline cache hit failed: %v\n%s", err, output)
	}
	assertNativePrepareInstalled(t, destination, fixture.dll)
	assertNoNativePrepareTemps(t, cache, destination)

	missingRoot := t.TempDir()
	missingCache := filepath.Join(missingRoot, "cache")
	missingDestination := filepath.Join(missingRoot, "destination")
	output, err = runNativePrepare(append(nativePrepareArgs(
		missingCache, missingDestination, server.URL, fixture.hash,
	), "-Offline")...)
	if err == nil || !strings.Contains(output, "cache unavailable in offline mode") {
		t.Fatalf("offline cache miss lacked diagnostic: err=%v\n%s", err, output)
	}
	assertNoNativePrepareTemps(t, missingCache, missingDestination)
}

func TestNativePrepareRetriesTimedOutDownload(t *testing.T) {
	for _, shell := range []string{"pwsh.exe", "powershell.exe"} {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		t.Run(strings.TrimSuffix(shell, ".exe"), func(t *testing.T) {
			fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if requests.Add(1) == 1 {
					time.Sleep(2 * time.Second)
					return
				}
				_, _ = w.Write(fixture.archive)
			}))
			defer server.Close()

			root := t.TempDir()
			cache := filepath.Join(root, "cache")
			destination := filepath.Join(root, "destination")
			args := append(nativePrepareArgs(cache, destination, server.URL, fixture.hash),
				"-DownloadAttempts", "2",
				"-DownloadTimeoutSeconds", "1",
				"-DownloadRetrySeconds", "0",
			)
			output, err := runNativePrepareWithShell(shell, args...)
			if err != nil {
				t.Fatalf("retry prepare failed: %v\n%s", err, output)
			}
			if got := requests.Load(); got != 2 {
				t.Fatalf("retry prepare requests = %d, want 2", got)
			}
			if !strings.Contains(output, "Downloading "+nativePrepareAsset+" attempt=2/2") {
				t.Fatalf("retry attempt was not reported:\n%s", output)
			}
			assertNativePrepareInstalled(t, destination, fixture.dll)
			assertNoNativePrepareTemps(t, cache, destination)
		})
	}
}

func TestNativePrepareSerializesConcurrentColdDownload(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write(fixture.archive)
	}))
	defer server.Close()

	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	args := nativePrepareArgs(cache, destination, server.URL, fixture.hash)
	start := make(chan struct{})
	results := make(chan string, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			output, err := runNativePrepare(args...)
			results <- fmt.Sprintf("err=%v\n%s", err, output)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for result := range results {
		if !strings.Contains(result, "err=<nil>") {
			t.Fatalf("concurrent prepare failed:\n%s", result)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("concurrent cold cache downloads = %d, want 1", got)
	}
	assertNativePrepareInstalled(t, destination, fixture.dll)
	assertNoNativePrepareTemps(t, cache, destination)
}

func TestNativePrepareBadDownloadHashCleansPartial(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture.archive)
	}))
	defer server.Close()

	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	output, err := runNativePrepare(nativePrepareArgs(
		cache, destination, server.URL, strings.Repeat("0", 64),
	)...)
	if err == nil || !strings.Contains(output, "native archive SHA-256 mismatch") {
		t.Fatalf("bad archive hash was not rejected: err=%v\n%s", err, output)
	}
	if _, statErr := os.Stat(filepath.Join(cache, nativePrepareAsset)); !os.IsNotExist(statErr) {
		t.Fatalf("untrusted archive entered cache: err=%v", statErr)
	}
	assertNoNativePrepareTemps(t, cache, destination)
}

func TestNativePrepareRejectsMismatchedHotCacheWithoutNetwork(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(fixture.archive)
	}))
	defer server.Close()

	for _, offline := range []bool{false, true} {
		name := "online"
		if offline {
			name = "offline"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "cache")
			destination := filepath.Join(root, "destination")
			writeNativePrepareCache(t, cache, fixture.archive)
			args := nativePrepareArgs(cache, destination, server.URL, strings.Repeat("0", 64))
			if offline {
				args = append(args, "-Offline")
			}
			output, err := runNativePrepare(args...)
			if err == nil || !strings.Contains(output, "native archive SHA-256 mismatch") {
				t.Fatalf("mismatched hot cache was not rejected: err=%v\n%s", err, output)
			}
			assertNoNativePrepareTemps(t, cache, destination)
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("mismatched hot cache caused %d network requests, want 0", got)
	}
}

func TestNativePrepareStrictManifest(t *testing.T) {
	manifestCases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "missing-schema", mutate: func(m map[string]any) { delete(m, "schema") }, want: "missing required field: schema"},
		{name: "schema", mutate: func(m map[string]any) { m["schema"] = "wrong" }, want: "field mismatch: schema"},
		{name: "native-version", mutate: func(m map[string]any) { m["nativeVersion"] = "17.3.3-abi1" }, want: "field mismatch: nativeVersion"},
		{name: "frida-version", mutate: func(m map[string]any) { m["fridaCoreVersion"] = "17.3.1" }, want: "field mismatch: fridaCoreVersion"},
		{name: "zlib-version", mutate: func(m map[string]any) { m["zlibVersion"] = "1.3.0" }, want: "field mismatch: zlibVersion"},
		{name: "abi-type", mutate: func(m map[string]any) { m["abiVersion"] = "1" }, want: "field mismatch: abiVersion"},
		{name: "abi-value", mutate: func(m map[string]any) { m["abiVersion"] = 2 }, want: "field mismatch: abiVersion"},
		{name: "os", mutate: func(m map[string]any) { m["os"] = "linux" }, want: "field mismatch: os"},
		{name: "arch", mutate: func(m map[string]any) { m["arch"] = "arm64" }, want: "field mismatch: arch"},
		{name: "dll", mutate: func(m map[string]any) { m["dll"] = "other.dll" }, want: "field mismatch: dll"},
		{name: "size-type", mutate: func(m map[string]any) { m["size"] = "25" }, want: "field mismatch: size"},
		{name: "size-value", mutate: func(m map[string]any) { m["size"] = int64(999) }, want: "field mismatch: size"},
		{name: "hash", mutate: func(m map[string]any) { m["sha256"] = strings.Repeat("0", 64) }, want: "DLL SHA-256 mismatch"},
		{name: "exports-missing", mutate: func(m map[string]any) { m["requiredExports"] = append([]string(nil), nativePrepareExports[1:]...) }, want: "required export set mismatch"},
		{name: "exports-extra", mutate: func(m map[string]any) {
			m["requiredExports"] = append(append([]string(nil), nativePrepareExports...), "unexpected")
		}, want: "required export set mismatch"},
		{name: "exports-duplicate", mutate: func(m map[string]any) {
			exports := append([]string(nil), nativePrepareExports...)
			exports[0] = exports[1]
			m["requiredExports"] = exports
		}, want: "required export set mismatch"},
	}

	for _, tc := range manifestCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{mutateManifest: tc.mutate})
			assertRejectedNativePrepareFixture(t, fixture, tc.want)
		})
	}

	t.Run("invalid-json", func(t *testing.T) {
		fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{manifestBytes: []byte("{not-json")})
		assertRejectedNativePrepareFixture(t, fixture, "manifest is not valid JSON")
	})
}

func TestNativePrepareRejectsMissingDuplicateAndUnsafeEntries(t *testing.T) {
	cases := []struct {
		name    string
		options nativePrepareArchiveOptions
		want    string
	}{
		{name: "missing-manifest", options: nativePrepareArchiveOptions{omitManifest: true}, want: "exactly one root manifest.json"},
		{name: "missing-dll", options: nativePrepareArchiveOptions{omitDLL: true}, want: "exactly one root manifest.json"},
		{name: "duplicate-manifest", options: nativePrepareArchiveOptions{duplicateManifest: true}, want: "exactly one root manifest.json"},
		{name: "parent", options: nativePrepareArchiveOptions{extraEntries: []nativePrepareEntry{{name: "../escape.txt", data: []byte("escape")}}}, want: "path traversal"},
		{name: "root", options: nativePrepareArchiveOptions{extraEntries: []nativePrepareEntry{{name: "/rooted.txt", data: []byte("escape")}}}, want: "path traversal"},
		{name: "drive-forward", options: nativePrepareArchiveOptions{extraEntries: []nativePrepareEntry{{name: "C:/escape.txt", data: []byte("escape")}}}, want: "path traversal"},
		{name: "drive-backslash", options: nativePrepareArchiveOptions{extraEntries: []nativePrepareEntry{{name: `C:\escape.txt`, data: []byte("escape")}}}, want: "path traversal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := makeNativePrepareFixture(t, tc.options)
			assertRejectedNativePrepareFixture(t, fixture, tc.want)
		})
	}
}

func TestNativePrepareAtomicPublishContract(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	scriptPath := filepath.Join(filepath.Dir(source), "native-prepare.ps1")
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, token := range []string{
		"$temporaryDLL = \"$installed.partial\"",
		"$temporaryManifest = \"$installedManifest.partial\"",
		"$destinationLockPath = Join-Path $stateRoot 'update.lock'",
		"$journalPath = Join-Path $stateRoot 'transaction.json'",
		"$currentPath = Join-Path $stateRoot 'current.json'",
		"function Recover-NativeTransaction",
		"phase = 'prepare'",
		"phase = 'verify'",
		"phase = 'publish'",
		"phase = 'cleanup'",
		"Move-Item -LiteralPath $versionStage -Destination $versionDirectory",
		"Move-Item -LiteralPath $temporaryManifest -Destination $installedManifest -Force",
		"Move-Item -LiteralPath $temporaryDLL -Destination $installed -Force",
		"Remove-Item -LiteralPath $partial,$temporaryDLL,$temporaryManifest",
	} {
		if !strings.Contains(text, token) {
			t.Errorf("native prepare atomic contract missing %q", token)
		}
	}
	prepare := strings.Index(text, "phase = 'prepare'")
	verify := strings.Index(text, "phase = 'verify'")
	publish := strings.Index(text, "phase = 'publish'")
	manifestPublish := strings.Index(text, "Move-Item -LiteralPath $temporaryManifest -Destination $installedManifest -Force")
	dllPublish := strings.Index(text, "Move-Item -LiteralPath $temporaryDLL -Destination $installed -Force")
	if prepare < 0 || verify < prepare || publish < verify || manifestPublish < 0 || dllPublish < manifestPublish {
		t.Fatal("native transaction must prepare, verify and publish an immutable version before committing the DLL marker")
	}
}

func TestNativePrepareVersionedPublishRollbackAndRetention(t *testing.T) {
	first := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("first native DLL")})
	second := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("second native DLL")})
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")

	writeNativePrepareCache(t, cache, first.archive)
	args := append(nativePrepareArgs(cache, destination, "http://127.0.0.1:1/unreachable", first.hash), "-Offline")
	if output, err := runNativePrepare(args...); err != nil {
		t.Fatalf("first version publish failed: %v\n%s", err, output)
	}
	writeNativePrepareCache(t, cache, second.archive)
	args = append(nativePrepareArgs(cache, destination, "http://127.0.0.1:1/unreachable", second.hash), "-Offline")
	if output, err := runNativePrepare(args...); err != nil {
		t.Fatalf("second version publish failed: %v\n%s", err, output)
	}
	assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), second.dll)

	rollbackArgs := []string{"-CacheDirectory", cache, "-DestinationDirectory", destination, "-ExpectedArchiveSHA256", first.hash, "-Rollback"}
	output, err := runNativePrepare(rollbackArgs...)
	if err != nil || !strings.Contains(output, "native_rollback=") {
		t.Fatalf("rollback failed: %v\n%s", err, output)
	}
	assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), first.dll)
	output, err = runNativePrepare(rollbackArgs...)
	if err != nil || !strings.Contains(output, "native_rollback=noop") {
		t.Fatalf("idempotent rollback failed: %v\n%s", err, output)
	}
	assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), first.dll)
	versions, err := os.ReadDir(filepath.Join(destination, ".native-runtime", "versions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("retained version count=%d, want 2", len(versions))
	}
	trust, err := os.ReadDir(filepath.Join(destination, ".native-runtime", "trust"))
	if err != nil {
		t.Fatal(err)
	}
	if len(trust) != len(versions) {
		t.Fatalf("trust record count=%d, want %d", len(trust), len(versions))
	}
}

func TestNativePrepareRollbackNoopRevalidatesCurrentGeneration(t *testing.T) {
	_, _, destination, retained, retainedHash := prepareNativeRollbackPair(t)
	firstRollback := nativeRollbackArgs(destination, retainedHash)
	if output, err := runNativePrepare(firstRollback...); err != nil {
		t.Fatalf("initial rollback failed: %v\n%s", err, output)
	}
	for _, name := range []string{"miniapp-frida.dll", "manifest.json", "source.zip"} {
		path := filepath.Join(retained, name)
		if err := os.WriteFile(path, []byte("tampered current retained "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		output, err := runNativePrepare(firstRollback...)
		if err == nil || strings.Contains(output, "native_rollback=noop") {
			t.Fatalf("tampered %s was treated as noop: err=%v\n%s", name, err, output)
		}
		// Recreate the fixture for the next independent artifact mutation.
		_, _, destination, retained, retainedHash = prepareNativeRollbackPair(t)
		if output, err := runNativePrepare(nativeRollbackArgs(destination, retainedHash)...); err != nil {
			t.Fatalf("fixture rollback failed: %v\n%s", err, output)
		}
	}
}

func TestNativePrepareInterruptedPublishRejectsTamperedPreviousVersion(t *testing.T) {
	first := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("journal previous")})
	second := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("journal candidate")})
	root := t.TempDir()
	cache, destination := filepath.Join(root, "cache"), filepath.Join(root, "destination")
	writeNativePrepareCache(t, cache, first.archive)
	if output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", first.hash), "-Offline")...); err != nil {
		t.Fatalf("baseline: %v\n%s", err, output)
	}
	previous := readNativeCurrentVersionDirectory(t, destination)
	if err := os.WriteFile(filepath.Join(previous, "source.zip"), []byte("tampered archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	journal, _ := json.Marshal(map[string]string{"phase": "publish", "stage": "", "previousVersionDirectory": previous})
	state := filepath.Join(destination, ".native-runtime")
	if err := os.WriteFile(filepath.Join(state, "transaction.json"), journal, 0o600); err != nil {
		t.Fatal(err)
	}
	writeNativePrepareCache(t, cache, second.archive)
	output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", second.hash), "-Offline")...)
	if err == nil || strings.Contains(output, "native_rollback=") {
		t.Fatalf("tampered journal recovery published: %v\n%s", err, output)
	}
}

func TestNativePrepareCanaryFailureRejectsTamperedPreviousVersion(t *testing.T) {
	first := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("canary previous")})
	second := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("canary candidate")})
	root := t.TempDir()
	cache, destination := filepath.Join(root, "cache"), filepath.Join(root, "destination")
	writeNativePrepareCache(t, cache, first.archive)
	if output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", first.hash), "-Offline")...); err != nil {
		t.Fatalf("baseline: %v\n%s", err, output)
	}
	previous := readNativeCurrentVersionDirectory(t, destination)
	if err := os.WriteFile(filepath.Join(previous, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeNativePrepareCache(t, cache, second.archive)
	output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", second.hash), "-Offline", "-CanaryCommand", "cmd /c exit 7")...)
	if err == nil || strings.Contains(output, "native canary failed") {
		t.Fatalf("tampered previous version was accepted: %v\n%s", err, output)
	}
}

func TestNativePrepareInterruptedPublishRejectsSelfConsistentPreviousReplacement(t *testing.T) {
	previous := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("journal pinned DLL")})
	candidate := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("journal next DLL")})
	root := t.TempDir()
	cache, destination := filepath.Join(root, "cache"), filepath.Join(root, "destination")
	publishNativePrepareFixture(t, cache, destination, previous)
	state := filepath.Join(destination, ".native-runtime")
	currentPath := filepath.Join(state, "current.json")
	currentBytes, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	pointer := readNativeCurrentPointer(t, destination)
	replaceNativeGenerationWithArchiveVariant(t, destination, previous, "replacement journal archive")
	journal, err := json.Marshal(map[string]string{
		"phase": "publish", "stage": "", "previousVersionDirectory": pointer.VersionDirectory,
		"previousSHA256": pointer.SHA256, "previousArchiveSHA256": pointer.ArchiveSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(state, "transaction.json")
	if err := os.WriteFile(journalPath, journal, 0o600); err != nil {
		t.Fatal(err)
	}
	writeNativePrepareCache(t, cache, candidate.archive)
	output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", candidate.hash), "-Offline")...)
	if err == nil || !strings.Contains(output, "native retained version does not match its trust record") {
		t.Fatalf("self-consistent journal replacement was accepted: err=%v\n%s", err, output)
	}
	assertFileContent(t, currentPath, currentBytes)
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatalf("failed recovery removed journal: %v", err)
	}
}

func TestNativePrepareCanaryRejectsSelfConsistentPreviousReplacement(t *testing.T) {
	previous := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("canary pinned DLL")})
	candidate := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("canary next DLL")})
	root := t.TempDir()
	cache, destination := filepath.Join(root, "cache"), filepath.Join(root, "destination")
	publishNativePrepareFixture(t, cache, destination, previous)
	state := filepath.Join(destination, ".native-runtime")
	currentPath, rollbackPath := filepath.Join(state, "current.json"), filepath.Join(state, "rollback.json")
	currentBytes, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	rollbackBytes := []byte("{\"sentinel\":\"rollback\"}\n")
	if err := os.WriteFile(rollbackPath, rollbackBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	replaceNativeGenerationWithArchiveVariant(t, destination, previous, "replacement canary archive")
	writeNativePrepareCache(t, cache, candidate.archive)
	output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", candidate.hash),
		"-Offline", "-CanaryCommand", "cmd /c exit 7")...)
	if err == nil || !strings.Contains(output, "native retained version does not match its trust record") ||
		strings.Contains(output, "native canary failed") {
		t.Fatalf("self-consistent canary recovery source was accepted: err=%v\n%s", err, output)
	}
	assertNativePrepareInstalled(t, destination, previous.dll)
	assertFileContent(t, currentPath, currentBytes)
	assertFileContent(t, rollbackPath, rollbackBytes)
}

func TestNativePrepareRejectsCurrentPointerArchivePinDowngrade(t *testing.T) {
	previous := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("pinned current DLL")})
	candidate := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("downgrade candidate DLL")})
	root := t.TempDir()
	cache, destination := filepath.Join(root, "cache"), filepath.Join(root, "destination")
	publishNativePrepareFixture(t, cache, destination, previous)
	state := filepath.Join(destination, ".native-runtime")
	currentPath := filepath.Join(state, "current.json")
	pointer := readNativeCurrentPointer(t, destination)
	downgraded, err := json.Marshal(map[string]string{
		"versionDirectory": pointer.VersionDirectory,
		"nativeVersion":    "17.3.2-abi1",
		"sha256":           pointer.SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, downgraded, 0o600); err != nil {
		t.Fatal(err)
	}
	writeNativePrepareCache(t, cache, candidate.archive)
	output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", candidate.hash), "-Offline")...)
	if err == nil || !strings.Contains(output, "native previous pointer has no archive SHA-256") {
		t.Fatalf("archive pin downgrade was accepted: err=%v\n%s", err, output)
	}
	assertNativePrepareInstalled(t, destination, previous.dll)
	assertFileContent(t, currentPath, downgraded)
}

func TestNativePrepareLegacyInstallDoesNotCreateVersionTrustState(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
	root := t.TempDir()
	cache, destination := filepath.Join(root, "cache"), filepath.Join(root, "destination")
	writeNativePrepareCache(t, cache, fixture.archive)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "miniapp-frida.dll"), []byte("legacy DLL"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "manifest.json"), []byte("legacy manifest"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", fixture.hash), "-Offline")...); err != nil {
		t.Fatalf("legacy migration failed: %v\n%s", err, output)
	}
	state := filepath.Join(destination, ".native-runtime")
	current := readNativeCurrentVersionDirectory(t, destination)
	if strings.Contains(filepath.Base(current), "legacy-") {
		t.Fatal("legacy install was registered as the current retained version")
	}
	if entries, err := os.ReadDir(filepath.Join(state, "versions")); err != nil || len(entries) != 1 {
		t.Fatalf("expected only the newly verified version, entries=%v err=%v", entries, err)
	}
	if entries, err := os.ReadDir(filepath.Join(state, "trust")); err != nil || len(entries) != 1 {
		t.Fatalf("expected only the newly verified trust record, entries=%v err=%v", entries, err)
	}
}

func TestNativePrepareRollbackRejectsTamperedRetainedVersion(t *testing.T) {
	_, _, destination, retained, retainedHash := prepareNativeRollbackPair(t)
	attacker := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("attacker-controlled retained DLL")})
	attackerRoot := t.TempDir()
	attackerCache := filepath.Join(attackerRoot, "cache")
	attackerDestination := filepath.Join(attackerRoot, "destination")
	writeNativePrepareCache(t, attackerCache, attacker.archive)
	args := append(nativePrepareArgs(attackerCache, attackerDestination, "unused", attacker.hash), "-Offline")
	if output, err := runNativePrepare(args...); err != nil {
		t.Fatalf("attacker generation prepare failed: %v\n%s", err, output)
	}
	attackerVersion := readNativeCurrentVersionDirectory(t, attackerDestination)
	attackerTrust := filepath.Join(attackerDestination, ".native-runtime", "trust", filepath.Base(attackerVersion)+".json")
	victimTrustRoot := filepath.Join(destination, ".native-runtime", "trust")
	if err := os.RemoveAll(retained); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(victimTrustRoot, filepath.Base(retained)+".json")); err != nil {
		t.Fatal(err)
	}
	tamperedRetained := filepath.Join(filepath.Dir(retained), filepath.Base(attackerVersion))
	if err := os.Rename(attackerVersion, tamperedRetained); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(attackerTrust, filepath.Join(victimTrustRoot, filepath.Base(attackerTrust))); err != nil {
		t.Fatal(err)
	}

	output, err := runNativePrepareWithSecurity("valid", "", nativeRollbackArgs(destination, retainedHash)...)
	if err == nil || !strings.Contains(output, "native retained version does not match its trust record") {
		t.Fatalf("tampered retained version was not rejected: err=%v\n%s", err, output)
	}
}

func TestNativePrepareVersionedStagingRetainsVerifiedSourceArchive(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("verified retained archive DLL")})
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	writeNativePrepareCache(t, cache, fixture.archive)
	args := append(nativePrepareArgs(cache, destination, "unused", fixture.hash), "-Offline")
	if output, err := runNativePrepare(args...); err != nil {
		t.Fatalf("versioned staging failed: %v\n%s", err, output)
	}
	versionDirectory := readNativeCurrentVersionDirectory(t, destination)
	retainedArchive := filepath.Join(versionDirectory, "source.zip")
	archive, err := os.ReadFile(retainedArchive)
	if err != nil {
		t.Fatal(err)
	}
	if got := nativePrepareSHA(archive); got != fixture.hash {
		t.Fatalf("retained source.zip hash = %s, want %s", got, fixture.hash)
	}
	if !bytes.Equal(archive, fixture.archive) {
		t.Fatal("retained source.zip differs from the verified staging archive")
	}
}

func TestNativePrepareRollbackRejectsSignatureAndTimestampFailures(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   string
	}{
		{status: "invalid", want: "Authenticode signature is invalid"},
		{status: "missing", want: "Authenticode signature is missing"},
		{status: "missing-timestamp", want: "Authenticode trusted timestamp is missing"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			_, _, destination, _, retainedHash := prepareNativeRollbackPair(t)
			output, err := runNativePrepareWithSecurity(tc.status, "", nativeRollbackArgs(destination, retainedHash)...)
			if err == nil || !strings.Contains(output, tc.want) {
				t.Fatalf("rollback signature state %q was not rejected: err=%v\n%s", tc.status, err, output)
			}
		})
	}
}

func TestNativePrepareRollbackRejectsHardlinkAndReparseCandidates(t *testing.T) {
	t.Run("hardlink", func(t *testing.T) {
		_, _, destination, retained, retainedHash := prepareNativeRollbackPair(t)
		dllPath := filepath.Join(retained, "miniapp-frida.dll")
		linkPath := filepath.Join(retained, "native-hardlink.dll")
		if err := os.Link(dllPath, linkPath); err != nil {
			t.Skipf("hard links unavailable: %v", err)
		}
		output, err := runNativePrepare(nativeRollbackArgs(destination, retainedHash)...)
		if err == nil || !strings.Contains(output, "exactly one hard link") {
			t.Fatalf("hardlinked retained DLL was not rejected: err=%v\n%s", err, output)
		}
	})

	t.Run("reparse", func(t *testing.T) {
		_, _, destination, retained, retainedHash := prepareNativeRollbackPair(t)
		dllPath := filepath.Join(retained, "miniapp-frida.dll")
		targetPath := filepath.Join(t.TempDir(), "retained-target.dll")
		data, err := os.ReadFile(dllPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(targetPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(dllPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(targetPath, dllPath); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		output, err := runNativePrepare(nativeRollbackArgs(destination, retainedHash)...)
		if err == nil || !strings.Contains(output, "reparse point") {
			t.Fatalf("reparse retained DLL was not rejected: err=%v\n%s", err, output)
		}
	})
}

func TestNativePrepareRollbackRejectsReplacementAfterValidation(t *testing.T) {
	_, currentDLL, destination, _, retainedHash := prepareNativeRollbackPair(t)
	output, err := runNativePrepareWithSecurity("valid", "after-rollback-validation", nativeRollbackArgs(destination, retainedHash)...)
	if err == nil || !strings.Contains(output, "native rollback source identity changed after validation") {
		t.Fatalf("rollback source replacement was not rejected: err=%v\n%s", err, output)
	}
	assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), currentDLL)
}

func TestNativePrepareRollbackPostPublishMismatchRestoresCurrent(t *testing.T) {
	_, currentDLL, destination, _, retainedHash := prepareNativeRollbackPair(t)
	currentPath := filepath.Join(destination, ".native-runtime", "current.json")
	rollbackPath := filepath.Join(destination, ".native-runtime", "rollback.json")
	rollbackBefore := []byte("{\"sentinel\":\"rollback\"}\n")
	if err := os.WriteFile(rollbackPath, rollbackBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runNativePrepareWithSecurity("valid", "after-rollback-publish", nativeRollbackArgs(destination, retainedHash)...)
	if err == nil || !strings.Contains(output, "native rollback published validation failed") {
		t.Fatalf("post-publish mismatch was not rejected: err=%v\n%s", err, output)
	}
	assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), currentDLL)
	after, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("failed rollback changed current pointer:\nbefore=%s\nafter=%s", before, after)
	}
	assertFileContent(t, rollbackPath, rollbackBefore)
}

func TestNativePrepareRollbackRejectsSelfConsistentCurrentReplacement(t *testing.T) {
	target := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("rollback pinned target")})
	current := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("rollback pinned current")})
	root := t.TempDir()
	cache, destination := filepath.Join(root, "cache"), filepath.Join(root, "destination")
	publishNativePrepareFixture(t, cache, destination, target)
	publishNativePrepareFixture(t, cache, destination, current)
	state := filepath.Join(destination, ".native-runtime")
	currentPath, rollbackPath := filepath.Join(state, "current.json"), filepath.Join(state, "rollback.json")
	currentBytes, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	rollbackBytes := []byte("{\"sentinel\":\"rollback\"}\n")
	if err := os.WriteFile(rollbackPath, rollbackBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	replaceNativeGenerationWithArchiveVariant(t, destination, current, "replacement current archive")
	output, err := runNativePrepareWithSecurity("valid", "after-rollback-publish", nativeRollbackArgs(destination, target.hash)...)
	if err == nil || !strings.Contains(output, "native retained version does not match its trust record") ||
		strings.Contains(output, "native rollback published validation failed") {
		t.Fatalf("self-consistent rollback recovery source was accepted: err=%v\n%s", err, output)
	}
	assertNativePrepareInstalled(t, destination, current.dll)
	assertFileContent(t, currentPath, currentBytes)
	assertFileContent(t, rollbackPath, rollbackBytes)
}

func TestNativePrepareMultiRoundUpdateSoak(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	for round := 0; round < 5; round++ {
		fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{
			dll: []byte(fmt.Sprintf("native update soak round %d", round)),
		})
		writeNativePrepareCache(t, cache, fixture.archive)
		args := append(nativePrepareArgs(cache, destination, "unused", fixture.hash), "-Offline")
		if output, err := runNativePrepare(args...); err != nil {
			t.Fatalf("round %d publish failed: %v\n%s", round, err, output)
		}
		assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), fixture.dll)
		pointerBytes, err := os.ReadFile(filepath.Join(destination, ".native-runtime", "current.json"))
		if err != nil {
			t.Fatalf("round %d current pointer: %v", round, err)
		}
		var pointer struct {
			VersionDirectory string `json:"versionDirectory"`
		}
		if err := json.Unmarshal(pointerBytes, &pointer); err != nil || pointer.VersionDirectory == "" {
			t.Fatalf("round %d invalid current pointer: %v %s", round, err, pointerBytes)
		}
		if info, err := os.Stat(pointer.VersionDirectory); err != nil || !info.IsDir() {
			t.Fatalf("round %d current version directory: info=%v err=%v", round, info, err)
		}
		assertNoNativePrepareTemps(t, cache, destination)
	}
	versions, err := os.ReadDir(filepath.Join(destination, ".native-runtime", "versions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("retained version count after soak=%d, want 2", len(versions))
	}
}

func TestNativePrepareCanaryFailureRollsBack(t *testing.T) {
	first := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("canary baseline DLL")})
	second := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("canary candidate DLL")})
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	writeNativePrepareCache(t, cache, first.archive)
	if output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", first.hash), "-Offline")...); err != nil {
		t.Fatalf("baseline publish failed: %v\n%s", err, output)
	}
	writeNativePrepareCache(t, cache, second.archive)
	args := append(nativePrepareArgs(cache, destination, "unused", second.hash), "-Offline", "-CanaryCommand", "cmd /c exit 7")
	output, err := runNativePrepare(args...)
	if err == nil || !strings.Contains(output, "native canary failed with exit code 7") {
		t.Fatalf("canary failure was not propagated: %v\n%s", err, output)
	}
	assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), first.dll)
}

func TestNativePrepareRecoversInterruptedPublishJournal(t *testing.T) {
	first := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("journal baseline DLL")})
	second := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("journal candidate DLL")})
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	writeNativePrepareCache(t, cache, first.archive)
	if output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", first.hash), "-Offline")...); err != nil {
		t.Fatalf("baseline publish failed: %v\n%s", err, output)
	}
	state := filepath.Join(destination, ".native-runtime")
	firstPointer, err := os.ReadFile(filepath.Join(state, "current.json"))
	if err != nil {
		t.Fatal(err)
	}
	var firstCurrent nativeCurrentPointer
	if err := json.Unmarshal(firstPointer, &firstCurrent); err != nil {
		t.Fatal(err)
	}
	writeNativePrepareCache(t, cache, second.archive)
	if output, err := runNativePrepare(append(nativePrepareArgs(cache, destination, "unused", second.hash), "-Offline")...); err != nil {
		t.Fatalf("candidate publish failed: %v\n%s", err, output)
	}
	journal, err := json.Marshal(map[string]string{
		"phase": "publish", "stage": "", "previousVersionDirectory": firstCurrent.VersionDirectory,
		"previousSHA256": firstCurrent.SHA256, "previousArchiveSHA256": firstCurrent.ArchiveSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "transaction.json"), journal, 0o600); err != nil {
		t.Fatal(err)
	}
	args := append(nativePrepareArgs(cache, destination, "unused", second.hash), "-Offline")
	output, err := runNativePrepareWithPublishFailure("after-dll-backup", args...)
	if err == nil || !strings.Contains(output, "injected native publish failure") {
		t.Fatalf("post-recovery failure was not injected: %v\n%s", err, output)
	}
	assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), first.dll)
	if _, err := os.Stat(filepath.Join(state, "transaction.json")); !os.IsNotExist(err) {
		t.Fatalf("recovery journal remains: %v", err)
	}
}

func TestNativePrepareDestinationLockTimeout(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	state := filepath.Join(destination, ".native-runtime")
	if err := os.MkdirAll(filepath.Join(state, "versions"), 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(state, "update.lock")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	output, runErr := runNativePrepare("-CacheDirectory", cache, "-DestinationDirectory", destination,
		"-ExpectedArchiveSHA256", strings.Repeat("A", 64), "-Rollback", "-LockTimeoutSeconds", "1")
	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	if runErr == nil || !strings.Contains(output, "native destination lock timeout") {
		t.Fatalf("destination lock did not time out: %v\n%s", runErr, output)
	}
}

func TestNativePreparePublishFailureRestoresPreviousInstall(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	writeNativePrepareCache(t, cache, fixture.archive)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	dllPath := filepath.Join(destination, "miniapp-frida.dll")
	manifestPath := filepath.Join(destination, "manifest.json")
	oldDLL := []byte("previous DLL marker")
	oldManifest := []byte("locked previous manifest")
	if err := os.WriteFile(dllPath, oldDLL, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, oldManifest, 0o644); err != nil {
		t.Fatal(err)
	}

	name, err := syscall.UTF16PtrFromString(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ,
		0,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("lock installed manifest: %v", err)
	}

	args := append(nativePrepareArgs(
		cache, destination, "http://127.0.0.1:1/unreachable", fixture.hash,
	), "-Offline")
	output, runErr := runNativePrepare(args...)
	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	if runErr == nil {
		t.Fatalf("publish unexpectedly succeeded with locked manifest:\n%s", output)
	}
	assertFileContent(t, dllPath, oldDLL)
	assertFileContent(t, manifestPath, oldManifest)
	assertNoNativePrepareTemps(t, cache, destination)
}

func TestNativePrepareRollsBackEveryPublishStep(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
	for _, step := range []string{
		"after-dll-backup",
		"after-manifest-backup",
		"after-manifest-publish",
		"after-dll-publish",
	} {
		t.Run(step, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "cache")
			destination := filepath.Join(root, "destination")
			writeNativePrepareCache(t, cache, fixture.archive)
			if err := os.MkdirAll(destination, 0o755); err != nil {
				t.Fatal(err)
			}
			oldDLL := []byte("previous trusted DLL")
			oldManifest := []byte("previous trusted manifest")
			if err := os.WriteFile(filepath.Join(destination, "miniapp-frida.dll"), oldDLL, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(destination, "manifest.json"), oldManifest, 0o644); err != nil {
				t.Fatal(err)
			}

			args := append(nativePrepareArgs(cache, destination, "http://127.0.0.1:1/unreachable", fixture.hash), "-Offline")
			output, err := runNativePrepareWithPublishFailure(step, args...)
			if err == nil || !strings.Contains(output, "injected native publish failure: "+step) {
				t.Fatalf("publish failure %q was not propagated: err=%v\n%s", step, err, output)
			}
			assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), oldDLL)
			assertFileContent(t, filepath.Join(destination, "manifest.json"), oldManifest)
			assertNoNativePrepareTemps(t, cache, destination)
		})
	}
}

func TestNativePrepareLockedDLLAbortsBeforeManifestPublish(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	writeNativePrepareCache(t, cache, fixture.archive)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	dllPath := filepath.Join(destination, "miniapp-frida.dll")
	manifestPath := filepath.Join(destination, "manifest.json")
	oldDLL := []byte("previous DLL marker")
	oldManifest := []byte("previous trusted manifest")
	if err := os.WriteFile(dllPath, oldDLL, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, oldManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(dllPath)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ,
		0,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("lock installed DLL: %v", err)
	}
	output, runErr := runNativePrepare(append(nativePrepareArgs(
		cache, destination, "http://127.0.0.1:1/unreachable", fixture.hash,
	), "-Offline")...)
	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	if runErr == nil {
		t.Fatalf("locked DLL publish unexpectedly succeeded:\n%s", output)
	}
	assertFileContent(t, dllPath, oldDLL)
	assertFileContent(t, manifestPath, oldManifest)
	assertNoNativePrepareTemps(t, cache, destination)
}

func TestNativePrepareLockTimeoutDoesNotDeleteOwnerTemps(t *testing.T) {
	fixture := makeNativePrepareFixture(t, nativePrepareArchiveOptions{})
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	partialPath := filepath.Join(cache, nativePrepareAsset+".partial")
	stagePath := filepath.Join(cache, nativePrepareAsset+".extracting")
	temporaryDLL := filepath.Join(destination, "miniapp-frida.dll.partial")
	temporaryManifest := filepath.Join(destination, "manifest.json.partial")
	for path, data := range map[string][]byte{
		partialPath:       []byte("owner download"),
		temporaryDLL:      []byte("owner DLL partial"),
		temporaryManifest: []byte("owner manifest partial"),
	} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(stagePath, 0o755); err != nil {
		t.Fatal(err)
	}
	stageSentinel := filepath.Join(stagePath, "owner")
	if err := os.WriteFile(stageSentinel, []byte("owner extraction"), 0o644); err != nil {
		t.Fatal(err)
	}

	lockPath := filepath.Join(cache, nativePrepareAsset+".lock")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("lock cache: %v", err)
	}
	args := nativePrepareArgs(cache, destination, "http://127.0.0.1:1/unreachable", fixture.hash)
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-LockTimeoutSeconds" {
			args[i+1] = "1"
		}
	}
	output, runErr := runNativePrepare(args...)
	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	if runErr == nil || !strings.Contains(output, "native cache lock timeout") {
		t.Fatalf("locked cache did not time out: err=%v\n%s", runErr, output)
	}
	assertFileContent(t, partialPath, []byte("owner download"))
	assertFileContent(t, temporaryDLL, []byte("owner DLL partial"))
	assertFileContent(t, temporaryManifest, []byte("owner manifest partial"))
	assertFileContent(t, stageSentinel, []byte("owner extraction"))
}

func makeNativePrepareFixture(t *testing.T, options nativePrepareArchiveOptions) nativePrepareFixture {
	t.Helper()
	dll := []byte("fixture miniapp-frida DLL bytes\x00\x01\x02")
	if options.dll != nil {
		dll = options.dll
	}
	manifest := map[string]any{
		"schema":           "miniapp-bridge.native-manifest.v1",
		"nativeVersion":    "17.3.2-abi1",
		"fridaCoreVersion": "17.3.2",
		"zlibVersion":      "1.3.1",
		"abiVersion":       1,
		"os":               "windows",
		"arch":             "amd64",
		"dll":              "miniapp-frida.dll",
		"size":             int64(len(dll)),
		"sha256":           nativePrepareSHA(dll),
		"requiredExports":  append([]string(nil), nativePrepareExports...),
	}
	if options.mutateManifest != nil {
		options.mutateManifest(manifest)
	}
	manifestBytes := options.manifestBytes
	if manifestBytes == nil {
		var err error
		manifestBytes, err = json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
	}

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	if !options.omitManifest {
		writeNativePrepareZipEntry(t, writer, "manifest.json", manifestBytes)
		if options.duplicateManifest {
			writeNativePrepareZipEntry(t, writer, "manifest.json", manifestBytes)
		}
	}
	if !options.omitDLL {
		writeNativePrepareZipEntry(t, writer, "miniapp-frida.dll", dll)
	}
	for _, entry := range options.extraEntries {
		writeNativePrepareZipEntry(t, writer, entry.name, entry.data)
	}
	if err := writer.SetComment(options.archiveComment); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archive := buffer.Bytes()
	return nativePrepareFixture{archive: append([]byte(nil), archive...), hash: nativePrepareSHA(archive), dll: dll}
}

func writeNativePrepareZipEntry(t *testing.T, writer *zip.Writer, name string, data []byte) {
	t.Helper()
	entry, err := writer.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(data); err != nil {
		t.Fatal(err)
	}
}

func writeNativePrepareCache(t *testing.T, cache string, archive []byte) {
	t.Helper()
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, nativePrepareAsset), archive, 0o644); err != nil {
		t.Fatal(err)
	}
}

func publishNativePrepareFixture(t *testing.T, cache, destination string, fixture nativePrepareFixture) {
	t.Helper()
	writeNativePrepareCache(t, cache, fixture.archive)
	args := append(nativePrepareArgs(cache, destination, "unused", fixture.hash), "-Offline")
	if output, err := runNativePrepare(args...); err != nil {
		t.Fatalf("native fixture publish failed: %v\n%s", err, output)
	}
}

func replaceNativeGenerationWithArchiveVariant(
	t *testing.T, destination string, original nativePrepareFixture, comment string,
) {
	t.Helper()
	pointer := readNativeCurrentPointer(t, destination)
	replacement := makeNativePrepareFixture(t, nativePrepareArchiveOptions{
		dll: original.dll, archiveComment: comment,
	})
	if replacement.hash == original.hash {
		t.Fatal("archive variant did not change archive SHA-256")
	}
	root := t.TempDir()
	replacementDestination := filepath.Join(root, "destination")
	publishNativePrepareFixture(t, filepath.Join(root, "cache"), replacementDestination, replacement)
	replacementVersion := readNativeCurrentVersionDirectory(t, replacementDestination)
	if filepath.Base(replacementVersion) != filepath.Base(pointer.VersionDirectory) {
		t.Fatalf("replacement version directory changed: %q != %q", replacementVersion, pointer.VersionDirectory)
	}
	trustName := filepath.Base(pointer.VersionDirectory) + ".json"
	victimTrust := filepath.Join(destination, ".native-runtime", "trust", trustName)
	replacementTrust := filepath.Join(replacementDestination, ".native-runtime", "trust", trustName)
	if err := os.RemoveAll(pointer.VersionDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(victimTrust); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementVersion, pointer.VersionDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementTrust, victimTrust); err != nil {
		t.Fatal(err)
	}
}

func assertRejectedNativePrepareFixture(t *testing.T, fixture nativePrepareFixture, want string) {
	t.Helper()
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination := filepath.Join(root, "destination")
	writeNativePrepareCache(t, cache, fixture.archive)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	oldDLL := []byte("previous trusted DLL")
	oldManifest := []byte("previous trusted manifest")
	if err := os.WriteFile(filepath.Join(destination, "miniapp-frida.dll"), oldDLL, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "manifest.json"), oldManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(nativePrepareArgs(cache, destination, "http://127.0.0.1:1/unreachable", fixture.hash), "-Offline")
	output, err := runNativePrepare(args...)
	if err == nil || !strings.Contains(output, want) {
		t.Fatalf("fixture was not rejected with %q: err=%v\n%s", want, err, output)
	}
	assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), oldDLL)
	assertFileContent(t, filepath.Join(destination, "manifest.json"), oldManifest)
	assertNoNativePrepareTemps(t, cache, destination)
}

func nativePrepareArgs(cache, destination, sourceURL, hash string) []string {
	args := []string{
		"-CacheDirectory", cache,
		"-DestinationDirectory", destination,
		"-SourceURL", sourceURL,
		"-LockTimeoutSeconds", "10",
	}
	if hash != "" {
		args = append(args, "-ExpectedArchiveSHA256", hash)
	}
	return args
}

func runNativePrepare(args ...string) (string, error) {
	shell := "pwsh.exe"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "powershell.exe"
	}
	return runNativePrepareWithShell(shell, args...)
}

func runNativePrepareWithShell(shell string, args ...string) (string, error) {
	return runNativePrepareCommand(shell, "", args...)
}

func runNativePrepareWithPublishFailure(step string, args ...string) (string, error) {
	shell := "pwsh.exe"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "powershell.exe"
	}
	return runNativePrepareCommand(shell, step, args...)
}

func runNativePrepareCommand(shell, publishFailure string, args ...string) (string, error) {
	return runNativePrepareSecurityCommand(shell, publishFailure, "valid", "", args...)
}

func runNativePrepareWithSecurity(signatureStatus, rollbackFailure string, args ...string) (string, error) {
	shell := "pwsh.exe"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "powershell.exe"
	}
	return runNativePrepareSecurityCommand(shell, "", signatureStatus, rollbackFailure, args...)
}

func runNativePrepareSecurityCommand(shell, publishFailure, signatureStatus, rollbackFailure string, args ...string) (string, error) {
	_, source, _, _ := runtime.Caller(0)
	testRoot, err := os.MkdirTemp("", "miniapp-native-prepare-test-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(testRoot)
	testScript := filepath.Join(testRoot, "native-prepare.test.ps1")
	assembler := filepath.Join(filepath.Dir(source), "test-support", "new-native-prepare-test-script.ps1")
	assemble := exec.Command(shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", assembler, "-SourcePath", filepath.Join(filepath.Dir(source), "native-prepare.ps1"),
		"-OutputPath", testScript)
	if output, assembleErr := assemble.CombinedOutput(); assembleErr != nil {
		return string(output), fmt.Errorf("assemble native prepare test script: %w", assembleErr)
	}
	command := exec.Command(shell, append([]string{
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", testScript,
	}, args...)...)
	for _, entry := range os.Environ() {
		upper := strings.ToUpper(entry)
		if !strings.HasPrefix(upper, "MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_PUBLISH_FAILURE=") &&
			!strings.HasPrefix(upper, "MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_SIGNATURE_STATUS=") &&
			!strings.HasPrefix(upper, "MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_EXPORTS_STATUS=") &&
			!strings.HasPrefix(upper, "MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_ROLLBACK_FAILURE=") {
			command.Env = append(command.Env, entry)
		}
	}
	if publishFailure != "" {
		command.Env = append(command.Env, "MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_PUBLISH_FAILURE="+publishFailure)
	}
	if signatureStatus != "" {
		command.Env = append(command.Env, "MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_SIGNATURE_STATUS="+signatureStatus)
	}
	command.Env = append(command.Env, "MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_EXPORTS_STATUS=valid")
	if rollbackFailure != "" {
		command.Env = append(command.Env, "MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_ROLLBACK_FAILURE="+rollbackFailure)
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

func nativeRollbackArgs(destination, retainedHash string) []string {
	return []string{"-DestinationDirectory", destination, "-ExpectedArchiveSHA256", retainedHash, "-Rollback"}
}

func readNativeCurrentVersionDirectory(t *testing.T, destination string) string {
	t.Helper()
	return readNativeCurrentPointer(t, destination).VersionDirectory
}

type nativeCurrentPointer struct {
	VersionDirectory string `json:"versionDirectory"`
	SHA256           string `json:"sha256"`
	ArchiveSHA256    string `json:"archiveSHA256"`
}

func readNativeCurrentPointer(t *testing.T, destination string) nativeCurrentPointer {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(destination, ".native-runtime", "current.json"))
	if err != nil {
		t.Fatal(err)
	}
	var current nativeCurrentPointer
	if err := json.Unmarshal(data, &current); err != nil {
		t.Fatal(err)
	}
	if current.VersionDirectory == "" || current.SHA256 == "" || current.ArchiveSHA256 == "" {
		t.Fatalf("current pointer is incomplete: %s", data)
	}
	return current
}

func prepareNativeRollbackPair(t *testing.T) (retainedDLL, currentDLL []byte, destination, retained, retainedHash string) {
	t.Helper()
	first := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("A62 retained native DLL")})
	second := makeNativePrepareFixture(t, nativePrepareArchiveOptions{dll: []byte("A62 current native DLL")})
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	destination = filepath.Join(root, "destination")
	for _, fixture := range []nativePrepareFixture{first, second} {
		writeNativePrepareCache(t, cache, fixture.archive)
		args := append(nativePrepareArgs(cache, destination, "unused", fixture.hash), "-Offline")
		if output, err := runNativePrepare(args...); err != nil {
			t.Fatalf("prepare rollback fixture failed: %v\n%s", err, output)
		}
	}
	currentVersion := readNativeCurrentVersionDirectory(t, destination)
	versions, err := os.ReadDir(filepath.Join(destination, ".native-runtime", "versions"))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range versions {
		candidate := filepath.Join(destination, ".native-runtime", "versions", version.Name())
		if !version.IsDir() || strings.EqualFold(candidate, currentVersion) {
			continue
		}
		if retained != "" {
			t.Fatalf("multiple retained rollback candidates: %q and %q", retained, candidate)
		}
		retained = candidate
	}
	if retained == "" {
		t.Fatal("retained rollback candidate not found")
	}
	return first.dll, second.dll, destination, retained, first.hash
}

func assertNativePrepareInstalled(t *testing.T, destination string, dll []byte) {
	t.Helper()
	assertFileContent(t, filepath.Join(destination, "miniapp-frida.dll"), dll)
	manifestData, err := os.ReadFile(filepath.Join(destination, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("installed manifest is invalid: %v", err)
	}
	if got, _ := manifest["sha256"].(string); got != nativePrepareSHA(dll) {
		t.Fatalf("installed manifest hash = %q, want %q", got, nativePrepareSHA(dll))
	}
}

func assertNoNativePrepareTemps(t *testing.T, cache, destination string) {
	t.Helper()
	paths := []string{
		filepath.Join(cache, nativePrepareAsset+".partial"),
		filepath.Join(cache, nativePrepareAsset+".extracting"),
		filepath.Join(cache, nativePrepareAsset+".lock"),
		filepath.Join(destination, "miniapp-frida.dll.partial"),
		filepath.Join(destination, "manifest.json.partial"),
		filepath.Join(destination, ".native-runtime", "update.lock"),
		filepath.Join(destination, ".native-runtime", "transaction.json"),
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary native prepare artifact remains: %s (err=%v)", path, err)
		}
	}
	for _, pattern := range []string{
		filepath.Join(destination, "miniapp-frida.dll.backup-*"),
		filepath.Join(destination, "manifest.json.backup-*"),
		filepath.Join(destination, ".native-runtime", "versions", "*.staging-*"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Fatalf("temporary native prepare backups remain: %v", matches)
		}
	}
}

func assertFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}

func nativePrepareSHA(data []byte) string {
	return strings.ToUpper(fmt.Sprintf("%x", sha256.Sum256(data)))
}
