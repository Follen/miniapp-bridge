//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const nativeReleaseAsset = "miniapp-frida-native-17.3.2-abi1-windows-amd64.zip"

var nativeReleaseExports = []string{
	"mb_abi_version", "mb_native_version", "mb_frida_core_version", "mb_zlib_version",
	"mb_zlib_compress", "mb_zlib_decompress", "mb_bytes_free",
	"mb_device_open", "mb_device_enumerate", "mb_processes_free", "mb_device_attach",
	"mb_device_close", "mb_runtime_shutdown", "mb_session_load_script", "mb_session_detach",
	"mb_script_post", "mb_script_unload", "mb_error_free",
}

type nativeReleaseManifest struct {
	Schema           string   `json:"schema"`
	NativeVersion    string   `json:"nativeVersion"`
	FridaCoreVersion string   `json:"fridaCoreVersion"`
	ZlibVersion      string   `json:"zlibVersion"`
	ABIVersion       int      `json:"abiVersion"`
	OS               string   `json:"os"`
	Arch             string   `json:"arch"`
	DLL              string   `json:"dll"`
	Size             int64    `json:"size"`
	SHA256           string   `json:"sha256"`
	RequiredExports  []string `json:"requiredExports"`
}

func TestNativeReleaseArchiveContract(t *testing.T) {
	repo := filepath.Clean("..")
	runtimeDir := filepath.Join(repo, "third_party", "frida", "runtime-17.3.2")
	dllPath := filepath.Join(runtimeDir, "miniapp-frida.dll")
	if _, err := os.Stat(dllPath); err != nil {
		t.Skipf("native shim has not been built: %v", err)
	}

	out := t.TempDir()
	manifestOut := filepath.Join(t.TempDir(), "metadata")
	args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File",
		filepath.Join(repo, "scripts", "native-release.ps1"),
		"-RuntimeDirectory", runtimeDir,
		"-OutputDirectory", out,
		"-ManifestOutputDirectory", manifestOut,
	}
	runRelease := func() ([]byte, error) {
		return exec.Command("powershell.exe", args...).CombinedOutput()
	}
	output, err := runRelease()
	if err != nil {
		t.Fatalf("native release failed: %v\n%s", err, output)
	}
	for _, marker := range []string{"dll_size=", "dll_sha256=", "archive_sha256=", "sha256sums="} {
		if !bytes.Contains(output, []byte(marker)) {
			t.Errorf("release output missing %q:\n%s", marker, output)
		}
	}

	archivePath := filepath.Join(out, nativeReleaseAsset)
	firstArchiveSHA := fileSHA256(t, archivePath)
	secondOutput, secondErr := runRelease()
	if secondErr != nil {
		t.Fatalf("second native release failed: %v\n%s", secondErr, secondOutput)
	}
	archiveSHA := fileSHA256(t, archivePath)
	if archiveSHA != firstArchiveSHA {
		t.Fatalf("native release is not reproducible: first=%s second=%s", firstArchiveSHA, archiveSHA)
	}
	verifySumFile(t, filepath.Join(out, "SHA256SUMS"), map[string]string{nativeReleaseAsset: archiveSHA})

	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	wantNames := []string{
		"FRIDA_COPYING", "FRIDA_COPYING.LIB", "LICENSE", "SHA256SUMS",
		"THIRD_PARTY_NOTICES.md", "ZLIB_LICENSE", "manifest.json", "miniapp-frida.dll",
	}
	gotNames := make([]string, 0, len(reader.File))
	entries := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		gotNames = append(gotNames, file.Name)
		stream, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		entries[file.Name] = data
	}
	sort.Strings(gotNames)
	if strings.Join(gotNames, "\n") != strings.Join(wantNames, "\n") {
		t.Fatalf("archive entries mismatch\ngot:  %q\nwant: %q", gotNames, wantNames)
	}

	manifestBytes := entries["manifest.json"]
	if bytes.HasPrefix(manifestBytes, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatal("manifest.json has a UTF-8 BOM")
	}
	var manifest nativeReleaseManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	dllBytes := entries["miniapp-frida.dll"]
	dllSHA := bytesSHA256(dllBytes)
	if manifest.Schema != "miniapp-bridge.native-manifest.v1" ||
		manifest.NativeVersion != "17.3.2-abi1" || manifest.FridaCoreVersion != "17.3.2" ||
		manifest.ZlibVersion != "1.3.1" || manifest.ABIVersion != 1 ||
		manifest.OS != "windows" || manifest.Arch != "amd64" || manifest.DLL != "miniapp-frida.dll" ||
		manifest.Size != int64(len(dllBytes)) || !strings.EqualFold(manifest.SHA256, dllSHA) {
		t.Fatalf("manifest does not describe packaged DLL: %+v size=%d sha256=%s", manifest, len(dllBytes), dllSHA)
	}
	sort.Strings(manifest.RequiredExports)
	wantExports := append([]string(nil), nativeReleaseExports...)
	sort.Strings(wantExports)
	if strings.Join(manifest.RequiredExports, "\n") != strings.Join(wantExports, "\n") {
		t.Fatalf("manifest exports mismatch\ngot:  %q\nwant: %q", manifest.RequiredExports, wantExports)
	}

	internalSums := parseSumFile(t, entries["SHA256SUMS"])
	delete(entries, "SHA256SUMS")
	if len(internalSums) != len(entries) {
		t.Fatalf("internal SHA256SUMS covers %d entries, want %d: %v", len(internalSums), len(entries), internalSums)
	}
	for name, data := range entries {
		if got := internalSums[name]; !strings.EqualFold(got, bytesSHA256(data)) {
			t.Errorf("internal sum for %s = %q, want %s", name, got, bytesSHA256(data))
		}
	}
	for archiveName, sourceName := range map[string]string{
		"FRIDA_COPYING":     "COPYING",
		"FRIDA_COPYING.LIB": "COPYING.LIB",
	} {
		want, readErr := os.ReadFile(filepath.Join(repo, "licenses", "frida-17.3.2", sourceName))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(entries[archiveName], want) {
			t.Fatalf("%s differs from pinned source license", archiveName)
		}
	}

	sidecar, err := os.ReadFile(filepath.Join(manifestOut, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sidecar, manifestBytes) {
		t.Fatal("manifest sidecar differs from archive manifest")
	}
	matches, err := filepath.Glob(filepath.Join(out, ".native-release-*"))
	if err != nil {
		t.Fatal(err)
	}
	partials, err := filepath.Glob(filepath.Join(out, ".*.partial*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches)+len(partials) != 0 {
		t.Fatalf("release left staging files: %v %v", matches, partials)
	}
}

func verifySumFile(t *testing.T, path string, want map[string]string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := parseSumFile(t, data)
	if len(got) != len(want) {
		t.Fatalf("%s has %d entries, want %d: %v", path, len(got), len(want), got)
	}
	for name, hash := range want {
		if !strings.EqualFold(got[name], hash) {
			t.Fatalf("%s hash for %s = %q, want %s", path, name, got[name], hash)
		}
	}
}

func parseSumFile(t *testing.T, data []byte) map[string]string {
	t.Helper()
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatal("SHA256SUMS has a UTF-8 BOM")
	}
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(strings.TrimSuffix(line, "\r"), "  ", 2)
		if len(parts) != 2 || len(parts[0]) != sha256.Size*2 {
			t.Fatalf("invalid SHA256SUMS line %q", line)
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			t.Fatalf("invalid SHA-256 in line %q: %v", line, err)
		}
		if _, exists := result[parts[1]]; exists {
			t.Fatalf("duplicate SHA256SUMS entry %q", parts[1])
		}
		result[parts[1]] = strings.ToUpper(parts[0])
	}
	return result
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytesSHA256(data)
}

func bytesSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return strings.ToUpper(fmt.Sprintf("%x", sum[:]))
}
