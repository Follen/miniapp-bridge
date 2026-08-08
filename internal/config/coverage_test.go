package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRemainingStructuralBranches(t *testing.T) {
	for _, args := range [][]string{
		{"--help=yes"},
		{"--debug-main=false"},
		{"--debug-frida=false"},
		{"--record"},
		{"--replay", "--help"},
		{"--debug-port"},
		{"--cdp-port", "--help"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v) unexpectedly succeeded", args)
		}
	}
	o, err := Parse([]string{"--record=capture.bin", "--replay=replay.bin"})
	if err != nil || o.RecordPath != "capture.bin" || o.ReplayPath != "replay.bin" {
		t.Fatalf("options=%+v err=%v", o, err)
	}
}

func TestLoadAddressBranches(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadAddress(dir, 42); err == nil || !strings.Contains(err.Error(), "version config not found") {
		t.Fatalf("missing err=%v", err)
	}
	path := filepath.Join(dir, "addresses.42.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAddress(dir, 42); err == nil {
		t.Fatal("expected malformed JSON error")
	}
	if err := os.WriteFile(path, []byte(`{"Version":42,"SceneOffsets":[1,2,3]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadAddress(dir, 42)
	if err != nil || config.Version != 42 {
		t.Fatalf("config=%+v err=%v", config, err)
	}
}
