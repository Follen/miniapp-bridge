package capture

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMetadataSidecarPreservesLegacyCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frames.capture")
	recorder, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, time.August, 9, 1, 2, 3, 4, time.FixedZone("test", 8*60*60))
	if err := recorder.WriteFrame(DirectionUpstream, stamp, []byte("up")); err != nil {
		t.Fatal(err)
	}
	if err := recorder.WriteFrame("", time.Time{}, []byte("default")); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Write([]byte("legacy")); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	frames, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"up", "default", "legacy"}
	if len(frames) != len(want) {
		t.Fatalf("frames=%d want %d", len(frames), len(want))
	}
	for i := range want {
		if string(frames[i]) != want[i] {
			t.Fatalf("frame[%d]=%q want %q", i, frames[i], want[i])
		}
	}
	metadata, err := ReplayMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != len(want) {
		t.Fatalf("metadata=%d want %d", len(metadata), len(want))
	}
	if metadata[0].Index != 0 || metadata[0].Direction != DirectionUpstream || !metadata[0].Timestamp.Equal(stamp.UTC()) || metadata[0].Size != 2 {
		t.Fatalf("upstream metadata=%+v", metadata[0])
	}
	for i := 1; i < len(metadata); i++ {
		if metadata[i].Index != uint64(i) || metadata[i].Direction != DirectionUnknown || metadata[i].Timestamp.IsZero() || metadata[i].Size != uint32(len(want[i])) {
			t.Fatalf("metadata[%d]=%+v", i, metadata[i])
		}
	}
}

func TestMetadataSidecarFailuresAreDiagnostic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.bin")
	if err := os.Mkdir(MetadataPath(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(path); err == nil {
		t.Fatal("metadata create failure was not returned")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed Start left primary capture: %v", err)
	}

	if _, err := ReplayMetadata(filepath.Join(t.TempDir(), "missing.capture")); err == nil {
		t.Fatal("missing metadata sidecar replayed")
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.capture")
	if err := os.WriteFile(MetadataPath(corrupt), []byte("{bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if metadata, err := ReplayMetadata(corrupt); err == nil || metadata != nil {
		t.Fatalf("corrupt metadata=%+v err=%v", metadata, err)
	}

	encodeFail := &failWriter{failAt: 1}
	r := &Recorder{w: bufio.NewWriter(io.Discard), metadataWriter: bufio.NewWriterSize(encodeFail, 1)}
	if err := r.WriteFrame(DirectionDownstream, time.Now(), []byte("x")); err == nil {
		t.Fatal("metadata encode failure was not returned")
	}
	flushFail := &failWriter{failAt: 1}
	r = &Recorder{w: bufio.NewWriter(io.Discard), metadataWriter: bufio.NewWriter(flushFail)}
	if err := r.WriteFrame(DirectionDownstream, time.Now(), []byte("x")); err == nil {
		t.Fatal("metadata flush failure was not returned")
	}

	closeFlushFail := &failWriter{failAt: 1}
	r = &Recorder{metadataWriter: bufio.NewWriter(closeFlushFail)}
	_, _ = r.metadataWriter.WriteString("pending")
	if err := r.Close(); err == nil {
		t.Fatal("metadata close flush failure was not returned")
	}
	closedFile, err := os.Open(MetadataPath(corrupt))
	if err != nil {
		t.Fatal(err)
	}
	if err := closedFile.Close(); err != nil {
		t.Fatal(err)
	}
	r = &Recorder{metadataFile: closedFile}
	if err := r.Close(); err == nil {
		t.Fatal("metadata file close failure was not returned")
	}
}

func TestRecorderWriteFrameConcurrentClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.capture")
	r, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := 0; i < 64; i++ {
				err := r.WriteFrame(DirectionUpstream, time.Now(), []byte("frame"))
				if err != nil && !errors.Is(err, ErrRecorderClosed) {
					t.Errorf("write: %v", err)
				}
			}
		}()
	}
	group.Add(1)
	go func() {
		defer group.Done()
		if err := r.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	group.Wait()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	frames, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := ReplayMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != len(metadata) {
		t.Fatalf("frames=%d metadata=%d", len(frames), len(metadata))
	}
	for i := range metadata {
		if metadata[i].Index != uint64(i) || metadata[i].Direction != DirectionUpstream || metadata[i].Size != uint32(len(frames[i])) {
			t.Fatalf("metadata[%d]=%+v", i, metadata[i])
		}
	}
}
