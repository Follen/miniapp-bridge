package wmpf

import (
	"bytes"
	"errors"
	"testing"
)

func TestUnwrapDebugMessageRejectsDeclaredSizeBeforeDecompression(t *testing.T) {
	message := DebugMessage{
		Category:     CategoryChromeDevtools,
		Data:         []byte("not a zlib stream"),
		CompressAlgo: CompressZlib,
		OriginalSize: 65,
	}

	unwrapped, err := UnwrapDebugMessageWithLimit(message, 64)
	if !errors.Is(err, ErrDecompressedDebugMessageTooLarge) {
		t.Fatalf("declared oversize error = %v", err)
	}
	if unwrapped.Category != message.Category || !bytes.Equal(unwrapped.Raw, message.Data) {
		t.Fatalf("rejected message context = %+v", unwrapped)
	}
}

func TestUnwrapDebugMessageRejectsUndeclaredActualSize(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 65)
	compressed, _, err := WrapData(payload, CategoryChromeDevtools, CompressZlib)
	if err != nil {
		t.Fatal(err)
	}

	_, err = UnwrapDebugMessageWithLimit(DebugMessage{
		Category:     CategoryChromeDevtools,
		Data:         compressed,
		CompressAlgo: CompressZlib,
	}, 64)
	if !errors.Is(err, ErrDecompressedDebugMessageTooLarge) {
		t.Fatalf("actual oversize error = %v", err)
	}
}

func TestUnwrapDebugMessageRejectsDeclaredSizeMismatch(t *testing.T) {
	payload := []byte("declared-size")
	compressed, _, err := WrapData(payload, CategoryChromeDevtools, CompressZlib)
	if err != nil {
		t.Fatal(err)
	}

	_, err = UnwrapDebugMessageWithLimit(DebugMessage{
		Category:     CategoryChromeDevtools,
		Data:         compressed,
		CompressAlgo: CompressZlib,
		OriginalSize: uint32(len(payload) + 1),
	}, 64)
	if !errors.Is(err, ErrDecompressedDebugMessageSizeMismatch) {
		t.Fatalf("size mismatch error = %v", err)
	}
}

func TestZlibDecompressBoundedRejectsInvalidLimit(t *testing.T) {
	if _, err := zlibDecompressBounded(nil, 0, 0); !errors.Is(err, ErrDecompressedDebugMessageTooLarge) {
		t.Fatalf("invalid limit error = %v", err)
	}
}

func TestZlibDecompressBoundedAcceptsExactLimit(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 64)
	compressed, _, err := WrapData(payload, CategoryChromeDevtools, CompressZlib)
	if err != nil {
		t.Fatal(err)
	}
	output, err := zlibDecompressBounded(compressed, uint32(len(payload)), len(payload))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, payload) {
		t.Fatalf("decompressed payload = %q", output)
	}
}

func TestUnwrapDebugMessageBoundsUncompressedOutput(t *testing.T) {
	message := DebugMessage{Category: CategoryChromeDevtools, Data: []byte("12345")}
	for name, limit := range map[string]int{"invalid": 0, "oversize": 4} {
		t.Run(name, func(t *testing.T) {
			unwrapped, err := UnwrapDebugMessageWithLimit(message, limit)
			if !errors.Is(err, ErrDecompressedDebugMessageTooLarge) {
				t.Fatalf("error=%v", err)
			}
			if !bytes.Equal(unwrapped.Raw, message.Data) {
				t.Fatalf("raw=%q", unwrapped.Raw)
			}
		})
	}
	unwrapped, err := UnwrapDebugMessageWithLimit(message, len(message.Data))
	if err != nil || !bytes.Equal(unwrapped.Data.([]byte), message.Data) {
		t.Fatalf("exact output=%+v err=%v", unwrapped, err)
	}
}
