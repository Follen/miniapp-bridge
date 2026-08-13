package wmpf

import (
	"errors"
	"fmt"
)

// DefaultMaxDecompressedDebugMessageBytes bounds one inflated WMPF debug
// message. It is intentionally much smaller than the native shim's process
// safety ceiling while leaving room for large CDP payloads.
const DefaultMaxDecompressedDebugMessageBytes = 16 << 20

var (
	// ErrDecompressedDebugMessageTooLarge reports a declared or actual output
	// larger than the active WMPF decompression limit.
	ErrDecompressedDebugMessageTooLarge = errors.New("wmpf: decompressed debug message exceeds limit")
	// ErrDecompressedDebugMessageSizeMismatch reports a non-zero OriginalSize
	// that does not match the inflated payload.
	ErrDecompressedDebugMessageSizeMismatch = errors.New("wmpf: decompressed debug message size mismatch")
)

func zlibDecompress(data []byte) ([]byte, error) {
	return zlibDecompressBounded(data, 0, DefaultMaxDecompressedDebugMessageBytes)
}

func zlibDecompressBounded(data []byte, expectedSize uint32, maxOutput int) ([]byte, error) {
	if maxOutput <= 0 {
		return nil, fmt.Errorf("%w: limit=%d", ErrDecompressedDebugMessageTooLarge, maxOutput)
	}
	if len(data) > maxOutput {
		return nil, fmt.Errorf("%w: compressed=%d limit=%d", ErrDecompressedDebugMessageTooLarge, len(data), maxOutput)
	}
	if uint64(expectedSize) > uint64(maxOutput) {
		return nil, fmt.Errorf("%w: declared=%d limit=%d", ErrDecompressedDebugMessageTooLarge, expectedSize, maxOutput)
	}

	output, err := zlibDecompressPlatform(data, int(expectedSize), maxOutput)
	if err != nil {
		return nil, err
	}
	if len(output) > maxOutput {
		return nil, fmt.Errorf("%w: actual=%d limit=%d", ErrDecompressedDebugMessageTooLarge, len(output), maxOutput)
	}
	if expectedSize != 0 && uint64(len(output)) != uint64(expectedSize) {
		return nil, fmt.Errorf("%w: declared=%d actual=%d", ErrDecompressedDebugMessageSizeMismatch, expectedSize, len(output))
	}
	return output, nil
}
