package frida

import (
	"strings"
	"testing"

	"github.com/Follen/miniapp-bridge/internal/version"
)

func TestSourceForConfigEmbedsJSONConfiguration(t *testing.T) {
	source := SourceForConfig(version.AddressConfig{
		Version:             25297,
		LoadStartHookOffset: "0x1234",
		CDPFilterHookOffset: "0x5678",
		SceneOffsets:        []int{1, 2, 3, 4, 5, 6},
	})
	if strings.Contains(source, "@@CONFIG@@") {
		t.Fatal("agent config placeholder was not replaced")
	}
	for _, want := range []string{`"Version":25297`, `"LoadStartHookOffset":"0x1234"`, `"CDPFilterHookOffset":"0x5678"`} {
		if !strings.Contains(source, want) {
			t.Fatalf("agent source missing %q", want)
		}
	}
}
