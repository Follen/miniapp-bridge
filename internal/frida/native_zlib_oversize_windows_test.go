//go:build windows && frida && !race

package frida

import (
	"strings"
	"testing"
	"unsafe"
)

func TestNativeZlibOversizeInputValidation(t *testing.T) {
	marker := byte(0)
	huge := unsafe.Slice(&marker, maxNativeZlibOutput+1)
	if _, err := ZlibCompress(huge); err == nil || !strings.Contains(err.Error(), "input exceeds") {
		t.Fatalf("large compress err=%v", err)
	}
	if _, err := ZlibDecompress(huge, 0); err == nil || !strings.Contains(err.Error(), "input or expected output exceeds") {
		t.Fatalf("large decompress err=%v", err)
	}
}
