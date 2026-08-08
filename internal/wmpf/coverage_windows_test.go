//go:build windows && cgo

package wmpf

import (
	"testing"
	"unsafe"
)

func TestCoverageWindowsZlibFailures(t *testing.T) {
	originalAlloc := zlibOutputAlloc
	originalCompress := zlibCompressCall
	defer func() {
		zlibOutputAlloc = originalAlloc
		zlibCompressCall = originalCompress
	}()

	zlibOutputAlloc = func(uintptr) unsafe.Pointer { return nil }
	_, err := zlibCompress([]byte("allocation"))
	requireError(t, err)

	zlibOutputAlloc = originalAlloc
	zlibCompressCall = func(unsafe.Pointer, *uint64, unsafe.Pointer, int) int { return -2 }
	_, err = zlibCompress([]byte("compress"))
	requireError(t, err)

	_, err = zlibDecompress(nil)
	requireError(t, err)
}
