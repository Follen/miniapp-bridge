package version

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOffsetRemainingBranches(t *testing.T) {
	for _, input := range []string{"", "0x-not-hex", "not-decimal"} {
		if _, err := (AddressConfig{}).Offset(input); err == nil {
			t.Errorf("Offset(%q) unexpectedly succeeded", input)
		}
	}
	if got, err := (AddressConfig{}).Offset(" 42 "); err != nil || got != 42 {
		t.Fatalf("offset=%d err=%v", got, err)
	}
}

func TestLoadFileAndDirRemainingBranches(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	if _, err := LoadFile(missing); err == nil {
		t.Fatal("expected missing file error")
	}
	path := filepath.Join(dir, "addresses.1.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected decode error")
	}
	if err := os.WriteFile(path, []byte(`{"Version":0,"SceneOffsets":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected invalid config error")
	}
	if _, err := LoadDir(t.TempDir()); err == nil {
		t.Fatal("expected empty directory error")
	}
	if _, err := LoadDir("["); err == nil {
		t.Fatal("expected malformed glob error")
	}
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("expected invalid member error")
	}
	if err := os.WriteFile(path, []byte(`{"Version":1,"LoadStartHookOffset":"0x1","CDPFilterHookOffset":"0x2","SceneOffsets":[1,2,3]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configs, err := LoadDir(dir)
	if err != nil || configs[1].Version != 1 {
		t.Fatalf("configs=%+v err=%v", configs, err)
	}
}
