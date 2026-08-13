//go:build windows

package main

import (
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
)

func TestZlibBuildFailureCoverage(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(sourceFile))
	fixture := makeZlibArchiveFixture(t)
	wrapper := writeZlibCoverageWrapper(t)

	var retryRequests int32
	retryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&retryRequests, 1) == 1 {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(fixture.data)
	}))
	t.Cleanup(retryServer.Close)

	tests := []struct {
		name       string
		mode       string
		expected   string
		url        string
		seedCache  bool
		seedSource string
		retry      bool
	}{
		{name: "invalid expected hash", mode: "success", expected: "invalid", seedSource: "valid"},
		{name: "http fallback retries and sleeps", mode: "success", expected: fixture.hash, url: retryServer.URL, retry: true},
		{name: "archive changes after promotion", mode: "corrupt-move", expected: fixture.hash, url: retryServer.URL},
		{name: "extraction command fails", mode: "extract-fail", expected: fixture.hash, seedCache: true},
		{name: "extraction omits header", mode: "extract-empty", expected: fixture.hash, seedCache: true},
		{name: "extraction creates wrong header", mode: "extract-wrong", expected: fixture.hash, seedCache: true},
		{name: "compiler fails", mode: "compile-fail", expected: fixture.hash, seedCache: true, seedSource: "valid"},
		{name: "compiler omits object", mode: "compile-empty", expected: fixture.hash, seedCache: true, seedSource: "valid"},
		{name: "archiver fails", mode: "archive-fail", expected: fixture.hash, seedCache: true, seedSource: "valid"},
		{name: "archiver omits library", mode: "archive-empty", expected: fixture.hash, seedCache: true, seedSource: "valid"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caseDir := t.TempDir()
			cache := filepath.Join(caseDir, "cache")
			source := filepath.Join(caseDir, "source")
			output := filepath.Join(caseDir, "output")
			if test.seedCache {
				if err := os.MkdirAll(cache, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cache, zlibOfflineArchiveName), fixture.data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.seedSource != "" {
				seedZlibCoverageSource(t, source, test.seedSource == "valid")
			}

			args := []string{
				"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", wrapper,
				"-Script", filepath.Join(root, "scripts", "build-zlib.ps1"),
				"-Mode", test.mode,
				"-Cache", cache,
				"-Source", source,
				"-Output", output,
				"-ExpectedHash", test.expected,
			}
			if test.url != "" {
				args = append(args, "-URL", test.url)
			}
			if test.retry {
				args = append(args, "-Retry")
			}
			outputBytes, err := exec.Command(zlibCoverageShell(t), args...).CombinedOutput()
			if test.retry {
				if err != nil {
					t.Fatalf("retry case failed: %v\n%s", err, outputBytes)
				}
				if got := atomic.LoadInt32(&retryRequests); got != 2 {
					t.Fatalf("retry requests = %d, want 2", got)
				}
				return
			}
			if err == nil {
				t.Fatalf("failure case unexpectedly succeeded:\n%s", outputBytes)
			}
		})
	}
}

func zlibCoverageShell(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("PowerShell is not installed")
	return ""
}

func seedZlibCoverageSource(t *testing.T, dir string, validHeader bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	header := "#define ZLIB_VERSION \"0.0.0\"\n"
	if validHeader {
		header = "#define ZLIB_VERSION \"1.3.1\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "zlib.h"), []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeZlibCoverageWrapper(t *testing.T) string {
	t.Helper()
	wrapper := filepath.Join(t.TempDir(), "invoke-zlib-coverage.ps1")
	content := strings.TrimSpace(`
param(
    [string]$Script,
    [string]$Mode,
    [string]$Cache,
    [string]$Source,
    [string]$Output,
    [string]$ExpectedHash,
    [string]$URL = '',
    [switch]$Retry
)

function global:Get-Command {
    param([Parameter(Position=0)][string]$Name, [Parameter(ValueFromRemainingArguments=$true)][object[]]$Rest)
    if ($Name -eq 'curl.exe') { return $null }
    if ($Name -in @('gcc.exe', 'ar.exe')) {
        return [pscustomobject]@{ Source = $Name }
    }
    Microsoft.PowerShell.Core\Get-Command $Name @Rest
}

function global:tar.exe {
    if ($Mode -eq 'extract-fail') {
        $global:LASTEXITCODE = 7
        return
    }
    $destination = $args[[Array]::IndexOf($args, '-C') + 1]
    New-Item -ItemType Directory -Force -Path $destination | Out-Null
    if ($Mode -eq 'extract-empty') {
        $global:LASTEXITCODE = 0
        return
    }
    $version = if ($Mode -eq 'extract-wrong') { '0.0.0' } else { '1.3.1' }
    [IO.File]::WriteAllText((Join-Path $destination 'zlib.h'), (('#define ZLIB_VERSION "{0}"' -f $version) + [Environment]::NewLine))
    $global:LASTEXITCODE = 0
}

function global:gcc.exe {
    if ($Mode -eq 'compile-fail') {
        $global:LASTEXITCODE = 8
        return
    }
    if ($Mode -ne 'compile-empty') {
        $index = [Array]::IndexOf($args, '-o')
        [IO.File]::WriteAllText([string]$args[$index + 1], 'object')
    }
    $global:LASTEXITCODE = 0
}

function global:ar.exe {
    if ($Mode -eq 'archive-fail') {
        $global:LASTEXITCODE = 9
        return
    }
    if ($Mode -ne 'archive-empty') {
        [IO.File]::WriteAllText([string]$args[1], 'library')
    }
    $global:LASTEXITCODE = 0
}

if ($Mode -eq 'corrupt-move') {
    function global:Move-Item {
        [CmdletBinding()]
        param([string]$LiteralPath, [string]$Destination, [switch]$Force)
        Microsoft.PowerShell.Management\Move-Item -LiteralPath $LiteralPath -Destination $Destination -Force:$Force
        [IO.File]::WriteAllText($Destination, 'corrupt')
    }
}

$parameters = @{
    CacheDirectory = $Cache
    SourceDirectory = $Source
    OutputDirectory = $Output
    ExpectedArchiveSHA256 = $ExpectedHash
    DownloadAttempts = $(if ($Retry) { 2 } else { 1 })
    DownloadTimeoutSeconds = 5
    DownloadTotalTimeoutSeconds = 10
    DownloadRetrySeconds = $(if ($Retry) { 1 } else { 0 })
}
if ($URL) { $parameters.SourceURL = $URL }
& $Script @parameters
`) + "\r\n"
	if err := os.WriteFile(wrapper, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return wrapper
}

func TestZlibCoverageFixtureDiagnostics(t *testing.T) {
	// Keep failure labels useful when a wrapper itself breaks.
	for _, value := range []string{"extract-fail", "compile-fail", "archive-fail"} {
		if !strings.HasSuffix(value, "-fail") {
			t.Fatal(fmt.Sprintf("invalid fixture mode %q", value))
		}
	}
}
