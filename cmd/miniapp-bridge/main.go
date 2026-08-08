package main

import (
	"context"
	"fmt"
	"miniapp-bridge/internal/app"
	"miniapp-bridge/internal/capture"
	"miniapp-bridge/internal/config"
	"miniapp-bridge/internal/logging"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	o, e := config.Parse(os.Args[1:])
	if e == config.ErrHelp {
		fmt.Println(config.HelpText)
		return
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	a := app.New(o.DebugPort, o.CDPPort, logging.New(o.DebugMain, o.DebugFrida))
	if o.RecordPath != "" {
		r, e := capture.Start(o.RecordPath)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		a.SetRecorder(r)
	}
	if e := a.Start(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	closeNative, e := startNative(context.Background(), a.Log)
	if e != nil {
		_ = a.Close(context.Background())
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	if o.ReplayPath != "" {
		if e := a.Replay(o.ReplayPath); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	// Close network clients and listeners before detaching Frida. The reference
	// process drops its WebSocket clients as part of process shutdown; keeping
	// the target connection open while unloading the Agent introduces a detach
	// race in the target's debug session.
	if e := a.Close(context.Background()); e != nil {
		fmt.Fprintln(os.Stderr, e)
	}
	if e := closeNative(); e != nil {
		fmt.Fprintln(os.Stderr, e)
	}
}
