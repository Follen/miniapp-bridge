//go:build windows && frida

package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	agent "github.com/Follen/miniapp-bridge/frida"
	fridacore "github.com/Follen/miniapp-bridge/internal/frida"
	"github.com/Follen/miniapp-bridge/internal/process"
	"github.com/Follen/miniapp-bridge/internal/version"
)

var nativePathEnvironmentMu sync.Mutex

const (
	nativeFileFlagOpenReparsePoint = 0x00200000
	nativeFileFlagSequentialScan   = 0x08000000
	nativeFileNameNormalized       = 0x0
	nativeVolumeNameDOS            = 0x0
)

var (
	nativeKernel32                     = syscall.NewLazyDLL("kernel32.dll")
	nativeGetFinalPathNameByHandleW    = nativeKernel32.NewProc("GetFinalPathNameByHandleW")
	nativeGetLongPathNameW             = nativeKernel32.NewProc("GetLongPathNameW")
	nativeExpectedManifest             = DefaultNativeManifest
	nativeOpenTrustedRuntime           = openTrustedNativeRuntime
	nativeAbsPath                      = filepath.Abs
	nativeNewFile                      = os.NewFile
	nativeGetFileInformationByHandle   = syscall.GetFileInformationByHandle
	nativeGetFinalPathNameByHandleCall = func(handle syscall.Handle, buffer *uint16, size uint32, flags uint32) (uint32, error) {
		result, _, callErr := nativeGetFinalPathNameByHandleW.Call(
			uintptr(handle), uintptr(unsafe.Pointer(buffer)), uintptr(size), uintptr(flags),
		)
		if result == 0 {
			return 0, callErr
		}
		return uint32(result), nil
	}
	nativeGetLongPathNameCall = func(path, buffer *uint16, size uint32) (uint32, error) {
		result, _, callErr := nativeGetLongPathNameW.Call(
			uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(buffer)), uintptr(size),
		)
		if result == 0 {
			return 0, callErr
		}
		return uint32(result), nil
	}
)

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
	nativeBindTarget = func(ctx context.Context, target process.Process) (process.Process, error) {
		return process.BindTarget(ctx, target, "", "host", time.Now().UTC())
	}
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
	target    TargetStatus
	runtime   nativeRuntimeLease
}

type nativeRuntimeLease interface {
	Verify() error
	Close() error
}

type nativeRuntimeIdentity struct {
	volumeSerial uint32
	fileIndex    uint64
	size         uint64
}

type trustedNativeRuntime struct {
	path      string
	canonical string
	file      *os.File
	identity  nativeRuntimeIdentity
}

func defaultNativeStarter(path, addressConfigDir string) NativeStarter {
	return func(ctx context.Context, publish func(LogEvent)) (NativeSession, error) {
		resolved, err := resolveNativePath(path)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
		}
		expected := nativeExpectedManifest()
		// The sidecar is compatibility metadata, never a source of trust. The
		// executable's compiled manifest supplies the only accepted size/hash.
		_, err = loadNativeManifest(resolved)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
		}
		runtimeLease, err := nativeOpenTrustedRuntime(resolved, expected)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
		}
		leaseOwned := false
		defer func() {
			if !leaseOwned {
				_ = runtimeLease.Close()
			}
		}()
		if err := CheckNativeRuntime(resolved, expected); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
		}
		if err := runtimeLease.Verify(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
		}
		device, err := openNativeDevice(resolved)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, publicNativeLoadError(err))
		}
		if err := runtimeLease.Verify(); err != nil {
			_ = device.Close()
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
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
			BindTarget: nativeBindTarget,
		}).Attach(ctx)
		if err != nil {
			_ = device.Close()
			return nil, fmt.Errorf("%w: %w", ErrNativeUnavailable, err)
		}
		publish(LogEvent{Level: "info", Message: fmt.Sprintf("[frida] attached pid=%d version=%d path=%s", target.PID, target.Version, target.Path)})
		result := &platformNativeSession{
			device: device, session: session, script: script, configDir: addressConfigDir, configs: configs,
			metadata: NativeStatus{Attached: true, Version: NativeVersion, ABI: NativeABIVersion, Path: resolved},
			target:   TargetStatus{Attached: true, Target: Target{PID: target.PID, ParentPID: target.ParentPID, Name: target.Name, Path: target.Path, Version: target.Version}},
			runtime:  runtimeLease,
		}
		leaseOwned = true
		return result, nil
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
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "read manifest", Path: manifestPath, Err: err}
	}
	if len(data) > 1<<20 {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "read manifest", Path: manifestPath, Err: errors.New("manifest exceeds 1 MiB")}
	}
	manifest, err := decodeNativeManifestStrict(data)
	if err != nil {
		return NativeManifest{}, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "decode manifest", Path: manifestPath, Err: err}
	}
	expected := nativeExpectedManifest()
	if !nativeManifestsEqual(manifest, expected) {
		code := ErrNativeManifest
		if manifest.Size != expected.Size || manifest.SHA256 != expected.SHA256 {
			code = ErrNativeHashMismatch
		}
		return NativeManifest{}, &NativeRuntimeError{
			Code: code, Operation: "manifest trust root", Path: manifestPath,
			Expected: fmt.Sprintf("size=%d sha256=%s", expected.Size, expected.SHA256),
			Actual:   fmt.Sprintf("size=%d sha256=%s", manifest.Size, manifest.SHA256), Err: ErrNativeManifest,
		}
	}
	return manifest, nil
}

