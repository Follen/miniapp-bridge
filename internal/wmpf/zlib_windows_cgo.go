//go:build windows && cgo

package wmpf

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/zlib/src-1.3.1
#cgo LDFLAGS: ${SRCDIR}/../../third_party/zlib/lib/windows-x86_64/libz.a
#include <stdlib.h>
#include "zlib.h"
*/
import "C"

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"unsafe"
)

var zlibOutputAlloc = func(size uintptr) unsafe.Pointer {
	return C.malloc(C.size_t(size))
}

var zlibCompressCall = func(output unsafe.Pointer, outputSize *uint64, source unsafe.Pointer, sourceLen int) int {
	cOutputSize := C.uLong(*outputSize)
	result := C.compress2(
		(*C.Bytef)(output),
		&cOutputSize,
		(*C.Bytef)(source),
		C.uLong(sourceLen),
		C.Z_DEFAULT_COMPRESSION,
	)
	*outputSize = uint64(cOutputSize)
	return int(result)
}

func zlibCompress(data []byte) ([]byte, error) {
	var source unsafe.Pointer
	if len(data) > 0 {
		source = C.CBytes(data)
		defer C.free(source)
	}
	outputSize := uint64(C.compressBound(C.uLong(len(data))))
	output := zlibOutputAlloc(uintptr(outputSize))
	if output == nil {
		return nil, fmt.Errorf("zlib output allocation failed")
	}
	defer C.free(output)
	result := zlibCompressCall(output, &outputSize, source, len(data))
	if result != int(C.Z_OK) {
		return nil, fmt.Errorf("zlib compress failed: %d", result)
	}
	return C.GoBytes(output, C.int(outputSize)), nil
}

func zlibDecompress(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func ZlibVersion() string { return C.GoString(C.zlibVersion()) }
