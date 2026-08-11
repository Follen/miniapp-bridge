//go:build windows && frida

package sdk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	fridacore "github.com/Follen/miniapp-bridge/internal/frida"
)

func TestNativeLoadErrorsMapToPublicSentinels(t *testing.T) {
	detail := errors.New("native loader detail")
	tests := []struct {
		code fridacore.NativeLoadCode
		want error
	}{
		{fridacore.NativeLoadExportMissing, ErrNativeExportMissing},
		{fridacore.NativeLoadVersionMismatch, ErrNativeVersionMismatch},
		{fridacore.NativeLoadABIMismatch, ErrNativeABIMismatch},
		{fridacore.NativeLoadFailure, ErrNativeLoad},
		{fridacore.NativeLoadConflict, ErrNativeLoad},
	}
	for _, test := range tests {
		err := publicNativeLoadError(&fridacore.NativeLoadError{Code: test.code, Err: detail})
		if !errors.Is(err, test.want) || !errors.Is(err, detail) {
			t.Fatalf("code=%d error=%v want=%v", test.code, err, test.want)
		}
	}
	if err := publicNativeLoadError(detail); !errors.Is(err, ErrNativeLoad) || !errors.Is(err, detail) {
		t.Fatalf("untyped load error=%v", err)
	}
}

func TestNativeStarterRejectsMalformedPEBeforeLoader(t *testing.T) {
	oldNewDevice := nativeNewDevice
	defer func() { nativeNewDevice = oldNewDevice }()
	called := false
	nativeNewDevice = func() (platformDevice, error) {
		called = true
		return nil, errors.New("loader should not be reached")
	}
	bad := filepath.Join(t.TempDir(), NativeDLLFileName)
	if err := os.WriteFile(bad, []byte("not a PE"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeStarterManifest(t, bad, nil)
	starter := defaultNativeStarter(bad, "")
	_, err := starter(context.Background(), func(LogEvent) {})
	if !errors.Is(err, ErrNativeUnavailable) || !errors.Is(err, ErrNativeWrongArch) {
		t.Fatalf("starter error=%v", err)
	}
	if called {
		t.Fatal("native loader was called for invalid DLL")
	}
}

func TestNativeStarterRejectsMissingManifestBeforeLoader(t *testing.T) {
	oldNewDevice := nativeNewDevice
	defer func() { nativeNewDevice = oldNewDevice }()
	called := false
	nativeNewDevice = func() (platformDevice, error) { called = true; return nil, errors.New("unexpected loader") }
	dll := copyStarterExecutable(t)
	_, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {})
	if !errors.Is(err, ErrNativeUnavailable) || !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("starter error=%v", err)
	}
	if called {
		t.Fatal("native loader was called without a manifest")
	}
}

func TestNativeStarterRejectsMissingDLLBeforeLoader(t *testing.T) {
	oldNewDevice := nativeNewDevice
	defer func() { nativeNewDevice = oldNewDevice }()
	called := false
	nativeNewDevice = func() (platformDevice, error) { called = true; return nil, errors.New("unexpected loader") }
	dir := t.TempDir()
	dll := filepath.Join(dir, NativeDLLFileName)
	manifest := DefaultNativeManifest()
	manifest.Size = 1
	manifest.SHA256 = fmt.Sprintf("%064x", 1)
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {})
	if !errors.Is(err, ErrNativeUnavailable) || !errors.Is(err, ErrNativeMissing) {
		t.Fatalf("starter error=%v", err)
	}
	if called {
		t.Fatal("native loader was called without a DLL")
	}
}

func TestNativeStarterRejectsMalformedManifestBeforeLoader(t *testing.T) {
	oldNewDevice := nativeNewDevice
	defer func() { nativeNewDevice = oldNewDevice }()
	called := false
	nativeNewDevice = func() (platformDevice, error) { called = true; return nil, errors.New("unexpected loader") }
	dll := copyStarterExecutable(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(dll), "manifest.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {})
	if !errors.Is(err, ErrNativeUnavailable) || !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("starter error=%v", err)
	}
	if called {
		t.Fatal("native loader was called with a malformed manifest")
	}
}

func TestNativeStarterRejectsHashMismatchBeforeLoader(t *testing.T) {
	oldNewDevice := nativeNewDevice
	defer func() { nativeNewDevice = oldNewDevice }()
	called := false
	nativeNewDevice = func() (platformDevice, error) { called = true; return nil, errors.New("unexpected loader") }
	dll := copyStarterExecutable(t)
	writeStarterManifest(t, dll, func(manifest *NativeManifest) { manifest.SHA256 = fmt.Sprintf("%064x", 1) })
	_, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {})
	if !errors.Is(err, ErrNativeUnavailable) || !errors.Is(err, ErrNativeHashMismatch) {
		t.Fatalf("starter error=%v", err)
	}
	if called {
		t.Fatal("native loader was called after a hash mismatch")
	}
}

func TestNativeStarterValidManifestReachesLoader(t *testing.T) {
	oldNewDevice := nativeNewDevice
	defer func() { nativeNewDevice = oldNewDevice }()
	called := false
	wantErr := errors.New("loader reached")
	nativeNewDevice = func() (platformDevice, error) { called = true; return nil, wantErr }
	dll := copyStarterExecutable(t)
	writeStarterManifest(t, dll, nil)
	_, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {})
	if !errors.Is(err, ErrNativeUnavailable) || !errors.Is(err, wantErr) {
		t.Fatalf("starter error=%v", err)
	}
	if !called {
		t.Fatal("native loader was not called after validation succeeded")
	}
}

func copyStarterExecutable(t *testing.T) string {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	dll := filepath.Join(t.TempDir(), NativeDLLFileName)
	out, err := os.OpenFile(dll, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dll
}

func writeStarterManifest(t *testing.T, dll string, mutate func(*NativeManifest)) {
	t.Helper()
	data, err := os.ReadFile(dll)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	manifest := DefaultNativeManifest()
	manifest.Size = int64(len(data))
	manifest.SHA256 = hex.EncodeToString(sum[:])
	if mutate != nil {
		mutate(&manifest)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dll), "manifest.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}
