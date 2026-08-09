package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestBilingualReadmeReleaseContract(t *testing.T) {
	root := readmeRepositoryRoot(t)
	english := readReadme(t, filepath.Join(root, "README.md"))
	chinese := readReadme(t, filepath.Join(root, "README.zh.md"))

	for name, document := range map[string]string{"README.md": english, "README.zh.md": chinese} {
		for _, marker := range []string{
			"[English](README.md)",
			"[CI](https://github.com/Follen/miniapp-bridge/actions/workflows/ci.yml/badge.svg)",
			"[Release](https://github.com/Follen/miniapp-bridge/actions/workflows/release.yml/badge.svg)",
			"github.com/Follen/miniapp-bridge/sdk",
			"github.com/Follen/miniapp-bridge/sdk@v0.0.1",
			"127.0.0.1:9421",
			"127.0.0.1:62000",
			"devtools://devtools/bundled/inspector.html?ws=127.0.0.1:62000",
			"miniapp-bridge-v0.0.1-windows-amd64.zip",
			"miniapp-frida-native-17.3.2-abi1-windows-amd64.zip",
			"native-v17.3.2-abi1",
			"1597ADCC6B3B13B5BCBA910904046AB7D2E1E3D73AE16961C73E400373BDE87A",
			"2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d",
			"GPL-2.0-only",
			"47",
			"100.0%",
			"queue: max",
		} {
			if !strings.Contains(document, marker) {
				t.Errorf("%s is missing %q", name, marker)
			}
		}
		if count := strings.Count(document, "```"); count == 0 || count%2 != 0 {
			t.Errorf("%s has %d code fences, want a positive even count", name, count)
		}
	}

	heading := regexp.MustCompile(`(?m)^## `)
	if got, want := len(heading.FindAllString(english, -1)), len(heading.FindAllString(chinese, -1)); got != want || got != 9 {
		t.Fatalf("bilingual section count: English=%d Chinese=%d want=9 each", got, want)
	}
	if !strings.Contains(english, "[Simplified Chinese](README.zh.md)") && !strings.Contains(english, "[\u7b80\u4f53\u4e2d\u6587](README.zh.md)") {
		t.Fatal("README.md does not link README.zh.md")
	}
	if !strings.Contains(chinese, "[\u7b80\u4f53\u4e2d\u6587](README.zh.md)") {
		t.Fatal("README.zh.md does not identify its Chinese entry")
	}

	checkReadmeLocalLinks(t, root, "README.md", english)
	checkReadmeLocalLinks(t, root, "README.zh.md", chinese)
}

func TestBilingualReadmeNativeBuildAndArchiveContract(t *testing.T) {
	root := readmeRepositoryRoot(t)
	for name, document := range map[string]string{
		"README.md":    readReadme(t, filepath.Join(root, "README.md")),
		"README.zh.md": readReadme(t, filepath.Join(root, "README.zh.md")),
	} {
		for _, prerequisite := range []string{
			"MinGW-w64",
			"gcc.exe",
			"ar.exe",
			"Visual Studio 2022 C++",
			"Build Tools",
			"MSVC",
		} {
			if !strings.Contains(document, prerequisite) {
				t.Errorf("%s is missing native build prerequisite %q", name, prerequisite)
			}
		}

		for _, archiveEntry := range []string{
			"miniapp-frida.dll",
			"manifest.json",
			"LICENSE",
			"ZLIB_LICENSE",
			"THIRD_PARTY_NOTICES.md",
			"SHA256SUMS",
		} {
			if !strings.Contains(document, archiveEntry) {
				t.Errorf("%s is missing native archive entry %q", name, archiveEntry)
			}
		}
	}
}

func TestNativeReleaseOfflineDocumentationContract(t *testing.T) {
	root := readmeRepositoryRoot(t)
	document := readReadme(t, filepath.Join(root, "docs", "native-release.md"))
	for _, marker := range []string{"-Offline", "NativePrepareOptions{Offline: true}"} {
		if !strings.Contains(document, marker) {
			t.Errorf("docs/native-release.md is missing offline-mode marker %q", marker)
		}
	}
	for _, archiveEntry := range []string{
		"miniapp-frida.dll",
		"manifest.json",
		"LICENSE",
		"ZLIB_LICENSE",
		"THIRD_PARTY_NOTICES.md",
		"SHA256SUMS",
	} {
		if !strings.Contains(document, archiveEntry) {
			t.Errorf("docs/native-release.md is missing native archive entry %q", archiveEntry)
		}
	}
}

func TestVerificationDocumentationNativeCommandOrder(t *testing.T) {
	root := readmeRepositoryRoot(t)
	document := readReadme(t, filepath.Join(root, "docs", "verification.md"))
	build := strings.Index(document, "scripts/build-windows.ps1")
	resolve := strings.Index(document, "Resolve-Path '.\\dist\\miniapp-frida.dll'")
	if build < 0 || resolve < 0 || build > resolve {
		t.Fatal("docs/verification.md must build the native DLL before resolving it")
	}

	prewarm := strings.Index(document, "Copy-Item")
	offline := strings.Index(document, "scripts/native-prepare.ps1 `\n    -Offline")
	if prewarm < 0 || offline < 0 || prewarm > offline {
		t.Fatal("docs/verification.md must prewarm the isolated native cache before offline preparation")
	}
	for _, marker := range []string{"-CacheDirectory $nativeCache", "-DestinationDirectory $nativeDestination"} {
		if !strings.Contains(document, marker) {
			t.Errorf("docs/verification.md is missing isolated native preparation marker %q", marker)
		}
	}
}

func checkReadmeLocalLinks(t *testing.T, root, name, document string) {
	t.Helper()
	linkPattern := regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	for _, match := range linkPattern.FindAllStringSubmatch(document, -1) {
		target := strings.TrimSpace(match[1])
		if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		if index := strings.IndexByte(target, '#'); index >= 0 {
			target = target[:index]
		}
		path := filepath.Join(root, filepath.FromSlash(target))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s local link %q is invalid: %v", name, match[1], err)
		}
	}
}

func readmeRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(source))
}

func readReadme(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}
