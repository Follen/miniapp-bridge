package main

import (
	"os"
	"strings"
	"testing"
)

func TestAuditMainIsThinSDKAdapter(t *testing.T) {
	t.Parallel()
	source := readMainAuditSource(t, "main.go")
	last := -1
	for _, token := range []string{"config.Parse(", "newService(", "notify(", "service.Start(", "<-lifetime.Done()", "service.Close(context.Background())"} {
		relative := strings.Index(source[last+1:], token)
		if relative < 0 {
			t.Fatalf("token %q not found after=%d", token, last)
		}
		index := last + 1 + relative
		if index <= last {
			t.Fatalf("token %q index=%d after=%d", token, index, last)
		}
		last = index
	}
	for _, forbidden := range []string{"startNative(", "nativeCloser", "NativeStarter", "internal/frida", "internal/logging"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("CLI owns forbidden implementation %q", forbidden)
		}
	}
}

func TestAuditMainForwardsCLIOptionsToSDK(t *testing.T) {
	t.Parallel()
	source := readMainAuditSource(t, "main.go")
	for _, token := range []string{"DebugPort: o.DebugPort", "CDPPort: o.CDPPort", "RecordPath: o.RecordPath", "ReplayPath: o.ReplayPath", "DebugMain: o.DebugMain", "DebugFrida: o.DebugFrida"} {
		if !strings.Contains(source, token) {
			t.Fatalf("SDK option %q is not forwarded", token)
		}
	}
}

func readMainAuditSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
