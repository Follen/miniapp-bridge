//go:build !windows || !frida

package main

import (
	"context"
	"testing"

	"miniapp-bridge/internal/logging"
)

func TestStartNativeDisabledIsNoopAndClosable(t *testing.T) {
	closeNative, err := startNative(context.Background(), logging.NewWithWriters(false, false, nilWriter{}, nilWriter{}))
	if err != nil {
		t.Fatal(err)
	}
	if closeNative == nil {
		t.Fatal("disabled native backend returned nil close function")
	}
	if err := closeNative(); err != nil {
		t.Fatal(err)
	}
}

type nilWriter struct{}

func (nilWriter) Write([]byte) (int, error) { return 0, nil }
