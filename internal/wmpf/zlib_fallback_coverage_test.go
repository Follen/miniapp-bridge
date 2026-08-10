//go:build !windows || !frida

package wmpf

import (
	"errors"
	"io"
	"testing"
)

type fallbackErrorWriter struct {
	writeErr error
	closeErr error
}

func (writer *fallbackErrorWriter) Write(data []byte) (int, error) {
	if writer.writeErr != nil {
		return 0, writer.writeErr
	}
	return len(data), nil
}

func (writer *fallbackErrorWriter) Close() error {
	return writer.closeErr
}

func TestFallbackZlibCompressFailures(t *testing.T) {
	originalFactory := zlibWriterFactory
	defer func() { zlibWriterFactory = originalFactory }()

	writeErr := errors.New("write failed")
	zlibWriterFactory = func(io.Writer) io.WriteCloser {
		return &fallbackErrorWriter{writeErr: writeErr}
	}
	if _, err := zlibCompress([]byte("write")); !errors.Is(err, writeErr) {
		t.Fatalf("zlibCompress write error = %v, want %v", err, writeErr)
	}

	closeErr := errors.New("close failed")
	zlibWriterFactory = func(io.Writer) io.WriteCloser {
		return &fallbackErrorWriter{closeErr: closeErr}
	}
	if _, err := zlibCompress([]byte("close")); !errors.Is(err, closeErr) {
		t.Fatalf("zlibCompress close error = %v, want %v", err, closeErr)
	}
}

func TestFallbackZlibDecompressFailures(t *testing.T) {
	if _, err := zlibDecompress(nil); err == nil {
		t.Fatal("empty zlib stream unexpectedly decoded")
	}

	compressed, err := zlibCompress([]byte("checksum failure"))
	if err != nil {
		t.Fatal(err)
	}
	compressed[len(compressed)-1] ^= 0xff
	if _, err := zlibDecompress(compressed); err == nil {
		t.Fatal("zlib stream with corrupt checksum unexpectedly decoded")
	}
}
