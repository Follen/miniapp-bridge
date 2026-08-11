package logging

import (
	"strings"
	"testing"
)

func TestPayloadSummaryIsBoundedAndDeterministic(t *testing.T) {
	summary := PayloadSummary([]byte("payload"))
	if !strings.HasPrefix(summary, "bytes=7 sha256=") || len(summary) != len("bytes=7 sha256=")+16 {
		t.Fatalf("summary=%q", summary)
	}
	if summary != PayloadSummary([]byte("payload")) {
		t.Fatal("payload summary is not deterministic")
	}
}

func TestSanitizeErrorTextRedactsAbsoluteWindowsPaths(t *testing.T) {
	if got := SanitizeErrorText(""); got != "" {
		t.Fatalf("empty text=%q", got)
	}
	input := "open C:\\\\Users\\\\fixture\\\\AppData\\\\Local\\\\Temp\\\\miniapp.dll failed"
	got := SanitizeErrorText(input)
	if strings.Contains(got, "C:\\\\Users") || !strings.Contains(got, "<path-redacted>") {
		t.Fatalf("sanitized=%q", got)
	}
	if got := SanitizeErrorText("operation failed"); got != "operation failed" {
		t.Fatalf("unchanged=%q", got)
	}
}
