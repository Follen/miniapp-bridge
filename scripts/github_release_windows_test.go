//go:build windows

package main

import (
	"os"
	"os/exec"
	"regexp"
	"testing"
)

func TestGitHubReleasePowerShellSemVerIsCaseSensitive(t *testing.T) {
	workflow := readReleaseWorkflow(t)
	match := regexp.MustCompile(`(?m)^\s*\$semver = '([^']+)'\s*$`).FindStringSubmatch(workflow)
	if len(match) != 2 {
		t.Fatal("release workflow lacks an explicit SemVer validation expression")
	}

	shell := "pwsh.exe"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "powershell.exe"
	}
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: "v0.0.1", want: "true"},
		{value: "v0.0.1-rc.1", want: "true"},
		{value: "V0.0.1", want: "false"},
		{value: "V1.2.3-rc.1", want: "false"},
		{value: "v0.0.1+build.7", want: "false"},
		{value: "v2.0.0", want: "false"},
	} {
		t.Run(test.value, func(t *testing.T) {
			command := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command",
				"if (($env:TEST_VALUE -cmatch $env:TEST_PATTERN) -eq [bool]::Parse($env:TEST_EXPECT)) { exit 0 }; exit 1")
			command.Env = append(os.Environ(),
				"TEST_PATTERN="+match[1],
				"TEST_VALUE="+test.value,
				"TEST_EXPECT="+test.want,
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("PowerShell SemVer result mismatch: %v\n%s", err, output)
			}
		})
	}
}
