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
		"[int]$DownloadAttempts = 3",
		"[int]$DownloadTimeoutSeconds = 300",
		"[int]$DownloadRetrySeconds = 5",
		"$headerValid -and $libraryValid",
		"if ($Offline)",
		"Frida SDK cache is unavailable or invalid in offline mode",
		"Get-FileHash -Algorithm SHA256 -LiteralPath $archive",
		"tar.exe -xJf $archive -C $stagingDevkit",
		"$parameters.ConnectionTimeoutSeconds",
		"$parameters.OperationTimeoutSeconds",
		"$parameters.TimeoutSec",
		"for ($attempt = 1; $attempt -le $DownloadAttempts; $attempt++)",
		"Frida SDK download failed after $DownloadAttempts attempts",
		"$devkit.extracting-$([guid]::NewGuid().ToString('N'))",
		"$backupDevkit = \"$devkit.backup-$([guid]::NewGuid().ToString('N'))\"",
		"function Move-DirectoryAtomically",
		"[System.IO.Directory]::Move($Source, $Destination)",
		"Frida SDK publication failed: $publishError",
		"rollback failed:",
		"backup retained at $backupDevkit",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("ensure-frida-devkit.ps1 is missing offline-cache contract %q", token)
		}
	}

	validDevkit := strings.Index(script, "if ($headerValid -and $libraryValid)")
	offlineGuard := strings.Index(script, "if ($Offline)")
	download := strings.Index(script, "Invoke-VerifiedDownload -URL $archiveURL")
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
		"$stagingDevkit = \"$devkit.extracting-$([guid]::NewGuid().ToString('N'))\"",
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

func TestFridaBootstrapPublishesValidatedDevkitWithRollback(t *testing.T) {
	script := readFridaBootstrap(t)
	validated := strings.Index(script, "if (-not (Test-ExpectedHash -Path $stagingLibrary")
	backup := strings.Index(script, "Move-DirectoryAtomically -Source $devkit -Destination $backupDevkit")
	publish := strings.Index(script, "Move-DirectoryAtomically -Source $stagingDevkit -Destination $devkit")
	rollback := strings.Index(script, "Move-DirectoryAtomically -Source $backupDevkit -Destination $devkit")
	cleanup := strings.Index(script, "Remove-Item -LiteralPath $backupDevkit -Recurse -Force -ErrorAction Stop")
	if validated < 0 || backup < 0 || publish < 0 || rollback < 0 || cleanup < 0 {
		t.Fatal("validated devkit publication must include backup, publish, rollback, and cleanup operations")
	}
	if !(validated < backup && backup < publish && publish < rollback && publish < cleanup) {
		t.Fatal("devkit must be validated before backup/publish, with rollback and post-publish cleanup")
	}
	removeDevkit := strings.Index(script, "Remove-Item -LiteralPath $devkit -Recurse -Force")
	if removeDevkit >= 0 && removeDevkit < backup {
		t.Fatal("publisher must not unconditionally delete the existing devkit before creating a backup")
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
