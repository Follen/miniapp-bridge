//go:build windows && frida

package wmpf

import (
	"errors"
	"fmt"
	"testing"
)

func TestCoverageWindowsZlibFailure(t *testing.T) {
	_, err := zlibDecompress(nil)
	requireError(t, err)
}

func TestWindowsZlibUsesCommonOutputValidation(t *testing.T) {
	original := nativeZlibDecompressWithLimit
	t.Cleanup(func() { nativeZlibDecompressWithLimit = original })

	var receivedExpected int
	nativeZlibDecompressWithLimit = func(_ []byte, expected, _ int) ([]byte, error) {
		receivedExpected = expected
		return make([]byte, 65), nil
	}
	if _, err := zlibDecompressBounded([]byte("compressed"), 64, 64); !errors.Is(err, ErrDecompressedDebugMessageTooLarge) {
		t.Fatalf("native actual oversize error = %v", err)
	}
	if receivedExpected != 64 {
		t.Fatalf("native expected size = %d, want 64", receivedExpected)
	}
}

func TestWindowsZlibUsesCommonDeclaredSizeValidation(t *testing.T) {
	original := nativeZlibDecompressWithLimit
	t.Cleanup(func() { nativeZlibDecompressWithLimit = original })

	var receivedExpected, receivedLimit int
	nativeZlibDecompressWithLimit = func(_ []byte, expected, limit int) ([]byte, error) {
		receivedExpected = expected
		receivedLimit = limit
		return make([]byte, expected-1), nil
	}
	if _, err := zlibDecompressBounded([]byte("compressed"), 64, 64); !errors.Is(err, ErrDecompressedDebugMessageSizeMismatch) {
		t.Fatalf("native declared-size mismatch error = %v", err)
	}
	if receivedExpected != 64 || receivedLimit != 64 {
		t.Fatalf("native limits = expected %d limit %d, want 64/64", receivedExpected, receivedLimit)
	}
}

func TestWindowsZlibNormalizesSizeMismatch(t *testing.T) {
	original := nativeZlibDecompressWithLimit
	t.Cleanup(func() { nativeZlibDecompressWithLimit = original })

	for name, nativeErr := range map[string]error{
		"smaller": fmt.Errorf("frida: zlib decompress: zlib decompressed size mismatch: expected=64 actual=63"),
		"larger":  fmt.Errorf("frida: zlib decompress: zlib decompress failed: -5"),
	} {
		t.Run(name, func(t *testing.T) {
			nativeZlibDecompressWithLimit = func([]byte, int, int) ([]byte, error) { return nil, nativeErr }
			if _, err := zlibDecompressBounded([]byte("compressed"), 64, 64); !errors.Is(err, ErrDecompressedDebugMessageSizeMismatch) {
				t.Fatalf("native mismatch error = %v", err)
			}
		})
	}
}

func TestWindowsZlibNormalizesNativeOutputLimitError(t *testing.T) {
	original := nativeZlibDecompressWithLimit
	t.Cleanup(func() { nativeZlibDecompressWithLimit = original })

	nativeZlibDecompressWithLimit = func(_ []byte, expected, limit int) ([]byte, error) {
		if expected != 0 || limit != 64 {
			t.Fatalf("native arguments = expected %d limit %d, want 0/64", expected, limit)
		}
		return nil, fmt.Errorf("frida: zlib decompress: output exceeds configured limit")
	}
	if _, err := zlibDecompressBounded([]byte("compressed"), 0, 64); !errors.Is(err, ErrDecompressedDebugMessageTooLarge) {
		t.Fatalf("native output-limit error = %v", err)
	}
}
