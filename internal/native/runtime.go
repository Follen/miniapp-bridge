package native

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	NativeVersion        = "17.3.2-abi1.1"
	FridaCoreVersion     = "17.3.2"
	ZlibVersion          = "1.3.1"
	NativeABIVersion     = uint32(1)
	NativeDLLFileName    = "miniapp-frida.dll"
	nativeManifestSchema = "miniapp-bridge.native-manifest.v1"
)

var requiredNativeExports = []string{
	"mb_abi_version", "mb_native_version", "mb_frida_core_version", "mb_zlib_version",
	"mb_zlib_compress", "mb_zlib_decompress", "mb_bytes_free",
	"mb_device_open", "mb_device_enumerate", "mb_processes_free", "mb_device_attach",
	"mb_device_close", "mb_runtime_shutdown", "mb_session_load_script", "mb_session_detach",
	"mb_script_post", "mb_script_unload", "mb_error_free",
}

const (
	defaultArchiveLimit  int64 = 256 << 20
	defaultDLLLimit      int64 = 256 << 20
	defaultManifestLimit int64 = 1 << 20
	defaultZIPEntryLimit       = 256
)

var (
	ErrNativeMissing      = errors.New("native runtime missing")
	ErrNativeHashMismatch = errors.New("native runtime hash mismatch")
	ErrNativeWrongArch    = errors.New("native runtime architecture mismatch")
	ErrNativeManifest     = errors.New("native runtime manifest invalid")
	ErrNativeOffline      = errors.New("native runtime unavailable offline")
	ErrNativeDownload     = errors.New("native runtime download failed")
	ErrNativeCache        = errors.New("native runtime cache failure")
	ErrNativeArchive      = errors.New("native runtime archive invalid")
)

type Error struct {
	Code      error
	Operation string
	Path      string
	Expected  string
	Actual    string
	Err       error
}

// NativeRuntimeError is the stable name used by SDK adapters.
type NativeRuntimeError = Error

func (e *Error) Error() string {
	parts := []string{e.Operation}
	if e.Path != "" {
		parts = append(parts, e.Path)
	}
	if e.Expected != "" || e.Actual != "" {
		parts = append(parts, fmt.Sprintf("expected=%s actual=%s", e.Expected, e.Actual))
	}
	if e.Err != nil {
		parts = append(parts, e.Err.Error())
	}
	return "native: " + strings.Join(parts, ": ")
}
func (e *Error) Unwrap() error        { return e.Err }
func (e *Error) Is(target error) bool { return target == e.Code || errors.Is(e.Err, target) }

type Manifest struct {
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

type PrepareOptions struct {
	CacheDir           string
	SourceURL          string
	ExpectedArchiveSHA string
	Manifest           Manifest
	Offline            bool
	HTTPClient         *http.Client
}

var (
	nativeUserCacheDir = os.UserCacheDir
	nativeOpenPartial  = func(dir, pattern string) (string, io.WriteCloser, error) {
		file, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return "", nil, err
		}
		return file.Name(), file, nil
	}
	nativeHashFile      = fileSHA256
	nativeWriteFile     = os.WriteFile
	nativeReplaceFile   = replaceFileAtomic
	nativeTryLockFile   = tryLockFile
	nativeArchiveLimit  = defaultArchiveLimit
	nativeDLLLimit      = defaultDLLLimit
	nativeManifestLimit = defaultManifestLimit
	nativeZIPEntryLimit = defaultZIPEntryLimit
)

func DefaultManifest() Manifest {
	return Manifest{
		Schema: nativeManifestSchema, NativeVersion: NativeVersion, FridaCoreVersion: FridaCoreVersion,
		ZlibVersion: ZlibVersion, ABIVersion: NativeABIVersion, OS: "windows", Arch: "amd64",
		DLL: NativeDLLFileName, Size: NativeDLLSize, SHA256: NativeDLLSHA256,
		RequiredExports: append([]string(nil), requiredNativeExports...),
	}
}

