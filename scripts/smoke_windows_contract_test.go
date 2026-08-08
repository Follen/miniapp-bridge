package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsSmokeUsesGracefulProcessGroupShutdown(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(source), "smoke-windows.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	required := []string{
		"go build -o $runnerExe ./scripts/smoke-process-runner",
		"-stop-file=",
		"--debug-frida",
		"New-Item -ItemType File -Path $stopFile",
		"action-required=open-or-reload-miniapp",
		"$attachLogOffset",
		"$postAttachOutput",
		"$attachedTargetPid",
		"AppletIndexContainer::OnLoadStart onEnter",
		"agent-on-load-start=true",
		"observed-new-target-pids=",
		"Get-CimInstance Win32_Process",
		"ParentProcessId",
		"StartTimeUtcTicks",
		"MainWindowHandle",
		"CommandLine",
		"$upstreamPeerTargets",
		"$tcpConnections",
		"$lastPeerPortCandidates",
		"Get-NetTCPConnection -State Established",
		"$selectedAppletRendererTargets",
		"$attachedHostTargets",
		"$attachedHostTarget",
		"attached-host",
		"$reusedAppletRendererTargets",
		"ParentId -eq $attachedTargetPid",
		"renderer-selection=reused",
		"Test-IsAnyAppletRendererProcess",
		"--wmpf-render-type=4",
		"--wmpf-appid=(?!preload-)",
		"fallback-applet-renderer=true",
		"no type=4 applet renderer correlated with the post-attach load",
		"go run scripts/smoke-client.go --url ws://127.0.0.1:62000 --mode matrix",
		"live CDP matrix failed with exit $LASTEXITCODE",
		"go run scripts/smoke-client.go --url ws://127.0.0.1:62000 --mode link",
		"CDP link smoke failed with exit $LASTEXITCODE",
		"$trackedTargetRoles",
		"Start-Sleep -Seconds 5",
		"tracked target process exited during bridge shutdown",
		"tracked target window stopped responding after bridge shutdown",
		"child-exit-code=0",
		"target-survives-shutdown=true",
		"target-process-delta: new=",
		"transient-exited=",
		"ports-released=true",
		"force termination fallback was used",
		"[switch]$KeepBridgeRunning",
		"bridge-kept-running=true",
		"bridge-stop-file=$stopFile",
		"bridge-stop-command=New-Item -ItemType File -LiteralPath",
		"devtools-url=devtools://devtools/bundled/inspector.html?ws=127.0.0.1:62000",
	}
	for _, token := range required {
		if !strings.Contains(script, token) {
			t.Errorf("smoke-windows.ps1 is missing %q", token)
		}
	}
	if strings.Contains(script, "Stop-Process -Id $process.Id -Force") {
		t.Fatal("legacy unconditional force termination is still present")
	}
	if strings.Contains(script, "$survivingPeers.Count -eq 0") {
		t.Fatal("smoke still accepts any surviving upstream peer without checking process identity")
	}
	if strings.Contains(script, "$newRendererTargets.Count -eq 0") ||
		strings.Contains(script, "$newAppletRendererTargets.Count -eq 0") {
		t.Fatal("smoke still requires a newly created renderer instead of accepting a post-attach reload on a reused renderer")
	}
	if strings.Contains(script, "$initialAppletRendererTargets | Select-Object -First 1") {
		t.Fatal("smoke accepts an arbitrary pre-existing applet renderer without correlating it to the post-attach load")
	}
	if strings.Contains(script, "foreach ($item in $candidateRendererTargets)") ||
		strings.Contains(script, "foreach ($item in @($candidateRendererTargets))") {
		t.Fatal("smoke tracks candidate renderers instead of only the selected applet renderers")
	}
	if strings.Contains(script, "foreach ($item in $newRendererTargets)") ||
		strings.Contains(script, "foreach ($item in @($newRendererTargets))") {
		t.Fatal("smoke tracks every new renderer instead of only the selected applet renderers")
	}
	if strings.Contains(script, "trackedTargetRoles[$item.Identity] = 'renderer'") {
		t.Fatal("smoke can track an unselected generic renderer by role")
	}
	if !strings.Contains(script, "'renderer,applet-renderer'") {
		t.Fatal("smoke does not distinguish the applet renderer from generic renderer processes")
	}
	if !strings.Contains(script, "function Normalize-TcpAddress") ||
		!strings.Contains(script, "IsIPv4MappedToIPv6") ||
		!strings.Contains(script, "MapToIPv4") {
		t.Fatal("smoke does not normalize IPv4-mapped IPv6 TCP addresses")
	}
	if !strings.Contains(script, "$_.LocalPort -eq $serverConnection.RemotePort") ||
		!strings.Contains(script, "$_.RemotePort -eq $serverConnection.LocalPort") ||
		!strings.Contains(script, "(Normalize-TcpAddress $_.LocalAddress) -eq (Normalize-TcpAddress $serverConnection.RemoteAddress)") ||
		!strings.Contains(script, "(Normalize-TcpAddress $_.RemoteAddress) -eq (Normalize-TcpAddress $serverConnection.LocalAddress)") {
		t.Fatal("upstream peer is not resolved from both sides of the TCP address and port tuple")
	}
	if !strings.Contains(script, "$_.OwningProcess -gt 0") {
		t.Fatal("upstream peer lookup does not filter system-owned connections")
	}
	if !strings.Contains(script, "for ($attempt = 0; $attempt -lt 10; $attempt++)") {
		t.Fatal("upstream peer lookup lacks process-snapshot retry")
	}
	for _, token := range []string{
		"$tcpValidationConnections = @(Get-NetTCPConnection -State Established",
		"$currentServers = @($tcpValidationConnections | Where-Object",
		"$currentPeers = @(foreach ($serverConnection in $currentServers)",
		"$serverMatch = @($currentServers | Where-Object",
		"$peerMatch = @($currentPeers | Where-Object",
		"$peerTupleMatch = @($currentPeers | Where-Object",
		"$peerIdentity",
		"upstream-peer-validated=true",
		"server-four-tuple-missing",
		"peer-four-tuple-missing",
		"peer-owning-pid-changed",
		"peer-pid-start-time-changed",
		"reselected-peer=",
		"failed TOCTOU validation after 10 attempts",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("smoke-windows.ps1 is missing TOCTOU validation token %q", token)
		}
	}
	peerDeadline := strings.Index(script, "$deadline = [DateTime]::UtcNow.AddSeconds($UpstreamWaitSeconds)")
	tcpSnapshot := strings.Index(script, "$tcpConnections = @(Get-NetTCPConnection -State Established")
	serverRefresh := strings.Index(script, "$upstream = @($tcpConnections | Where-Object")
	peerMatch := strings.Index(script, "$upstreamPeerConnections = @(foreach ($serverConnection in $upstream)")
	peerPID := strings.Index(script, "$upstreamPeerPids = @($upstreamPeerConnections")
	if peerDeadline < 0 || tcpSnapshot < peerDeadline || serverRefresh < tcpSnapshot || peerMatch < serverRefresh || peerPID < peerMatch {
		t.Fatal("upstream peer lookup must derive both endpoints from one TCP-table snapshot before PID extraction")
	}
	validationTCP := strings.Index(script, "$tcpValidationConnections = @(Get-NetTCPConnection -State Established")
	validationSnapshot := -1
	if validationTCP >= 0 {
		relative := strings.Index(script[validationTCP:], "$connectedTargetSnapshot = @(Get-TargetProcessSnapshot)")
		if relative >= 0 {
			validationSnapshot = validationTCP + relative
		}
	}
	serverMatchValidation := strings.Index(script, "$serverMatch = @($currentServers | Where-Object")
	peerMatchValidation := strings.Index(script, "$peerMatch = @($currentPeers | Where-Object")
	validationSuccess := strings.Index(script, "upstream-peer-validated=true")
	if validationTCP < peerPID || validationSnapshot < validationTCP || serverMatchValidation < validationSnapshot || peerMatchValidation < serverMatchValidation || validationSuccess < peerMatchValidation {
		t.Fatal("upstream peer TOCTOU validation must reread TCP and process snapshots before accepting the peer")
	}
	if !strings.Contains(script, "$peerProcess[0].Identity -ne $peerIdentity") {
		t.Fatal("peer start-time validation diagnostic is missing its identity comparison")
	}
	if !strings.Contains(script, "if ($upstreamPeerConnections.Count -gt 0) { break }") ||
		!strings.Contains(script, "could not be identified within ${UpstreamWaitSeconds}s; server=") ||
		!strings.Contains(script, "reverse-port-candidates=") {
		t.Fatal("upstream peer lookup lacks bounded same-snapshot retry or endpoint diagnostics")
	}
	attachLogOffset := strings.Index(script, "$attachLogOffset")
	onLoadStartCheck := strings.Index(script, "$postAttachOutput -match 'AppletIndexContainer::OnLoadStart onEnter'")
	upstreamCheck := strings.Index(script, "if (-not $upstream)")
	firstCDPCheck := strings.Index(script, "go run scripts/smoke-client.go --url ws://127.0.0.1:62000 --mode matrix")
	if attachLogOffset < 0 || onLoadStartCheck < attachLogOffset || upstreamCheck < attachLogOffset || firstCDPCheck < onLoadStartCheck || firstCDPCheck < upstreamCheck {
		t.Fatal("post-attach OnLoadStart, upstream identification, and CDP matrix are not enforced in order")
	}
	newSelection := strings.Index(script, "renderer-selection=new")
	reusedSelection := strings.Index(script, "renderer-selection=reused")
	selectedCollection := strings.Index(script, "$selectedAppletRendererTargets =")
	newRendererSelection := strings.Index(script, "$newAppletRendererTargets = @($newRendererTargets |")
	attachedHostSelection := strings.Index(script, "$attachedHostTargets = @($initialTargetSnapshot | Where-Object { $_.Id -eq $attachedTargetPid })")
	attachedHostCount := strings.Index(script, "$attachedHostTargets.Count -ne 1")
	attachedHostRole := strings.Index(script, "$trackedTargetRoles[$attachedHostTarget.Identity] = 'attached-host'")
	upstreamTracking := strings.Index(script, "foreach ($item in @($upstreamPeerTargets))")
	tracking := strings.Index(script, "foreach ($item in @($selectedAppletRendererTargets))")
	windowTracking := strings.Index(script, "foreach ($item in $newWindowTargets)")
	if newSelection < 0 || reusedSelection < 0 || tracking < newSelection || tracking < reusedSelection {
		t.Fatal("new and reused applet renderer selections must both feed exact process tracking")
	}
	if selectedCollection < 0 || selectedCollection > tracking {
		t.Fatal("selected applet renderer collection must be materialized before renderer tracking")
	}
	if newRendererSelection < 0 || newSelection <= newRendererSelection {
		t.Fatal("new applet renderer selection block is missing or emitted after its diagnostic")
	}
	newRendererBlock := script[newRendererSelection:newSelection]
	if !strings.Contains(newRendererBlock, "ParentId -eq $attachedTargetPid") ||
		!strings.Contains(newRendererBlock, "Sort-Object StartTimeUtcTicks -Descending") ||
		!strings.Contains(newRendererBlock, "Select-Object -First 1") {
		t.Fatal("new applet renderer selection must be parent-correlated, newest-first, and limited to one process")
	}
	if attachedHostSelection < 0 || attachedHostCount < attachedHostSelection || attachedHostRole < attachedHostCount || attachedHostRole > upstreamTracking {
		t.Fatal("attached Frida host must be selected by exact initial PID identity before upstream tracking")
	}
	if upstreamTracking < 0 || tracking < 0 || windowTracking < 0 || attachedHostRole > upstreamTracking || upstreamTracking > tracking || tracking > windowTracking {
		t.Fatal("attached host, upstream peer, selected applet renderer, and window owner must be tracked independently and in order")
	}
	identityCheck := strings.Index(script, "$finalTargetsByIdentity.ContainsKey($_)")
	survivalSuccess := strings.Index(script, "target-survives-shutdown=true")
	if identityCheck < windowTracking || identityCheck > survivalSuccess {
		t.Fatal("target survival success is emitted before exact PID/start-time identity checks")
	}
	stopRequest := strings.Index(script, "New-Item -ItemType File -Path $stopFile")
	lastCDPCheck := strings.LastIndex(script, "go run scripts/smoke-client.go")
	if stopRequest < lastCDPCheck {
		t.Fatal("graceful shutdown is requested before CDP verification completes")
	}
	keepMode := strings.Index(script, "if ($KeepBridgeRunning)")
	keepOutput := strings.Index(script, "bridge-kept-running=true")
	if keepMode < 0 || keepOutput < keepMode {
		t.Fatal("interactive keep-running mode is missing or emitted outside its branch")
	}
}
