package config

import "testing"

func TestParseOptions(t *testing.T) {
	o, err := Parse([]string{"--debug-port", "1234", "--cdp-port", "2345", "--debug-main", "--record", "capture.bin", "--replay", "capture.bin"})
	if err != nil || o.DebugPort != 1234 || o.CDPPort != 2345 || !o.DebugMain || o.RecordPath != "capture.bin" || o.ReplayPath != "capture.bin" {
		t.Fatalf("options=%+v err=%v", o, err)
	}
	if _, err := Parse([]string{"--debug-port", "0"}); err == nil {
		t.Fatal("expected invalid port")
	}
	for _, value := range []string{"123junk", "1.5", "NaN", "Infinity", "65536", ""} {
		if _, err := Parse([]string{"--debug-port", value}); err == nil {
			t.Errorf("accepted invalid reference port %q", value)
		}
	}
	for value, want := range map[string]int{"1e3": 1000, "1.0": 1, "0x10": 16, "0b10": 2, "0o10": 8, "+42": 42} {
		o, err := Parse([]string{"--debug-port", value})
		if err != nil || o.DebugPort != want {
			t.Errorf("port %q=%d err=%v want %d", value, o.DebugPort, err, want)
		}
	}
	if _, err := Parse([]string{"--help"}); err != ErrHelp {
		t.Fatalf("help err=%v", err)
	}
}
