package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGitHubReleaseWorkflowSecurityContract(t *testing.T) {
	workflow := readReleaseWorkflow(t)
	requireReleaseTokens(t, workflow, []string{
		"group: release-native-17.3.2-abi1",
		"queue: max",
		"push:\n    tags:\n      - 'v*'",
		"workflow_dispatch:",
		"required: true",
		"type: string",
		"permissions:\n  contents: read",
		"concurrency:",
		"cancel-in-progress: false",
		"runs-on: windows-2022",
		"timeout-minutes: 90",
		"verify:\n    name: Verify and package Windows amd64",
		"verify:\n    name: Verify and package Windows amd64\n    runs-on: windows-2022\n    timeout-minutes: 90\n    permissions:\n      contents: read",
		"release:\n    name: Publish GitHub Releases",
		"permissions:\n      contents: write",
		"persist-credentials: false",
		"fetch-depth: 0",
		"Reconcile and publish GitHub Releases",
	})
	for _, forbidden := range []string{"pull_request:", "pull_request_target:", "permissions: write-all", "gh release create", "gh release upload"} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("release workflow contains forbidden token %q", forbidden)
		}
	}

	jobs := releaseJobBodies(t, workflow)
	for name, body := range jobs {
		if name != "release" && strings.Contains(body, "contents: write") {
			t.Fatalf("non-publisher job %q must not receive contents:write", name)
		}
	}
	if !strings.Contains(jobs["release"], "contents: write") {
		t.Fatal("release job must be the only job with contents:write")
	}
	if strings.Contains(jobs["release"], "actions/checkout") || strings.Contains(jobs["release"], "scripts\\") || strings.Contains(jobs["release"], "scripts/") {
		t.Fatal("write-enabled release job must not check out or execute repository scripts")
	}

	allowedActions := map[string]bool{
		"actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683":          true,
		"actions/setup-go@0a12ed9d6a96ab950c8f026ed9f722fe0da7ef32":          true,
		"actions/cache@5a3ec84eff668545956fd18022155c47e93e2684":             true,
		"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02":   true,
		"actions/download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093": true,
	}
	uses := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^\s]+)(?:\s+#.*)?$`).FindAllStringSubmatch(workflow, -1)
	if len(uses) != 5 {
		t.Fatalf("release workflow actions=%d want 5", len(uses))
	}
	for _, match := range uses {
		if !allowedActions[match[1]] {
			t.Fatalf("release workflow uses non-approved action %q", match[1])
		}
	}
}

func TestGitHubReleaseWorkflowBuildAndVersionContract(t *testing.T) {
	workflow := readReleaseWorkflow(t)
	requireReleaseTokens(t, workflow, []string{
		"REQUESTED_TAG: ${{ github.event_name == 'workflow_dispatch' && inputs.tag || github.ref_name }}",
		"release tag must be a complete SemVer tag beginning with v",
		"$env:REQUESTED_TAG -cnotmatch $semver",
		"git show-ref --verify --quiet \"refs/tags/$env:REQUESTED_TAG\"",
		"git rev-list -n 1 \"refs/tags/$env:REQUESTED_TAG\"",
		"checked out commit $sourceCommit does not match tag",
		"go mod verify",
		"third_party/frida/devkit-17.3.2",
		"gcc.exe",
		"ar.exe",
		"Microsoft.VisualStudio.Component.VC.Tools.x86.x64",
		"go test ./... -count=1",
		".\\scripts\\coverage-gate.ps1",
		".\\scripts\\build-windows.ps1",
		".\\scripts\\package-windows-release.ps1 -Version $env:RELEASE_TAG",
		"native_tag=native-v17.3.2-abi1",
		"manifest.nativeVersion",
		"native archive hash $nativeHash does not match SDK pin $env:NATIVE_ARCHIVE_SHA256",
		"sha256sum --check SHA256SUMS",
		"if-no-files-found: error",
	})
	if strings.Contains(workflow, "            third_party/zlib/src-1.3.1") {
		t.Fatal("release workflow must not cache the extracted zlib source tree")
	}
	semverLine := regexp.MustCompile(`(?m)^\s*\$semver = '([^']+)'\s*$`).FindStringSubmatch(workflow)
	if len(semverLine) != 2 {
		t.Fatal("release workflow lacks an explicit SemVer validation expression")
	}
	semver, err := regexp.Compile(semverLine[1])
	if err != nil {
		t.Fatalf("invalid release SemVer expression: %v", err)
	}
	for _, valid := range []string{"v0.0.0", "v0.1.0", "v1.2.3", "v1.2.3-rc.1", "v1.2.3-alpha-beta.1"} {
		if !semver.MatchString(valid) {
			t.Errorf("SemVer expression rejected %q", valid)
		}
	}
	for _, invalid := range []string{
		"1.2.3", "v1", "v1.2", "v01.2.3", "v1.02.3", "v1.2.03", "v1.2.3-01", "vlatest",
		"v0.1.0+build.7", "v1.2.3+build.7", "v2.0.0", "v2.0.0-rc.1", "v10.0.0",
		"V0.0.1", "V1.2.3-rc.1",
	} {
		if semver.MatchString(invalid) {
			t.Errorf("SemVer expression accepted %q", invalid)
		}
	}
}

