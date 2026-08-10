package native

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeStrictManifestAndPrepareBranches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, NativeDLLFileName)
	data := []byte("fixture")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	manifestForDLL(&m, data)
	badABI := m
	badABI.ABIVersion++
	if err := VerifyManifest(path, badABI); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("ABI mismatch=%v", err)
	}
	if _, err := Prepare(context.Background(), PrepareOptions{Manifest: m, ExpectedArchiveSHA: "abc", Offline: true}); !errors.Is(err, ErrNativeManifest) {
		t.Fatalf("bad archive trust=%v", err)
	}

	archive := makeArchive(t, m, data, "")
	client := &http.Client{Transport: nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}
	originalOpen := nativeOpenPartial
	nativeOpenPartial = func(string, string) (string, io.WriteCloser, error) {
		return "", nil, errors.New("open partial injected")
	}
	defer func() { nativeOpenPartial = originalOpen }()
	if _, err := Prepare(context.Background(), PrepareOptions{CacheDir: t.TempDir(), HTTPClient: client, Manifest: m}); !errors.Is(err, ErrNativeCache) {
		t.Fatalf("open partial=%v", err)
	}
}

func TestNativeExtractDuplicateLimitAndVerifyBranches(t *testing.T) {
	data := []byte("fixture")
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	manifestForDLL(&m, data)

	oldLimit := nativeZIPEntryLimit
	nativeZIPEntryLimit = 1
	archive := makeArchive(t, m, data, "")
	zipPath := filepath.Join(t.TempDir(), "limit.zip")
	if err := os.WriteFile(zipPath, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(zipPath, t.TempDir(), m); !errors.Is(err, ErrNativeArchive) {
		t.Fatalf("entry limit=%v", err)
	}
	nativeZIPEntryLimit = oldLimit

	duplicateManifest := zipEntriesOrdered(t, []zipEntry{
		{"manifest.json", mustJSON(t, m)}, {"manifest.json", mustJSON(t, m)}, {m.DLL, data},
	})
	path := filepath.Join(t.TempDir(), "duplicate-manifest.zip")
	if err := os.WriteFile(path, duplicateManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(path, t.TempDir(), m); !errors.Is(err, ErrNativeArchive) {
		t.Fatalf("duplicate manifest=%v", err)
	}
	duplicateDLL := zipEntriesOrdered(t, []zipEntry{
		{"manifest.json", mustJSON(t, m)}, {m.DLL, data}, {m.DLL, data},
	})
	path = filepath.Join(t.TempDir(), "duplicate-dll.zip")
	if err := os.WriteFile(path, duplicateDLL, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(path, t.TempDir(), m); !errors.Is(err, ErrNativeArchive) {
		t.Fatalf("duplicate DLL=%v", err)
	}

	wrong := []byte("different")
	wrongArchive := zipEntriesOrdered(t, []zipEntry{
		{"manifest.json", mustJSON(t, m)}, {m.DLL, wrong},
	})
	path = filepath.Join(t.TempDir(), "verify.zip")
	if err := os.WriteFile(path, wrongArchive, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(path, t.TempDir(), m); !errors.Is(err, ErrNativeHashMismatch) {
		t.Fatalf("verify extracted DLL=%v", err)
	}
}

func TestNativeExtractManifestCommitAndRollbackBranches(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	manifestForDLL(&m, data)
	archive := makeArchive(t, m, data, "")
	path := filepath.Join(t.TempDir(), "valid.zip")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	originalReplace := nativeReplaceFile
	nativeReplaceFile = func(_, destination string) error {
		if filepath.Base(destination) == "manifest.json" {
			return errors.New("manifest commit injected")
		}
		return replaceFileAtomic("", destination)
	}
	_, err = extractArchive(path, t.TempDir(), m)
	nativeReplaceFile = originalReplace
	if !errors.Is(err, ErrNativeCache) {
		t.Fatalf("manifest commit=%v", err)
	}

	stage := t.TempDir()
	destination := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreManifest(filepath.Join(stage, "missing-stage"), destination, []byte("old"), true); err == nil {
		t.Fatal("rollback write unexpectedly succeeded")
	}
	if err := restoreManifest(stage, "bad\x00destination", []byte("old"), true); err == nil {
		t.Fatal("rollback replace unexpectedly succeeded")
	}
	removeDir := t.TempDir()
	removeDestination := filepath.Join(removeDir, "manifest.json")
	if err := os.Mkdir(removeDestination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(removeDestination, "held"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreManifest(stage, removeDestination, nil, false); err == nil {
		t.Fatal("rollback remove unexpectedly succeeded")
	}

	cache := t.TempDir()
	if err := os.Mkdir(filepath.Join(cache, "manifest.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	originalReplace = nativeReplaceFile
	nativeReplaceFile = func(_, destination string) error {
		if filepath.Base(destination) == "manifest.json" {
			return nil
		}
		return errors.New("DLL commit injected")
	}
	_, err = extractArchive(path, cache, m)
	nativeReplaceFile = originalReplace
	if !errors.Is(err, ErrNativeCache) {
		t.Fatalf("rollback remove failure=%v", err)
	}
}

func TestNativeDecodeManifestStrictBranches(t *testing.T) {
	m := DefaultManifest()
	m.OS, m.Arch = runtime.GOOS, runtime.GOARCH
	valid := mustJSON(t, m)
	for name, input := range map[string][]byte{
		"not object":  []byte("[]"),
		"key error":   []byte("{,"),
		"unknown":     []byte(`{"unknown":1}`),
		"duplicate":   []byte(`{"schema":"x","schema":"y"}`),
		"value error": []byte(`{"schema":`),
		"missing":     []byte(`{}`),
		"trailing":    append(valid, []byte(`{}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeManifest(input); err == nil {
				t.Fatal("malformed manifest accepted")
			}
		})
	}
	typeError := bytes.Replace(valid, []byte(`"schema":"miniapp-bridge.native-manifest.v1"`), []byte(`"schema":1`), 1)
	if _, err := decodeManifest(typeError); err == nil {
		t.Fatal("typed manifest mismatch accepted")
	}
}

func TestNativeReadZipManifestLimit(t *testing.T) {
	archive := zipEntriesOrdered(t, []zipEntry{{"manifest.json", []byte(strings.Repeat("x", 8))}})
	path := filepath.Join(t.TempDir(), "manifest-limit.zip")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	original := nativeManifestLimit
	nativeManifestLimit = 1
	defer func() { nativeManifestLimit = original }()
	if _, err := readZipFile(r.File[0]); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}

type zipEntry struct {
	name string
	data []byte
}

func zipEntriesOrdered(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
