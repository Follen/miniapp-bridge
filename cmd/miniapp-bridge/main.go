package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Follen/miniapp-bridge/internal/config"
	"github.com/Follen/miniapp-bridge/sdk"
)

type bridgeService interface {
	Start(context.Context) error
	Close(context.Context) error
}

type bridgeServiceFactory func(sdk.Options) (bridgeService, error)
type signalContextFactory func(context.Context, ...os.Signal) (context.Context, context.CancelFunc)

var (
	exitProcess                           = os.Exit
	newBridgeService bridgeServiceFactory = func(options sdk.Options) (bridgeService, error) {
		service, err := sdk.New(options)
		if err != nil {
			return nil, err
		}
		return service, nil
	}
	notifyBridgeSignals signalContextFactory = signal.NotifyContext
)

func main() { exitProcess(run()) }

func run() int {
	return runCLI(os.Args[1:], os.Stdout, os.Stderr, newBridgeService, notifyBridgeSignals)
}

func runCLI(args []string, stdout, stderr io.Writer, newService bridgeServiceFactory, notify signalContextFactory) int {
	o, err := config.Parse(args)
	if err == config.ErrHelp {
		fmt.Fprintln(stdout, config.HelpText)
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	service, err := newService(sdk.Options{
		DebugPort: o.DebugPort, CDPPort: o.CDPPort,
		RecordPath: o.RecordPath, ReplayPath: o.ReplayPath,
		DebugMain: o.DebugMain, DebugFrida: o.DebugFrida,
		Stdout: stdout, Stderr: stderr,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	lifetime, cancel := notify(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := service.Start(lifetime); err != nil {
		fmt.Fprintln(stderr, err)
		_ = service.Close(context.Background())
		return 1
	}
	<-lifetime.Done()
	if err := service.Close(context.Background()); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
