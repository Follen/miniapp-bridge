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
		"observed-new-target-pids=",
		"Get-CimInstance Win32_Process",
		"ParentProcessId",
		"StartTimeUtcTicks",
		"MainWindowHandle",
		"CommandLine",
		"$upstreamPeerTargets",
		"$newRendererTargets",
		"$newAppletRendererTargets",
		"Test-IsAnyAppletRendererProcess",
		"--wmpf-render-type=4",
		"--wmpf-appid=(?!preload-)",
		"fallback-applet-renderer=true",
		"no new type=4 applet renderer remained after the CDP checks",
		"$trackedTargetRoles",
		"Start-Sleep -Seconds 5",
		"no new renderer remained after the CDP checks",
		"no new type=4 applet renderer remained after the CDP checks",
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
	if !strings.Contains(script, "'renderer,applet-renderer'") {
		t.Fatal("smoke does not distinguish the applet renderer from generic renderer processes")
	}
	if !strings.Contains(script, "$_.LocalPort -eq $serverConnection.RemotePort") ||
		!strings.Contains(script, "$_.RemotePort -eq $serverConnection.LocalPort") {
		t.Fatal("upstream peer is not resolved from both sides of the TCP port tuple")
	}
	if !strings.Contains(script, "$_.OwningProcess -gt 0") {
		t.Fatal("upstream peer lookup does not filter system-owned connections")
	}
	if !strings.Contains(script, "for ($attempt = 0; $attempt -lt 10; $attempt++)") {
		t.Fatal("upstream peer lookup lacks process-snapshot retry")
	}
	identityCheck := strings.Index(script, "$finalTargetsByIdentity.ContainsKey($_)")
	survivalSuccess := strings.Index(script, "target-survives-shutdown=true")
	if identityCheck < 0 || identityCheck > survivalSuccess {
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
