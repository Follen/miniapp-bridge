//go:build windows && frida

package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fridacore "github.com/Follen/miniapp-bridge/internal/frida"
	"github.com/Follen/miniapp-bridge/internal/process"
	"github.com/Follen/miniapp-bridge/internal/version"
)

type starterCoverageDevice struct {
	processes    []process.Process
	enumerateErr error
	attachErr    error
	session      fridacore.Session
	handler      func(fridacore.Message)
	closeErr     error
	attachPID    uint32
	closeCalls   int
}

func (d *starterCoverageDevice) Enumerate(context.Context) ([]process.Process, error) {
	return d.processes, d.enumerateErr
}

func (d *starterCoverageDevice) Attach(pid uint32) (fridacore.Session, error) {
	d.attachPID = pid
	return d.session, d.attachErr
}

func (d *starterCoverageDevice) SetMessageHandler(handler func(fridacore.Message)) {
	d.handler = handler
}

func (d *starterCoverageDevice) Close() error {
	d.closeCalls++
	return d.closeErr
}

type starterCoverageSession struct {
	script      fridacore.Script
	loadErr     error
	detachErr   error
	loadCalls   int
	detachCalls int
	source      string
}

func (s *starterCoverageSession) LoadScript(source string) (fridacore.Script, error) {
	s.loadCalls++
	s.source = source
	return s.script, s.loadErr
}

func (s *starterCoverageSession) Detach() error {
	s.detachCalls++
	return s.detachErr
}

type starterCoverageScript struct {
	unloadErr   error
	unloadCalls int
}

func (s *starterCoverageScript) Unload() error {
	s.unloadCalls++
	return s.unloadErr
}

func (*starterCoverageScript) Post([]byte) error { return nil }

func preserveNativeStarterHooks(t *testing.T) {
	t.Helper()
	executable, lookup, set, unset := nativeExecutable, nativeLookupEnv, nativeSetEnv, nativeUnsetEnv
	newDevice, shutdown := nativeNewDevice, nativeShutdown
	t.Cleanup(func() {
		nativeExecutable, nativeLookupEnv, nativeSetEnv, nativeUnsetEnv = executable, lookup, set, unset
		nativeNewDevice, nativeShutdown = newDevice, shutdown
	})
}

func TestNativeNewDeviceDefaultLoaderFailure(t *testing.T) {
	preserveNativeStarterHooks(t)
	t.Setenv("MINIAPP_BRIDGE_NATIVE_PATH", filepath.Join(t.TempDir(), NativeDLLFileName))
	if _, err := nativeNewDevice(); err == nil {
		t.Fatal("nativeNewDevice() succeeded with a missing runtime")
	}
}

func TestDefaultNativeStarterResolveAndLoaderErrors(t *testing.T) {
	preserveNativeStarterHooks(t)
	if _, err := defaultNativeStarter("relative.dll", "")(context.Background(), func(LogEvent) {}); !errors.Is(err, ErrNativeUnavailable) {
		t.Fatalf("relative path error = %v", err)
	}

	dll := copyStarterExecutable(t)
	writeStarterManifest(t, dll, nil)
	loadErr := &fridacore.NativeLoadError{Code: fridacore.NativeLoadExportMissing, Err: errors.New("missing export")}
	nativeNewDevice = func() (platformDevice, error) { return nil, loadErr }
	if _, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {}); !errors.Is(err, ErrNativeExportMissing) {
		t.Fatalf("loader error = %v", err)
	}
}

