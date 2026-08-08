package capture

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRecordReplayRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frames.bin")
	recorder, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{{0, 1, 2}, {}, []byte("cdp")}
	for _, frame := range want {
		if err := recorder.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("frames=%d want %d", len(got), len(want))
	}
	for i := range want {
		if string(got[i]) != string(want[i]) {
			t.Fatalf("frame %d=%x want %x", i, got[i], want[i])
		}
	}
}

func TestRecorderSerializesConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.bin")
	recorder, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(value byte) {
			defer group.Done()
			if err := recorder.Write([]byte{value}); err != nil {
				t.Errorf("write: %v", err)
			}
		}(byte(i))
	}
	group.Wait()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	frames, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 32 {
		t.Fatalf("frames=%d want 32", len(frames))
	}
	seen := make(map[byte]bool)
	for _, frame := range frames {
		if len(frame) != 1 {
			t.Fatalf("corrupt frame %x", frame)
		}
		seen[frame[0]] = true
	}
	if len(seen) != 32 {
		t.Fatalf("unique frames=%d want 32", len(seen))
	}
}

func TestReplayRejectsCorruptRecords(t *testing.T) {
	tests := map[string][]byte{
		"truncated header": {0, 0, 0},
		"truncated frame":  {0, 0, 0, 4, 1, 2},
		"oversized frame": func() []byte {
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], MaxFrameSize+1)
			return header[:]
		}(),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "corrupt.bin")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Replay(path); err == nil {
				t.Fatal("corrupt capture replayed without error")
			}
		})
	}
}
