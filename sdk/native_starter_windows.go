//go:build windows && frida

package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	agent "github.com/Follen/miniapp-bridge/frida"
	fridacore "github.com/Follen/miniapp-bridge/internal/frida"
	"github.com/Follen/miniapp-bridge/internal/version"
)

var nativePathEnvironmentMu sync.Mutex

type platformDevice interface {
	fridacore.MetadataDevice
	SetMessageHandler(func(fridacore.Message))
	Close() error
}

var (
	nativeExecutable = os.Executable
	nativeLookupEnv  = os.LookupEnv
	nativeSetEnv     = os.Setenv
	nativeUnsetEnv   = os.Unsetenv
	nativeNewDevice  = func() (platformDevice, error) { return fridacore.NewNativeDevice() }
	nativeShutdown   = fridacore.ShutdownRuntime
)

type platformNativeSession struct {
	mu        sync.Mutex
	closeOnce sync.Once
	closeErr  error
	device    platformDevice
	session   fridacore.Session
	script    fridacore.Script
	configDir string
	configs   map[int]version.AddressConfig
	metadata  NativeStatus
}

func defaultNativeStarter(path, addressConfigDir string) NativeStarter {
	return func(ctx context.Context, publish func(LogEvent)) (NativeSession, error) {
		resolved, err := resolveNativePath(path)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
		}
		// Validate the on-disk runtime before any cgo/LoadLibraryExW call. This
		// keeps missing, malformed, wrong-arch, and hash-mismatched assets as
		// ordinary SDK errors instead of process-loader failures.
		manifest, err := loadNativeManifest(resolved)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
		}
		if err := CheckNativeRuntime(resolved, manifest); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
		}
		device, err := openNativeDevice(resolved)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, publicNativeLoadError(err))
		}
		device.SetMessageHandler(func(message fridacore.Message) {
			level := "debug"
			text := string(message.Payload)
			if message.Type == "error" {
				level = "error"
			}
			if text == "" {
				text = message.Type
			}
			publish(LogEvent{Level: level, Message: "[frida] " + text})
		})
		configs := version.EmbeddedConfigs()
		session, script, target, err := (fridacore.Bootstrap{
			Device: device, ConfigDir: addressConfigDir, Configs: configs, Agent: agent.SourceForConfig,
		}).Attach(ctx)
		if err != nil {
			_ = device.Close()
			nativeShutdown()
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
		}
		publish(LogEvent{Level: "info", Message: fmt.Sprintf("[frida] attached pid=%d version=%d path=%s", target.PID, target.Version, target.Path)})
		return &platformNativeSession{
			device: device, session: session, script: script, configDir: addressConfigDir, configs: configs,
			metadata: NativeStatus{Attached: true, Version: NativeVersion, ABI: NativeABIVersion, Path: resolved},
		}, nil
	}
}

func publicNativeLoadError(err error) error {
	public := ErrNativeLoad
	var loadErr *fridacore.NativeLoadError
	if errors.As(err, &loadErr) {
		switch loadErr.Code {
		case fridacore.NativeLoadExportMissing:
			public = ErrNativeExportMissing
		case fridacore.NativeLoadVersionMismatch:
			public = ErrNativeVersionMismatch
		case fridacore.NativeLoadABIMismatch:
			public = ErrNativeABIMismatch
		}
	}
	return errors.Join(public, err)
}

