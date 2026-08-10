package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCoverageGateRunsTaggedRaceAndUsesStableScope(t *testing.T) {
	_, source, _, ok := runtimeCallerForCoverageGate(t)
	if !ok {
		t.Fatal("runtime caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	data, err := os.ReadFile(filepath.Join(root, "scripts", "coverage-gate.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, token := range []string{
		"go test ./cmd/... ./frida -count=1 -timeout 90s",
		"go test -tags frida ./internal/... ./sdk -count=1 -timeout 180s",
		"go test -tags frida -race ./... -count=1 -timeout 240s",
		"go test -race ./... -count=1 -timeout 180s",
		"cli_frida_go_statements=100.0%",
		"internal_go_statements=100.0%",
		"sdk_go_statements=100.0%",
		"tagged_internal_sdk_go_statements=100.0%",
		"smoke_runner_go_statements=100.0%",
		"MINIAPP_BRIDGE_NATIVE_PATH",
		"build-frida-shim.ps1",
		"third_party\\frida\\runtime-17.3.2",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("coverage gate is missing %q", token)
		}
	}
	for _, forbidden := range []string{"CGO_CFLAGS", "CGO_LDFLAGS", "build-zlib.ps1"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("coverage gate must not depend on host zlib through %q", forbidden)
		}
	}
}

func TestBuildWindowsUsesRepositoryRootAndTaggedRace(t *testing.T) {
	_, source, _, ok := runtimeCallerForCoverageGate(t)
	if !ok {
		t.Fatal("runtime caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	data, err := os.ReadFile(filepath.Join(root, "scripts", "build-windows.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, token := range []string{
		"$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path",
		"Set-Location $repo",
		"go test -tags frida -race ./... -count=1",
		"dumpbin /exports",
		"$manifest.requiredExports",
		"dumpbin /dependents",
		"zlib1\\.dll",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("Windows build script is missing %q", token)
		}
	}
	for _, forbidden := range []string{"CGO_CFLAGS", "CGO_LDFLAGS", "build-zlib.ps1"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("Windows build must not depend on host zlib through %q", forbidden)
		}
	}
}

func runtimeCallerForCoverageGate(t *testing.T) (uintptr, string, int, bool) {
	return runtime.Caller(0)
}