func TestDefaultNativeStarterBootstrapFailureAndSuccess(t *testing.T) {
	preserveNativeStarterHooks(t)
	dll := copyStarterExecutable(t)
	writeStarterManifest(t, dll, nil)
	shutdownCalls := 0
	nativeShutdown = func() { shutdownCalls++ }

	failing := &starterCoverageDevice{enumerateErr: errors.New("enumerate failed")}
	nativeNewDevice = func() (platformDevice, error) { return failing, nil }
	if _, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {}); !errors.Is(err, ErrNativeUnavailable) {
		t.Fatalf("bootstrap failure = %v", err)
	}
	if failing.closeCalls != 1 || shutdownCalls != 1 {
		t.Fatalf("failure cleanup close=%d shutdown=%d", failing.closeCalls, shutdownCalls)
	}

	script := &starterCoverageScript{}
	session := &starterCoverageSession{script: script}
	device := &starterCoverageDevice{
		processes: []process.Process{
			{PID: 10, ParentPID: 99, Name: "WeChatAppEx.exe", Version: 25297},
			{PID: 11, ParentPID: 99, Name: "WeChatAppEx.exe", Version: 25297},
			{PID: 99, Name: "host", Version: 25297},
		},
		session: session,
	}
	nativeNewDevice = func() (platformDevice, error) { return device, nil }
	var logs []LogEvent
	native, err := defaultNativeStarter(dll, "")(context.Background(), func(event LogEvent) { logs = append(logs, event) })
	if err != nil {
		t.Fatal(err)
	}
	if device.handler == nil || device.attachPID != 99 || session.source == "" {
		t.Fatalf("bootstrap handler=%v pid=%d source=%d", device.handler != nil, device.attachPID, len(session.source))
	}
	device.handler(fridacore.Message{Type: "send", Payload: []byte("payload")})
	device.handler(fridacore.Message{Type: "error"})
	if len(logs) != 3 ||
		logs[0].Level != "info" || logs[0].Message != "[frida] attached pid=99 version=25297 path=" ||
		logs[1].Level != "debug" || logs[1].Message != "[frida] payload" ||
		logs[2].Level != "error" || logs[2].Message != "[frida] error" {
		t.Fatalf("logs = %+v", logs)
	}
	metadata := native.(NativeMetadata).NativeMetadata()
	if !metadata.Attached || metadata.Path != dll {
		t.Fatalf("metadata = %+v", metadata)
	}
	if err := native.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLoadNativeManifestValidationBranches(t *testing.T) {
	dir := t.TempDir()
	dll := filepath.Join(dir, NativeDLLFileName)
	base := DefaultNativeManifest()
	base.Size = 1
	base.SHA256 = strings.Repeat("0", 64)

	tests := []struct {
		name   string
		mutate func(*NativeManifest)
		tail   string
	}{
		{name: "extra value", tail: ` {}`},
		{name: "invalid tail", tail: ` {`},
		{name: "native version", mutate: func(m *NativeManifest) { m.NativeVersion = "wrong" }},
		{name: "zlib version", mutate: func(m *NativeManifest) { m.ZlibVersion = "wrong" }},
		{name: "dll", mutate: func(m *NativeManifest) { m.DLL = "wrong.dll" }},
		{name: "size", mutate: func(m *NativeManifest) { m.Size = 0 }},
		{name: "short sha", mutate: func(m *NativeManifest) { m.SHA256 = "0" }},
		{name: "nonhex sha", mutate: func(m *NativeManifest) { m.SHA256 = strings.Repeat("z", 64) }},
		{name: "export length", mutate: func(m *NativeManifest) { m.RequiredExports = nil }},
		{name: "export member", mutate: func(m *NativeManifest) { m.RequiredExports[0] = "mb_unknown" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := base
			manifest.RequiredExports = append([]string(nil), base.RequiredExports...)
			if test.mutate != nil {
				test.mutate(&manifest)
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(data, test.tail...), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadNativeManifest(dll); !errors.Is(err, ErrNativeManifest) {
				t.Fatalf("loadNativeManifest() error = %v", err)
			}
		})
	}
}

func TestSameNativeExportSetMismatchBranches(t *testing.T) {
	if sameNativeExportSet([]string{"one"}, []string{"one", "two"}) {
		t.Fatal("different lengths matched")
	}
	if sameNativeExportSet([]string{"one", "one"}, []string{"one", "two"}) {
		t.Fatal("different members matched")
	}
}

func TestResolveNativePathDefaultsAndErrors(t *testing.T) {
	preserveNativeStarterHooks(t)
	wantErr := errors.New("executable unavailable")
	nativeExecutable = func() (string, error) { return "", wantErr }
	if _, err := resolveNativePath(""); !errors.Is(err, wantErr) {
		t.Fatalf("resolve error = %v", err)
	}
	nativeExecutable = func() (string, error) { return filepath.Join(`C:\fixture`, "bridge.exe"), nil }
	path, err := resolveNativePath("")
	if err != nil || path != filepath.Join(`C:\fixture`, NativeDLLFileName) {
		t.Fatalf("resolve path = %q, %v", path, err)
	}
}

func TestOpenNativeDeviceEnvironmentBranches(t *testing.T) {
	preserveNativeStarterHooks(t)
	wantSetErr := errors.New("set failed")
	nativeLookupEnv = func(string) (string, bool) { return "", false }
	nativeSetEnv = func(string, string) error { return wantSetErr }
	if _, err := openNativeDevice(`C:\fixture\native.dll`); !errors.Is(err, wantSetErr) {
		t.Fatalf("set error = %v", err)
	}

	device := &starterCoverageDevice{}
	var values []string
	nativeLookupEnv = func(string) (string, bool) { return "previous", true }
	nativeSetEnv = func(_, value string) error { values = append(values, value); return nil }
	nativeNewDevice = func() (platformDevice, error) { return device, nil }
	if got, err := openNativeDevice("next"); err != nil || got != device {
		t.Fatalf("existing environment device=%v error=%v", got, err)
	}
	if strings.Join(values, ",") != "next,previous" {
		t.Fatalf("environment values = %v", values)
	}

	unsetCalls := 0
	nativeLookupEnv = func(string) (string, bool) { return "", false }
	nativeSetEnv = func(string, string) error { return nil }
	nativeUnsetEnv = func(string) error { unsetCalls++; return nil }
	if _, err := openNativeDevice("next"); err != nil || unsetCalls != 1 {
		t.Fatalf("unset calls=%d error=%v", unsetCalls, err)
	}
}

func TestPlatformNativeSessionAttachDetachBranches(t *testing.T) {
	config := version.EmbeddedConfigs()
	versionID := 25297
	if _, ok := config[versionID]; !ok {
		t.Fatalf("embedded config %d missing", versionID)
	}

	t.Run("input and config errors", func(t *testing.T) {
		native := &platformNativeSession{device: &starterCoverageDevice{}, configs: config}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := native.AttachTarget(canceled, Target{Version: versionID}); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled attach = %v", err)
		}
		if err := native.AttachTarget(context.Background(), Target{}); err == nil {
			t.Fatal("zero-version target accepted")
		}
		if err := native.AttachTarget(context.Background(), Target{Version: -1}); err == nil {
			t.Fatal("unsupported target accepted")
		}
	})

	t.Run("previous detach error", func(t *testing.T) {
		wantErr := errors.New("unload failed")
		native := &platformNativeSession{device: &starterCoverageDevice{}, configs: config, script: &starterCoverageScript{unloadErr: wantErr}}
		if err := native.AttachTarget(context.Background(), Target{Version: versionID}); !errors.Is(err, wantErr) {
			t.Fatalf("detach error = %v", err)
		}
	})

	t.Run("device attach error", func(t *testing.T) {
		wantErr := errors.New("attach failed")
		native := &platformNativeSession{device: &starterCoverageDevice{attachErr: wantErr}, configs: config}
		if err := native.AttachTarget(context.Background(), Target{PID: 7, Version: versionID}); !errors.Is(err, wantErr) {
			t.Fatalf("attach error = %v", err)
		}
	})

	t.Run("script load error detaches", func(t *testing.T) {
		wantErr := errors.New("load failed")
		session := &starterCoverageSession{loadErr: wantErr}
		native := &platformNativeSession{device: &starterCoverageDevice{session: session}, configs: config}
		if err := native.AttachTarget(context.Background(), Target{PID: 7, Version: versionID}); !errors.Is(err, wantErr) {
			t.Fatalf("load error = %v", err)
		}
		if session.detachCalls != 1 {
			t.Fatalf("detach calls = %d", session.detachCalls)
		}
	})

	t.Run("success and detach", func(t *testing.T) {
		script := &starterCoverageScript{}
		session := &starterCoverageSession{script: script}
		native := &platformNativeSession{device: &starterCoverageDevice{session: session}, configs: config}
		if err := native.AttachTarget(context.Background(), Target{PID: 7, Version: versionID}); err != nil {
			t.Fatal(err)
		}
		if !native.NativeMetadata().Attached {
			t.Fatal("metadata not attached")
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := native.DetachTarget(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled detach = %v", err)
		}
		if err := native.DetachTarget(context.Background()); err != nil {
			t.Fatal(err)
		}
		if script.unloadCalls != 1 || session.detachCalls != 1 || native.NativeMetadata().Attached {
			t.Fatalf("unload=%d detach=%d metadata=%+v", script.unloadCalls, session.detachCalls, native.NativeMetadata())
		}
	})
}

func TestPlatformNativeSessionAddressConfigFile(t *testing.T) {
	config := version.EmbeddedConfigs()[25297]
	dir := t.TempDir()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "addresses.25297.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	native := &platformNativeSession{configDir: dir}
	if got, err := native.addressConfig(25297); err != nil || got.Version != 25297 {
		t.Fatalf("file config = %+v, %v", got, err)
	}
	if _, err := native.addressConfig(1); err == nil {
		t.Fatal("missing file config accepted")
	}
}

func TestPlatformNativeSessionDetachAndCloseErrorPriority(t *testing.T) {
	preserveNativeStarterHooks(t)
	shutdownCalls := 0
	nativeShutdown = func() { shutdownCalls++ }
	unloadErr, detachErr, closeErr := errors.New("unload"), errors.New("detach"), errors.New("close")
	script := &starterCoverageScript{unloadErr: unloadErr}
	session := &starterCoverageSession{detachErr: detachErr}
	device := &starterCoverageDevice{closeErr: closeErr}
	native := &platformNativeSession{device: device, session: session, script: script, metadata: NativeStatus{Attached: true}}
	if err := native.Close(context.Background()); !errors.Is(err, unloadErr) {
		t.Fatalf("close error = %v", err)
	}
	if err := native.Close(context.Background()); !errors.Is(err, unloadErr) {
		t.Fatalf("second close error = %v", err)
	}
	if script.unloadCalls != 1 || session.detachCalls != 1 || device.closeCalls != 1 || shutdownCalls != 1 {
		t.Fatalf("unload=%d detach=%d close=%d shutdown=%d", script.unloadCalls, session.detachCalls, device.closeCalls, shutdownCalls)
	}

	native = &platformNativeSession{device: &starterCoverageDevice{}, session: &starterCoverageSession{detachErr: detachErr}}
	if err := native.detachLocked(); !errors.Is(err, detachErr) {
		t.Fatalf("detach-only error = %v", err)
	}
	native = &platformNativeSession{device: &starterCoverageDevice{closeErr: closeErr}}
	if err := native.Close(context.Background()); !errors.Is(err, closeErr) {
		t.Fatalf("device close error = %v", err)
	}
}

func TestTaggedServiceNativePathWithoutStarterAndRecordingClose(t *testing.T) {
	s := newSDK(t, Options{NativePath: `C:\fixture\miniapp-frida.dll`, Native: disabledNativeStarter})
	s.nativeStarter = nil
	if err := s.Start(context.Background()); !errors.Is(err, ErrNativeUnavailable) {
		t.Fatalf("path without starter = %v", err)
	}
	_ = s.Close(context.Background())

	s = newSDK(t, Options{Native: disabledNativeStarter})
	s.mu.Lock()
	s.status.Recording.Active = true
	s.mu.Unlock()
	if err := s.closeApp(); err != nil {
		t.Fatal(err)
	}
	if s.Status().Recording.Active {
		t.Fatal("recording remained active")
	}
}
