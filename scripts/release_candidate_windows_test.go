//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseCandidateCreateVerifyAndRebind(t *testing.T) {
	root := newReleaseCandidateFixture(t)
	first := strings.Repeat("a", 40)
	runReleaseCandidate(t, root, "Create", first, true)
	runReleaseCandidate(t, root, "Verify", first, true)

	metadataPath := filepath.Join(root, "dist", "candidate", "candidate.json")
	var metadata map[string]any
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["sourceCommit"] != first || metadata["buildCommit"] != first {
		t.Fatalf("unexpected candidate identity: %#v", metadata)
	}

	writeCandidateFixtureFile(t, root, "docs/operations.md", []byte("updated documentation\n"))
	runGit(t, root, "add", "docs/operations.md")
	runGit(t, root, "commit", "-m", "docs update")
	second := strings.Repeat("b", 40)
	runReleaseCandidate(t, root, "Rebind", second, true)
	runReleaseCandidate(t, root, "Verify", second, true)

	data, err = os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["sourceCommit"] != second || metadata["buildCommit"] != first {
		t.Fatalf("rebind must preserve build provenance: %#v", metadata)
	}
}

func TestReleaseCandidateFailsClosedOnPayloadOrProductionInputChange(t *testing.T) {
	t.Run("payload", func(t *testing.T) {
		root := newReleaseCandidateFixture(t)
		commit := strings.Repeat("c", 40)
		runReleaseCandidate(t, root, "Create", commit, true)
		writeCandidateFixtureFile(t, root, "dist/candidate/miniapp-frida.dll", []byte("tampered"))
		runReleaseCandidate(t, root, "Verify", commit, false)
	})

	t.Run("production-input", func(t *testing.T) {
		root := newReleaseCandidateFixture(t)
		commit := strings.Repeat("d", 40)
		runReleaseCandidate(t, root, "Create", commit, true)
		writeCandidateFixtureFile(t, root, "internal/example.go", []byte("package internal\n\nconst changed = true\n"))
		runGit(t, root, "add", "internal/example.go")
		runGit(t, root, "commit", "-m", "production change")
		runReleaseCandidate(t, root, "Rebind", strings.Repeat("e", 40), false)
	})
}

func newReleaseCandidateFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string][]byte{
		"go.mod":                             []byte("module example.invalid/candidate\n\ngo 1.26\n"),
		"go.sum":                             {},
		"README.md":                          []byte("readme\n"),
		"README.zh.md":                       []byte("readme zh\n"),
		"LICENSE":                            []byte("license\n"),
		"THIRD_PARTY_NOTICES.md":             []byte("notices\n"),
		"licenses/frida-17.3.2/COPYING":      []byte("copying\n"),
		"licenses/frida-17.3.2/COPYING.LIB":  []byte("copying lib\n"),
		"third_party/zlib/src-1.3.1/LICENSE": []byte("zlib\n"),
		"internal/example.go":                []byte("package internal\n"),
		"scripts/build-windows.ps1":          []byte("Write-Output build\n"),
		"dist/miniapp-bridge.exe":            []byte("MZ exe"),
		"dist/miniapp-frida.dll":             []byte("MZ dll"),
		"dist/miniapp-bridge.cdx.json":       []byte("{\"bomFormat\":\"CycloneDX\",\"specVersion\":\"1.6\",\"metadata\":{\"component\":{}}}\n"),
		"dist/native/miniapp-frida-native-17.3.2-abi1.1-windows-amd64.zip": []byte("native zip"),
	}
	dll := files["dist/miniapp-frida.dll"]
	sum := sha256.Sum256(dll)
	manifest, err := json.Marshal(map[string]any{
		"schema": "miniapp-bridge.native-manifest.v1", "nativeVersion": "17.3.2-abi1.1",
		"os": "windows", "arch": "amd64", "size": len(dll), "sha256": strings.ToUpper(hex.EncodeToString(sum[:])),
	})
	if err != nil {
		t.Fatal(err)
	}
	files["dist/manifest.json"] = manifest
	for name, content := range files {
		writeCandidateFixtureFile(t, root, name, content)
	}
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "candidate@example.invalid")
	runGit(t, root, "config", "user.name", "Candidate Test")
	runGit(t, root, "add", "go.mod", "go.sum", "README.md", "README.zh.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "licenses", "internal", "scripts")
	runGit(t, root, "commit", "-m", "fixture")
	return root
}

func writeCandidateFixtureFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", arguments, err, output)
	}
}

func runReleaseCandidate(t *testing.T, root, mode, commit string, wantSuccess bool) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(source), "release-candidate.ps1")
	command := exec.Command("pwsh.exe", "-NoProfile", "-NonInteractive", "-File", script,
		"-Mode", mode, "-SourceCommit", commit, "-RepositoryRoot", root,
		"-InputDirectory", filepath.Join(root, "dist"),
		"-OutputDirectory", filepath.Join(root, "dist", "candidate"))
	output, err := command.CombinedOutput()
	if wantSuccess && err != nil {
		t.Fatalf("candidate %s failed: %v\n%s", mode, err, output)
	}
	if !wantSuccess && err == nil {
		t.Fatalf("candidate %s unexpectedly succeeded:\n%s", mode, output)
	}
}
