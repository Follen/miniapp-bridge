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
	assertBlockLines(t, workflow, "on", []string{"push:", "pull_request:", "workflow_dispatch:"})
	assertBlockLines(t, workflow, "permissions", []string{"contents: read"})

	for _, required := range []string{
		"  group: ci-${{ github.workflow }}-${{ github.event.pull_request.head.sha || github.sha }}\n",
		"  cancel-in-progress: true\n",
		"GO_VERSION: 1.26.x",
		"GOVULNCHECK_VERSION: v1.6.0",
		"ACTIONLINT_VERSION: 1.7.7",
		"FRIDA_CORE_VERSION: 17.3.2",
		"ZLIB_VERSION: 1.3.1",
		"ZLIB_ARCHIVE_SHA256: 17E88863F3600672AB49182F217281B6FC4D3C762BDE361935E436A95214D05C",
		"runs-on: ubuntu-latest",
		"timeout-minutes: 30",
		"go mod download",
		"go mod verify",
		"go mod tidy",
		"git diff --exit-code -- go.mod go.sum",
		"git ls-files -z -- '*.go'",
		"gofmt -l \"${go_files[@]}\"",
		"go run \"github.com/rhysd/actionlint/cmd/actionlint@v${ACTIONLINT_VERSION}\" -ignore 'unexpected key.*queue.*concurrency'",
		"go test ./... -count=1 -timeout 120s",
		"go vet ./...",
		"go test -race ./... -count=1 -timeout 240s",
		"runs-on: windows-2022",
		"timeout-minutes: 90",
		"Microsoft.VisualStudio.Component.VC.Tools.x86.x64",
		"gcc.exe",
		"ar.exe",
		"third_party/downloads/frida-core-devkit-17.3.2-windows-x86_64.tar.xz",
		"third_party/downloads/cache/zlib-1.3.1.tar.gz",
		"third_party/downloads/frida-core-devkit-17.3.2-windows-x86_64.tar.xz",
		"hashFiles('go.sum')",
		"frida-${{ env.FRIDA_CORE_VERSION }}-zlib-${{ env.ZLIB_VERSION }}",
		"${{ env.ZLIB_ARCHIVE_SHA256 }}-${{ hashFiles('go.sum') }}",
		"Populate Frida devkit archive cache",
		"GH_TOKEN: ${{ github.token }}",
		"gh release download $env:FRIDA_CORE_VERSION --repo frida/frida --pattern $asset --output $archive --clobber",
		"8AF15423D6E534626F91A67FAA0582E42C67A07A95A190F4C622695105549C72",
		"Frida devkit archive SHA-256 mismatch",
		".\\scripts\\build-windows.ps1",
		"Windows build and release behavior gates",
		"Test(BuildWindows|NativeRelease|PackageWindowsRelease|NativePrepare)",
		".\\scripts\\coverage-gate.ps1",
		"retention-days: 7",
	} {
		if !strings.Contains(withoutYAMLComments(workflow), required) {
			t.Errorf("workflow is missing contract marker %q", required)
		}
	}

	usesPattern := regexp.MustCompile(`(?m)^\s*uses:\s+([^\s#]+)`)
	uses := usesPattern.FindAllStringSubmatch(withoutYAMLComments(workflow), -1)
	wantActionCounts := map[string]int{
		"actions/checkout":        2,
		"actions/setup-go":        2,
		"actions/cache":           1,
		"actions/upload-artifact": 3,
	}
	approvedActions := map[string]bool{
		"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1":        true,
		"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e":        true,
		"actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9":           true,
		"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a": true,
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
	if len(gotActionCounts) != len(wantActionCounts) {
		t.Errorf("workflow action set mismatch: got %v, want %v", gotActionCounts, wantActionCounts)
	}
	for action, want := range wantActionCounts {
		if got := gotActionCounts[action]; got != want {
			t.Errorf("workflow action count for %s = %d, want %d", action, got, want)
		}
	}
	assertEveryCheckoutDisablesCredentials(t, workflow)

	cacheStep := workflowStep(t, workflow, "Restore pinned native caches")
	assertMultilineValues(t, cacheStep, "path", []string{
		"third_party/downloads/frida-core-devkit-17.3.2-windows-x86_64.tar.xz",
		"third_party/downloads/cache/zlib-1.3.1.tar.gz",
	})

	buildStep := workflowStep(t, workflow, "Windows native build")
	populateStep := workflowStep(t, workflow, "Populate Frida devkit archive cache")
	populate := strings.Index(workflow, "Populate Frida devkit archive cache")
	removeSource := strings.Index(buildStep, "Remove-Item -LiteralPath $zlibSource -Recurse -Force")
	buildWindows := strings.Index(buildStep, ".\\scripts\\build-windows.ps1")
	if populate < 0 || strings.Index(workflow, "gh release download $env:FRIDA_CORE_VERSION") < populate || strings.Index(workflow, "Get-FileHash -Algorithm SHA256 -LiteralPath $archive") < populate {
		t.Fatal("Windows CI must populate and verify the pinned Frida archive before building")
	}
	if !strings.Contains(populateStep, "GH_TOKEN: ${{ github.token }}") {
		t.Fatal("Frida cache population must use the workflow token for authenticated GitHub downloads")
	}
	if removeSource < 0 || buildWindows < 0 || removeSource > buildWindows {
		t.Fatal("Windows build must remove the cached zlib source tree before the verified archive build")
	}
	behaviorStep := workflowStep(t, workflow, "Windows build and release behavior gates")
	for _, marker := range []string{
		"Test(BuildWindows|NativeRelease|PackageWindowsRelease|NativePrepare)",
		"windows-build-release-behavior.log",
	} {
		if !strings.Contains(behaviorStep, marker) {
			t.Errorf("Windows behavior gate is missing %q", marker)
		}
	}
	if strings.Contains(workflow, "PowerShell command coverage") || strings.Contains(workflow, ".\\scripts\\powershell-coverage.ps1") {
		t.Fatal("Windows required CI must use production behavior gates instead of PowerShell numerical command coverage")
	}

	linuxLogs := workflowStep(t, workflow, "Upload Linux test logs")
	assertStepLine(t, linuxLogs, "if: always()")
	assertStepLine(t, linuxLogs, "path: ci-artifacts/**")
	windowsLogs := workflowStep(t, workflow, "Upload Windows verification logs")
	assertStepLine(t, windowsLogs, "if: always()")
	assertStepLine(t, windowsLogs, "path: ci-artifacts/**")
	if strings.Contains(windowsLogs, "dist/") {
		t.Fatal("always-run Windows log upload must not include build outputs")
	}
	windowsBinaries := workflowStep(t, workflow, "Upload trusted Windows binaries")
	assertStepLine(t, windowsBinaries, "if: success() && (github.event_name == 'push' || github.event_name == 'workflow_dispatch')")
	assertMultilineValues(t, windowsBinaries, "path", []string{
		"dist/miniapp-bridge.exe",
		"dist/miniapp-frida.dll",
		"dist/manifest.json",
		"dist/native/miniapp-frida-native-17.3.2-abi1-windows-amd64.zip",
		"dist/native/SHA256SUMS",
	})
	assertStepLine(t, windowsBinaries, "if-no-files-found: error")
	if strings.Contains(windowsBinaries, "ci-artifacts/") {
		t.Fatal("trusted binary upload must remain separate from always-run logs")
	}
	moduleGate := strings.Index(workflow, "go mod download")
	formatGate := strings.Index(workflow, "git ls-files -z -- '*.go'")
	actionlintGate := strings.Index(workflow, "go run \"github.com/rhysd/actionlint/cmd/actionlint@v${ACTIONLINT_VERSION}\" -ignore 'unexpected key.*queue.*concurrency'")
	unitTests := strings.Index(workflow, "go test ./... -count=1 -timeout 120s")
	if moduleGate < 0 || formatGate < moduleGate || actionlintGate < formatGate || unitTests < actionlintGate {
		t.Fatal("module, gofmt, and actionlint gates must run before Linux tests")
	}

	for _, forbidden := range []string{
		"pull_request_target:",
		"write-all",
		"${{ secrets.",
		"path: .\n",
		"path: **",
	} {
		if strings.Contains(withoutYAMLComments(workflow), forbidden) {
			t.Errorf("workflow contains forbidden security or artifact marker %q", forbidden)
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
