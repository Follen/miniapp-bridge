//go:build windows

package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type windowsReleaseFixture struct {
	root, input, native, output string
	dll, manifest, nativeZIP    []byte
}

func TestPackageWindowsReleaseProducesReproducibleVerifiedAssets(t *testing.T) {
	fixture := newWindowsReleaseFixture(t)
	output, err := fixture.run("v0.0.1")
	if err != nil {
		t.Fatalf("package release failed: %v\n%s", err, output)
	}
	for _, marker := range []string{"product_asset=", "product_sha256=", "native_asset=", "native_sha256=", "manifest=", "sha256sums="} {
		if !strings.Contains(output, marker) {
			t.Errorf("release output missing %q:\n%s", marker, output)
		}
	}

	productName := "miniapp-bridge-v0.0.1-windows-amd64.zip"
	nativeName := "miniapp-frida-native-17.3.2-abi1-windows-amd64.zip"
	productPath := filepath.Join(fixture.output, productName)
	firstHash := fileSHA256(t, productPath)
	secondOutput, secondErr := fixture.run("v0.0.1")
	if secondErr != nil {
		t.Fatalf("second package release failed: %v\n%s", secondErr, secondOutput)
	}
	if got := fileSHA256(t, productPath); got != firstHash {
		t.Fatalf("product archive is not reproducible: first=%s second=%s", firstHash, got)
	}

	manifestPath := filepath.Join(fixture.output, "manifest.json")
	manifestHash := fileSHA256(t, manifestPath)
	verifySumFile(t, filepath.Join(fixture.output, "SHA256SUMS"), map[string]string{
		productName:     firstHash,
		nativeName:      bytesSHA256(fixture.nativeZIP),
		"manifest.json": manifestHash,
	})
	assertFileContent(t, filepath.Join(fixture.output, nativeName), fixture.nativeZIP)
	assertFileContent(t, manifestPath, fixture.manifest)
	assertFileContent(t, filepath.Join(fixture.output, "native-compat", nativeName), fixture.nativeZIP)
	verifySumFile(t, filepath.Join(fixture.output, "native-compat", "SHA256SUMS"), map[string]string{
		nativeName: bytesSHA256(fixture.nativeZIP),
	})
	assertReleaseBundleEntries(t, fixture.output, []string{
		productName,
		nativeName,
		"manifest.json",
		"SHA256SUMS",
		"native-compat/" + nativeName,
		"native-compat/SHA256SUMS",
	})

	reader, err := zip.OpenReader(productPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	wantNames := []string{"LICENSE", "README.md", "README.zh.md", "THIRD_PARTY_NOTICES.md", "ZLIB_LICENSE", "manifest.json", "miniapp-bridge.exe", "miniapp-frida.dll"}
	gotNames := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		gotNames = append(gotNames, file.Name)
		if !file.Modified.Equal(reader.File[0].Modified) {
			t.Errorf("zip entry %s has a different timestamp", file.Name)
		}
	}
	sort.Strings(gotNames)
	sort.Strings(wantNames)
	if strings.Join(gotNames, "\n") != strings.Join(wantNames, "\n") {
		t.Fatalf("product archive entries mismatch\ngot:  %q\nwant: %q", gotNames, wantNames)
	}
	assertNoWindowsReleaseTemps(t, fixture.output)
}

