package frida

import (
	"context"
	"errors"
	"testing"

	"github.com/Follen/miniapp-bridge/internal/process"
)

func TestMockLifecycleBranches(t *testing.T) {
	device := NewMockDevice()
	session, err := device.Attach(42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := device.Attach(42); err == nil {
		t.Fatal("expected duplicate attach error")
	}
	script, err := session.LoadScript("source")
	if err != nil {
		t.Fatal(err)
	}
	if err := script.Post([]byte("message")); err != nil {
		t.Fatal(err)
	}
	if err := script.Unload(); err != nil {
		t.Fatal(err)
	}
	if err := script.Post(nil); err == nil {
		t.Fatal("expected post-after-unload error")
	}
	if err := session.Detach(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.LoadScript("source"); err == nil {
		t.Fatal("expected load-after-detach error")
	}
}

func TestBootstrapRemainingErrors(t *testing.T) {
	if _, _, _, err := (Bootstrap{}).Attach(context.Background()); err == nil {
		t.Fatal("expected nil device error")
	}
	for name, device := range map[string]*auditDevice{
		"missing child":           {processes: []process.Process{{PID: 1, Name: "other.exe"}}, session: &auditSession{}},
		"missing parent metadata": {processes: []process.Process{{PID: 1, ParentPID: 9, Name: "WeChatAppEx.exe"}}, session: &auditSession{}},
		"attach failure":          {processes: auditProcesses(), session: &auditSession{}, attachErr: errors.New("attach failed")},
	} {
		t.Run(name, func(t *testing.T) {
			bootstrap := Bootstrap{Device: device, ConfigDir: writeAuditConfig(t)}
			if _, _, _, err := bootstrap.Attach(context.Background()); err == nil {
				t.Fatal("expected bootstrap error")
			}
		})
	}
}
