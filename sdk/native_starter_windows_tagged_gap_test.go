//go:build windows && frida

package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

// preserveNativeRuntimeHooks keeps the Windows-only seams isolated between
// tests. The defaults are the production implementations.
func preserveNativeRuntimeHooks(t *testing.T) {
	t.Helper()
	absPath := nativeAbsPath
	newFile := nativeNewFile
	fileInfo := nativeGetFileInformationByHandle
	finalPath := nativeGetFinalPathNameByHandleCall
	openRuntime := nativeOpenTrustedRuntime
	t.Cleanup(func() {
		nativeAbsPath = absPath
		nativeNewFile = newFile
		nativeGetFileInformationByHandle = fileInfo
		nativeGetFinalPathNameByHandleCall = finalPath
		nativeOpenTrustedRuntime = openRuntime
	})
}

func TestNativeFinalPathDefaultCallHandlesInvalidHandle(t *testing.T) {
	preserveNativeRuntimeHooks(t)
	buffer := make([]uint16, 8)
	length, _ := nativeGetFinalPathNameByHandleCall(
		syscall.InvalidHandle, &buffer[0], uint32(len(buffer)), nativeFileNameNormalized|nativeVolumeNameDOS,
	)
	if length != 0 {
		t.Fatalf("invalid handle returned path length %d", length)
	}
}

func TestDefaultNativeStarterLeaseVerificationFailures(t *testing.T) {
	preserveNativeStarterHooks(t)
	preserveNativeRuntimeHooks(t)
	dll := copyStarterExecutable(t)
	writeStarterManifest(t, dll, nil)

	firstErr := errors.New("first verify failed")
	firstLease := &taggedGapRuntimeLease{verifyErrs: []error{firstErr}}
	nativeOpenTrustedRuntime = func(string, NativeManifest) (nativeRuntimeLease, error) {
		return firstLease, nil
	}
	if _, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {}); !errors.Is(err, firstErr) {
		t.Fatalf("first verify error = %v", err)
	}
	if firstLease.closeCalls != 1 {
		t.Fatalf("first lease close calls = %d", firstLease.closeCalls)
	}

	secondErr := errors.New("second verify failed")
	secondLease := &taggedGapRuntimeLease{verifyErrs: []error{nil, secondErr}}
	device := &starterCoverageDevice{}
	nativeOpenTrustedRuntime = func(string, NativeManifest) (nativeRuntimeLease, error) {
		return secondLease, nil
	}
	nativeNewDevice = func() (platformDevice, error) { return device, nil }
	if _, err := defaultNativeStarter(dll, "")(context.Background(), func(LogEvent) {}); !errors.Is(err, secondErr) {
		t.Fatalf("second verify error = %v", err)
	}
	if secondLease.closeCalls != 1 || device.closeCalls != 1 {
		t.Fatalf("second cleanup lease=%d device=%d", secondLease.closeCalls, device.closeCalls)
	}
}

type taggedGapRuntimeLease struct {
	verifyErrs []error
	verifies   int
	closeCalls int
}

func (lease *taggedGapRuntimeLease) Verify() error {
	index := lease.verifies
	lease.verifies++
	if index >= len(lease.verifyErrs) {
		return nil
	}
	return lease.verifyErrs[index]
}

func (lease *taggedGapRuntimeLease) Close() error {
	lease.closeCalls++
	return nil
}

func TestLoadNativeManifestReadAndSizeLimits(t *testing.T) {
	preserveNativeRuntimeHooks(t)
	dir := t.TempDir()
	dll := filepath.Join(dir, NativeDLLFileName)
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.Mkdir(manifestPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := loadNativeManifest(dll); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("directory manifest error = %v", err)
	}
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, bytes.Repeat([]byte{'x'}, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadNativeManifest(dll); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("oversized manifest error = %v", err)
	}
}

func TestDecodeNativeManifestStrictErrorShapes(t *testing.T) {
	base := DefaultNativeManifest()
	valid, err := marshalStarterManifest(base)
	if err != nil {
		t.Fatal(err)
	}
	valid = bytes.TrimSpace(valid)
	largeABI := bytes.Replace(valid, []byte(fmt.Sprintf(`"abiVersion":%d`, base.ABIVersion)), []byte(`"abiVersion":999999999999999999999999999999`), 1)
	tests := []struct {
		name string
		data []byte
	}{
		{name: "initial token error", data: []byte("{")},
		{name: "non object", data: []byte("[]")},
		{name: "key token error", data: []byte(`{"`)},
		{name: "non string key", data: []byte(`{{}}`)},
		{name: "value decode error", data: []byte(`{"schema":`)},
		{name: "unmarshal type error", data: largeABI},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeNativeManifestStrict(test.data); err == nil {
				t.Fatal("malformed manifest accepted")
			}
		})
	}
}

