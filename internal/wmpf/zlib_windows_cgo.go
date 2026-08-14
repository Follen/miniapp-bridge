//go:build windows && frida

package wmpf

import (
	"fmt"
	"strings"

	fridacore "github.com/Follen/miniapp-bridge/internal/frida"
)

var nativeZlibDecompressWithLimit = fridacore.ZlibDecompressWithLimit

func zlibCompress(data []byte) ([]byte, error) {
	return fridacore.ZlibCompress(data)
}

func zlibDecompressPlatform(data []byte, expectedSize, maxOutput int) ([]byte, error) {
	output, err := nativeZlibDecompressWithLimit(data, expectedSize, maxOutput)
	if err != nil && expectedSize != 0 && (strings.Contains(err.Error(), "decompressed size mismatch") || strings.Contains(err.Error(), "zlib decompress failed: -5")) {
		return nil, fmt.Errorf("%w: declared=%d: %v", ErrDecompressedDebugMessageSizeMismatch, expectedSize, err)
	}
	if err != nil && expectedSize == 0 && strings.Contains(err.Error(), "zlib decompress failed: -5") {
		return nil, fmt.Errorf("%w: limit=%d: %v", ErrDecompressedDebugMessageTooLarge, maxOutput, err)
	}
	if err != nil && (strings.Contains(err.Error(), "input or expected output exceeds") || strings.Contains(err.Error(), "output exceeds")) {
		return nil, fmt.Errorf("%w: limit=%d: %v", ErrDecompressedDebugMessageTooLarge, maxOutput, err)
	}
	return output, err
}

func ZlibVersion() string { return "1.3.1" }