func VerifyManifest(path string, m Manifest) error {
	if err := validateManifest(m, path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Error{Code: ErrNativeMissing, Operation: "stat", Path: path, Err: err}
		}
		return &Error{Code: ErrNativeCache, Operation: "stat", Path: path, Err: err}
	}
	if info.Size() != m.Size {
		return &Error{Code: ErrNativeHashMismatch, Operation: "size", Path: path, Expected: fmt.Sprint(m.Size), Actual: fmt.Sprint(info.Size())}
	}
	got, err := nativeHashFile(path)
	if err != nil {
		return &Error{Code: ErrNativeCache, Operation: "hash", Path: path, Err: err}
	}
	if !strings.EqualFold(got, m.SHA256) {
		return &Error{Code: ErrNativeHashMismatch, Operation: "hash", Path: path, Expected: strings.ToUpper(m.SHA256), Actual: got}
	}
	return verifyPlatformFile(path)
}

func validateManifest(m Manifest, source string) error {
	if m.DLL != NativeDLLFileName || filepath.Base(m.DLL) != m.DLL || m.DLL == "." || m.DLL == ".." {
		return &Error{Code: ErrNativeManifest, Operation: "manifest dll", Path: source, Expected: NativeDLLFileName, Actual: m.DLL, Err: ErrNativeManifest}
	}
	checks := []struct {
		operation string
		expected  string
		actual    string
	}{
		{"manifest schema", nativeManifestSchema, m.Schema},
		{"manifest native version", NativeVersion, m.NativeVersion},
		{"manifest frida version", FridaCoreVersion, m.FridaCoreVersion},
		{"manifest zlib version", ZlibVersion, m.ZlibVersion},
	}
	for _, check := range checks {
		if check.actual != check.expected {
			return &Error{Code: ErrNativeManifest, Operation: check.operation, Path: source, Expected: check.expected, Actual: check.actual, Err: ErrNativeManifest}
		}
	}
	if m.ABIVersion != NativeABIVersion {
		return &Error{Code: ErrNativeManifest, Operation: "manifest ABI", Path: source, Expected: fmt.Sprint(NativeABIVersion), Actual: fmt.Sprint(m.ABIVersion), Err: ErrNativeManifest}
	}
	if m.OS != runtime.GOOS || m.Arch != runtime.GOARCH {
		return &Error{Code: ErrNativeWrongArch, Operation: "manifest platform", Path: source, Expected: runtime.GOOS + "/" + runtime.GOARCH, Actual: m.OS + "/" + m.Arch, Err: ErrNativeWrongArch}
	}
	if m.Size <= 0 || m.Size > defaultDLLLimit || !validSHA256(m.SHA256) {
		return &Error{Code: ErrNativeManifest, Operation: "manifest trust", Path: source, Expected: "positive bounded size and SHA-256", Actual: fmt.Sprintf("size=%d sha256=%s", m.Size, m.SHA256), Err: ErrNativeManifest}
	}
	if !sameStringSet(m.RequiredExports, requiredNativeExports) {
		return &Error{Code: ErrNativeManifest, Operation: "manifest exports", Path: source, Expected: strings.Join(requiredNativeExports, ","), Actual: strings.Join(m.RequiredExports, ","), Err: ErrNativeManifest}
	}
	return nil
}

func validSHA256(value string) bool {
	return len(value) == sha256.Size*2 && strings.IndexFunc(value, func(r rune) bool {
		return !strings.ContainsRune("0123456789abcdefABCDEF", r)
	}) < 0
}

