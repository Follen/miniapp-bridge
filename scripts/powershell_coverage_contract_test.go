package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPowerShellCoverageUsesRealPesterCounters(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	data, err := os.ReadFile(filepath.Join(root, "scripts", "powershell-coverage.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, required := range []string{
		"Invoke-Pester",
		"-CodeCoverage $CoveragePath",
		"CommandBaseAst",
		"Pester CommandBaseAst breakpoints + child ledger",
		"NumberOfCommandsAnalyzed",
		"NumberOfCommandsExecuted",
		"NumberOfCommandsMissed",
		"PowerShell command coverage must be 100.00%",
		"threshold_percent = 100.0",
		"result = 'passed'",
		"no fallback or synthetic percentage is accepted",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("PowerShell coverage gate is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"commands_executed = $analyzed",
		"command_percent = 100.0",
		"catch { $percent = 100",
	} {
		if strings.Contains(script, forbidden) {
			t.Errorf("PowerShell coverage gate contains synthetic success marker %q", forbidden)
		}
	}
}

func TestCShimCoverageUsesNativeGcovSummary(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	data, err := os.ReadFile(filepath.Join(root, "scripts", "cshim-coverage.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(data), "\r\n", "\n")
	for _, required := range []string{
		"-f', '-o'",
		"Lines executed:",
		"Branches executed:",
		"Function",
		"c_shim_line_coverage=100.00%",
		"c_shim_function_coverage=100.00%",
		"C shim line coverage must be 100.00%",
		"C shim function coverage must be 100.00%",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("C shim coverage gate is missing %q", required)
		}
	}
}
