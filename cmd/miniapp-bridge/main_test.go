package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Follen/miniapp-bridge/sdk"
)

type fakeBridgeService struct {
	startErr, closeErr error
	starts, closes     int
}

func (s *fakeBridgeService) Start(context.Context) error {
	s.starts++
	return s.startErr
}

func (s *fakeBridgeService) Close(context.Context) error {
	s.closes++
	return s.closeErr
}

func canceledSignalContext(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	cancel()
	return ctx, cancel
}

func TestRunCLIExitPaths(t *testing.T) {
	var stdout, stderr bytes.Buffer
	unusedFactory := func(sdk.Options) (bridgeService, error) {
		t.Fatal("service factory must not be called")
		return nil, nil
	}
	unusedSignals := func(context.Context, ...os.Signal) (context.Context, context.CancelFunc) {
		t.Fatal("signal factory must not be called")
		return nil, nil
	}

	if code := runCLI([]string{"--help"}, &stdout, &stderr, unusedFactory, unusedSignals); code != 0 || !strings.Contains(stdout.String(), "Usage: miniapp-bridge") {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCLI([]string{"--unknown"}, &stdout, &stderr, unusedFactory, unusedSignals); code != 2 || !strings.Contains(stderr.String(), "unknown option") {
		t.Fatalf("parse code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	constructorErr := errors.New("constructor failed")
	stderr.Reset()
	if code := runCLI(nil, &stdout, &stderr, func(sdk.Options) (bridgeService, error) {
		return nil, constructorErr
	}, unusedSignals); code != 1 || !strings.Contains(stderr.String(), constructorErr.Error()) {
		t.Fatalf("constructor code=%d stderr=%q", code, stderr.String())
	}

	startErr := errors.New("start failed")
	startFailure := &fakeBridgeService{startErr: startErr}
	stderr.Reset()
	if code := runCLI(nil, &stdout, &stderr, func(sdk.Options) (bridgeService, error) {
		return startFailure, nil
	}, canceledSignalContext); code != 1 || startFailure.starts != 1 || startFailure.closes != 1 || !strings.Contains(stderr.String(), startErr.Error()) {
		t.Fatalf("start code=%d service=%+v stderr=%q", code, startFailure, stderr.String())
	}

	success := &fakeBridgeService{}
	stderr.Reset()
	if code := runCLI(nil, &stdout, &stderr, func(sdk.Options) (bridgeService, error) {
		return success, nil
	}, canceledSignalContext); code != 0 || success.starts != 1 || success.closes != 1 || stderr.Len() != 0 {
		t.Fatalf("success code=%d service=%+v stderr=%q", code, success, stderr.String())
	}

	closeErr := errors.New("close failed")
	closeFailure := &fakeBridgeService{closeErr: closeErr}
	stderr.Reset()
	if code := runCLI(nil, &stdout, &stderr, func(sdk.Options) (bridgeService, error) {
		return closeFailure, nil
	}, canceledSignalContext); code != 1 || closeFailure.starts != 1 || closeFailure.closes != 1 || !strings.Contains(stderr.String(), closeErr.Error()) {
		t.Fatalf("close code=%d service=%+v stderr=%q", code, closeFailure, stderr.String())
	}
}

func TestMainAndDefaultFactory(t *testing.T) {
	if service, err := newBridgeService(sdk.Options{DebugPort: -1}); err == nil || service != nil {
		t.Fatalf("default factory service=%v err=%v", service, err)
	}
	serviceFromFactory, err := newBridgeService(sdk.Options{})
	if err != nil || serviceFromFactory == nil {
		t.Fatalf("default factory service=%v err=%v", serviceFromFactory, err)
	}
	if err := serviceFromFactory.Close(context.Background()); err != nil {
		t.Fatalf("close default service: %v", err)
	}

	previousArgs := os.Args
	previousExit := exitProcess
	previousFactory := newBridgeService
	previousSignals := notifyBridgeSignals
	t.Cleanup(func() {
		os.Args = previousArgs
		exitProcess = previousExit
		newBridgeService = previousFactory
		notifyBridgeSignals = previousSignals
	})

	service := &fakeBridgeService{}
	os.Args = []string{"miniapp-bridge"}
	newBridgeService = func(sdk.Options) (bridgeService, error) { return service, nil }
	notifyBridgeSignals = canceledSignalContext
	exitCode := -1
	exitProcess = func(code int) { exitCode = code }

	main()
	if exitCode != 0 || service.starts != 1 || service.closes != 1 {
		t.Fatalf("main exit=%d service=%+v", exitCode, service)
	}
}
