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
		"Move-Item -LiteralPath $temporaryManifest -Destination $installedManifest -Force",
		"Move-Item -LiteralPath $temporaryDLL -Destination $installed -Force",
		"Remove-Item -LiteralPath $partial,$temporaryDLL,$temporaryManifest",
	} {
		if !strings.Contains(text, token) {
			t.Errorf("native prepare atomic contract missing %q", token)
		}
	}
	removeMarker := strings.Index(text, "Remove-Item -LiteralPath $installed -Force")
	manifestPublish := strings.Index(text, "Move-Item -LiteralPath $temporaryManifest -Destination $installedManifest -Force")
	dllPublish := strings.Index(text, "Move-Item -LiteralPath $temporaryDLL -Destination $installed -Force")
	if removeMarker < 0 || manifestPublish < removeMarker || dllPublish < manifestPublish {
		t.Fatal("native publish must remove the old DLL marker, publish manifest, then publish DLL last")
	}
}

func TestNativePreparePublishFailureCleansPartialsAndReadinessMarker(t *testing.T) {
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
	oldManifest := []byte("locked previous manifest")
	if err := os.WriteFile(dllPath, []byte("previous DLL marker"), 0o644); err != nil {
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
	if _, err := os.Stat(dllPath); !os.IsNotExist(err) {
		t.Fatalf("DLL readiness marker remained after failed publish: err=%v", err)
	}
	assertFileContent(t, manifestPath, oldManifest)
	assertNoNativePrepareTemps(t, cache, destination)
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
	_, source, _, _ := runtime.Caller(0)
	shell := "pwsh.exe"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "powershell.exe"
	}
	command := exec.Command(shell, append([]string{
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", filepath.Join(filepath.Dir(source), "native-prepare.ps1"),
	}, args...)...)
	output, err := command.CombinedOutput()
	return string(output), err
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
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary native prepare artifact remains: %s (err=%v)", path, err)
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
