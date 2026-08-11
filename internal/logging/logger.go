package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
)

type Logger struct {
	MainDebug  bool
	FridaDebug bool
	std        *log.Logger
	err        *log.Logger
}

func New(mainDebug, fridaDebug bool) *Logger {
	return NewWithWriters(mainDebug, fridaDebug, os.Stdout, os.Stderr)
}
func NewWithWriters(mainDebug, fridaDebug bool, stdout, stderr io.Writer) *Logger {
	return &Logger{MainDebug: mainDebug, FridaDebug: fridaDebug, std: log.New(stdout, "", 0), err: log.New(stderr, "", 0)}
}
func (l *Logger) Info(v ...any)  { l.std.Println(v...) }
func (l *Logger) Error(v ...any) { l.err.Println(v...) }
func (l *Logger) Main(v ...any) {
	if l.MainDebug {
		l.std.Println(v...)
	}
}
func (l *Logger) Frida(v ...any) {
	if l.FridaDebug {
		l.std.Println(v...)
	}
}

var windowsPathRE = regexp.MustCompile("(?i)(?:[a-z]:\\\\\\\\|\\\\\\\\\\\\\\\\)[^ \\\\t\\\\r\\\\n\\\"']+")

// PayloadSummary provides bounded diagnostics for protocol bytes without
// emitting the payload itself.
func PayloadSummary(payload []byte) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("bytes=%d sha256=%s", len(payload), hex.EncodeToString(sum[:8]))
}

// SanitizeErrorText removes likely absolute Windows paths from diagnostics.
// It deliberately keeps operation names and error codes useful for triage.
func SanitizeErrorText(text string) string {
	if text == "" {
		return text
	}
	return windowsPathRE.ReplaceAllString(text, "<path-redacted>")
}
