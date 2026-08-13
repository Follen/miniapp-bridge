package wmpf

import (
	"errors"
	"testing"
)

func TestCoverageGateCompressedInputLimit(t *testing.T) {
	if _, err := zlibDecompressBounded(make([]byte, 65), 0, 64); !errors.Is(err, ErrDecompressedDebugMessageTooLarge) {
		t.Fatalf("compressed input limit error=%v", err)
	}
}
