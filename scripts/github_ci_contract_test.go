package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestGitHubCIWorkflowContract(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := strings.ReplaceAll(string(data), "\r\n", "\n")

	if strings.Contains(workflow, "\t") {
		t.Fatal("workflow YAML must use spaces for indentation")
	}
	assertBlockLines(t, workflow, "on", []string{"pull_request:", "push:", "branches:", "- main", "workflow_dispatch:"})
	assertBlockLines(t, workflow, "permissions", []string{"contents: read", "actions: read", "pull-requests: read"})

	required := []string{
		"group: ci-${{ github.event.pull_request.number || github.ref }}",
		"cancel-in-progress: true",
		"classify:\n    name: Classify changes",
		"run_full: ${{ steps.paths.outputs.run_full }}",
		"git diff --name-only \"$PR_BASE_SHA\"...HEAD",
		"run_full=true",
		"quick:\n    name: Workflow and documentation contracts",
		"windows-build:\n    name: Windows build",
		"windows-core-gates:\n    name: Windows core gates",
		"windows-behavior:\n    name: Windows behavior",
		"go-coverage:\n    name: Go 100% coverage",
		"c-coverage:\n    name: C 100% coverage",
		"windows-pe-package:\n    name: Windows PE and package",
		"candidate:\n    name: Create verified candidate",
		"promote-main:\n    name: Promote candidate to main SHA",
		"ci-gate:\n    name: ci-gate\n    if: always()",
		".\\scripts\\build-windows.ps1 -SkipTests",
		".\\scripts\\coverage-gate.ps1 -Mode Go -UseExistingNative",
		".\\scripts\\coverage-gate.ps1 -Mode C",
		".\\scripts\\release-candidate.ps1 -Mode Create",
		".\\scripts\\release-candidate.ps1 -Mode Rebind",
		"$_.merge_commit_sha -eq $env:GITHUB_SHA",
		"multiple merged PRs resolve to main commit",
		"name: windows-build-${{ needs.classify.outputs.source_sha }}",
		"name: release-candidate-${{ needs.classify.outputs.source_sha }}",
		"name: release-candidate-${{ github.sha }}",
		"retention-days: 30",
		"[[ \"$CLASSIFY\" == success && \"$QUICK\" == success ]]",
		"[[ \"$result\" == success ]] || exit 1",
		"[[ \"$result\" == skipped ]] || exit 1",
	}
	for _, marker := range required {
		if !strings.Contains(withoutYAMLComments(workflow), marker) {
			t.Errorf("workflow is missing contract marker %q", marker)
		}
	}
	if strings.Count(workflow, ".\\scripts\\build-windows.ps1") != 1 {
		t.Fatal("the full Windows build must have exactly one producer")
	}
	if got := strings.Count(workflow, "Download the single Windows build"); got != 5 {
		t.Fatalf("Windows build consumers=%d want 5", got)
	}

	usesPattern := regexp.MustCompile(`(?m)^\s*uses:\s+([^\s#]+)`)
	uses := usesPattern.FindAllStringSubmatch(withoutYAMLComments(workflow), -1)
	approvedActions := map[string]bool{
		"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1":          true,
		"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e":          true,
		"actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9":             true,
		"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a":   true,
		"actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c": true,
	}
	gotActionCounts := make(map[string]int)
	fullSHA := regexp.MustCompile(`^([^@]+)@[0-9a-f]{40}$`)
	for _, match := range uses {
		if !approvedActions[match[1]] {
			t.Errorf("workflow uses an unapproved or non-Node-24 action pin: %q", match[1])
		}
		parts := fullSHA.FindStringSubmatch(match[1])
		if parts == nil {
			t.Errorf("every uses entry must be pinned to a full lowercase commit SHA: %q", match[1])
			continue
		}
		gotActionCounts[parts[1]]++
	}
	if gotActionCounts["actions/cache"] != 1 {
		t.Fatalf("native cache action count=%d want 1", gotActionCounts["actions/cache"])
	}
	assertEveryCheckoutDisablesCredentials(t, workflow)

	cacheStep := workflowStep(t, workflow, "Restore pinned native caches")
	assertMultilineValues(t, cacheStep, "path", []string{
		"third_party/downloads/frida-core-devkit-17.3.2-windows-x86_64.tar.xz",
		"third_party/downloads/cache/zlib-1.3.1.tar.gz",
	})

	buildStep := workflowStep(t, workflow, "Build Windows release payload once")
	populateStep := workflowStep(t, workflow, "Populate Frida devkit archive cache")
	populate := strings.Index(workflow, "Populate Frida devkit archive cache")
	buildWindows := strings.Index(buildStep, ".\\scripts\\build-windows.ps1 -SkipTests")
	if populate < 0 || strings.Index(workflow, "gh release download $env:FRIDA_CORE_VERSION") < populate || strings.Index(workflow, "Get-FileHash $archive -Algorithm SHA256") < populate {
		t.Fatal("Windows CI must populate and verify the pinned Frida archive before building")
	}
	if !strings.Contains(populateStep, "GH_TOKEN: ${{ github.token }}") {
		t.Fatal("Frida cache population must use the workflow token for authenticated GitHub downloads")
	}
	if buildWindows < 0 {
		t.Fatal("Windows build producer must call build-windows.ps1")
	}
	behaviorStep := workflowStep(t, workflow, "Windows build release rollback and recovery behavior")
	for _, marker := range []string{
		"Test(BuildWindows|NativeRelease|PackageWindowsRelease|NativePrepare)",
		"windows-behavior.log",
	} {
		if !strings.Contains(behaviorStep, marker) {
			t.Errorf("Windows behavior gate is missing %q", marker)
		}
	}
	goCoverageStep := workflowStep(t, workflow, "Enforce Go statement coverage race and vet")
	if !strings.Contains(goCoverageStep, "New-Item -ItemType Directory -Force ci-artifacts") {
		t.Fatal("Go coverage must create its log directory before Tee-Object")
	}
	peStep := workflowStep(t, workflow, "Enforce PE architecture imports and security flags")
	for _, marker := range []string{"vswhere.exe", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64", "Visual Studio C++ build tools were not found"} {
		if !strings.Contains(peStep, marker) {
			t.Errorf("PE gate must resolve Visual Studio tools dynamically; missing %q", marker)
		}
	}
	if strings.Contains(workflow, "PowerShell command coverage") || strings.Contains(workflow, ".\\scripts\\powershell-coverage.ps1") {
		t.Fatal("Windows required CI must use production behavior gates instead of PowerShell numerical command coverage")
	}
	if got := strings.Count(workflow, "-fuzztime=1000x -parallel=1 -timeout=90s"); got != 1 {
		t.Fatalf("Linux fuzz smoke must use one fixed execution budget: got %d commands", got)
	}
	if strings.Contains(workflow, "-fuzztime=2s") {
		t.Fatal("fuzz smoke must not use wall-clock budgets that can fail during Go fuzz shutdown")
	}

	coreGateLogs := workflowStep(t, workflow, "Upload Windows core gate logs")
	assertStepLine(t, coreGateLogs, "if: always()")
	assertStepLine(t, coreGateLogs, "path: ci-artifacts/**")
	windowsLogs := workflowStep(t, workflow, "Upload Windows behavior logs")
	assertStepLine(t, windowsLogs, "if: always()")
	assertStepLine(t, windowsLogs, "path: ci-artifacts/**")
	if strings.Contains(windowsLogs, "dist/") {
		t.Fatal("always-run Windows log upload must not include build outputs")
	}
	moduleGate := strings.Index(workflow, "go mod download")
	formatGate := strings.Index(workflow, "git ls-files -- '*.go'")
	actionlintGate := strings.Index(workflow, "go run \"github.com/rhysd/actionlint/cmd/actionlint@v${ACTIONLINT_VERSION}\" -ignore 'unexpected key.*queue.*concurrency'")
	unitTests := strings.Index(workflow, "- name: Unit vet and vulnerability gates")
	if moduleGate < 0 || formatGate < moduleGate || unitTests < formatGate || actionlintGate < 0 {
		t.Fatalf("module and gofmt gates must run before unit tests, and actionlint must remain present: module=%d format=%d unit=%d actionlint=%d", moduleGate, formatGate, unitTests, actionlintGate)
	}
	if !strings.Contains(workflow, "needs: [classify, quick, windows-build, windows-core-gates, windows-behavior, go-coverage, c-coverage, windows-pe-package, candidate, promote-main]") {
		t.Fatal("ci-gate must require both the quick contracts and every classified core job")
	}

	for _, forbidden := range []string{
		"pull_request_target:",
		"write-all",
		"${{ secrets.",
		"path: **",
		"timeout-minutes: 90",
		"  push:\n  pull_request:",
	} {
		if strings.Contains(withoutYAMLComments(workflow), forbidden) {
			t.Errorf("workflow contains forbidden security or artifact marker %q", forbidden)
		}
	}
	for _, step := range allWorkflowSteps(workflow) {
		if strings.Contains(step, "actions/upload-artifact@") && strings.Contains(step, "path: .\n") {
			t.Fatal("an upload-artifact step must not publish the checkout root")
		}
	}
}

