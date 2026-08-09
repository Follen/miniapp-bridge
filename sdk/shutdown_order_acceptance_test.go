package sdk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"
)

type orderedShutdownNative struct {
	service *Service
	pending <-chan pendingResult
	started chan struct{}
	release chan struct{}
	err     chan error
}

func (native *orderedShutdownNative) Close(context.Context) error {
	select {
	case result := <-native.pending:
		if !errors.Is(result.err, ErrClosed) {
			native.err <- fmt.Errorf("pending result before native teardown=%v", result.err)
		}
	default:
		native.err <- errors.New("pending request was not failed before native teardown")
	}

	native.service.mu.Lock()
	pendingCount := len(native.service.pending)
	native.service.mu.Unlock()
	if pendingCount != 0 {
		native.err <- fmt.Errorf("pending table size before native teardown=%d", pendingCount)
	}
	for _, port := range []int{native.service.Status().DebugPort, native.service.Status().CDPPort} {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			native.err <- fmt.Errorf("listener %d was not released before native teardown: %w", port, err)
			continue
		}
		_ = listener.Close()
	}
	if recorder := native.service.app.TakeRecorder(); recorder != nil {
		_ = recorder.Close()
		native.err <- errors.New("recorder ownership remained when native teardown started")
	}
	assertBusesOpenDuringNativeTeardown(native.service, native.err)
	close(native.started)
	<-native.release
	return nil
}

func assertBusesOpenDuringNativeTeardown(service *Service, failures chan<- error) {
	service.logs.mu.Lock()
	logsClosed := service.logs.closed
	service.logs.mu.Unlock()
	service.statuses.mu.Lock()
	statusesClosed := service.statuses.closed
	service.statuses.mu.Unlock()
	service.cdpEvents.mu.Lock()
	cdpClosed := service.cdpEvents.closed
	service.cdpEvents.mu.Unlock()
	service.contexts.mu.Lock()
	contextsClosed := service.contexts.closed
	service.contexts.mu.Unlock()
	if logsClosed || statusesClosed || cdpClosed || contextsClosed {
		failures <- fmt.Errorf("subscription buses closed before native teardown: logs=%t status=%t cdp=%t contexts=%t", logsClosed, statusesClosed, cdpClosed, contextsClosed)
	}
}

func TestShutdownOrderAcrossServiceAppNativeAndSubscriptions(t *testing.T) {
	debugPort, cdpPort := sdkFreePort(t), sdkFreePort(t)
	for cdpPort == debugPort {
		cdpPort = sdkFreePort(t)
	}
	pending := make(chan pendingResult, 1)
	native := &orderedShutdownNative{
		pending: pending,
		started: make(chan struct{}),
		release: make(chan struct{}),
		err:     make(chan error, 16),
	}
	service, err := New(Options{
		DebugPort:        debugPort,
		CDPPort:          cdpPort,
		RecordPath:       filepath.Join(t.TempDir(), "shutdown.capture"),
		SubscriberBuffer: 16,
		Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
			return native, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	native.service = service
	logs := service.SubscribeLogs()
	statuses := service.SubscribeStatus()
	cdp := service.SubscribeCDP()
	contexts := service.SubscribeContexts()
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.pending["shutdown-order"] = pending
	service.mu.Unlock()

	closed := make(chan error, 1)
	go func() { closed <- service.Close(context.Background()) }()
	select {
	case <-native.started:
	case <-time.After(2 * time.Second):
		t.Fatal("native teardown did not start")
	}
	select {
	case err := <-native.err:
		t.Fatal(err)
	default:
	}
	close(native.release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	close(native.err)
	for err := range native.err {
		t.Error(err)
	}

	service.logs.mu.Lock()
	logsClosed := service.logs.closed
	service.logs.mu.Unlock()
	service.statuses.mu.Lock()
	statusesClosed := service.statuses.closed
	service.statuses.mu.Unlock()
	service.cdpEvents.mu.Lock()
	cdpClosed := service.cdpEvents.closed
	service.cdpEvents.mu.Unlock()
	service.contexts.mu.Lock()
	contextsClosed := service.contexts.closed
	service.contexts.mu.Unlock()
	if !logsClosed || !statusesClosed || !cdpClosed || !contextsClosed {
		t.Fatalf("subscription buses remained open after Close: logs=%t status=%t cdp=%t contexts=%t", logsClosed, statusesClosed, cdpClosed, contextsClosed)
	}
	assertSubscriptionDrainsAndCloses(t, logs.Channel())
	assertSubscriptionDrainsAndCloses(t, statuses.Channel())
	assertSubscriptionDrainsAndCloses(t, cdp.Channel())
	assertSubscriptionDrainsAndCloses(t, contexts.Channel())
}

func assertSubscriptionDrainsAndCloses[T any](t *testing.T, channel <-chan T) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case _, open := <-channel:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("subscription channel did not close")
		}
	}
}