func sameStringSet(got, want []string) bool {
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

// CheckNativeRuntime validates the default pinned runtime, or an explicit
// manifest when supplied by a release/channel configuration.
func CheckNativeRuntime(path string, manifests ...Manifest) error {
	m := DefaultManifest()
	if len(manifests) > 0 {
		m = manifests[0]
	}
	return VerifyManifest(path, m)
}

func Prepare(ctx context.Context, opts PrepareOptions) (string, error) {
	m := opts.Manifest
	if manifestIsZero(m) {
		m = DefaultManifest()
	}
	if err := validateManifest(m, m.DLL); err != nil {
		return "", err
	}
	expectedArchiveSHA := strings.TrimSpace(opts.ExpectedArchiveSHA)
	if expectedArchiveSHA == "" {
		expectedArchiveSHA = NativeArchiveSHA256
	}
	if !validSHA256(expectedArchiveSHA) {
		return "", &Error{Code: ErrNativeManifest, Operation: "archive trust", Expected: "SHA-256", Actual: expectedArchiveSHA, Err: ErrNativeManifest}
	}
	cache := opts.CacheDir
	if cache == "" {
		var err error
		cache, err = nativeUserCacheDir()
		if err != nil {
			return "", &Error{Code: ErrNativeCache, Operation: "cache directory", Err: err}
		}
		cache = filepath.Join(cache, "miniapp-bridge", "native", m.NativeVersion, runtime.GOOS+"-"+runtime.GOARCH)
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return "", &Error{Code: ErrNativeCache, Operation: "create cache", Path: cache, Err: err}
	}
	dll := filepath.Join(cache, m.DLL)
	unlock, err := acquireLock(ctx, dll+".lock")
	if err != nil {
		return "", err
	}
	defer unlock()
	cacheErr := verifyCachedRuntime(dll, m)
	if cacheErr == nil {
		return dll, nil
	}
	if opts.Offline {
		return "", &Error{Code: ErrNativeOffline, Operation: "offline cache", Path: dll, Err: cacheErr}
	}
	url := opts.SourceURL
	if url == "" {
		url = fmt.Sprintf("https://github.com/Follen/miniapp-bridge/releases/download/native-v%s/miniapp-frida-native-%s-windows-amd64.zip", m.NativeVersion, m.NativeVersion)
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", &Error{Code: ErrNativeDownload, Operation: "request", Err: err}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", &Error{Code: ErrNativeDownload, Operation: "download", Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &Error{Code: ErrNativeDownload, Operation: "download", Actual: resp.Status, Err: ErrNativeDownload}
	}
	tmp, f, err := nativeOpenPartial(cache, "."+m.DLL+".*.partial")
	if err != nil {
		return "", &Error{Code: ErrNativeCache, Operation: "open partial", Path: tmp, Err: err}
	}
	copied, copyErr := io.Copy(f, io.LimitReader(resp.Body, nativeArchiveLimit+1))
	closeErr := f.Close()
	if copied > nativeArchiveLimit {
		_ = os.Remove(tmp)
		return "", &Error{Code: ErrNativeDownload, Operation: "download size", Err: errors.New("native archive exceeds 256 MiB")}
	}
	if copyErr != nil {
		_ = os.Remove(tmp)
		return "", &Error{Code: ErrNativeDownload, Operation: "write archive", Err: copyErr}
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", &Error{Code: ErrNativeCache, Operation: "close archive", Err: closeErr}
	}
	defer os.Remove(tmp)
	got, e := nativeHashFile(tmp)
	if e != nil {
		return "", &Error{Code: ErrNativeCache, Operation: "hash archive", Err: e}
	}
	if !strings.EqualFold(got, expectedArchiveSHA) {
		return "", &Error{Code: ErrNativeHashMismatch, Operation: "archive hash", Expected: strings.ToUpper(expectedArchiveSHA), Actual: got}
	}
	return extractArchive(tmp, cache, m)
}

func manifestIsZero(m Manifest) bool {
	return m.Schema == "" && m.NativeVersion == "" && m.FridaCoreVersion == "" && m.ZlibVersion == "" &&
		m.ABIVersion == 0 && m.OS == "" && m.Arch == "" && m.DLL == "" && m.Size == 0 && m.SHA256 == "" && len(m.RequiredExports) == 0
}

func PrepareNativeRuntime(ctx context.Context, opts PrepareOptions) (string, error) {
	return Prepare(ctx, opts)
}

func verifyCachedRuntime(dll string, expected Manifest) error {
	manifestPath := filepath.Join(filepath.Dir(dll), "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		code := ErrNativeCache
		if errors.Is(err, os.ErrNotExist) {
			code = ErrNativeMissing
		}
		return &Error{Code: code, Operation: "read manifest", Path: manifestPath, Err: err}
	}
	installed, err := decodeManifest(data)
	if err != nil {
		return &Error{Code: ErrNativeManifest, Operation: "decode manifest", Path: manifestPath, Err: err}
	}
	if !manifestsMatch(installed, expected) {
		return &Error{Code: ErrNativeManifest, Operation: "cached manifest mismatch", Path: manifestPath, Expected: expected.NativeVersion, Actual: installed.NativeVersion}
	}
	return VerifyManifest(dll, installed)
}

func extractArchive(archive, cache string, expected Manifest) (string, error) {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return "", &Error{Code: ErrNativeArchive, Operation: "open archive", Err: err}
	}
	defer r.Close()
	if len(r.File) > nativeZIPEntryLimit {
		return "", &Error{Code: ErrNativeArchive, Operation: "zip entries", Expected: fmt.Sprint(nativeZIPEntryLimit), Actual: fmt.Sprint(len(r.File)), Err: ErrNativeArchive}
	}
	stage, err := os.MkdirTemp(cache, ".native-extract-")
	if err != nil {
		return "", &Error{Code: ErrNativeCache, Operation: "create staging", Err: err}
	}
	defer os.RemoveAll(stage)
	var manifest Manifest
	var haveManifest, haveDLL bool
	for _, file := range r.File {
		if err := validateZIPEntry(file); err != nil {
			return "", err
		}
		name := file.Name
		switch name {
		case "manifest.json":
			if haveManifest {
				return "", &Error{Code: ErrNativeArchive, Operation: "duplicate manifest", Path: name, Err: ErrNativeArchive}
			}
			data, e := readZipFile(file)
			if e != nil {
				return "", &Error{Code: ErrNativeArchive, Operation: "read manifest", Err: e}
			}
			if manifest, e = decodeManifest(data); e != nil {
				return "", &Error{Code: ErrNativeManifest, Operation: "decode manifest", Err: e}
			}
			haveManifest = true
		case expected.DLL:
			if haveDLL {
				return "", &Error{Code: ErrNativeArchive, Operation: "duplicate dll", Path: name, Err: ErrNativeArchive}
			}
			target := filepath.Join(stage, expected.DLL)
			in, e := file.Open()
			if e != nil {
				return "", e
			}
			out, e := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
			if e == nil {
				var copied int64
				copied, e = io.Copy(out, io.LimitReader(in, nativeDLLLimit+1))
				if e == nil && copied > nativeDLLLimit {
					e = errors.New("native DLL exceeds extraction limit")
				}
				_ = out.Close()
			}
			_ = in.Close()
			if e != nil {
				return "", &Error{Code: ErrNativeArchive, Operation: "extract dll", Err: e}
			}
			haveDLL = true
		}
	}
	if !haveManifest || !haveDLL {
		return "", &Error{Code: ErrNativeArchive, Operation: "required entries", Err: ErrNativeArchive}
	}
	if !manifestsMatch(manifest, expected) {
		return "", &Error{Code: ErrNativeManifest, Operation: "manifest mismatch", Expected: expected.NativeVersion, Actual: manifest.NativeVersion}
	}
	dll := filepath.Join(stage, manifest.DLL)
	if err := VerifyManifest(dll, manifest); err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	stagedManifest := filepath.Join(stage, "manifest.json")
	if err := nativeWriteFile(stagedManifest, append(data, '\n'), 0o600); err != nil {
		return "", &Error{Code: ErrNativeCache, Operation: "install manifest", Err: err}
	}
	finalManifest := filepath.Join(cache, "manifest.json")
	oldManifest, oldManifestErr := os.ReadFile(finalManifest)
	if oldManifestErr != nil && !errors.Is(oldManifestErr, os.ErrNotExist) {
		return "", &Error{Code: ErrNativeCache, Operation: "backup manifest", Path: finalManifest, Err: oldManifestErr}
	}
	if err := nativeReplaceFile(stagedManifest, finalManifest); err != nil {
		return "", &Error{Code: ErrNativeCache, Operation: "install manifest", Path: finalManifest, Err: err}
	}
	// The DLL is the readiness marker. Commit it last, after the matching
	// manifest is durable, so an interrupted install cannot become a cache hit.
	final := filepath.Join(cache, manifest.DLL)
	if err := nativeReplaceFile(dll, final); err != nil {
		rollbackErr := restoreManifest(stage, finalManifest, oldManifest, oldManifestErr == nil)
		return "", &Error{Code: ErrNativeCache, Operation: "install dll", Path: final, Err: errors.Join(err, rollbackErr)}
	}
	return final, nil
}

func manifestsMatch(got, want Manifest) bool {
	return got.Schema == want.Schema && got.NativeVersion == want.NativeVersion &&
		got.FridaCoreVersion == want.FridaCoreVersion && got.ZlibVersion == want.ZlibVersion &&
		got.ABIVersion == want.ABIVersion && got.OS == want.OS && got.Arch == want.Arch &&
		got.DLL == want.DLL && got.Size == want.Size && strings.EqualFold(got.SHA256, want.SHA256) &&
		sameStringSet(got.RequiredExports, want.RequiredExports)
}

func validateZIPEntry(file *zip.File) error {
	name := file.Name
	driveQualified := len(name) >= 2 && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) && name[1] == ':'
	clean := path.Clean(name)
	if name == "" || file.FileInfo().IsDir() || strings.ContainsRune(name, '\\') || strings.ContainsRune(name, '\x00') ||
		path.IsAbs(name) || driveQualified || clean != name || clean == ".." || strings.HasPrefix(clean, "../") {
		return &Error{Code: ErrNativeArchive, Operation: "zip path", Path: name, Err: ErrNativeArchive}
	}
	return nil
}

