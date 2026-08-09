package sdk

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestNativeInfoLogUsesAppLoggerOnce(t *testing.T) {
	var output bytes.Buffer
	native := &advancedNative{}
	s, err := New(Options{
		DebugPort: sdkFreePort(t),
		CDPPort:   sdkFreePort(t),
		Stdout:    &output,
		Stderr:    &output,
		Native: func(_ context.Context, publish func(LogEvent)) (NativeSession, error) {
			publish(LogEvent{Level: "info", Message: "[frida] attached pid=7 version=25297 path=TARGET"})
			return native, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	logs := s.SubscribeLogs()
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-logs.Channel():
			if event.Level == "info" && strings.TrimSpace(event.Message) == "[frida] attached pid=7 version=25297 path=TARGET" {
				goto attachLogReceived
			}
		case <-deadline:
			t.Fatal("timed out waiting for attach log")
		}
	}

attachLogReceived:
	if got := strings.Count(output.String(), "[frida] attached pid=7 version=25297 path=TARGET"); got != 1 {
		t.Fatalf("attach log output count=%d output=%q", got, output.String())
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = logs.Close()
}
