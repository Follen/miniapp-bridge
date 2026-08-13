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

func TestBuildWindowsCompilesExecutableAgainstGeneratedNativeTrustRoot(t *testing.T) {
	root := filepath.Clean("..")
	data, err := os.ReadFile(filepath.Join(root, "scripts", "build-windows.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	markers := []string{
		"& $PSScriptRoot\\native-release.ps1",
		"internal\\native\\trust_native_generated.go",
		"//go:build native_generated",
		"go test -tags 'frida,native_generated' ./internal/native ./sdk -run '^$'",
		"go build -tags 'frida,native_generated' -trimpath -o dist/miniapp-bridge.exe",
		"Remove-Item -LiteralPath $generatedTrust -Force -ErrorAction SilentlyContinue",
	}
	previous := -1
	for _, marker := range markers {
		index := strings.Index(script, marker)
		if index < 0 {
			t.Fatalf("build script is missing generated trust-root contract %q", marker)
		}
		if index <= previous {
			t.Fatalf("build script trust-root step %q is out of order", marker)
		}
		previous = index
	}

	defaultTrust, err := os.ReadFile(filepath.Join(root, "internal", "native", "trust_default.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(defaultTrust), "//go:build !native_generated") {
		t.Fatal("default and generated native trust roots must have mutually exclusive build constraints")
	}
}
