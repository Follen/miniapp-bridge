package capture

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

const captureFuzzInputLimit = 64 << 10

func FuzzCaptureDecode(f *testing.F) {
	f.Add(encodedFrames([]byte("one"), nil, []byte("three")), uint16(8), uint8(3))
	f.Add([]byte{0, 0, 0, 4, 1}, uint16(0), uint8(0))
	f.Add([]byte{0xff, 0xff, 0xff, 0xff}, uint16(math.MaxUint16), uint8(math.MaxUint8))

	f.Fuzz(func(t *testing.T, data []byte, byteLimit uint16, frameLimit uint8) {
		if len(data) > captureFuzzInputLimit {
			t.Skip()
		}
		var snapshot bytes.Buffer
		frames, total, err := validateReader(
			context.Background(), bytes.NewReader(data), uint64(byteLimit), uint64(frameLimit), &snapshot,
		)
		if err != nil {
			return
		}
		if byteLimit != 0 && total > uint64(byteLimit) {
			t.Fatalf("validated bytes=%d exceed limit=%d", total, byteLimit)
		}
		if frameLimit != 0 && frames > uint64(frameLimit) {
			t.Fatalf("validated frames=%d exceed limit=%d", frames, frameLimit)
		}
		if !bytes.Equal(snapshot.Bytes(), data) {
			t.Fatalf("validated snapshot changed input: got=%d want=%d", snapshot.Len(), len(data))
		}

		replayed, err := replayFramesContext(context.Background(), bytes.NewReader(data))
		if err != nil {
			t.Fatalf("validated capture failed replay: %v", err)
		}
		var replayBytes uint64
		for _, frame := range replayed {
			replayBytes += uint64(len(frame))
		}
		if uint64(len(replayed)) != frames || replayBytes != total {
			t.Fatalf("replay frames/bytes=%d/%d want=%d/%d", len(replayed), replayBytes, frames, total)
		}
	})
}

func FuzzCaptureBoundaryArithmetic(f *testing.F) {
	f.Add(uint32(0), uint16(0), uint8(0), []byte(nil))
	f.Add(uint32(4), uint16(4), uint8(1), []byte("data"))
	f.Add(uint32(MaxFrameSize), uint16(math.MaxUint16), uint8(1), []byte("short"))
	f.Add(uint32(MaxFrameSize+1), uint16(math.MaxUint16), uint8(1), []byte(nil))
	f.Add(uint32(math.MaxUint32), uint16(math.MaxUint16), uint8(math.MaxUint8), []byte(nil))

	f.Fuzz(func(t *testing.T, declared uint32, byteLimit uint16, frameLimit uint8, payload []byte) {
		if len(payload) > captureFuzzInputLimit-4 {
			t.Skip()
		}
		wire := make([]byte, 4+len(payload))
		binary.BigEndian.PutUint32(wire, declared)
		copy(wire[4:], payload)

		frames, total, err := validateReader(
			context.Background(), bytes.NewReader(wire), uint64(byteLimit), uint64(frameLimit), nil,
		)
		if declared > MaxFrameSize {
			if !errors.Is(err, ErrFrameTooLarge) {
				t.Fatalf("declared=%d error=%v want ErrFrameTooLarge", declared, err)
			}
			return
		}
		if byteLimit != 0 && uint64(declared) > uint64(byteLimit) {
			if !errors.Is(err, ErrCaptureTooLarge) {
				t.Fatalf("declared=%d byteLimit=%d error=%v want ErrCaptureTooLarge", declared, byteLimit, err)
			}
			return
		}
		if err == nil {
			if frames == 0 || total < uint64(declared) {
				t.Fatalf("successful validation frames/bytes=%d/%d declared=%d", frames, total, declared)
			}
		}
	})
}
