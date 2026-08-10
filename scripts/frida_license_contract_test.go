package main

import (
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
