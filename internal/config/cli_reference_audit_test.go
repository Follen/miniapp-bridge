package config

import (
	"errors"
	"strings"
	"testing"
)

func TestAuditCLIReferenceDefaultsAndFlags(t *testing.T) {
	t.Parallel()
	o, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.DebugPort != 9421 || o.CDPPort != 62000 || o.DebugMain || o.DebugFrida {
		t.Fatalf("defaults=%+v", o)
	}
	o, err = Parse([]string{"--debug-main", "--debug-frida"})
	if err != nil || !o.DebugMain || !o.DebugFrida {
		t.Fatalf("flags=%+v err=%v", o, err)
	}
}

func TestAuditCLIReferenceNumberLexemes(t *testing.T) {
	t.Parallel()
	valid := map[string]int{
		" 42 ": 42, "+42": 42, "042": 42, "4.2e1": 42,
		"0x2a": 42, "0X2A": 42, "0b101010": 42, "0o52": 42,
		"1.0": 1, "65535.0": 65535,
	}
	for input, want := range valid {
		o, err := Parse([]string{"--debug-port", input})
		if err != nil || o.DebugPort != want {
			t.Errorf("port %q=%d err=%v, want %d", input, o.DebugPort, err, want)
		}
	}
	for _, input := range []string{"", "0", "-1", "1.5", "65536", "NaN", "Infinity", "1_000", "0x"} {
		if _, err := Parse([]string{"--cdp-port", input}); err == nil {
			t.Errorf("accepted invalid port %q", input)
		}
	}
}

func TestAuditCLIReferenceEqualsAndRepeatedOptions(t *testing.T) {
	t.Parallel()
	o, err := Parse([]string{"--debug-port=1000", "--debug-port", "2000", "--cdp-port=3000"})
	if err != nil {
		t.Fatal(err)
	}
	if o.DebugPort != 2000 || o.CDPPort != 3000 {
		t.Fatalf("options=%+v", o)
	}
}

func TestAuditCLIReferenceHelpStillValidatesArgumentStructure(t *testing.T) {
	t.Parallel()
	_, err := Parse([]string{"--help", "--unknown"})
	if err == nil || errors.Is(err, ErrHelp) || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("help plus unknown err=%v, want unknown-option error", err)
	}
	_, err = Parse([]string{"--help", "positional"})
	if err == nil || errors.Is(err, ErrHelp) {
		t.Fatalf("help plus positional err=%v, want positional error", err)
	}
	if _, err = Parse([]string{"--help", "--debug-port", "not-a-number"}); !errors.Is(err, ErrHelp) {
		t.Fatalf("help plus semantically invalid port err=%v, want help", err)
	}
}
