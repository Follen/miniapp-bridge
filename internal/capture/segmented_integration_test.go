package capture

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSegmentedProductionReplayCompatibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "production.capture")
	recorder, err := StartSegmented(path, SegmentOptions{SegmentMaxBytes: 80, RetainSegments: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StartSegmented(path, SegmentOptions{}); !errors.Is(err, ErrCaptureInUse) {
		t.Fatalf("concurrent start=%v", err)
	}
	stamp := time.Date(2026, time.August, 12, 1, 2, 3, 4, time.FixedZone("fixture", 8*60*60))
	for _, item := range []struct {
		direction Direction
		timestamp time.Time
		frame     string
	}{
		{DirectionUpstream, stamp, "up"},
		{DirectionDownstream, stamp.Add(time.Second), "down"},
		{"", time.Time{}, "default"},
	} {
		for {
			err := recorder.WriteFrame(item.direction, item.timestamp, []byte(item.frame))
			if errors.Is(err, ErrCaptureBackpressure) {
				time.Sleep(time.Millisecond)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock remains after close: %v", err)
	}

	frames, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := ReplayMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || len(metadata) != 3 {
		t.Fatalf("frames=%d metadata=%d", len(frames), len(metadata))
	}
	for index, want := range []string{"up", "down", "default"} {
		if string(frames[index]) != want || metadata[index].Index != uint64(index) || metadata[index].Size != uint32(len(want)) {
			t.Fatalf("frame[%d]=%q metadata=%+v", index, frames[index], metadata[index])
		}
	}
	if metadata[0].Direction != DirectionUpstream || !metadata[0].Timestamp.Equal(stamp.UTC()) || metadata[1].Direction != DirectionDownstream || metadata[2].Direction != DirectionUnknown || metadata[2].Timestamp.IsZero() {
		t.Fatalf("metadata=%+v", metadata)
	}
	if err := Validate(path); err != nil {
		t.Fatal(err)
	}
	var each []string
	if err := ReplayEachContext(nil, path, func(frame []byte) error {
		each = append(each, string(frame))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(each) != len(frames) {
		t.Fatalf("streamed=%v", each)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReplayContext(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled replay=%v", err)
	}
	if err := ValidateContext(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled validate=%v", err)
	}
	if err := ReplayEachContext(cancelled, path, func([]byte) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stream=%v", err)
	}
}

func TestSegmentedFallbackAndSnapshotFailures(t *testing.T) {
	preserveSegmentHooks(t)
	path := filepath.Join(t.TempDir(), "capture")
	r, err := StartSegmented(path, SegmentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Write([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	if err := r.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	want := errors.New("injected")
	originalSnapshot := createSnapshotFile
	t.Cleanup(func() { createSnapshotFile = originalSnapshot })
	createSnapshotFile = func() (*os.File, error) { return nil, want }
	if _, err := ReplayContext(context.Background(), path); !errors.Is(err, want) {
		t.Fatalf("replay snapshot create=%v", err)
	}
	if err := ReplayEachContext(context.Background(), path, func([]byte) error { return nil }); !errors.Is(err, want) {
		t.Fatalf("stream snapshot create=%v", err)
	}
	createSnapshotFile = originalSnapshot
	if err := snapshotSegmentsContext(context.Background(), path, &errorWriter{err: want}); !errors.Is(err, want) {
		t.Fatalf("snapshot write=%v", err)
	}
	if err := replaySegmentsContext(context.Background(), path, func([]byte, FrameMetadata) error { return want }); !errors.Is(err, want) {
		t.Fatalf("segment submit=%v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	seen := 0
	if err := replaySegmentsContext(cancelled, path, func([]byte, FrameMetadata) error {
		seen++
		cancel()
		return nil
	}); !errors.Is(err, context.Canceled) || seen != 1 {
		t.Fatalf("mid-segment cancellation seen=%d err=%v", seen, err)
	}

	segmentGlob = func(string) ([]string, error) { return nil, want }
	if err := replaySegmentsContext(context.Background(), path, func([]byte, FrameMetadata) error { return nil }); !errors.Is(err, want) {
		t.Fatalf("direct replay glob=%v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := ReplayMetadata(missing); !errors.Is(err, want) {
		t.Fatalf("metadata glob=%v", err)
	}
	if _, err := ReplayContext(context.Background(), missing); !errors.Is(err, want) {
		t.Fatalf("replay glob=%v", err)
	}
	if err := ValidateContext(context.Background(), missing); !errors.Is(err, want) {
		t.Fatalf("validate glob=%v", err)
	}
	if err := ReplayEachContext(context.Background(), missing, func([]byte) error { return nil }); !errors.Is(err, want) {
		t.Fatalf("stream glob=%v", err)
	}
}

func TestSegmentedStartLeaseAndEncodingFailures(t *testing.T) {
	preserveSegmentHooks(t)
	want := errors.New("injected")
	segmentStat = func(string) (os.FileInfo, error) { return nil, want }
	if _, err := StartSegmented("capture", SegmentOptions{}); !errors.Is(err, want) {
		t.Fatalf("stat=%v", err)
	}
	segmentStat = os.Stat
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StartSegmented(filepath.Join(parent, "capture"), SegmentOptions{}); err == nil {
		t.Fatal("non-directory parent accepted")
	}
	segmentOpenLock = func(string) (*os.File, error) { return nil, os.ErrExist }
	if _, err := StartSegmented(filepath.Join(t.TempDir(), "capture"), SegmentOptions{}); !errors.Is(err, ErrCaptureInUse) {
		t.Fatalf("existing lock=%v", err)
	}
	segmentOpenLock = func(string) (*os.File, error) { return nil, want }
	if _, err := StartSegmented(filepath.Join(t.TempDir(), "capture"), SegmentOptions{}); !errors.Is(err, want) {
		t.Fatalf("lock=%v", err)
	}

	for direction, encoded := range map[Direction]byte{DirectionUnknown: 0, DirectionUpstream: 1, DirectionDownstream: 2, "other": 0} {
		if got := encodeDirection(direction); got != encoded {
			t.Fatalf("encode %q=%d", direction, got)
		}
	}
	for encoded, direction := range map[byte]Direction{0: DirectionUnknown, 1: DirectionUpstream, 2: DirectionDownstream} {
		got, err := decodeDirection(encoded)
		if err != nil || got != direction {
			t.Fatalf("decode %d=%q,%v", encoded, got, err)
		}
	}
	if _, err := decodeDirection(3); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("invalid direction=%v", err)
	}
	if err := segmentSnapshotBudgetError(MaxCaptureFrames+1, 0); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("frame budget=%v", err)
	}
	if err := segmentSnapshotBudgetError(0, MaxCaptureSize+1); !errors.Is(err, ErrCaptureTooLarge) {
		t.Fatalf("byte budget=%v", err)
	}
	if err := segmentSnapshotBudgetError(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := writeSegmentSnapshotRecord(io.Discard, MaxCaptureFrames+1, 0, nil); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("snapshot record budget=%v", err)
	}

	lock, err := os.CreateTemp(t.TempDir(), "closed-lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	r := &SegmentedRecorder{queue: make(chan segmentRequest), done: make(chan struct{}), lockFile: lock, lockPath: lock.Name()}
	close(r.queue)
	r.run()
	if !errors.Is(r.failure(), os.ErrInvalid) && r.failure() == nil {
		t.Fatal("closed lock failure was not retained")
	}
}

func TestSegmentedReplayRejectsUnknownDirection(t *testing.T) {
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
	data[24] = 3
	if err := os.WriteFile(segment, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReplaySegments(path, func([]byte) error { return nil }); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("direction corruption=%v", err)
	}
}

var _ io.Writer = (*errorWriter)(nil)
