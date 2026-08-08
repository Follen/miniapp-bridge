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
		"go test -tags frida -race ./... -count=1 -timeout 240s",
		"go test -race ./... -count=1 -timeout 180s",
		"internal_go_statements=100.0%",
		"smoke_runner_go_statements=100.0%",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("coverage gate is missing %q", token)
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
	} {
		if !strings.Contains(script, token) {
			t.Errorf("Windows build script is missing %q", token)
		}
	}
}

func runtimeCallerForCoverageGate(t *testing.T) (uintptr, string, int, bool) {
	return runtime.Caller(0)
}