func assertBlockLines(t *testing.T, workflow, key string, want []string) {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	start := -1
	for i, line := range lines {
		if line == key+":" {
			if start >= 0 {
				t.Fatalf("workflow has duplicate top-level %s blocks", key)
			}
			start = i
		}
	}
	if start < 0 {
		t.Fatalf("workflow is missing top-level %s block", key)
	}
	var got []string
	for _, line := range lines[start+1:] {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if len(line) == len(strings.TrimLeft(line, " ")) {
			break
		}
		got = append(got, strings.TrimSpace(stripYAMLComment(line)))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("top-level %s block mismatch: got %q, want %q", key, got, want)
	}
}

func workflowStep(t *testing.T, workflow, name string) string {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	want := "      - name: " + name
	start := -1
	for i, line := range lines {
		if stripYAMLComment(line) == want {
			if start >= 0 {
				t.Fatalf("workflow has duplicate step %q", name)
			}
			start = i
		}
	}
	if start < 0 {
		t.Fatalf("workflow is missing step %q", name)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "      - ") {
			end = i
			break
		}
	}
	return withoutYAMLComments(strings.Join(lines[start:end], "\n"))
}

func assertStepLine(t *testing.T, step, want string) {
	t.Helper()
	for _, line := range strings.Split(step, "\n") {
		if strings.TrimSpace(line) == want {
			return
		}
	}
	t.Errorf("step is missing exact line %q", want)
}

