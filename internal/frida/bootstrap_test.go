package frida

import (
	"context"
	"github.com/Follen/miniapp-bridge/internal/process"
	"github.com/Follen/miniapp-bridge/internal/version"
	"testing"
)

type fakeDevice struct {
	processes []process.Process
	attached  uint32
	source    string
}

func (d *fakeDevice) Enumerate(context.Context) ([]process.Process, error) { return d.processes, nil }
func (d *fakeDevice) Attach(pid uint32) (Session, error) {
	d.attached = pid
	return &fakeSession{owner: d}, nil
}

type fakeSession struct{ owner *fakeDevice }

func (s *fakeSession) LoadScript(src string) (Script, error) {
	s.owner.source = src
	return &fakeScript{}, nil
}
func (s *fakeSession) Detach() error { return nil }

type fakeScript struct{}

func (*fakeScript) Unload() error     { return nil }
func (*fakeScript) Post([]byte) error { return nil }
func TestBootstrapRequiresExactConfig(t *testing.T) {
	d := &fakeDevice{processes: []process.Process{{PID: 10, ParentPID: 99, Name: "WeChatAppEx.exe", Version: 25297}, {PID: 11, ParentPID: 99, Name: "WeChatAppEx.exe", Version: 25297}, {PID: 99, Name: "host", Version: 25297}}}
	b := Bootstrap{Device: d, ConfigDir: "../../configs/addresses"}
	s, script, target, e := b.Attach(context.Background())
	if e != nil || s == nil || script == nil || target.PID != 99 {
		t.Fatalf("attach target=%+v err=%v", target, e)
	}
	if d.attached != 99 || d.source == "" {
		t.Fatalf("attach pid=%d source=%d", d.attached, len(d.source))
	}
}

func TestBootstrapUsesEmbeddedConfigWithoutDirectory(t *testing.T) {
	d := &fakeDevice{processes: []process.Process{{PID: 10, ParentPID: 99, Name: "WeChatAppEx.exe", Version: 25297}, {PID: 11, ParentPID: 99, Name: "WeChatAppEx.exe", Version: 25297}, {PID: 99, Name: "host", Version: 25297}}}
	b := Bootstrap{Device: d, Configs: version.EmbeddedConfigs()}
	_, script, target, err := b.Attach(context.Background())
	if err != nil || script == nil || target.Version != 25297 {
		t.Fatalf("embedded attach target=%+v err=%v", target, err)
	}
	if d.source == "" {
		t.Fatal("embedded config did not produce Agent source")
	}
}
