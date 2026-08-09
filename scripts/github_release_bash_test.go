package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGitHubReleaseEmbeddedBashSyntax(t *testing.T) {
	bash, err := usableBash()
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("a working Bash is not installed: %v", err)
		}
		t.Fatal(err)
	}
	step := workflowStep(t, readReleaseWorkflow(t), "Reconcile and publish GitHub Releases")
	lines := strings.Split(step, "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "run: |" {
			start = index + 1
			break
		}
	}
	if start < 0 || start >= len(lines) {
		t.Fatal("release publisher step has no Bash body")
	}
	body := make([]string, 0, len(lines)-start)
	for _, line := range lines[start:] {
		body = append(body, strings.TrimPrefix(line, "          "))
	}
	path := filepath.Join(t.TempDir(), "release-publisher.sh")
	if err := os.WriteFile(path, []byte(strings.Join(body, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("release publisher Bash syntax is invalid: %v\n%s", err, output)
	}
}

func usableBash() (string, error) {
	var candidates []string
	if configured := os.Getenv("BASH"); configured != "" {
		candidates = append(candidates, configured)
	}
	if resolved, err := exec.LookPath("bash"); err == nil {
		candidates = append(candidates, resolved)
	}
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432")} {
			if root == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(root, "Git", "bin", "bash.exe"),
				filepath.Join(root, "Git", "usr", "bin", "bash.exe"),
			)
		}
		if git, err := exec.LookPath("git"); err == nil {
			root := filepath.Dir(filepath.Dir(git))
			candidates = append(candidates,
				filepath.Join(root, "bin", "bash.exe"),
				filepath.Join(root, "usr", "bin", "bash.exe"),
			)
		}
	}

	seen := make(map[string]bool)
	var failures []error
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		key := strings.ToLower(candidate)
		if seen[key] {
			continue
		}
		seen[key] = true
		if output, err := exec.Command(candidate, "--version").CombinedOutput(); err == nil {
			return candidate, nil
		} else {
			failures = append(failures, errors.New(candidate+": "+err.Error()+": "+strings.TrimSpace(string(output))))
		}
	}
	if len(failures) == 0 {
		return "", errors.New("bash was not found")
	}
	return "", errors.Join(failures...)
}
