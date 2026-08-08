//go:build !windows || !cgo

package wmpf

import (
	"bytes"
	"compress/zlib"
	"io"
)

func zlibCompress(data []byte) ([]byte, error) {
	var output bytes.Buffer
	writer := zlib.NewWriter(&output)
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func zlibDecompress(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func ZlibVersion() string { return "go-compress/zlib" }
