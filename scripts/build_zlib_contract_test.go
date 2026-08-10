package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestZlibBuildDownloadsPinnedArchiveIntoIgnoredCache(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))

	script := readContractFile(t, filepath.Join(root, "scripts", "build-zlib.ps1"))
	for _, token := range []string{
		"https://github.com/madler/zlib/archive/refs/tags/v1.3.1.tar.gz",
		"third_party\\downloads\\cache",
		"17E88863F3600672AB49182F217281B6FC4D3C762BDE361935E436A95214D05C",
		"function Invoke-BoundedDownload",
		"Get-Command curl.exe -ErrorAction SilentlyContinue",
		"--connect-timeout $connectTimeoutSeconds",
		"--max-time $TimeoutSeconds",
		"[int]$DownloadTotalTimeoutSeconds = 900",
		"$attemptTimeout = [Math]::Max(1, [Math]::Min($DownloadTimeoutSeconds, [Math]::Ceiling($remaining)))",
		"[System.Net.Http.HttpCompletionOption]::ResponseContentRead",
		"$client.Timeout = [TimeSpan]::FromSeconds($TimeoutSeconds)",
		"Invoke-VerifiedDownload -URL $archiveURL -Destination $partialArchive",
		"$archive.partial",
		"Get-FileHash -Algorithm SHA256 -LiteralPath $Destination",
		"Move-Item -LiteralPath $partialArchive -Destination $archive -Force",
		"tar.exe -xzf $archive",
		"--strip-components 1",
		"zlib 1.3.1 extraction did not produce zlib.h",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("build-zlib.ps1 is missing %q", token)
		}
	}

	ignore := readContractFile(t, filepath.Join(root, ".gitignore"))
	if !strings.Contains(ignore, "third_party/downloads/") {
		t.Fatal("zlib archive download cache is not ignored")
	}
	if !strings.Contains(ignore, "third_party/zlib/src-1.3.1/") {
		t.Fatal("zlib extraction cache is not ignored")
	}
}

func TestZlibBuildSupportsVerifiedOfflineCache(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	script := readContractFile(t, filepath.Join(root, "scripts", "build-zlib.ps1"))

	for _, token := range []string{
		"param(",
		"[switch]$Offline",
		"[string]$SourceDirectory = ''",
		"[string]$OutputDirectory = ''",
		"[string]$ExpectedArchiveSHA256 =",
		"if ($Offline)",
		"zlib archive cache is unavailable or invalid in offline mode",
		"Get-FileHash -Algorithm SHA256 -LiteralPath $archive",
		"tar.exe -xzf $archive -C $source --strip-components 1",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("build-zlib.ps1 is missing offline-cache contract %q", token)
		}
	}

	archiveValidity := strings.Index(script, "$archiveIsValid")
	archiveRecovery := strings.Index(script, "if (!$archiveIsValid)")
	offlineGuard := strings.Index(script, "if ($Offline)")
	downloadOffset := strings.Index(script[archiveRecovery:], "Invoke-VerifiedDownload -URL $archiveURL")
	download := -1
	if downloadOffset >= 0 {
		download = archiveRecovery + downloadOffset
	}
	offlineError := strings.Index(script, "zlib archive cache is unavailable or invalid in offline mode")
	if archiveValidity < 0 || archiveRecovery < 0 || offlineGuard < archiveRecovery ||
		offlineError < offlineGuard || download < offlineGuard {
		t.Fatal("offline cache validation must precede recovery, report invalid cache, and guard the bounded downloader")
	}

	// A verified cache must flow through extraction without requiring a download.
	extraction := strings.Index(script, "tar.exe -xzf $archive")
	if extraction < 0 || extraction < archiveRecovery {
		t.Fatal("verified archive cache must remain usable by the extraction path")
	}
}

func TestZlibBuildUsesIsolatedObjectDirectory(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	script := readContractFile(t, filepath.Join(root, "scripts", "build-zlib.ps1"))
	for _, token := range []string{
		"[Guid]::NewGuid().ToString('N')",
		"$stagedLibrary = Join-Path $build 'libz.a'",
		"zlib compile did not produce object",
		"zlib archive creation did not produce library",
		"Move-Item -LiteralPath $stagedLibrary -Destination $library -Force",
		"finally {",
		"Remove-Item -LiteralPath $build -Recurse -Force",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("build-zlib.ps1 is missing isolated-build contract %q", token)
		}
	}
	if strings.Contains(script, "Get-ChildItem -LiteralPath $build -Filter '*.o'") {
		t.Fatal("build-zlib.ps1 still cleans a shared object directory")
	}
}

func TestFridaShimBuildIsReproducible(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	script := readContractFile(t, filepath.Join(root, "scripts", "build-frida-shim.ps1"))
	if !strings.Contains(script, "/link /Brepro") {
		t.Fatal("Frida shim linker is missing /Brepro")
	}
	if !strings.Contains(script, "ensure-frida-devkit.ps1") {
		t.Fatal("Frida shim build does not bootstrap the pinned SDK")
	}
	for _, token := range []string{
		"build-zlib.ps1",
		"third_party\\zlib\\src-1.3.1",
		"$zlibObjectDirectory",
		"$zlibObjects",
		"'adler32', 'compress', 'crc32', 'deflate'",
		`"{3}" {9} "{4}" setupapi.lib`,
	} {
		if !strings.Contains(script, token) {
			t.Errorf("Frida shim build does not embed pinned zlib contract %q", token)
		}
	}
	bootstrap := readContractFile(t, filepath.Join(root, "scripts", "ensure-frida-devkit.ps1"))
	for _, token := range []string{
		"frida-core-devkit-17.3.2-windows-x86_64.tar.xz",
		"8AF15423D6E534626F91A67FAA0582E42C67A07A95A190F4C622695105549C72",
		"6B4DEE14C19BDB03CAA4A25BE51564AA249BC1167AA8DED26F562E238D0B3462",
		"D763BCF99EFDE43A3DE4138B19D70EC64B586286413473EAA21E6C59B7410A30",
		"function Invoke-BoundedDownload",
		"'--max-time', [string]$TimeoutSeconds",
		"$process.WaitForExit($waitMilliseconds)",
		"curl.exe exceeded hard timeout of ${TimeoutSeconds}s",
		"Get-FileHash -Algorithm SHA256",
		"$archive.partial",
		"Move-Item -LiteralPath $partialArchive -Destination $archive -Force",
		"$devkit.extracting-$([guid]::NewGuid().ToString('N'))",
		"function Expand-FridaArchive",
		"tar.exe -xJf $Archive -C $Destination",
	} {
		if !strings.Contains(bootstrap, token) {
			t.Errorf("ensure-frida-devkit.ps1 is missing %q", token)
		}
	}
	ignore := readContractFile(t, filepath.Join(root, ".gitignore"))
	if !strings.Contains(ignore, "third_party/frida/devkit-17.3.2/") {
		t.Fatal("Frida SDK extraction cache is not ignored")
	}
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
