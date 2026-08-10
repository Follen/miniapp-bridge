package frida

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	agent "github.com/Follen/miniapp-bridge/frida"
	"github.com/Follen/miniapp-bridge/internal/version"
)

func TestAuditEmbeddedAgentMatchesPinnedReference(t *testing.T) {
	t.Parallel()
	canonical := strings.ReplaceAll(agent.AgentSource, "\r\n", "\n")
	sum := sha256.Sum256([]byte(canonical))
	got := strings.ToUpper(hex.EncodeToString(sum[:]))
	const want = "D2D6BCECA0ACD668E6F5C387E3FF3F8E6FA5E0C2B5EC561DB01983E75EAE6FAC"
	if got != want {
		t.Fatalf("embedded Agent SHA-256=%s, want %s", got, want)
	}
}

func TestAuditAgentConfigReplacementIsStructuredAndComplete(t *testing.T) {
	t.Parallel()
	config := version.AddressConfig{Version: 25297, LoadStartHookOffset: "0x2A5D800", CDPFilterHookOffset: "0x38EA370", SceneOffsets: []int{64, 1496, 8, 1432, 16, 456}}
	source := agent.SourceForConfig(config)
	if strings.Contains(source, "@@CONFIG@@") {
		t.Fatal("Agent still contains config placeholder")
	}
	re := regexp.MustCompile("(?s)const rawConfig = `([^`]*)`;")
	match := re.FindStringSubmatch(source)
	if len(match) != 2 {
		t.Fatal("Agent rawConfig literal not found")
	}
	var got version.AddressConfig
	if err := json.Unmarshal([]byte(match[1]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != config.Version || got.LoadStartHookOffset != config.LoadStartHookOffset || got.CDPFilterHookOffset != config.CDPFilterHookOffset || len(got.SceneOffsets) != 6 {
		t.Fatalf("embedded config=%+v", got)
	}
}

func TestAuditAgentConfigReplacementCoversEveryPinnedVersion(t *testing.T) {
	t.Parallel()
	configs, err := version.LoadDir("../../configs/addresses")
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 47 {
		t.Fatalf("address configs=%d, want 47", len(configs))
	}
	re := regexp.MustCompile("(?s)const rawConfig = `([^`]*)`;")
	for number, config := range configs {
		source := agent.SourceForConfig(config)
		if strings.Contains(source, "@@CONFIG@@") {
			t.Errorf("version %d still contains config placeholder", number)
			continue
		}
		match := re.FindStringSubmatch(source)
		if len(match) != 2 {
			t.Errorf("version %d rawConfig literal not found", number)
			continue
		}
		var got version.AddressConfig
		if err := json.Unmarshal([]byte(match[1]), &got); err != nil {
			t.Errorf("version %d embedded config: %v", number, err)
			continue
		}
		if !reflect.DeepEqual(got, config) {
			t.Errorf("version %d embedded config=%+v want=%+v", number, got, config)
		}
	}
}

func TestAuditAgentContainsEveryReferencePatchBranch(t *testing.T) {
	t.Parallel()
	required := []string{
		`version >= 13331`, `findModuleByName("flue.dll")`, `findModuleByName("WeChatAppEx.exe")`,
		`readU32() == 6`, `writeU32(0x0)`, `writeInt(1101)`,
		`1005, 1007, 1008, 1027, 1035, 1053, 1074, 1145, 1178, 1256, 1260, 1302`,
		`this.context.rdx & 0xff`, `patchOnLoadStart(mainModule.base, config)`, `patchCDPFilter(mainModule.base, config)`,
	}
	for _, token := range required {
		if !strings.Contains(agent.AgentSource, token) {
			t.Errorf("embedded Agent missing reference branch %q", token)
		}
	}
}

func TestAuditAgentOwnsOnlyReferenceInterceptorsAndNeverTerminatesTarget(t *testing.T) {
	t.Parallel()
	if got := strings.Count(agent.AgentSource, "Interceptor.attach"); got != 2 {
		t.Fatalf("Agent Interceptor.attach count=%d, want the two reference hooks", got)
	}
	for _, forbidden := range []string{
		"Interceptor.replace", "Interceptor.replaceFast", "Memory.patchCode",
		"Process.kill", "TerminateProcess", "ExitProcess", "abort()", "exit(",
	} {
		if strings.Contains(agent.AgentSource, forbidden) {
			t.Errorf("Agent unexpectedly contains target-termination or unowned-patch primitive %q", forbidden)
		}
	}
}
