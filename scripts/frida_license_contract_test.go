package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFridaLicenseDistributionContract(t *testing.T) {
	root := filepath.Clean("..")
	licenses := map[string]string{
		"COPYING":     "5EA1544B51A28BC823B03159190D4108F9FB4F4EF912389F5137C6D295E175B2",
		"COPYING.LIB": "CC535C21133C895B56B374C8A1DC1EB948D99003ED2B47372069456B62F42B24",
	}
	for name, want := range licenses {
		data, err := os.ReadFile(filepath.Join(root, "licenses", "frida-17.3.2", name))
		if err != nil {
			t.Fatal(err)
		}
		got := strings.ToUpper(fmt.Sprintf("%x", sha256.Sum256(data)))
		if got != want {
			t.Fatalf("Frida %s SHA-256=%s, want %s", name, got, want)
		}
	}

	for _, path := range []string{
		"scripts/native-release.ps1",
		"scripts/package-windows-release.ps1",
		"README.md",
		"README.zh.md",
		"docs/native-release.md",
		"THIRD_PARTY_NOTICES.md",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, token := range []string{"FRIDA_COPYING", "FRIDA_COPYING.LIB"} {
			if !strings.Contains(text, token) {
				t.Errorf("%s does not name packaged license %s", path, token)
			}
		}
	}
}

func TestReleaseTextInputsHaveStableLineEndings(t *testing.T) {
	root := filepath.Clean("..")
	paths := []string{
		"LICENSE",
		"README.md",
		"README.zh.md",
		"THIRD_PARTY_NOTICES.md",
		"licenses/frida-17.3.2/COPYING",
		"licenses/frida-17.3.2/COPYING.LIB",
	}
	attributes, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	attributeLines := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(attributes)), "\n") {
		attributeLines[strings.TrimSuffix(line, "\r")] = struct{}{}
	}
	for _, path := range paths {
		attribute := "/" + filepath.ToSlash(path) + " -text"
		if _, ok := attributeLines[attribute]; !ok {
			t.Errorf(".gitattributes lacks stable release input rule %q", attribute)
		}
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
			t.Errorf("release text input %s has a UTF-8 BOM", path)
		}
		if bytes.Contains(data, []byte{'\r'}) {
			t.Errorf("release text input %s is not LF-only", path)
		}
	}
}
