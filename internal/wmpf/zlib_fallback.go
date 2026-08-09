//go:build !windows || !frida

package wmpf

import (
	"bytes"
	"compress/zlib"
	"io"
)

var zlibWriterFactory = func(output io.Writer) io.WriteCloser {
	return zlib.NewWriter(output)
}

func zlibCompress(data []byte) ([]byte, error) {
	var output bytes.Buffer
	writer := zlibWriterFactory(&output)
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
