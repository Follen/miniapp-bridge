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

func zlibDecompressPlatform(data []byte, _ int, maxOutput int) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	output, err := io.ReadAll(io.LimitReader(reader, int64(maxOutput)))
	if err != nil || len(output) < maxOutput {
		return output, err
	}
	var extra [1]byte
	n, err := reader.Read(extra[:])
	if n != 0 {
		output = append(output, extra[0])
	}
	if err == io.EOF {
		err = nil
	}
	return output, err
}

func ZlibVersion() string { return "go-compress/zlib" }