func TestNativeManifestAndExportComparisonsComplete(t *testing.T) {
	want := DefaultNativeManifest()
	got := want
	got.RequiredExports = append([]string(nil), want.RequiredExports...)
	if len(got.RequiredExports) == 0 {
		t.Fatal("default export set unexpectedly empty")
	}
	got.RequiredExports[0] = got.RequiredExports[0] + "_changed"
	if nativeManifestsEqual(got, want) {
		t.Fatal("different export member matched")
	}
	if !sameNativeExportSet([]string{"one", "two"}, []string{"two", "one"}) {
		t.Fatal("equivalent export sets did not match")
	}
}

func TestOpenTrustedNativeRuntimeValidationHooks(t *testing.T) {
	preserveNativeRuntimeHooks(t)
	expected := DefaultNativeManifest()

	absErr := errors.New("absolute path failed")
	nativeAbsPath = func(string) (string, error) { return "", absErr }
	if _, err := openTrustedNativeRuntime("ignored", expected); !errors.Is(err, absErr) {
		t.Fatalf("absolute path error = %v", err)
	}

	nativeAbsPath = filepath.Abs
	wrongName := filepath.Join(t.TempDir(), "other.dll")
	if _, err := openTrustedNativeRuntime(wrongName, expected); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("filename error = %v", err)
	}

	nulPath := filepath.Join(t.TempDir(), NativeDLLFileName) + "\x00"
	nativeAbsPath = func(string) (string, error) { return nulPath, nil }
	nulExpected := expected
	nulExpected.DLL = filepath.Base(nulPath)
	if _, err := openTrustedNativeRuntime("ignored", nulExpected); err == nil {
		t.Fatal("NUL runtime path accepted")
	}

	dll := copyStarterExecutable(t)
	nativeAbsPath = filepath.Abs
	nativeNewFile = func(uintptr, string) *os.File { return nil }
	if _, err := openTrustedNativeRuntime(dll, expected); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("nil os.File error = %v", err)
	}
}

func taggedGapInfo(size uint64) syscall.ByHandleFileInformation {
	return syscall.ByHandleFileInformation{
		VolumeSerialNumber: 7,
		FileIndexHigh:      8,
		FileIndexLow:       9,
		FileSizeHigh:       uint32(size >> 32),
		FileSizeLow:        uint32(size),
		NumberOfLinks:      1,
	}
}

func taggedGapFile(t *testing.T) (*os.File, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.dll")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("runtime")); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file, path
}

func taggedGapWriteUTF16(buffer *uint16, size uint32, value string) uint32 {
	encoded, err := syscall.UTF16FromString(value)
	if err != nil {
		return 0
	}
	copy((*[1 << 20]uint16)(unsafe.Pointer(buffer))[:size:size], encoded)
	return uint32(len(encoded) - 1)
}

func TestTrustedNativeRuntimeCaptureBranches(t *testing.T) {
	preserveNativeRuntimeHooks(t)
	file, path := taggedGapFile(t)
	expected := DefaultNativeManifest()
	expected.Size = int64(7)

	infoErr := errors.New("file info failed")
	nativeGetFileInformationByHandle = func(syscall.Handle, *syscall.ByHandleFileInformation) error { return infoErr }
	runtime := &trustedNativeRuntime{path: path, file: file}
	if err := runtime.capture(expected); !errors.Is(err, infoErr) {
		t.Fatalf("file info error = %v", err)
	}

	nativeGetFileInformationByHandle = func(_ syscall.Handle, info *syscall.ByHandleFileInformation) error {
		*info = taggedGapInfo(uint64(expected.Size))
		info.FileAttributes = syscall.FILE_ATTRIBUTE_REPARSE_POINT
		return nil
	}
	if err := runtime.capture(expected); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("reparse error = %v", err)
	}

	nativeGetFileInformationByHandle = func(_ syscall.Handle, info *syscall.ByHandleFileInformation) error {
		*info = taggedGapInfo(uint64(expected.Size))
		info.FileAttributes = syscall.FILE_ATTRIBUTE_DIRECTORY
		return nil
	}
	if err := runtime.capture(expected); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("directory error = %v", err)
	}

	nativeGetFileInformationByHandle = func(_ syscall.Handle, info *syscall.ByHandleFileInformation) error {
		*info = taggedGapInfo(uint64(expected.Size))
		info.NumberOfLinks = 2
		return nil
	}
	if err := runtime.capture(expected); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("hardlink error = %v", err)
	}

	nativeGetFileInformationByHandle = func(_ syscall.Handle, info *syscall.ByHandleFileInformation) error {
		*info = taggedGapInfo(uint64(expected.Size + 1))
		return nil
	}
	if err := runtime.capture(expected); !errors.Is(err, ErrNativeHashMismatch) {
		t.Fatalf("size error = %v", err)
	}

	nativeGetFileInformationByHandle = func(_ syscall.Handle, info *syscall.ByHandleFileInformation) error {
		*info = taggedGapInfo(uint64(expected.Size))
		return nil
	}
	finalErr := errors.New("final path failed")
	nativeGetFinalPathNameByHandleCall = func(syscall.Handle, *uint16, uint32, uint32) (uint32, error) {
		return 0, finalErr
	}
	if err := runtime.capture(expected); !errors.Is(err, finalErr) {
		t.Fatalf("final path error = %v", err)
	}

	nativeGetFinalPathNameByHandleCall = func(_ syscall.Handle, buffer *uint16, size uint32, _ uint32) (uint32, error) {
		return taggedGapWriteUTF16(buffer, size, path+"-alias"), nil
	}
	if err := runtime.capture(expected); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("path identity error = %v", err)
	}
}