func assertMultilineValues(t *testing.T, step, key string, want []string) {
	t.Helper()
	lines := strings.Split(step, "\n")
	start := -1
	indent := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == key+": |" {
			if start >= 0 {
				t.Fatalf("step has duplicate multiline %s keys", key)
			}
			start = i
			indent = len(line) - len(strings.TrimLeft(line, " "))
		}
	}
	if start < 0 {
		t.Fatalf("step is missing multiline %s key", key)
	}
	var got []string
	for _, line := range lines[start+1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lineIndent := len(line) - len(strings.TrimLeft(line, " "))
		if lineIndent <= indent {
			break
		}
		got = append(got, strings.TrimSpace(line))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("multiline %s values mismatch: got %q, want %q", key, got, want)
	}
}

func assertEveryCheckoutDisablesCredentials(t *testing.T, workflow string) {
	t.Helper()
	for _, step := range allWorkflowSteps(workflow) {
		if !strings.Contains(step, "uses: actions/checkout@") {
			continue
		}
		if strings.Count(step, "persist-credentials:") != 1 {
			t.Errorf("checkout step must contain exactly one persist-credentials setting")
		}
		assertStepLine(t, step, "persist-credentials: false")
	}
}

func allWorkflowSteps(workflow string) []string {
	lines := strings.Split(workflow, "\n")
	var steps []string
	for start := 0; start < len(lines); {
		if !strings.HasPrefix(lines[start], "      - ") {
			start++
			continue
		}
		end := start + 1
		for end < len(lines) && !strings.HasPrefix(lines[end], "      - ") {
			end++
		}
		steps = append(steps, withoutYAMLComments(strings.Join(lines[start:end], "\n")))
		start = end
	}
	return steps
}

func withoutYAMLComments(workflow string) string {
	lines := strings.Split(workflow, "\n")
	kept := lines[:0]
	for _, line := range lines {
		line = stripYAMLComment(line)
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func stripYAMLComment(line string) string {
	if index := strings.Index(line, " #"); index >= 0 {
		return strings.TrimRight(line[:index], " ")
	}
	return line
}

func TestGitHubCIYAMLHasConsistentIndentation(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(source)), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for number, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent%2 != 0 {
			t.Errorf("line %d has odd YAML indentation: %q", number+1, line)
		}
	}
}