func TestGitHubReleaseWorkflowNativeArchiveHashMatchesSDK(t *testing.T) {
	workflow := readReleaseWorkflow(t)
	workflowMatch := regexp.MustCompile(`(?m)^  NATIVE_ARCHIVE_SHA256: ([0-9A-F]{64})$`).FindStringSubmatch(workflow)
	if len(workflowMatch) != 2 {
		t.Fatal("release workflow lacks a pinned NATIVE_ARCHIVE_SHA256")
	}
	runtimeSource, err := os.ReadFile(filepath.Join("..", "internal", "native", "runtime.go"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeMatch := regexp.MustCompile(`NativeArchiveSHA256\s*=\s*"([0-9A-F]{64})"`).FindSubmatch(runtimeSource)
	if len(runtimeMatch) != 2 {
		t.Fatal("internal/native/runtime.go lacks a pinned NativeArchiveSHA256")
	}
	if workflowMatch[1] != string(runtimeMatch[1]) {
		t.Fatalf("release native archive SHA=%s, SDK pin=%s", workflowMatch[1], runtimeMatch[1])
	}
	const expected = "1597ADCC6B3B13B5BCBA910904046AB7D2E1E3D73AE16961C73E400373BDE87A"
	if workflowMatch[1] != expected {
		t.Fatalf("release native archive SHA=%s, want pinned artifact %s", workflowMatch[1], expected)
	}
}

func TestGitHubReleaseWorkflowArtifactAndCompatibilityContract(t *testing.T) {
	workflow := readReleaseWorkflow(t)
	requireReleaseTokens(t, workflow, []string{
		"miniapp-bridge-$RELEASE_TAG-windows-amd64.zip",
		"miniapp-frida-native-17.3.2-abi1-windows-amd64.zip",
		"release-bundle/manifest.json",
		"release-bundle/SHA256SUMS",
		"release-bundle/native-compat/$NATIVE_ASSET",
		"release-bundle/native-compat/SHA256SUMS",
		"reconcile_assets()",
		"unexpected release asset",
		"duplicate release asset",
		"published release asset is missing",
		"release asset differs byte-for-byte",
		"release asset count mismatch",
		"Accept: application/octet-stream",
		"Content-Type: application/octet-stream",
		"cmp \"$file\" \"$downloaded\"",
		"release_core=\"${RELEASE_VERSION%%+*}\"",
		"if [[ \"$release_core\" == *-* ]]; then product_prerelease=true; fi",
		"native_release=\"$(create_or_resume_draft \"$NATIVE_TAG\" \"$native_target\" \"$native_title\" false false false)\"",
		"publish_reconciled_draft \"$native_release\" \"$NATIVE_TAG\" \"$native_target\" \"$native_title\" false false",
	})
	if strings.Index(workflow, "Verify downloaded checksums") > strings.Index(workflow, "Reconcile and publish GitHub Releases") {
		t.Fatal("release must verify downloaded checksums before accessing the GitHub Releases API")
	}
}

func TestGitHubReleaseWorkflowRecoverablePublishingContract(t *testing.T) {
	workflow := readReleaseWorkflow(t)
	requireReleaseTokens(t, workflow, []string{
		"gh api --paginate --slurp \"repos/$GITHUB_REPOSITORY/releases?per_page=100\"",
		"multiple releases exist for tag $tag",
		"resolve_tag_commit()",
		"git/ref/tags/$tag",
		"git/tags/$object_sha",
		"tag $tag exceeds the annotated-tag peel limit",
		"remote_product_commit=\"$(resolve_tag_commit \"$RELEASE_TAG\")\"",
		"remote_commit=\"$(resolve_tag_commit \"$tag\")\"",
		"remote tag $tag moved: expected $SOURCE_COMMIT, got $remote_commit",
		"assert_release_metadata()",
		"target_commitish=$target",
		"prerelease=$prerelease",
		"-F draft=true",
		"make_latest=$latest",
		"generate_release_notes=true",
		"draft creation failed without a recoverable release",
		"asset upload raced or failed, verifying remote state",
		"removing incomplete draft asset before retry",
		"asset delete raced or failed, verifying remote state",
		"release asset upload did not converge after 3 attempts and a final read-only probe",
		"final release asset set mismatch",
		"--method PATCH \"repos/$GITHUB_REPOSITORY/releases/$release_id\"",
		"-F draft=false",
		"release publish request failed, verifying remote state",
		"release $tag did not publish",
		"product_already_published=true",
		"published product release is exact; no assets were overwritten",
	})

	if !strings.Contains(workflow, "if [[ \"$tag\" == \"$RELEASE_TAG\" ]]; then\n              remote_commit=\"$(resolve_tag_commit \"$tag\")\"") {
		t.Fatal("product tag must be resolved again inside the final publication path")
	}
	finalAssets := strings.Index(workflow, "reconcile_assets \"$refreshed\" false \"${files[@]}\"")
	finalTag := strings.Index(workflow, "remote_commit=\"$(resolve_tag_commit \"$tag\")\"")
	publish := strings.Index(workflow, "gh api --method PATCH \"repos/$GITHUB_REPOSITORY/releases/$release_id\"")
	if finalAssets < 0 || finalTag < 0 || publish < 0 || !(finalAssets < finalTag && finalTag < publish) {
		t.Fatal("final asset verification, remote tag resolution, and release publication must be adjacent and ordered")
	}
	if !strings.Contains(workflow, "false false false)\"") ||
		!strings.Contains(workflow, "false false \"${native_files[@]}\"") {
		t.Fatal("native draft creation and publication must explicitly select make_latest=false")
	}
	if strings.Contains(workflow, "release already exists: $RELEASE_TAG") {
		t.Fatal("an exact published product release must be verified idempotently on retry")
	}
	if strings.Contains(workflow, "$env:REQUESTED_TAG -notmatch $semver") {
		t.Fatal("PowerShell release tag matching must remain case-sensitive")
	}
	if !strings.Contains(workflow, "concurrency:\n  group: release-native-17.3.2-abi1\n  queue: max\n  cancel-in-progress: false") {
		t.Fatal("all product releases sharing the pinned native tag must be serialized")
	}
	reconcileStart := strings.Index(workflow, "          reconcile_assets() {")
	reconcileEnd := strings.Index(workflow, "          create_or_resume_draft() {")
	if reconcileStart < 0 || reconcileEnd < reconcileStart {
		t.Fatal("release workflow lacks a bounded reconcile_assets function")
	}
	reconcile := workflow[reconcileStart:reconcileEnd]
	retryLoop := strings.Index(reconcile, "for attempt in 1 2 3; do")
	starter := strings.Index(reconcile, `if [[ "$asset_state" != starter ]]`)
	draftGuard := strings.Index(reconcile, `if [[ "$allow_upload" != true || "$(jq -r '.draft' <<<"$release_json")" != true ]]`)
	deleteAsset := strings.Index(reconcile, `gh api --method DELETE`)
	uploadAsset := strings.Index(reconcile, `upload_url="https://uploads.github.com/`)
	finalProbe := strings.Index(reconcile, `final read-only probe`)
	finalSet := strings.Index(reconcile, `final release asset set mismatch`)
	if retryLoop < 0 || starter < retryLoop || draftGuard < starter || deleteAsset < draftGuard || uploadAsset < deleteAsset || finalProbe < uploadAsset || finalSet < finalProbe {
		t.Fatal("draft asset recovery must detect bad assets, guard mutation, delete, retry upload, and verify the final exact set in order")
	}
	if strings.Count(workflow, "gh api --method DELETE") != 1 {
		t.Fatal("draft recovery must have exactly one guarded release-asset deletion point")
	}
	publishedFlag := strings.Index(workflow, "product_already_published=true")
	publishedReconcile := strings.LastIndex(workflow, "reconcile_assets \"$product_release\" false \"${product_files[@]}\"")
	publishedTagCheck := strings.LastIndex(workflow, "remote_product_commit=\"$(resolve_tag_commit \"$RELEASE_TAG\")\"")
	if publishedFlag < 0 || publishedReconcile < publishedFlag || publishedTagCheck < publishedReconcile {
		t.Fatal("an existing exact product release must be reconciled and its tag rechecked without overwriting assets")
	}
}

func readReleaseWorkflow(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func requireReleaseTokens(t *testing.T, workflow string, tokens []string) {
	t.Helper()
	for _, token := range tokens {
		if !strings.Contains(workflow, token) {
			t.Errorf("release workflow missing %q", token)
		}
	}
}

func releaseJobBodies(t *testing.T, workflow string) map[string]string {
	t.Helper()
	jobsAt := strings.Index(workflow, "\njobs:\n")
	if jobsAt < 0 {
		t.Fatal("release workflow has no jobs mapping")
	}
	jobsText := workflow[jobsAt+len("\njobs:\n"):]
	heading := regexp.MustCompile(`(?m)^  ([a-z][a-z0-9_-]*):\n`)
	matches := heading.FindAllStringSubmatchIndex(jobsText, -1)
	jobs := make(map[string]string, len(matches))
	for index, match := range matches {
		end := len(jobsText)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		jobs[jobsText[match[2]:match[3]]] = jobsText[match[0]:end]
	}
	if jobs["verify"] == "" || jobs["release"] == "" {
		t.Fatalf("release workflow jobs=%v", jobs)
	}
	return jobs
}
