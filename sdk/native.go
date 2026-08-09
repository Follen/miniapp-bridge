package sdk

import (
	"context"
	"net/http"
	"path/filepath"

	nativecore "github.com/Follen/miniapp-bridge/internal/native"
)

var nativeAbsolutePath = filepath.Abs

const (
	NativeVersion       = nativecore.NativeVersion
	FridaCoreVersion    = nativecore.FridaCoreVersion
	ZlibVersion         = nativecore.ZlibVersion
	NativeABIVersion    = nativecore.NativeABIVersion
	NativeDLLFileName   = nativecore.NativeDLLFileName
	NativeDLLSize       = nativecore.NativeDLLSize
	NativeDLLSHA256     = nativecore.NativeDLLSHA256
	NativeArchiveSHA256 = nativecore.NativeArchiveSHA256
)

var (
	ErrNativeMissing      = nativecore.ErrNativeMissing
	ErrNativeHashMismatch = nativecore.ErrNativeHashMismatch
	ErrNativeWrongArch    = nativecore.ErrNativeWrongArch
	ErrNativeManifest     = nativecore.ErrNativeManifest
	ErrNativeOffline      = nativecore.ErrNativeOffline
	ErrNativeDownload     = nativecore.ErrNativeDownload
	ErrNativeCache        = nativecore.ErrNativeCache
	ErrNativeArchive      = nativecore.ErrNativeArchive
)

// NativeRuntimeError carries operation, path, expected/actual and sentinel
// details for native loading and preparation failures.
type NativeRuntimeError = nativecore.NativeRuntimeError

type NativeManifest struct {
	Schema           string
	NativeVersion    string
	FridaCoreVersion string
	ZlibVersion      string
	ABIVersion       uint32
	OS               string
	Arch             string
	DLL              string
	Size             int64
	SHA256           string
	RequiredExports  []string
}

type NativePrepareOptions struct {
	CacheDir           string
	SourceURL          string
	ExpectedArchiveSHA string
	Manifest           NativeManifest
	Offline            bool
	HTTPClient         *http.Client
}

func DefaultNativeManifest() NativeManifest {
	return fromNativeManifest(nativecore.DefaultManifest())
}

func CheckNativeRuntime(path string, manifest ...NativeManifest) error {
	if len(manifest) == 0 {
		return nativecore.CheckNativeRuntime(path)
	}
	return nativecore.CheckNativeRuntime(path, toNativeManifest(manifest[0]))
}

// InspectNativeRuntime validates a runtime file and returns the metadata a
// NativeSession can expose through NativeMetadata. Inspection does not load or
// attach the runtime, so Attached is always false; Service owns that state.
func InspectNativeRuntime(path string, manifest ...NativeManifest) (NativeStatus, error) {
	if err := CheckNativeRuntime(path, manifest...); err != nil {
		return NativeStatus{}, err
	}
	absolute, err := nativeAbsolutePath(path)
	if err != nil {
		return NativeStatus{}, &NativeRuntimeError{Code: ErrNativeCache, Operation: "absolute path", Path: path, Err: err}
	}
	m := DefaultNativeManifest()
	if len(manifest) > 0 && manifest[0].NativeVersion != "" {
		m = manifest[0]
	}
	return NativeStatus{Version: m.NativeVersion, ABI: m.ABIVersion, Path: filepath.Clean(absolute)}, nil
}

func PrepareNativeRuntime(ctx context.Context, opts NativePrepareOptions) (string, error) {
	return nativecore.Prepare(ctx, nativecore.PrepareOptions{
		CacheDir: opts.CacheDir, SourceURL: opts.SourceURL,
		ExpectedArchiveSHA: opts.ExpectedArchiveSHA,
		Manifest:           toNativeManifest(opts.Manifest),
		Offline:            opts.Offline, HTTPClient: opts.HTTPClient,
	})
}

func toNativeManifest(m NativeManifest) nativecore.Manifest {
	if m.NativeVersion == "" {
		return nativecore.DefaultManifest()
	}
	return nativecore.Manifest{Schema: m.Schema, NativeVersion: m.NativeVersion, FridaCoreVersion: m.FridaCoreVersion, ZlibVersion: m.ZlibVersion, ABIVersion: m.ABIVersion, OS: m.OS, Arch: m.Arch, DLL: m.DLL, Size: m.Size, SHA256: m.SHA256, RequiredExports: append([]string(nil), m.RequiredExports...)}
}

func fromNativeManifest(m nativecore.Manifest) NativeManifest {
	return NativeManifest{Schema: m.Schema, NativeVersion: m.NativeVersion, FridaCoreVersion: m.FridaCoreVersion, ZlibVersion: m.ZlibVersion, ABIVersion: m.ABIVersion, OS: m.OS, Arch: m.Arch, DLL: m.DLL, Size: m.Size, SHA256: m.SHA256, RequiredExports: append([]string(nil), m.RequiredExports...)}
}