func TestPackageWindowsReleasePublishesBundleAtomically(t *testing.T) {
	fixture := newWindowsReleaseFixture(t)
	output, err := fixture.run("v0.0.1")
	if err != nil {
		t.Fatalf("initial package release failed: %v\n%s", err, output)
	}
	original := snapshotReleaseBundle(t, fixture.output)
	if err := os.WriteFile(filepath.Join(fixture.input, "miniapp-bridge.exe"), []byte("MZ replacement executable"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, failPoint := range []string{"DuringStage", "AfterStage", "AfterBackup", "AfterPublish"} {
		output, err := fixture.run("v0.0.1", failPoint)
		if err == nil || !strings.Contains(output, "injected release packaging failure: "+failPoint) {
			t.Fatalf("failure point %s did not fail as expected: err=%v\n%s", failPoint, err, output)
		}
		if got := snapshotReleaseBundle(t, fixture.output); !reflect.DeepEqual(got, original) {
			t.Fatalf("failure point %s changed the previously published bundle", failPoint)
		}
		assertNoWindowsReleaseTemps(t, fixture.output)
	}

	output, err = fixture.run("v0.0.1")
	if err != nil {
		t.Fatalf("retry after injected failures failed: %v\n%s", err, output)
	}
	if got := snapshotReleaseBundle(t, fixture.output); reflect.DeepEqual(got, original) {
		t.Fatal("successful retry did not publish the replacement bundle")
	}
	assertNoWindowsReleaseTemps(t, fixture.output)
}

func TestPackageWindowsReleaseFirstFailureLeavesNoFinalBundle(t *testing.T) {
	for _, failPoint := range []string{"DuringStage", "AfterStage", "AfterBackup", "AfterPublish"} {
		t.Run(failPoint, func(t *testing.T) {
			fixture := newWindowsReleaseFixture(t)
			output, err := fixture.run("v0.0.1", failPoint)
			if err == nil || !strings.Contains(output, "injected release packaging failure: "+failPoint) {
				t.Fatalf("failure point %s did not fail as expected: err=%v\n%s", failPoint, err, output)
			}
			if _, statErr := os.Stat(fixture.output); !os.IsNotExist(statErr) {
				t.Fatalf("failed first publication left a final bundle: %v", statErr)
			}
			assertNoWindowsReleaseTemps(t, fixture.output)
		})
	}
}

func TestPackageWindowsReleaseRejectsInvalidInputs(t *testing.T) {
	t.Run("version", func(t *testing.T) {
		for _, version := range []string{
			"latest",
			"1.2.3",
			"v01.2.3",
			"v1.2.3-01",
			"v1.2.3+build.7",
			"v2.0.0",
			"v2.0.0-rc.1",
			"V0.0.1",
			"V1.2.3-rc.1",
		} {
			fixture := newWindowsReleaseFixture(t)
			output, err := fixture.run(version)
			if err == nil || !strings.Contains(output, "Version must be a Go-compatible semantic version tag") {
				t.Fatalf("invalid version %q was not rejected: err=%v\n%s", version, err, output)
			}
		}
	})

	t.Run("dll-manifest", func(t *testing.T) {
		fixture := newWindowsReleaseFixture(t)
		if err := os.WriteFile(filepath.Join(fixture.input, "miniapp-frida.dll"), []byte("tampered"), 0o644); err != nil {
			t.Fatal(err)
		}
		output, err := fixture.run("v1.0.0")
		if err == nil || !strings.Contains(output, "DLL does not match manifest") {
			t.Fatalf("tampered DLL was not rejected: err=%v\n%s", err, output)
		}
	})

	t.Run("native-hash", func(t *testing.T) {
		fixture := newWindowsReleaseFixture(t)
		nativeName := "miniapp-frida-native-17.3.2-abi1-windows-amd64.zip"
		if err := os.WriteFile(filepath.Join(fixture.native, nativeName), []byte("tampered"), 0o644); err != nil {
			t.Fatal(err)
		}
		output, err := fixture.run("v1.0.0")
		if err == nil || !strings.Contains(output, "native archive SHA-256 mismatch") {
			t.Fatalf("tampered native archive was not rejected: err=%v\n%s", err, output)
		}
	})

	t.Run("missing-bilingual-readme", func(t *testing.T) {
		fixture := newWindowsReleaseFixture(t)
		if err := os.Remove(filepath.Join(fixture.root, "README.zh.md")); err != nil {
			t.Fatal(err)
		}
		output, err := fixture.run("v1.0.0")
		if err == nil || !strings.Contains(output, "required release file missing") {
			t.Fatalf("missing Chinese README was not rejected: err=%v\n%s", err, output)
		}
	})
}

func newWindowsReleaseFixture(t *testing.T) windowsReleaseFixture {
	t.Helper()
	root := t.TempDir()
	input := filepath.Join(root, "dist")
	native := filepath.Join(input, "native")
	output := filepath.Join(input, "release")
	for _, directory := range []string{input, native, filepath.Join(root, "third_party", "zlib", "src-1.3.1")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	dll := []byte("MZ fixture miniapp-frida DLL")
	manifestValue := map[string]any{
		"schema": "miniapp-bridge.native-manifest.v1", "nativeVersion": "17.3.2-abi1",
		"fridaCoreVersion": "17.3.2", "zlibVersion": "1.3.1", "abiVersion": 1,
		"os": "windows", "arch": "amd64", "dll": "miniapp-frida.dll",
		"size": len(dll), "sha256": bytesSHA256(dll), "requiredExports": []string{"mb_abi_version"},
	}
	manifest, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	nativeZIP := []byte("fixture native zip")
	nativeName := "miniapp-frida-native-17.3.2-abi1-windows-amd64.zip"
	files := map[string][]byte{
		filepath.Join(input, "miniapp-bridge.exe"):                         []byte("MZ fixture executable"),
		filepath.Join(input, "miniapp-frida.dll"):                          dll,
		filepath.Join(input, "manifest.json"):                              manifest,
		filepath.Join(native, nativeName):                                  nativeZIP,
		filepath.Join(native, "SHA256SUMS"):                                []byte(bytesSHA256(nativeZIP) + "  " + nativeName + "\n"),
		filepath.Join(root, "README.md"):                                   []byte("English readme\n"),
		filepath.Join(root, "README.zh.md"):                                []byte("Chinese readme\n"),
		filepath.Join(root, "LICENSE"):                                     []byte("license\n"),
		filepath.Join(root, "THIRD_PARTY_NOTICES.md"):                      []byte("notices\n"),
		filepath.Join(root, "third_party", "zlib", "src-1.3.1", "LICENSE"): []byte("zlib license\n"),
	}
	for path, data := range files {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return windowsReleaseFixture{root: root, input: input, native: native, output: output, dll: dll, manifest: manifest, nativeZIP: nativeZIP}
}

func (f windowsReleaseFixture) run(version string, failPoint ...string) (string, error) {
	_, source, _, _ := runtime.Caller(0)
	shell := "pwsh.exe"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "powershell.exe"
	}
	command := exec.Command(shell,
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", filepath.Join(filepath.Dir(source), "package-windows-release.ps1"),
		"-Version", version,
		"-RepositoryRoot", f.root,
		"-InputDirectory", f.input,
		"-NativeDirectory", f.native,
		"-OutputDirectory", f.output,
	)
	if len(failPoint) > 0 {
		command.Args = append(command.Args, "-TestFailPoint", failPoint[0])
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

func assertNoWindowsReleaseTemps(t *testing.T, output string) {
	t.Helper()
	parent := filepath.Dir(output)
	leaf := filepath.Base(output)
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "."+leaf+".staging-") ||
			strings.HasPrefix(entry.Name(), "."+leaf+".backup-") ||
			strings.HasPrefix(entry.Name(), "."+leaf+".discard-") {
			t.Fatalf("temporary release artifact remains: %s", entry.Name())
		}
	}
}

func snapshotReleaseBundle(t *testing.T, root string) map[string][]byte {
	t.Helper()
	snapshot := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(relative)] = content
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertReleaseBundleEntries(t *testing.T, root string, want []string) {
	t.Helper()
	snapshot := snapshotReleaseBundle(t, root)
	got := make([]string, 0, len(snapshot))
	for name := range snapshot {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("release bundle entries mismatch\ngot:  %q\nwant: %q", got, want)
	}
}
