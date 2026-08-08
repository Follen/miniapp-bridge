package frida

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"miniapp-bridge/internal/process"
	"miniapp-bridge/internal/version"
)

type auditDevice struct {
	events    []string
	processes []process.Process
	session   *auditSession
	attachErr error
}

func (d *auditDevice) Enumerate(context.Context) ([]process.Process, error) {
	d.events = append(d.events, "enumerate")
	return d.processes, nil
}

func (d *auditDevice) Attach(uint32) (Session, error) {
	d.events = append(d.events, "attach")
	if d.attachErr != nil {
		return nil, d.attachErr
	}
	d.session.owner = d
	return d.session, nil
}

type auditSession struct {
	owner    *auditDevice
	loadErr  error
	detached bool
}

func (s *auditSession) LoadScript(string) (Script, error) {
	s.owner.events = append(s.owner.events, "load")
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return auditScript{}, nil
}

func (s *auditSession) Detach() error {
	s.detached = true
	s.owner.events = append(s.owner.events, "detach")
	return nil
}

type auditScript struct{}

func (auditScript) Unload() error     { return nil }
func (auditScript) Post([]byte) error { return nil }

func writeAuditConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	content := []byte(`{"Version":25297,"LoadStartHookOffset":"0x1","CDPFilterHookOffset":"0x2","SceneOffsets":[1,2,3]}`)
	if err := os.WriteFile(filepath.Join(dir, "addresses.25297.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func auditProcesses() []process.Process {
	return []process.Process{
		{PID: 7, ParentPID: 99, Name: "WeChatAppEx.exe"},
		{PID: 8, ParentPID: 99, Name: "WeChatAppEx.exe"},
		{PID: 99, Name: "WeChatAppEx.exe", Path: `C:\WMPF\25297\WeChatAppEx.exe`, Version: 25297},
	}
}

func TestAuditBootstrapOrderAndSelectedMetadata(t *testing.T) {
	t.Parallel()
	d := &auditDevice{processes: auditProcesses(), session: &auditSession{}}
	b := Bootstrap{Device: d, ConfigDir: writeAuditConfig(t), Agent: func(config version.AddressConfig) string {
		if config.Version != 25297 {
			t.Errorf("Agent config version=%d, want 25297", config.Version)
		}
		return "audit-agent"
	}}
	session, script, target, err := b.Attach(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if session == nil || script == nil {
		t.Fatal("successful bootstrap returned nil session or script")
	}
	if target.PID != 99 || target.Version != 25297 || target.Path == "" {
		t.Fatalf("selected metadata=%+v", target)
	}
	if want := []string{"enumerate", "attach", "load"}; !reflect.DeepEqual(d.events, want) {
		t.Fatalf("events=%v, want %v", d.events, want)
	}
}

func TestAuditBootstrapLoadFailureDetaches(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("load failed")
	s := &auditSession{loadErr: sentinel}
	d := &auditDevice{processes: auditProcesses(), session: s}
	b := Bootstrap{Device: d, ConfigDir: writeAuditConfig(t)}
	_, _, _, err := b.Attach(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("Attach error=%v, want %v", err, sentinel)
	}
	if !s.detached {
		t.Fatal("session was not detached after script load failure")
	}
	if want := []string{"enumerate", "attach", "load", "detach"}; !reflect.DeepEqual(d.events, want) {
		t.Fatalf("events=%v, want %v", d.events, want)
	}
}

func TestAuditBootstrapConfigFailureDoesNotAttach(t *testing.T) {
	t.Parallel()
	d := &auditDevice{processes: auditProcesses(), session: &auditSession{}}
	b := Bootstrap{Device: d, ConfigDir: t.TempDir()}
	if _, _, _, err := b.Attach(context.Background()); err == nil {
		t.Fatal("Attach succeeded without an address config")
	}
	if want := []string{"enumerate"}; !reflect.DeepEqual(d.events, want) {
		t.Fatalf("events=%v, want %v", d.events, want)
	}
}

func TestAuditBootstrapIgnoresMalformedUnrelatedVersionConfig(t *testing.T) {
	t.Parallel()
	dir := writeAuditConfig(t)
	if err := os.WriteFile(filepath.Join(dir, "addresses.99999.json"), []byte(`{"Version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &auditDevice{processes: auditProcesses(), session: &auditSession{}}
	b := Bootstrap{Device: d, ConfigDir: dir}
	if _, _, _, err := b.Attach(context.Background()); err != nil {
		t.Fatalf("target config is valid but unrelated config rejected bootstrap: %v", err)
	}
}

func TestAuditBootstrapPropagatesCanceledEnumeration(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &cancelAuditDevice{}
	b := Bootstrap{Device: d, ConfigDir: t.TempDir()}
	if _, _, _, err := b.Attach(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Attach error=%v, want context.Canceled", err)
	}
}

type cancelAuditDevice struct{}

func (*cancelAuditDevice) Enumerate(ctx context.Context) ([]process.Process, error) {
	return nil, ctx.Err()
}

func (*cancelAuditDevice) Attach(uint32) (Session, error) {
	return nil, errors.New("unexpected attach")
}