func decodeManifest(data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return Manifest{}, fmt.Errorf("manifest object: %w", err)
	}
	allowed := map[string]struct{}{
		"schema": {}, "nativeVersion": {}, "fridaCoreVersion": {}, "zlibVersion": {}, "abiVersion": {},
		"os": {}, "arch": {}, "dll": {}, "size": {}, "sha256": {}, "requiredExports": {},
	}
	seen := make(map[string]struct{}, len(allowed))
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return Manifest{}, err
		}
		key := keyToken.(string)
		if _, ok := allowed[key]; !ok {
			return Manifest{}, fmt.Errorf("unknown manifest field %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return Manifest{}, fmt.Errorf("duplicate manifest field %q", key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return Manifest{}, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return Manifest{}, err
	}
	if len(seen) != len(allowed) {
		return Manifest{}, errors.New("manifest is missing required fields")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("manifest has trailing JSON value")
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func restoreManifest(stage, destination string, old []byte, existed bool) error {
	if !existed {
		if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove uncommitted manifest: %w", err)
		}
		return nil
	}
	rollback := filepath.Join(stage, "manifest.rollback.json")
	if err := os.WriteFile(rollback, old, 0o600); err != nil {
		return fmt.Errorf("write manifest rollback: %w", err)
	}
	if err := replaceFileAtomic(rollback, destination); err != nil {
		return fmt.Errorf("restore manifest: %w", err)
	}
	return nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, nativeManifestLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > nativeManifestLimit {
		return nil, errors.New("native manifest exceeds extraction limit")
	}
	return data, nil
}
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}

func acquireLock(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, &Error{Code: ErrNativeCache, Operation: "acquire lock", Path: path, Err: err}
	}
	for {
		locked, lockErr := nativeTryLockFile(f)
		if lockErr != nil {
			_ = f.Close()
			return nil, &Error{Code: ErrNativeCache, Operation: "acquire lock", Path: path, Err: lockErr}
		}
		if locked {
			return func() {
				_ = unlockFile(f)
				_ = f.Close()
			}, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = f.Close()
			return nil, &Error{Code: ErrNativeCache, Operation: "acquire lock", Path: path, Err: ctx.Err()}
		case <-timer.C:
		}
	}
}
