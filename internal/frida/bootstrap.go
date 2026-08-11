package frida

import (
	"context"
	"fmt"
	"github.com/Follen/miniapp-bridge/frida"
	"github.com/Follen/miniapp-bridge/internal/process"
	"github.com/Follen/miniapp-bridge/internal/version"
	"path/filepath"
)

type MetadataDevice interface {
	Device
	Enumerate(context.Context) ([]process.Process, error)
}

type Bootstrap struct {
	Device    MetadataDevice
	ConfigDir string
	Configs   map[int]version.AddressConfig
	Agent     func(version.AddressConfig) string
	OnMessage func(Message)
}

func (b Bootstrap) Attach(ctx context.Context) (Session, Script, process.Process, error) {
	if b.Device == nil {
		return nil, nil, process.Process{}, fmt.Errorf("[frida] device unavailable")
	}
	ps, err := b.Device.Enumerate(ctx)
	if err != nil {
		return nil, nil, process.Process{}, err
	}
	pid, err := process.SelectParent(ps, "WeChatAppEx.exe")
	if err != nil {
		return nil, nil, process.Process{}, fmt.Errorf("[frida] WeChatAppEx.exe process not found")
	}
	var target process.Process
	for _, p := range ps {
		if p.PID == pid {
			target = p
			break
		}
	}
	if target.Version == 0 {
		return nil, nil, target, fmt.Errorf("[frida] error in find wmpf version")
	}
	var cfg version.AddressConfig
	if b.ConfigDir != "" {
		cfg, err = version.LoadFile(filepath.Join(b.ConfigDir, fmt.Sprintf("addresses.%d.json", target.Version)))
	} else {
		cfg, err = version.Select(b.Configs, target.Version)
	}
	if err != nil {
		return nil, nil, target, fmt.Errorf("[frida] version config not found: %d", target.Version)
	}
	s, err := b.Device.Attach(pid)
	if err != nil {
		return nil, nil, target, err
	}
	source := frida.AgentSource
	if b.Agent != nil {
		source = b.Agent(cfg)
	}
	script, err := s.LoadScript(source)
	if err != nil {
		_ = s.Detach()
		return nil, nil, target, err
	}
	return s, script, target, nil
}
