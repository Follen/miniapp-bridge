package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFridaBootstrapSupportsVerifiedOfflineCache(t *testing.T) {
	script := readFridaBootstrap(t)
	for _, token := range []string{
		"[switch]$Offline",
		"$headerValid -and $libraryValid",
		"if ($Offline)",
		"Frida SDK cache is unavailable or invalid in offline mode",
		"Get-FileHash -Algorithm SHA256 -LiteralPath $archive",
		"tar.exe -xJf $archive -C $stagingDevkit",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("ensure-frida-devkit.ps1 is missing offline-cache contract %q", token)
		}
	}

	validDevkit := strings.Index(script, "if ($headerValid -and $libraryValid)")
	offlineGuard := strings.Index(script, "if ($Offline)")
	download := strings.Index(script, "Invoke-WebRequest")
	if validDevkit < 0 || offlineGuard < validDevkit || download < offlineGuard {
		t.Fatal("verified devkit must bypass archive recovery and the offline guard must precede download")
	}
}

func TestFridaBootstrapSerializesCacheMutationAndAlwaysReleasesLock(t *testing.T) {
	script := readFridaBootstrap(t)
	for _, token := range []string{
		"[System.IO.FileMode]::OpenOrCreate",
		"[System.IO.FileAccess]::ReadWrite",
		"[System.IO.FileShare]::None",
		"Timed out waiting for Frida SDK cache lock",
		"Start-Sleep -Milliseconds 100",
		"$stagingDevkit = Join-Path $downloadDir",
		"if ($null -ne $lockStream) { $lockStream.Dispose() }",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("ensure-frida-devkit.ps1 is missing lock contract %q", token)
		}
	}

	lock := strings.Index(script, "$lockStream = [System.IO.File]::Open(")
	mutation := strings.Index(script, "Move-Item -LiteralPath $partialArchive -Destination $archive -Force")
	release := strings.LastIndex(script, "$lockStream.Dispose()")
	if lock < 0 || mutation < 0 || release < 0 || !(lock < mutation && mutation < release) {
		t.Fatal("cache mutation must be enclosed by the exclusive lock lifetime")
	}
}

func readFridaBootstrap(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(source), "ensure-frida-devkit.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
