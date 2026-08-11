package sdk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type nativeMetadataSession struct{ status NativeStatus }

func (s *nativeMetadataSession) Close(context.Context) error  { return nil }
func (s *nativeMetadataSession) NativeMetadata() NativeStatus { return s.status }

var _ NativeSession = (*nativeMetadataSession)(nil)
var _ NativeMetadata = (*nativeMetadataSession)(nil)

func TestInspectNativeRuntimeProvidesServiceMetadata(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	m := DefaultNativeManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	m.Size = int64(len(data))
	m.SHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))
	status, err := InspectNativeRuntime(exe, m)
	if err != nil {
		t.Fatal(err)
	}
	wantPath, _ := filepath.Abs(exe)
	if status.Attached || status.Version != NativeVersion || status.ABI != NativeABIVersion || status.Path != filepath.Clean(wantPath) {
		t.Fatalf("native status=%+v", status)
	}
	session := &nativeMetadataSession{status: status}
	got := session.NativeMetadata()
	got.Attached = true
	if got.Version != NativeVersion || got.ABI != NativeABIVersion || got.Path != status.Path || !got.Attached {
		t.Fatalf("service-fillable metadata=%+v", got)
	}
}

func TestCheckNativeRuntimeErrorMatrix(t *testing.T) {
	m := DefaultNativeManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	missing := filepath.Join(t.TempDir(), "missing.dll")
	assertNativeError(t, CheckNativeRuntime(missing, m), ErrNativeMissing, "stat")

	badVersion := m
	badVersion.NativeVersion = "wrong"
	assertNativeError(t, CheckNativeRuntime(missing, badVersion), ErrNativeManifest, "manifest native version")
	badPlatform := m
	badPlatform.OS = "other"
	assertNativeError(t, CheckNativeRuntime(missing, badPlatform), ErrNativeWrongArch, "manifest platform")

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	badHash := m
	badHash.Size = info.Size()
	badHash.SHA256 = strings.Repeat("0", 64)
	assertNativeError(t, CheckNativeRuntime(exe, badHash), ErrNativeHashMismatch, "hash")

	invalidPath := "bad\x00path"
	assertNativeError(t, CheckNativeRuntime(invalidPath, m), ErrNativeCache, "stat")
	if _, err := InspectNativeRuntime(missing, m); !errors.Is(err, ErrNativeMissing) {
		t.Fatalf("inspect missing err=%v", err)
	}
}

func TestInspectNativeRuntimeAbsolutePathFailure(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	m := DefaultNativeManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	m.Size = int64(len(data))
	m.SHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))

	original := nativeAbsolutePath
	nativeAbsolutePath = func(string) (string, error) { return "", errors.New("absolute path failed") }
	t.Cleanup(func() { nativeAbsolutePath = original })
	_, err = InspectNativeRuntime(exe, m)
	assertNativeError(t, err, ErrNativeCache, "absolute path")
}

func TestNativePublicErrorsAndStatusDoNotExposeHandles(t *testing.T) {
	for _, sentinel := range []error{ErrNativeMissing, ErrNativeHashMismatch, ErrNativeWrongArch, ErrNativeManifest, ErrNativeOffline, ErrNativeDownload, ErrNativeCache, ErrNativeArchive} {
		err := &NativeRuntimeError{Code: sentinel, Operation: "test", Err: sentinel}
		if !errors.Is(err, sentinel) {
			t.Fatalf("sentinel %v did not match", sentinel)
		}
		var typed *NativeRuntimeError
		if !errors.As(err, &typed) || typed.Operation != "test" {
			t.Fatalf("typed error=%#v", typed)
		}
	}
	typeOfStatus := reflect.TypeOf(NativeStatus{})
	for i := 0; i < typeOfStatus.NumField(); i++ {
		kind := typeOfStatus.Field(i).Type.Kind()
		if kind == reflect.Pointer || kind == reflect.UnsafePointer || kind == reflect.Uintptr {
			t.Fatalf("NativeStatus leaks handle-like field %s", typeOfStatus.Field(i).Name)
		}
	}
}

func assertNativeError(t *testing.T, err, sentinel error, operation string) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v, want %v", err, sentinel)
	}
	var typed *NativeRuntimeError
	if !errors.As(err, &typed) || typed.Operation != operation {
		t.Fatalf("typed error=%#v, operation=%q", typed, operation)
	}
}
