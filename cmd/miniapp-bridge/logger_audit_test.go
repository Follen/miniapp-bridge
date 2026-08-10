package main

import (
	"bytes"
	"testing"

	"github.com/Follen/miniapp-bridge/internal/logging"
)

func TestAuditLoggerReferenceStreamsAndDebugGates(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	logger := logging.NewWithWriters(false, false, &stdout, &stderr)
	logger.Info("info", 1)
	logger.Error("error", 2)
	logger.Main("hidden-main")
	logger.Frida("hidden-frida")
	if got := stdout.String(); got != "info 1\n" {
		t.Fatalf("stdout=%q, want console.log-compatible output", got)
	}
	if got := stderr.String(); got != "error 2\n" {
		t.Fatalf("stderr=%q, want console.error-compatible output", got)
	}

	stdout.Reset()
	stderr.Reset()
	logger = logging.NewWithWriters(true, true, &stdout, &stderr)
	logger.Main("main", 3)
	logger.Frida("frida", 4)
	if got := stdout.String(); got != "main 3\nfrida 4\n" {
		t.Fatalf("debug stdout=%q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("debug wrote stderr=%q", stderr.String())
	}
}
