//go:build windows && frida

package main

import (
	"context"
	"fmt"
	"sync"

	agent "miniapp-bridge/frida"
	fridacore "miniapp-bridge/internal/frida"
	"miniapp-bridge/internal/logging"
)

func startNative(ctx context.Context, log *logging.Logger) (func() error, error) {
	device, err := fridacore.NewNativeDevice()
	if err != nil {
		return nil, err
	}
	device.SetMessageHandler(func(message fridacore.Message) {
		if message.Type == "error" {
			log.Error("[frida] " + string(message.Payload))
		} else if len(message.Payload) != 0 {
			log.Frida("[frida] " + string(message.Payload))
		} else {
			log.Frida("[frida] " + message.Type)
		}
	})
	bootstrap := fridacore.Bootstrap{Device: device, ConfigDir: "configs/addresses", Agent: agent.SourceForConfig}
	session, script, target, err := bootstrap.Attach(ctx)
	if err != nil {
		_ = device.Close()
		fridacore.ShutdownRuntime()
		return nil, err
	}
	log.Info(fmt.Sprintf("[frida] attached pid=%d version=%d path=%s", target.PID, target.Version, target.Path))
	var once sync.Once
	return func() error {
		var first error
		once.Do(func() {
			if err := script.Unload(); err != nil {
				first = err
			}
			if err := session.Detach(); first == nil && err != nil {
				first = err
			}
			if err := device.Close(); first == nil && err != nil {
				first = err
			}
			fridacore.ShutdownRuntime()
		})
		return first
	}, nil
}
