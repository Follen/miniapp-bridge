//go:build windows && frida

package sdk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeManifestStrictParserRejectsUnknownDuplicateAndMissingFields(t *testing.T) {
	preserveNativeStarterHooks(t)
	dll := copyStarterExecutable(t)
	manifest := setStarterExpectedManifest(t, dll)
	valid, err := marshalStarterManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "unknown field", data: appendJSONField(valid, `"unexpected":true`)},
		{name: "duplicate field", data: appendJSONField(valid, `"schema":"`+manifest.Schema+`"`)},
		{name: "missing field", data: []byte(strings.Replace(string(valid), `,"arch":"amd64"`, "", 1))},
		{name: "uppercase field", data: []byte(strings.Replace(string(valid), `"schema"`, `"Schema"`, 1))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(filepath.Dir(dll), "manifest.json"), test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadNativeManifest(dll); !errors.Is(err, ErrNativeManifest) {
				t.Fatalf("loadNativeManifest() error = %v", err)
			}
		})
	}
}

func appendJSONField(object []byte, field string) []byte {
	trimmed := strings.TrimSpace(string(object))
	return []byte(strings.TrimSuffix(trimmed, "}") + "," + field + "}")
}

func TestTrustedNativeRuntimeRejectsHardlink(t *testing.T) {
	preserveNativeStarterHooks(t)
	dll := copyStarterExecutable(t)
	writeStarterManifest(t, dll, nil)
	link := filepath.Join(filepath.Dir(dll), "runtime-copy.dll")
	if err := os.Link(dll, link); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	defer os.Remove(link)
	called := false
	nativeNewDevice = func() (platformDevice, error) {
		called = true
		return nil, errors.New("loader should not be reached")
	}
	if _, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {}); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("hardlink error = %v", err)
	}
	if called {
		t.Fatal("native loader was called for a hardlinked runtime")
	}
}

func TestTrustedNativeRuntimeRejectsReparsePoint(t *testing.T) {
	preserveNativeStarterHooks(t)
	dir := t.TempDir()
	target := copyStarterExecutable(t)
	dll := filepath.Join(dir, NativeDLLFileName)
	if err := os.Symlink(target, dll); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	writeStarterManifest(t, dll, nil)
	called := false
	nativeNewDevice = func() (platformDevice, error) {
		called = true
		return nil, errors.New("loader should not be reached")
	}
	if _, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {}); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("reparse error = %v", err)
	}
	if called {
		t.Fatal("native loader was called for a reparse runtime")
	}
}

func TestTrustedNativeRuntimeBlocksReplacementDuringLoad(t *testing.T) {
	preserveNativeStarterHooks(t)
	dll := copyStarterExecutable(t)
	writeStarterManifest(t, dll, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	loadErr := errors.New("loader reached")
	nativeNewDevice = func() (platformDevice, error) {
		close(entered)
		<-release
		return nil, loadErr
	}
	result := make(chan error, 1)
	go func() {
		_, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {})
		result <- err
	}()
	<-entered
	probe, err := os.OpenFile(dll, os.O_WRONLY|os.O_TRUNC, 0)
	if err == nil {
		_ = probe.Close()
		t.Fatal("replacement write opened while trusted runtime was loading")
	}
	close(release)
	if err := <-result; !errors.Is(err, loadErr) {
		t.Fatalf("loader error = %v", err)
	}
	probe, err = os.OpenFile(dll, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("replacement write after lease release = %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
}

type securityRuntimeLease struct {
	verifyErr  error
	closeErr   error
	verifyCall int
	closeCall  int
}

func (lease *securityRuntimeLease) Verify() error {
	lease.verifyCall++
	return lease.verifyErr
}

func (lease *securityRuntimeLease) Close() error {
	lease.closeCall++
	return lease.closeErr
}

func TestPlatformNativeSessionClosesRuntimeLeaseOnce(t *testing.T) {
	lease := &securityRuntimeLease{}
	native := &platformNativeSession{device: &starterCoverageDevice{}, runtime: lease}
	if err := native.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := native.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lease.closeCall != 1 {
		t.Fatalf("runtime lease close calls = %d", lease.closeCall)
	}
}
