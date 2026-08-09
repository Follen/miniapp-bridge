package capture

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func encodedFrames(frames ...[]byte) []byte {
	var out bytes.Buffer
	for _, frame := range frames {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
		out.Write(header[:])
		out.Write(frame)
	}
	return out.Bytes()
}

func TestRecorderRejectsOversizeBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversize.capture")
	recorder, err := Start(path)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, MaxFrameSize+1)
	err = recorder.Write(frame)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversize write error=%v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, MetadataPath(path)} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != 0 {
			t.Fatalf("%s size=%d want 0", candidate, info.Size())
		}
	}
}

func TestValidateReaderAggregateLimitsWithoutRetainingFrames(t *testing.T) {
	data := encodedFrames([]byte("1234"), []byte("5678"), nil)
	if _, _, err := validateReader(context.Background(), bytes.NewReader(data), 7, 0, nil); !errors.Is(err, ErrCaptureTooLarge) {
		t.Fatalf("byte limit error=%v", err)
	}
	if _, _, err := validateReader(context.Background(), bytes.NewReader(data), 0, 2, nil); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("frame limit error=%v", err)
	}
	frames, size, err := validateReader(context.Background(), bytes.NewReader(data), 8, 3, nil)
	if err != nil || frames != 3 || size != 8 {
		t.Fatalf("validation frames=%d size=%d err=%v", frames, size, err)
	}
}

func TestReplayEachContextValidatesBeforeSubmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.capture")
	data := encodedFrames([]byte("valid"))
	data = append(data, 0, 0, 0, 4, 1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var submitted int
	err := ReplayEachContext(context.Background(), path, func([]byte) error {
		submitted++
		return nil
	})
	if err == nil {
		t.Fatal("corrupt capture replay succeeded")
	}
	if submitted != 0 {
		t.Fatalf("corrupt capture submitted %d frames", submitted)
	}
}

func TestReplayEachContextStreamsInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordered.capture")
	want := [][]byte{[]byte("one"), nil, []byte("three")}
	if err := os.WriteFile(path, encodedFrames(want...), 0o600); err != nil {
		t.Fatal(err)
	}
	var got [][]byte
	err := ReplayEachContext(context.Background(), path, func(frame []byte) error {
		got = append(got, append([]byte(nil), frame...))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frames=%q want %q", got, want)
	}
}

func TestReplayContextCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cancel.capture")
	if err := os.WriteFile(path, encodedFrames([]byte("one"), []byte("two")), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	frames, err := ReplayContext(ctx, path)
	if !errors.Is(err, context.Canceled) || frames != nil {
		t.Fatalf("pre-canceled replay frames=%q err=%v", frames, err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	var submitted int
	err = ReplayEachContext(ctx, path, func([]byte) error {
		submitted++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || submitted != 1 {
		t.Fatalf("canceled stream submitted=%d err=%v", submitted, err)
	}
}

func TestReplayEachContextPropagatesCallbackAndArgumentErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "callback.capture")
	if err := os.WriteFile(path, encodedFrames([]byte("one")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReplayEachContext(context.Background(), path, nil); err == nil {
		t.Fatal("nil callback succeeded")
	}
	want := errors.New("stop")
	if err := ReplayEachContext(context.Background(), path, func([]byte) error { return want }); !errors.Is(err, want) {
		t.Fatalf("callback error=%v", err)
	}
}
