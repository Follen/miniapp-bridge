package capture

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditCaptureUsesBigEndianLengthPrefixedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.bin")
	recorder, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Write([]byte{0xaa, 0xbb, 0xcc}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Write(nil); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 3, 0xaa, 0xbb, 0xcc, 0, 0, 0, 0}
	if string(raw) != string(want) {
		t.Fatalf("capture=%x want %x", raw, want)
	}
}

func TestAuditReplayReturnsNoPartialFramesAfterLaterCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.bin")
	raw := make([]byte, 0, 10)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 1)
	raw = append(raw, header[:]...)
	raw = append(raw, 0x42)
	binary.BigEndian.PutUint32(header[:], 4)
	raw = append(raw, header[:]...)
	raw = append(raw, 0x99)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	frames, err := Replay(path)
	if err == nil {
		t.Fatalf("corrupt replay succeeded with frames=%x", frames)
	}
	if frames != nil {
		t.Fatalf("corrupt replay exposed partial frames=%x", frames)
	}
}

func TestAuditReplayOversizeErrorIsDiagnostic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.bin")
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxFrameSize+1)
	if err := os.WriteFile(path, header[:], 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Replay(path)
	if err == nil || !strings.Contains(err.Error(), "67108865") || !strings.Contains(err.Error(), "67108864") {
		t.Fatalf("oversize error=%v", err)
	}
}
