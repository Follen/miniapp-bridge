package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const zlibArchiveSHA256 = "9a93b2b7dfdac77ceba5a558a580e74667dd6fede4585b91eefb60f03b72df23"

func TestZlibBuildRestoresIgnoredSourceFromPinnedArchive(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))

	script := readContractFile(t, filepath.Join(root, "scripts", "build-zlib.ps1"))
	for _, token := range []string{
		"third_party\\zlib\\zlib-1.3.1.tar.gz",
		"tar.exe -xzf $archive",
		"--strip-components 1",
		"zlib 1.3.1 extraction did not produce zlib.h",
	} {
		if !strings.Contains(script, token) {
			t.Errorf("build-zlib.ps1 is missing %q", token)
		}
	}

	ignore := readContractFile(t, filepath.Join(root, ".gitignore"))
	if !strings.Contains(ignore, "third_party/zlib/src-1.3.1/") {
		t.Fatal("zlib extraction cache is not ignored")
	}

	archive, err := os.ReadFile(filepath.Join(root, "third_party", "zlib", "zlib-1.3.1.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(archive)); got != zlibArchiveSHA256 {
		t.Fatalf("zlib archive SHA-256 = %s, want %s", got, zlibArchiveSHA256)
	}
}

func TestFridaShimBuildIsReproducible(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(source))
	script := readContractFile(t, filepath.Join(root, "scripts", "build-frida-shim.ps1"))
	if !strings.Contains(script, "/link /Brepro") {
		t.Fatal("Frida shim linker is missing /Brepro")
	}
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