func loadNativeManifest(dllPath string) (NativeManifest, error) {
	manifestPath := filepath.Join(filepath.Dir(dllPath), "manifest.json")
	file, err := os.Open(manifestPath)
	if err != nil {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "open manifest", Path: manifestPath, Err: err}
	}
	defer file.Close()

	var manifest NativeManifest
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	if err := decoder.Decode(&manifest); err != nil {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "decode manifest", Path: manifestPath, Err: err}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "decode manifest", Path: manifestPath, Err: err}
	}

	expected := DefaultNativeManifest()
	if manifest.NativeVersion != expected.NativeVersion || manifest.FridaCoreVersion != expected.FridaCoreVersion || manifest.ABIVersion != expected.ABIVersion {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "manifest version", Path: manifestPath, Expected: expected.NativeVersion, Actual: manifest.NativeVersion}
	}
	if manifest.ZlibVersion != expected.ZlibVersion {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "manifest zlib version", Path: manifestPath, Expected: expected.ZlibVersion, Actual: manifest.ZlibVersion}
	}
	if manifest.DLL != NativeDLLFileName {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "manifest dll", Path: manifestPath, Expected: NativeDLLFileName, Actual: manifest.DLL}
	}
	if manifest.Size <= 0 {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "manifest size", Path: manifestPath, Expected: "positive size", Actual: fmt.Sprint(manifest.Size)}
	}
	if len(manifest.SHA256) != 64 || strings.IndexFunc(manifest.SHA256, func(r rune) bool {
		return !strings.ContainsRune("0123456789abcdefABCDEF", r)
	}) >= 0 {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "manifest sha256", Path: manifestPath, Expected: "64 hexadecimal characters", Actual: manifest.SHA256}
	}
	if !sameNativeExportSet(manifest.RequiredExports, expected.RequiredExports) {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "manifest exports", Path: manifestPath, Expected: strings.Join(expected.RequiredExports, ","), Actual: strings.Join(manifest.RequiredExports, ",")}
	}
	return manifest, nil
}

func sameNativeExportSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	set := make(map[string]struct{}, len(want))
	for _, value := range want {
		set[value] = struct{}{}
	}
	for _, value := range got {
		if _, exists := set[value]; !exists {
			return false
		}
		delete(set, value)
	}
	return len(set) == 0
}

func resolveNativePath(path string) (string, error) {
	if path != "" {
		if !filepath.IsAbs(path) {
			return "", errors.New("native path must be absolute")
		}
		return filepath.Clean(path), nil
	}
	executable, err := nativeExecutable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	return filepath.Join(filepath.Dir(executable), NativeDLLFileName), nil
}

func openNativeDevice(path string) (platformDevice, error) {
	nativePathEnvironmentMu.Lock()
	defer nativePathEnvironmentMu.Unlock()
	previous, existed := nativeLookupEnv("MINIAPP_BRIDGE_NATIVE_PATH")
	if err := nativeSetEnv("MINIAPP_BRIDGE_NATIVE_PATH", path); err != nil {
		return nil, err
	}
	defer func() {
		if existed {
			_ = nativeSetEnv("MINIAPP_BRIDGE_NATIVE_PATH", previous)
		} else {
			_ = nativeUnsetEnv("MINIAPP_BRIDGE_NATIVE_PATH")
		}
	}()
	return nativeNewDevice()
}

func (n *platformNativeSession) NativeMetadata() NativeStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.metadata
}

func (n *platformNativeSession) AttachTarget(ctx context.Context, target Target) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if target.Version == 0 {
		return errors.New("target version is required")
	}
	config, err := n.addressConfig(target.Version)
	if err != nil {
		return fmt.Errorf("load target config: %w", err)
	}
	if err := n.detachLocked(); err != nil {
		return err
	}
	session, err := n.device.Attach(target.PID)
	if err != nil {
		return err
	}
	script, err := session.LoadScript(agent.SourceForConfig(config))
	if err != nil {
		_ = session.Detach()
		return err
	}
	n.session, n.script = session, script
	n.metadata.Attached = true
	return nil
}

func (n *platformNativeSession) addressConfig(targetVersion int) (version.AddressConfig, error) {
	if n.configDir != "" {
		return version.LoadFile(filepath.Join(n.configDir, fmt.Sprintf("addresses.%d.json", targetVersion)))
	}
	return version.Select(n.configs, targetVersion)
}

func (n *platformNativeSession) DetachTarget(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.detachLocked()
}

func (n *platformNativeSession) detachLocked() error {
	var result error
	if n.script != nil {
		result = n.script.Unload()
		n.script = nil
	}
	if n.session != nil {
		if err := n.session.Detach(); result == nil {
			result = err
		}
		n.session = nil
	}
	n.metadata.Attached = false
	return result
}

func (n *platformNativeSession) Close(context.Context) error {
	n.closeOnce.Do(func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.closeErr = n.detachLocked()
		if err := n.device.Close(); n.closeErr == nil {
			n.closeErr = err
		}
		nativeShutdown()
	})
	return n.closeErr
}
