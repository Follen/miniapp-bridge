package capture

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSegmentedRecorderRotationRetentionAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.segment")
	recorder, err := StartSegmented(path, SegmentOptions{SegmentMaxBytes: 34, RetainSegments: 2, QueueDepth: 8})
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range [][]byte{[]byte("one"), []byte("two"), []byte("three")} {
		for {
			if err := recorder.Write(frame); !errors.Is(err, ErrCaptureBackpressure) {
				if err != nil {
					t.Fatal(err)
				}
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	segments, _ := filepath.Glob(path + ".*")
	if len(segments) != 2 {
		t.Fatalf("segments=%v", segments)
	}
	var got []string
	if err := ReplaySegments(path, func(frame []byte) error { got = append(got, string(frame)); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "two" || got[1] != "three" {
		t.Fatalf("frames=%v", got)
	}
}

func TestSegmentedRecorderContinuesGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture")
	for _, value := range []string{"first", "second"} {
		r, err := StartSegmented(path, SegmentOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, segment := range []string{path + ".000001", path + ".000002"} {
		if _, err := os.Stat(segment); err != nil {
			t.Fatalf("segment %s: %v", segment, err)
		}
	}
}

func TestSegmentedReplayRejectsCRCAndCommitDamage(t *testing.T) {
	for _, offset := range []int{4, segmentHeaderSize + 1} {
		t.Run(string(rune('a'+offset)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "capture")
			r, err := StartSegmented(path, SegmentOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			segment := path + ".000001"
			data, err := os.ReadFile(segment)
			if err != nil {
				t.Fatal(err)
			}
			data[offset] ^= 0xff
			if err := os.WriteFile(segment, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := ReplaySegments(path, func([]byte) error { return nil }); !errors.Is(err, ErrCorruptRecord) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSegmentedRecorderDiskBackpressureAndDrainBudget(t *testing.T) {
	originalFree, originalWrite := diskFreeBytes, segmentWrite
	t.Cleanup(func() { diskFreeBytes, segmentWrite = originalFree, originalWrite })
	diskFreeBytes = func(string) (uint64, error) { return 1, nil }
	r, err := StartSegmented(filepath.Join(t.TempDir(), "disk"), SegmentOptions{MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); !errors.Is(err, ErrInsufficientDisk) {
		t.Fatalf("error=%v", err)
	}

	diskFreeBytes = func(string) (uint64, error) { return ^uint64(0), nil }
	blocked := make(chan struct{})
	started := make(chan struct{})
	segmentWrite = func(w io.Writer, data []byte) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-blocked
		return writeAll(w, data)
	}
	r, err = StartSegmented(filepath.Join(t.TempDir(), "drain"), SegmentOptions{QueueDepth: 1, CloseDrain: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := r.Write([]byte("queued")); err != nil {
		t.Fatal(err)
	}
	if err := r.Write([]byte("overflow")); !errors.Is(err, ErrCaptureBackpressure) {
		t.Fatalf("backpressure error=%v", err)
	}
	if err := r.Close(); !errors.Is(err, ErrCloseDrainTimeout) {
		t.Fatalf("error=%v", err)
	}
	close(blocked)
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("writer did not drain after release")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}
