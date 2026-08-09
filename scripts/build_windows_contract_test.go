//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWindowsExportCheckUsesStructuredSetComparison(t *testing.T) {
	root := filepath.Clean("..")
	data, err := os.ReadFile(filepath.Join(root, "scripts", "build-windows.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, marker := range []string{
		"$actualExports = @($exports | ForEach-Object",
		"^\\s*\\d+\\s+[0-9A-Fa-f]+\\s+[0-9A-Fa-f]+\\s+(mb_[A-Za-z0-9_]+)",
		"$missingExports = @($manifest.requiredExports | Where-Object { $_ -notin $actualExports })",
		"$unexpectedExports = @($actualExports | Where-Object { $_ -notin $manifest.requiredExports })",
	} {
		if !strings.Contains(script, marker) {
			t.Errorf("build script is missing export-check contract %q", marker)
		}
	}
	if strings.Contains(script, "$exports -notmatch") {
		t.Fatal("build script must not apply -notmatch directly to the output array")
	}
}