func decodeNativeManifestStrict(data []byte) (NativeManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return NativeManifest{}, fmt.Errorf("manifest object: %w", err)
	}
	allowed := map[string]struct{}{
		"schema": {}, "nativeVersion": {}, "fridaCoreVersion": {}, "zlibVersion": {}, "abiVersion": {},
		"os": {}, "arch": {}, "dll": {}, "size": {}, "sha256": {}, "requiredExports": {},
	}
	seen := make(map[string]struct{}, len(allowed))
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return NativeManifest{}, err
		}
		// Decoder.Token guarantees string keys while in object-key state. Keep
		// malformed defensive values on the ordinary unknown-field error path.
		key, _ := keyToken.(string)
		if _, ok := allowed[key]; !ok {
			return NativeManifest{}, fmt.Errorf("unknown manifest field %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return NativeManifest{}, fmt.Errorf("duplicate manifest field %q", key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return NativeManifest{}, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return NativeManifest{}, err
	}
	if len(seen) != len(allowed) {
		return NativeManifest{}, errors.New("manifest is missing required fields")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return NativeManifest{}, errors.New("manifest has trailing JSON value")
	}
	var raw struct {
		Schema           string   `json:"schema"`
		NativeVersion    string   `json:"nativeVersion"`
		FridaCoreVersion string   `json:"fridaCoreVersion"`
		ZlibVersion      string   `json:"zlibVersion"`
		ABIVersion       uint32   `json:"abiVersion"`
		OS               string   `json:"os"`
		Arch             string   `json:"arch"`
		DLL              string   `json:"dll"`
		Size             int64    `json:"size"`
		SHA256           string   `json:"sha256"`
		RequiredExports  []string `json:"requiredExports"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return NativeManifest{}, err
	}
	return NativeManifest{
		Schema: raw.Schema, NativeVersion: raw.NativeVersion, FridaCoreVersion: raw.FridaCoreVersion,
		ZlibVersion: raw.ZlibVersion, ABIVersion: raw.ABIVersion, OS: raw.OS, Arch: raw.Arch,
		DLL: raw.DLL, Size: raw.Size, SHA256: raw.SHA256, RequiredExports: raw.RequiredExports,
	}, nil
}

func nativeManifestsEqual(got, want NativeManifest) bool {
	if got.Schema != want.Schema || got.NativeVersion != want.NativeVersion || got.FridaCoreVersion != want.FridaCoreVersion ||
		got.ZlibVersion != want.ZlibVersion || got.ABIVersion != want.ABIVersion || got.OS != want.OS || got.Arch != want.Arch ||
		got.DLL != want.DLL || got.Size != want.Size || got.SHA256 != want.SHA256 || len(got.RequiredExports) != len(want.RequiredExports) {
		return false
	}
	for index := range want.RequiredExports {
		if got.RequiredExports[index] != want.RequiredExports[index] {
			return false
		}
	}
	return true
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

func openTrustedNativeRuntime(path string, expected NativeManifest) (nativeRuntimeLease, error) {
	absolute, err := nativeAbsPath(path)
	if err != nil {
		return nil, &NativeRuntimeError{Code: ErrNativeCache, Operation: "absolute runtime path", Path: path, Err: err}
	}
	absolute = filepath.Clean(absolute)
	if filepath.Base(absolute) != expected.DLL {
		return nil, &NativeRuntimeError{Code: ErrNativeManifest, Operation: "runtime filename", Path: absolute, Expected: expected.DLL, Actual: filepath.Base(absolute), Err: ErrNativeManifest}
	}
	wide, err := syscall.UTF16PtrFromString(absolute)
	if err != nil {
		return nil, &NativeRuntimeError{Code: ErrNativeCache, Operation: "runtime path", Path: absolute, Err: err}
	}
	handle, err := syscall.CreateFile(
		wide, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL|nativeFileFlagOpenReparsePoint|nativeFileFlagSequentialScan, 0,
	)
	if err != nil {
		code := ErrNativeCache
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) || errors.Is(err, syscall.ERROR_PATH_NOT_FOUND) {
			code = ErrNativeMissing
		}
		return nil, &NativeRuntimeError{Code: code, Operation: "open trusted runtime", Path: absolute, Err: err}
	}
	file := nativeNewFile(uintptr(handle), absolute)
	if file == nil {
		_ = syscall.CloseHandle(handle)
		return nil, &NativeRuntimeError{Code: ErrNativeCache, Operation: "own trusted runtime", Path: absolute, Err: errors.New("invalid file handle")}
	}
	runtime := &trustedNativeRuntime{path: absolute, file: file}
	if err := runtime.capture(expected); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	return runtime, nil
}

func (runtime *trustedNativeRuntime) capture(expected NativeManifest) error {
	info, err := nativeRuntimeHandleInfo(runtime.file)
	if err != nil {
		return err
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return &NativeRuntimeError{Code: ErrNativeCache, Operation: "runtime reparse point", Path: runtime.path, Err: errors.New("reparse points are not accepted")}
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return &NativeRuntimeError{Code: ErrNativeCache, Operation: "runtime file type", Path: runtime.path, Err: errors.New("runtime is not a regular file")}
	}
	if info.NumberOfLinks != 1 {
		return &NativeRuntimeError{Code: ErrNativeCache, Operation: "runtime hard links", Path: runtime.path, Expected: "1", Actual: fmt.Sprint(info.NumberOfLinks), Err: errors.New("runtime must have one file name")}
	}
	identity := nativeIdentityFromInfo(info)
	if identity.size != uint64(expected.Size) {
		return &NativeRuntimeError{Code: ErrNativeHashMismatch, Operation: "runtime handle size", Path: runtime.path, Expected: fmt.Sprint(expected.Size), Actual: fmt.Sprint(identity.size), Err: ErrNativeHashMismatch}
	}
	canonical, err := nativeFinalPath(runtime.file)
	if err != nil {
		return &NativeRuntimeError{Code: ErrNativeCache, Operation: "runtime final path", Path: runtime.path, Err: err}
	}
	if !nativePathsEqual(canonical, runtime.path) {
		return &NativeRuntimeError{Code: ErrNativeCache, Operation: "runtime path identity", Path: runtime.path, Expected: runtime.path, Actual: canonical, Err: errors.New("runtime path traverses an alias or reparse point")}
	}
	runtime.canonical = canonical
	runtime.identity = identity
	return nil
}

func (runtime *trustedNativeRuntime) Verify() error {
	if runtime == nil || runtime.file == nil {
		return &NativeRuntimeError{Code: ErrNativeCache, Operation: "verify trusted runtime", Err: errors.New("runtime lease is closed")}
	}
	info, err := nativeRuntimeHandleInfo(runtime.file)
	if err != nil {
		return err
	}
	if nativeIdentityFromInfo(info) != runtime.identity || info.NumberOfLinks != 1 || info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return &NativeRuntimeError{Code: ErrNativeCache, Operation: "runtime identity changed", Path: runtime.path, Err: errors.New("trusted runtime file identity changed")}
	}
	canonical, err := nativeFinalPath(runtime.file)
	if err != nil {
		return &NativeRuntimeError{Code: ErrNativeCache, Operation: "runtime final path", Path: runtime.path, Err: err}
	}
	if !nativePathsEqual(canonical, runtime.canonical) || !nativePathsEqual(canonical, runtime.path) {
		return &NativeRuntimeError{Code: ErrNativeCache, Operation: "runtime path identity changed", Path: runtime.path, Expected: runtime.canonical, Actual: canonical, Err: errors.New("trusted runtime path changed")}
	}
	return nil
}

func (runtime *trustedNativeRuntime) Close() error {
	if runtime == nil || runtime.file == nil {
		return nil
	}
	file := runtime.file
	runtime.file = nil
	return file.Close()
}

func nativeRuntimeHandleInfo(file *os.File) (syscall.ByHandleFileInformation, error) {
	var info syscall.ByHandleFileInformation
	if err := nativeGetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return info, &NativeRuntimeError{Code: ErrNativeCache, Operation: "runtime file identity", Path: file.Name(), Err: err}
	}
	return info, nil
}

func nativeIdentityFromInfo(info syscall.ByHandleFileInformation) nativeRuntimeIdentity {
	return nativeRuntimeIdentity{
		volumeSerial: info.VolumeSerialNumber,
		fileIndex:    uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
		size:         uint64(info.FileSizeHigh)<<32 | uint64(info.FileSizeLow),
	}
}

func nativeFinalPath(file *os.File) (string, error) {
	buffer := make([]uint16, 512)
	for {
		length, err := nativeGetFinalPathNameByHandleCall(
			syscall.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), nativeFileNameNormalized|nativeVolumeNameDOS,
		)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			return syscall.UTF16ToString(buffer[:length]), nil
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func nativePathsEqual(left, right string) bool {
	return strings.EqualFold(nativeNormalizeFinalPath(left), nativeNormalizeFinalPath(right))
}

func nativeNormalizeFinalPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\?\UNC\`) {
		path = `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	} else {
		path = strings.TrimPrefix(path, `\\?\`)
	}
	if wide, err := syscall.UTF16PtrFromString(path); err == nil {
		buffer := make([]uint16, 32768)
		if length, err := nativeGetLongPathNameCall(wide, &buffer[0], uint32(len(buffer))); err == nil && length > 0 && length < uint32(len(buffer)) {
			path = syscall.UTF16ToString(buffer[:length])
		}
	}
	return filepath.Clean(path)
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

// TargetMetadata exposes the attached process as values only. Service can
// consume this optional capability without receiving a Frida/native handle.
func (n *platformNativeSession) TargetMetadata() TargetStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.target
}

func (n *platformNativeSession) AttachTarget(ctx context.Context, target Target) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	resolved, err := n.resolveTarget(ctx, target)
	if err != nil {
		return err
	}
	config, err := n.addressConfig(resolved.Version)
	if err != nil {
		return fmt.Errorf("load target config: %w", err)
	}
	if err := n.detachLocked(); err != nil {
		return err
	}
	session, err := n.device.Attach(resolved.PID)
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
	n.target = TargetStatus{Attached: true, Target: resolved}
	return nil
}

func (n *platformNativeSession) resolveTarget(ctx context.Context, requested Target) (Target, error) {
	if requested.PID == 0 {
		return Target{}, errors.New("target PID is required")
	}
	processes, err := n.device.Enumerate(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("enumerate target: %w", err)
	}
	var discovered process.Process
	for _, candidate := range processes {
		if candidate.PID == requested.PID {
			discovered = candidate
			break
		}
	}
	if discovered.PID == 0 {
		return Target{}, fmt.Errorf("target PID %d was not found", requested.PID)
	}
	if requested.ParentPID != 0 && requested.ParentPID != discovered.ParentPID {
		return Target{}, fmt.Errorf("target parent PID mismatch: got %d want %d", discovered.ParentPID, requested.ParentPID)
	}
	if requested.Name != "" && !strings.EqualFold(requested.Name, discovered.Name) {
		return Target{}, fmt.Errorf("target name mismatch: got %q want %q", discovered.Name, requested.Name)
	}
	if requested.Path != "" && !strings.EqualFold(filepath.Clean(requested.Path), filepath.Clean(discovered.Path)) {
		return Target{}, fmt.Errorf("target path mismatch: got %q want %q", discovered.Path, requested.Path)
	}
	if requested.Version != 0 && requested.Version != discovered.Version {
		return Target{}, fmt.Errorf("target version mismatch: got %d want %d", discovered.Version, requested.Version)
	}
	if discovered.Version == 0 {
		return Target{}, errors.New("target version is required")
	}
	bound, err := nativeBindTarget(ctx, discovered)
	if err != nil {
		return Target{}, fmt.Errorf("target identity rejected: %w", err)
	}
	if bound.PID != discovered.PID {
		return Target{}, fmt.Errorf("target identity rejected: bound PID %d differs from requested %d", bound.PID, discovered.PID)
	}
	if bound.Version != 0 && bound.Version != discovered.Version {
		return Target{}, fmt.Errorf("target identity rejected: bound version %d differs from discovered %d", bound.Version, discovered.Version)
	}
	return Target{
		PID: bound.PID, ParentPID: bound.ParentPID, Name: bound.Name,
		Path: bound.Path, Version: bound.Version,
	}, nil
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
	n.target.Attached = false
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
		if n.runtime != nil {
			if err := n.runtime.Close(); n.closeErr == nil {
				n.closeErr = err
			}
			n.runtime = nil
		}
	})
	return n.closeErr
}
