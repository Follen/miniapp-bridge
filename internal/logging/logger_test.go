package logging

import (
	"bytes"
	"testing"
)

func TestReferenceConsoleStreamsAndDebugGates(t *testing.T) {
	var stdout, stderr bytes.Buffer
	logger := NewWithWriters(false, false, &stdout, &stderr)
	logger.Info("info", 1)
	logger.Error("error", 2)
	logger.Main("hidden-main")
	logger.Frida("hidden-frida")

	if got, want := stdout.String(), "info 1\n"; got != want {
		t.Fatalf("stdout=%q want %q", got, want)
	}
	if got, want := stderr.String(), "error 2\n"; got != want {
		t.Fatalf("stderr=%q want %q", got, want)
	}

	stdout.Reset()
	stderr.Reset()
	logger = NewWithWriters(true, true, &stdout, &stderr)
	logger.Main("main", 3)
	logger.Frida("frida", 4)
	if got, want := stdout.String(), "main 3\nfrida 4\n"; got != want {
		t.Fatalf("debug stdout=%q want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("debug output reached stderr: %q", stderr.String())
	}
}