func TestTrustedNativeRuntimeVerifyAndCloseBranches(t *testing.T) {
	preserveNativeRuntimeHooks(t)
	var nilRuntime *trustedNativeRuntime
	if err := nilRuntime.Verify(); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("nil verify error = %v", err)
	}
	if err := nilRuntime.Close(); err != nil {
		t.Fatalf("nil close error = %v", err)
	}

	file, path := taggedGapFile(t)
	expected := taggedGapInfo(7)
	runtime := &trustedNativeRuntime{path: path, canonical: path, file: file, identity: nativeIdentityFromInfo(expected)}
	infoErr := errors.New("verify info failed")
	nativeGetFileInformationByHandle = func(syscall.Handle, *syscall.ByHandleFileInformation) error { return infoErr }
	if err := runtime.Verify(); !errors.Is(err, infoErr) {
		t.Fatalf("verify info error = %v", err)
	}

	nativeGetFileInformationByHandle = func(_ syscall.Handle, info *syscall.ByHandleFileInformation) error {
		*info = expected
		info.FileIndexLow++
		return nil
	}
	if err := runtime.Verify(); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("identity change error = %v", err)
	}

	nativeGetFileInformationByHandle = func(_ syscall.Handle, info *syscall.ByHandleFileInformation) error {
		*info = expected
		info.NumberOfLinks = 2
		return nil
	}
	if err := runtime.Verify(); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("link change error = %v", err)
	}

	nativeGetFileInformationByHandle = func(_ syscall.Handle, info *syscall.ByHandleFileInformation) error {
		*info = expected
		info.FileAttributes = syscall.FILE_ATTRIBUTE_REPARSE_POINT
		return nil
	}
	if err := runtime.Verify(); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("reparse change error = %v", err)
	}

	nativeGetFileInformationByHandle = func(_ syscall.Handle, info *syscall.ByHandleFileInformation) error {
		*info = expected
		return nil
	}
	finalErr := errors.New("verify final path failed")
	nativeGetFinalPathNameByHandleCall = func(syscall.Handle, *uint16, uint32, uint32) (uint32, error) { return 0, finalErr }
	if err := runtime.Verify(); !errors.Is(err, finalErr) {
		t.Fatalf("verify final path error = %v", err)
	}

	nativeGetFinalPathNameByHandleCall = func(_ syscall.Handle, buffer *uint16, size uint32, _ uint32) (uint32, error) {
		return taggedGapWriteUTF16(buffer, size, path+"-changed"), nil
	}
	if err := runtime.Verify(); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("verify path change error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("runtime close error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("closed runtime close error = %v", err)
	}
}

func TestNativeFinalPathGrowthAndInfoError(t *testing.T) {
	preserveNativeRuntimeHooks(t)
	file, _ := taggedGapFile(t)
	infoErr := errors.New("info error")
	nativeGetFileInformationByHandle = func(syscall.Handle, *syscall.ByHandleFileInformation) error { return infoErr }
	if _, err := nativeRuntimeHandleInfo(file); !errors.Is(err, infoErr) {
		t.Fatalf("nativeRuntimeHandleInfo error = %v", err)
	}

	path := `C:\fixture\` + string(bytes.Repeat([]byte{'x'}, 600))
	calls := 0
	nativeGetFinalPathNameByHandleCall = func(_ syscall.Handle, buffer *uint16, size uint32, _ uint32) (uint32, error) {
		calls++
		encoded, _ := syscall.UTF16FromString(path)
		if uint32(len(encoded)) >= size {
			return uint32(len(encoded)), nil
		}
		return taggedGapWriteUTF16(buffer, size, path), nil
	}
	got, err := nativeFinalPath(file)
	if err != nil || got != path || calls != 2 {
		t.Fatalf("grown final path=%q calls=%d err=%v", got, calls, err)
	}
}

func TestNativeNormalizeFinalPathUNC(t *testing.T) {
	if got, want := nativeNormalizeFinalPath(`\\?\UNC\server\share\runtime.dll`), `\\server\share\runtime.dll`; got != want {
		t.Fatalf("UNC normalized path=%q want=%q", got, want)
	}
}

func TestNativeStarterManifestExportMismatchUsesEqualFields(t *testing.T) {
	manifest := DefaultNativeManifest()
	other := manifest
	other.RequiredExports = append([]string(nil), manifest.RequiredExports...)
	other.RequiredExports[len(other.RequiredExports)-1] += "_different"
	data, err := marshalStarterManifest(other)
	if err != nil {
		t.Fatal(err)
	}
	var decoded NativeManifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if nativeManifestsEqual(decoded, manifest) {
		t.Fatal("export mismatch was accepted")
	}
}
