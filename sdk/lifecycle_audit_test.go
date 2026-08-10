package sdk

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	bridgeapp "github.com/Follen/miniapp-bridge/internal/app"
)

type lifecycleNative struct {
	mu          sync.Mutex
	closed      int
	attachCalls int
	detachCalls int
	attachGate  chan struct{}
	attachErr   error
	detachErr   error
	metadata    NativeStatus
}

func disabledNativeStarter(context.Context, func(LogEvent)) (NativeSession, error) {
	return nil, nil
}

func (n *lifecycleNative) Close(context.Context) error {
	n.mu.Lock()
	n.closed++
	n.mu.Unlock()
	return nil
}

func (n *lifecycleNative) NativeMetadata() NativeStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.metadata
}

func (n *lifecycleNative) AttachTarget(context.Context, Target) error {
	n.mu.Lock()
	n.attachCalls++
	n.metadata.Attached = n.attachErr == nil
	gate := n.attachGate
	err := n.attachErr
	n.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return err
}

func (n *lifecycleNative) DetachTarget(context.Context) error {
	n.mu.Lock()
	n.detachCalls++
	n.metadata.Attached = false
	err := n.detachErr
	n.mu.Unlock()
	return err
}

func TestLifecycleStartCloseConcurrentPreservesStoppedState(t *testing.T) {
	entered := make(chan struct{})
	n := &lifecycleNative{}
	s := newSDK(t, Options{Native: func(ctx context.Context, _ func(LogEvent)) (NativeSession, error) {
		close(entered)
		<-ctx.Done()
		return n, nil
	}})
	startDone := make(chan error, 1)
	go func() { startDone <- s.Start(context.Background()) }()
	<-entered
	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close(context.Background()) }()
	if err := <-startDone; !errors.Is(err, ErrClosed) && !errors.Is(err, context.Canceled) {
		t.Fatalf("Start result=%v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close result=%v", err)
	}
	if got := s.Status().State; got != StateStopped {
		t.Fatalf("final state=%s", got)
	}
}

func TestLifecycleStartPreservesAlreadyTerminalState(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
		close(entered)
		<-release
		return &lifecycleNative{}, nil
	}})
	done := make(chan error, 1)
	go func() { done <- s.Start(context.Background()) }()
	<-entered
	s.mu.Lock()
	s.state, s.status.State = StateStopped, StateStopped
	s.mu.Unlock()
	close(release)
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after terminal transition=%v", err)
	}
	_ = s.closeApp()
	s.closeNative()
}

func TestLifecycleNativePathRequiresPlatformStarter(t *testing.T) {
	s := newSDK(t, Options{NativePath: "C:\\native\\miniapp-frida.dll"})
	err := s.Start(context.Background())
	if !errors.Is(err, ErrNativeUnavailable) {
		t.Fatalf("NativePath without starter=%v", err)
	}
	if got := s.Status().State; got != StateFailed {
		t.Fatalf("state=%s", got)
	}
	_ = s.Close(context.Background())

	n := &lifecycleNative{}
	s = newSDK(t, Options{
		NativePath: "C:\\native\\miniapp-frida.dll",
		Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
			return n, nil
		},
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("NativePath with starter=%v", err)
	}
	if got := s.Status().Native.Path; got != "C:\\native\\miniapp-frida.dll" {
		t.Fatalf("native path=%q", got)
	}
	_ = s.Close(context.Background())
}

func TestLifecycleDetachIsIdempotentAndUpdatesStatus(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Detach(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Detach before Start=%v", err)
	}
	_ = s.Close(context.Background())

	n := &lifecycleNative{metadata: NativeStatus{Version: "v", ABI: 7, Path: "native.dll"}}
	s = newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return n, nil }})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Status().Native; got.Version != "v" || got.ABI != 7 || got.Path != "native.dll" {
		t.Fatalf("native metadata=%+v", got)
	}
	if err := s.Attach(context.Background(), Target{PID: 1}); err != nil {
		t.Fatal(err)
	}
	if !s.Status().NativeAttached {
		t.Fatal("attach did not set status")
	}
	if err := s.Detach(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().NativeAttached {
		t.Fatal("detach left attached status")
	}
	if err := s.Detach(context.Background()); err != nil {
		t.Fatalf("repeat detach=%v", err)
	}
	n.mu.Lock()
	calls := n.detachCalls
	n.mu.Unlock()
	if calls != 1 {
		t.Fatalf("detach calls=%d", calls)
	}
	_ = s.Close(context.Background())
}

func TestLifecycleStatusOwnsAttachedBitWhenNativeMetadataIsStale(t *testing.T) {
	n := &lifecycleNative{metadata: NativeStatus{Version: "v", ABI: 1, Path: "native.dll"}}
	s := newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return n, nil }})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Attach(context.Background(), Target{PID: 1}); err != nil {
		t.Fatal(err)
	}
	if !s.Status().NativeAttached {
		t.Fatal("service did not own attached state")
	}
	if err := s.Detach(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().NativeAttached {
		t.Fatal("service retained attached state after detach")
	}
	_ = s.Close(context.Background())
}

func TestLifecycleTargetErrorsRefreshNativeMetadata(t *testing.T) {
	attachErr := errors.New("attach failed after detaching old target")
	n := &lifecycleNative{
		attachErr: attachErr,
		metadata:  NativeStatus{Attached: true, Version: "v"},
	}
	s := newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return n, nil }})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Attach(context.Background(), Target{PID: 3}); !errors.Is(err, attachErr) {
		t.Fatalf("Attach error=%v", err)
	}
	if s.Status().NativeAttached {
		t.Fatal("failed target switch left stale attached status")
	}
	_ = s.Close(context.Background())

	detachErr := errors.New("detach reported an error")
	n = &lifecycleNative{
		detachErr: detachErr,
		metadata:  NativeStatus{Attached: true, Version: "v"},
	}
	s = newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return n, nil }})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Detach(context.Background()); !errors.Is(err, detachErr) {
		t.Fatalf("Detach error=%v", err)
	}
	if s.Status().NativeAttached {
		t.Fatal("detach error left stale attached status")
	}
	_ = s.Close(context.Background())
}

func TestLifecycleCloseWaitsForAttachResource(t *testing.T) {
	gate := make(chan struct{})
	n := &lifecycleNative{attachGate: gate}
	s := newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return n, nil }})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	attachDone := make(chan error, 1)
	go func() { attachDone <- s.Attach(context.Background(), Target{PID: 2}) }()
	deadline := time.After(time.Second)
	for {
		n.mu.Lock()
		entered := n.attachCalls == 1
		n.mu.Unlock()
		if entered {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Attach did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := s.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close while attach=%v", err)
	}
	cancel()
	close(gate)
	if err := <-attachDone; err != nil {
		t.Fatalf("Attach=%v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("final Close=%v", err)
	}
	n.mu.Lock()
	closed := n.closed
	n.mu.Unlock()
	if closed != 1 {
		t.Fatalf("native close calls=%d", closed)
	}
}

func TestLifecycleDisconnectSkipsFullPendingChannel(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	full := make(chan pendingResult, 1)
	full <- pendingResult{}
	s.mu.Lock()
	s.pending["full-disconnect"] = full
	s.mu.Unlock()
	s.observeConnection(bridgeapp.ConnectionEvent{Kind: "upstream", Connected: false})
	s.mu.Lock()
	remaining := len(s.pending)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("pending after disconnect=%d", remaining)
	}
	_ = s.Close(context.Background())
}
