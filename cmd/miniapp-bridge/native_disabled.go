//go:build !windows || !frida

package main

import (
	"context"

	"miniapp-bridge/internal/logging"
)

func startNative(context.Context, *logging.Logger) (func() error, error) {
	return func() error { return nil }, nil
}
