//go:build windows && frida

package frida

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeRuntimePathAndLoaderErrorBranches(t *testing.T) {
	originalPath, originalExecutable := nativeRuntimePath, nativeExecutable
	t.Cleanup(func() {
		nativeRuntimePath = originalPath
		nativeExecutable = originalExecutable
	})
	t.Setenv("MINIAPP_BRIDGE_NATIVE_PATH", "")
	nativeExecutable = func() (string, error) { return "", errors.New("executable unavailable") }
	if _, err := nativeRuntimePath(); err == nil {
		t.Fatal("executable error was ignored")
	}
	nativeExecutable = originalExecutable
	path, err := nativeRuntimePath()
	if err != nil || filepath.Base(path) != "miniapp-frida.dll" {
		t.Fatalf("runtime path=%q err=%v", path, err)
	}
	nativeRuntimePath = func() (string, error) { return "", errors.New("path unavailable") }
	if err := loadNativeRuntime(); err == nil || !strings.Contains(err.Error(), "native runtime path") {
		t.Fatalf("path loader err=%v", err)
	}
	nativeRuntimePath = func() (string, error) { return "bad\x00path", nil }
	if err := loadNativeRuntime(); err == nil || !strings.Contains(err.Error(), "native runtime path") {
		t.Fatalf("UTF16 loader err=%v", err)
	}
	nativeRuntimePath = func() (string, error) { return filepath.Join(t.TempDir(), "missing.dll"), nil }
	if err := loadNativeRuntime(); err == nil || !strings.Contains(err.Error(), "native runtime") {
		t.Fatalf("missing loader err=%v", err)
	}
	if _, err := NewNativeDevice(); err == nil || !strings.Contains(err.Error(), "native runtime") {
		t.Fatalf("device loader err=%v", err)
	}
}
