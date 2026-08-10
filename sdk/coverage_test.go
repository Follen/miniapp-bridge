package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	bridgeapp "github.com/Follen/miniapp-bridge/internal/app"
	"github.com/Follen/miniapp-bridge/internal/capture"
	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
)

type advancedNative struct {
	mu                   sync.Mutex
	closed               int
	closeErr             error
	attachErr, detachErr error
	attached             []Target
}

type flipContext struct{ calls int }

func (c *flipContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *flipContext) Done() <-chan struct{}       { return nil }
func (c *flipContext) Err() error {
	c.calls++
	if c.calls > 1 {
		return context.Canceled
	}
	return nil
}
func (c *flipContext) Value(any) any { return nil }

type closeOnlyNative struct{}

func (closeOnlyNative) Close(context.Context) error { return nil }

func (n *advancedNative) Close(context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closed++
	return n.closeErr
}
func (n *advancedNative) AttachTarget(_ context.Context, t Target) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.attachErr != nil {
		return n.attachErr
	}
	n.attached = append(n.attached, t)
	return nil
}
func (n *advancedNative) DetachTarget(context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.detachErr
}

func newSDK(t *testing.T, extra Options) *Service {
	t.Helper()
	if extra.Native == nil && extra.NativePath == "" {
		extra.Native = disabledNativeStarter
	}
	if extra.DebugPort == 0 {
		extra.DebugPort = sdkFreePort(t)
	}
	if extra.CDPPort == 0 {
		extra.CDPPort = sdkFreePort(t)
	}
	s, err := New(extra)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func selectSDKContext(t *testing.T, s *Service, id string) {
	t.Helper()
	s.app.Contexts.Upsert(bridgecontext.Context{ID: id, Target: "test"})
	if !s.app.Contexts.Select(id) {
		t.Fatalf("select context %q", id)
	}
}

func TestNewValidationAndErrorModel(t *testing.T) {
	for _, o := range []Options{{DebugPort: -1}, {CDPPort: 70000}} {
		_, err := New(o)
		var structured *Error
		if err == nil || !errors.As(err, &structured) {
			t.Fatalf("expected structured option error: %v", err)
		}
	}
	var e *Error
	if !errors.As((&Error{Op: "x", Component: "y", Err: ErrClosed}), &e) || e.Error() != "x y: miniapp bridge is closed" || !errors.Is(e, ErrClosed) {
		t.Fatal("error wrapping contract")
	}
	if (&Error{Op: "x", Err: ErrClosed}).Error() != "x: miniapp bridge is closed" {
		t.Fatal("error formatting")
	}
	deferredPath := filepath.Join(t.TempDir(), "missing", "capture.bin")
	deferred, err := New(Options{DebugPort: sdkFreePort(t), CDPPort: sdkFreePort(t), RecordPath: deferredPath, Native: disabledNativeStarter})
	if err != nil {
		t.Fatalf("New should remain allocation-only: %v", err)
	}
	if err := deferred.Start(nil); err == nil {
		deferred.Close(nil)
		t.Fatal("invalid record path should fail during Start")
	} else if !strings.Contains(err.Error(), "record") {
		deferred.Close(nil)
		t.Fatalf("invalid record error=%v", err)
	}
	if err := deferred.Close(nil); err != nil {
		t.Fatalf("close deferred recorder service: %v", err)
	}
	path := filepath.Join(t.TempDir(), "initial.bin")
	s, err := New(Options{DebugPort: sdkFreePort(t), CDPPort: sdkFreePort(t), RecordPath: path, Native: disabledNativeStarter})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(nil); err != nil {
		t.Fatal(err)
	}
}

func TestNativePublicWrappers(t *testing.T) {
	m := DefaultNativeManifest()
	if m.NativeVersion != NativeVersion || m.FridaCoreVersion != FridaCoreVersion || m.ABIVersion != NativeABIVersion || m.DLL != NativeDLLFileName {
		t.Fatalf("default manifest=%+v", m)
	}
	missing := filepath.Join(t.TempDir(), "missing.dll")
	wantMissingCode, wantMissingOperation := ErrNativeMissing, "stat"
	if runtime.GOOS != "windows" {
		// DefaultNativeManifest is pinned to the Windows release artifact;
		// validation therefore precedes file and cache checks elsewhere.
		wantMissingCode, wantMissingOperation = ErrNativeWrongArch, "manifest platform"
	}
	assertNativeError(t, CheckNativeRuntime(missing), wantMissingCode, wantMissingOperation)
	assertNativeError(t, CheckNativeRuntime(missing, m), wantMissingCode, wantMissingOperation)
	assertNativeError(t, CheckNativeRuntime(missing, NativeManifest{}), wantMissingCode, wantMissingOperation)
	wantPrepareCode, wantPrepareOperation := ErrNativeOffline, "offline cache"
	if runtime.GOOS != "windows" {
		wantPrepareCode, wantPrepareOperation = ErrNativeWrongArch, "manifest platform"
	}
	assertNativeError(t, func() error {
		_, err := PrepareNativeRuntime(context.Background(), NativePrepareOptions{CacheDir: t.TempDir(), Manifest: m, Offline: true})
		return err
	}(), wantPrepareCode, wantPrepareOperation)
}

func TestStartFailurePathsAndNativeCloseError(t *testing.T) {
	// Listener failure is returned as a component error and leaves the service failed.
	l := sdkFreePort(t)
	ln, err := net.Listen("tcp", "127.0.0.1:"+itoa(l))
	if err != nil {
		t.Fatal(err)
	}
	s := newSDK(t, Options{DebugPort: l})
	if err := s.Start(context.Background()); err == nil || s.Status().State != StateFailed {
		t.Fatalf("listener failure=%v state=%s", err, s.Status().State)
	}
	if err := s.Start(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("restart failed service=%v", err)
	}
	_ = ln.Close()

	nativeErr := errors.New("native boom")
	s = newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return nil, nativeErr }})
	if err := s.Start(context.Background()); !errors.Is(err, nativeErr) || s.Status().State != StateFailed {
		t.Fatalf("native failure=%v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	s = newSDK(t, Options{})
	if err := s.Start(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled start=%v", err)
	}
	s = newSDK(t, Options{ReplayPath: filepath.Join(t.TempDir(), "missing.bin")})
	if err := s.Start(context.Background()); err == nil || s.Status().State != StateFailed {
		t.Fatalf("replay start=%v", err)
	}

	n := &advancedNative{closeErr: errors.New("close boom")}
	s = newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return n, nil }})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(context.Background()); !errors.Is(err, n.closeErr) {
		t.Fatalf("close error=%v", err)
	}
	s = newSDK(t, Options{})
	if err := s.start(&flipContext{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-listener cancellation=%v", err)
	}
	_ = s.closeApp()
}

func TestStartFailureRollsBackActiveRecording(t *testing.T) {
	debugPort, cdpPort := sdkFreePort(t), sdkFreePort(t)
	recordPath := filepath.Join(t.TempDir(), "startup-failure.bin")
	nativeErr := errors.New("native startup failure after recorder opened")
	s, err := New(Options{
		DebugPort:  debugPort,
		CDPPort:    cdpPort,
		RecordPath: recordPath,
		Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
			return nil, nativeErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); !errors.Is(err, nativeErr) {
		t.Fatalf("start error=%v", err)
	}
	status := s.Status()
	if status.State != StateFailed || !errors.Is(status.Err, nativeErr) {
		t.Fatalf("failed status=%+v", status)
	}
	if status.Recording.Active || status.Recording.Path != recordPath {
		t.Fatalf("recording status after rollback=%+v", status.Recording)
	}
	if recorder := s.app.TakeRecorder(); recorder != nil {
		t.Fatal("app retained recorder ownership after rollback")
	}
	reopened, err := capture.Start(recordPath)
	if err != nil {
		t.Fatalf("recording path remained locked after rollback: %v", err)
	}
	if err := reopened.Write([]byte("after rollback")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	frames, err := capture.Replay(recordPath)
	if err != nil || len(frames) != 1 || string(frames[0]) != "after rollback" {
		t.Fatalf("reopened recording=%q err=%v", frames, err)
	}
	for _, port := range []int{debugPort, cdpPort} {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Fatalf("port %d was not released: %v", port, err)
		}
		_ = listener.Close()
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("close after failed start=%v", err)
	}
}

func TestStartCancellationWhileNativeBlocks(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := newSDK(t, Options{Native: func(ctx context.Context, log func(LogEvent)) (NativeSession, error) {
		close(entered)
		log(LogEvent{Level: "debug", Message: "native"})
		<-release // deliberately ignore ctx to exercise Close timeout
		return &advancedNative{}, nil
	}})
	startDone := make(chan error, 1)
	go func() { startDone <- s.Start(context.Background()) }()
	<-entered
	waitDone := make(chan error, 1)
	go func() { waitDone <- s.Start(context.Background()) }()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := s.Start(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent start timeout=%v", err)
	}
	waitCancel()
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := s.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close timeout=%v", err)
	}
	cancel()
	close(release)
	if err := <-startDone; err == nil {
		t.Fatal("blocked start should be canceled")
	}
	if err := <-waitDone; err == nil {
		t.Fatal("waiting start should return terminal error")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("final close=%v", err)
	}
	if err := s.StartRecording(filepath.Join(t.TempDir(), "closed.bin")); !errors.Is(err, ErrClosed) {
		t.Fatalf("record after close=%v", err)
	}
}

func TestRequestResponseNotifyAndCDPEvents(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Notify(Request{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := s.Send(context.Background(), Request{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := s.Send(nil, Request{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := s.SendRaw(context.Background(), []byte(`{"id":1}`)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := s.Send(context.Background(), Request{Method: "x", Params: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("unmarshallable params should fail")
	}
	if err := s.Notify(Request{Method: "x"}); !errors.Is(err, ErrNotRunning) {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	selectSDKContext(t, s, "request-context")
	defer s.Close(context.Background())
	upstream := &routeFrameClient{frames: make(chan []byte, 8)}
	s.app.DebugHub.Add(upstream)
	defer s.app.DebugHub.Remove(upstream)
	cdp := s.SubscribeCDP(SubscriptionOptions{Buffer: 8})
	respCh := make(chan Response, 1)
	go func() {
		r, e := s.Send(context.Background(), Request{ID: 7, Method: "Runtime.enable", Params: map[string]any{"x": 1}})
		if e != nil {
			t.Errorf("send=%v", e)
		}
		respCh <- r
	}()
	time.Sleep(10 * time.Millisecond)
	s.observeCDP([]byte(`{"id":7,"error":{"code":-1,"message":"bad","data":"x"}}`))
	resp := <-respCh
	if resp.ID != json.Number("7") || resp.Error == nil || resp.Error.Code != -1 {
		t.Fatalf("response=%+v", resp)
	}
	select {
	case ev := <-cdp.Channel():
		if ev.Response == nil {
			t.Fatal("missing response event")
		}
	case <-time.After(time.Second):
		t.Fatal("event timeout")
	}
	if err := s.Notify(Request{Method: "Runtime.enable"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Notify(Request{Method: "x", Params: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("unmarshallable notify should fail")
	}
	if _, err := s.SendRaw(context.Background(), []byte(`{"method":"Runtime.enable"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendRaw(nil, []byte(`{"method":"Runtime.enable"}`)); err != nil {
		t.Fatal(err)
	}
	// Unknown response IDs are observable but do not fail.
	s.observeCDP([]byte(`{"id":999,"result":{}}`))
	s.observeCDP([]byte(`{`))
}

func TestSendReturnsClosedAppErrorAndCleansPending(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(context.Background(), Request{ID: "closed-app", Method: "x"}); err == nil {
		t.Fatal("closed app should reject send")
	}
	if err := s.Notify(Request{Method: "x"}); err == nil {
		t.Fatal("closed app should reject notify")
	}
	if len(s.pending) != 0 {
		t.Fatalf("pending requests=%d", len(s.pending))
	}
	_ = s.Close(context.Background())
}

func TestRequestCancellationAndDuplicateID(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	selectSDKContext(t, s, "request-context")
	defer s.Close(context.Background())
	upstream := &routeFrameClient{frames: make(chan []byte, 8)}
	s.app.DebugHub.Add(upstream)
	defer s.app.DebugHub.Remove(upstream)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.Send(ctx, Request{ID: "cancel", Method: "x"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel=%v", err)
	}
	first := make(chan error, 1)
	go func() { _, err := s.Send(context.Background(), Request{ID: "same", Method: "x"}); first <- err }()
	time.Sleep(10 * time.Millisecond)
	if _, err := s.Send(context.Background(), Request{ID: "same", Method: "x"}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate=%v", err)
	}
	s.observeCDP([]byte(`{"id":"same","result":{}}`))
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestCloseWakesPendingRequest(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	selectSDKContext(t, s, "request-context")
	upstream := &routeFrameClient{frames: make(chan []byte, 1)}
	s.app.DebugHub.Add(upstream)
	defer s.app.DebugHub.Remove(upstream)
	done := make(chan error, 1)
	go func() {
		_, err := s.Send(context.Background(), Request{ID: "waiting", Method: "Runtime.enable"})
		done <- err
	}()
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		pending := len(s.pending)
		s.mu.Unlock()
		if pending == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("request was not pending")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatalf("pending request result=%v", err)
	}
}

func TestCloseStateBranchesAndFullPendingChannel(t *testing.T) {
	s := newSDK(t, Options{})
	s.mu.Lock()
	s.state = StateStarting
	s.startDone = make(chan struct{})
	done := s.startDone
	s.mu.Unlock()
	go func() {
		s.mu.Lock()
		s.state = StateNew
		s.mu.Unlock()
		close(done)
	}()
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	s = newSDK(t, Options{})
	s.mu.Lock()
	s.state = StateStopping
	s.closeDone = make(chan struct{})
	close(s.closeDone)
	s.closeErr = errors.New("already stopping")
	s.mu.Unlock()
	if err := s.Close(context.Background()); !errors.Is(err, s.closeErr) {
		t.Fatal(err)
	}

	s = newSDK(t, Options{})
	s.mu.Lock()
	s.state = StateStopping
	s.closeDone = make(chan struct{})
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(s.closeDone)

	s = newSDK(t, Options{})
	s.resourceMu.Lock()
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if err := s.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s.resourceMu.Unlock()
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	s = newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	full := make(chan pendingResult, 1)
	full <- pendingResult{}
	s.mu.Lock()
	s.pending["full"] = full
	s.mu.Unlock()
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestContextsTargetRecordingReplayAndSubscriptions(t *testing.T) {
	n := &advancedNative{}
	s := newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return n, nil }})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctxSub := s.SubscribeContexts(SubscriptionOptions{Buffer: 4})
	s.app.Contexts.Upsert(bridgecontext.Context{ID: "ctx-a", Target: "main"})
	s.observeContext(bridgeapp.ContextEvent{Kind: "added", Context: bridgecontext.Context{ID: "ctx-a", Target: "main"}})
	if err := s.SelectContext("missing"); !errors.Is(err, ErrUnknownContext) {
		t.Fatal(err)
	}
	if err := s.SelectContext("ctx-a"); err != nil {
		t.Fatal(err)
	}
	if got := s.Contexts(); len(got) != 1 || got[0].ID != "ctx-a" {
		t.Fatalf("contexts=%+v", got)
	}
	if err := s.Attach(context.Background(), Target{PID: 42}); err != nil {
		t.Fatal(err)
	}
	if err := s.Attach(nil, Target{PID: 44}); err != nil {
		t.Fatal(err)
	}
	if err := s.Detach(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Detach(nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-ctxSub.Channel():
		case <-time.After(time.Second):
			t.Fatal("context event timeout")
		}
	}
	if err := s.StartRecording(filepath.Join(t.TempDir(), "missing", "capture.bin")); err == nil {
		t.Fatal("invalid recording path should fail")
	}
	if err := s.StartRecording(filepath.Join(t.TempDir(), "capture.bin")); err != nil {
		t.Fatal(err)
	}
	if err := s.StartRecording(filepath.Join(t.TempDir(), "capture2.bin")); err != nil {
		t.Fatal(err)
	}
	if err := s.StopRecording(); err != nil {
		t.Fatal(err)
	}
	if err := s.StopRecording(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if _, err := s.Discover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Discover(nil); err != nil {
			t.Fatal(err)
		}
	} else {
		for _, ctx := range []context.Context{context.Background(), nil} {
			_, err := s.Discover(ctx)
			var structured *Error
			if !errors.Is(err, ErrNativeUnavailable) || !errors.As(err, &structured) || structured.Op != "discover" || structured.Component != "process" {
				t.Fatalf("unsupported discovery error=%v", err)
			}
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Discover(canceled); err == nil {
		t.Fatal("canceled discovery should fail")
	}

	// Replay a valid context frame and observe it through the SDK.
	path := filepath.Join(t.TempDir(), "replay.bin")
	r, err := capture.Start(path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := wmpf.EncodeCategory(wmpf.CategoryAddJsContext, wmpf.JsContext{ID: "ctx-replay", Name: "worker"})
	if err := r.Write(wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryAddJsContext, Data: data})); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if err := s.Replay(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		if len(s.Contexts()) > 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("replay context timeout")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	_ = s.Attach(context.Background(), Target{PID: 43})
}

func TestAttachDetachErrorsAndUnavailable(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Attach(context.Background(), Target{}); !errors.Is(err, ErrNotRunning) {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Attach(context.Background(), Target{}); !errors.Is(err, ErrNativeUnavailable) {
		t.Fatal(err)
	}
	if err := s.Detach(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = s.Close(context.Background())
	s = newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return closeOnlyNative{}, nil }})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Detach(context.Background()); !errors.Is(err, ErrNativeUnavailable) {
		t.Fatal(err)
	}
	_ = s.Close(context.Background())
	n := &advancedNative{attachErr: errors.New("attach"), detachErr: errors.New("detach")}
	s = newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return n, nil }})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Attach(context.Background(), Target{}); !errors.Is(err, n.attachErr) {
		t.Fatal(err)
	}
	if err := s.Detach(context.Background()); !errors.Is(err, n.detachErr) {
		t.Fatal(err)
	}
	_ = s.Close(context.Background())
}

func TestSubscriptionsAndWriters(t *testing.T) {
	s := newSDK(t, Options{SubscriberBuffer: 2, Stdout: io.Discard, Stderr: io.Discard})
	status := s.SubscribeStatus()
	if status.Channel() == nil || status.Close() != nil {
		t.Fatal("status subscription")
	}
	for _, sub := range []*Subscription[LogEvent]{s.SubscribeLogs(), s.SubscribeLogs(), nil} {
		if sub != nil {
			_ = sub.Close()
			_ = sub.Close()
			_ = sub.Channel()
			_ = sub.Err()
		}
	}
	var nilSub *Subscription[int]
	if nilSub.Channel() != nil || nilSub.Close() != nil || nilSub.Err() != nil || (&Subscription[int]{}).Channel() != nil || (&Subscription[int]{}).Close() != nil || (&Subscription[int]{}).Err() != nil {
		t.Fatal("nil subscription contract")
	}
	var bus eventBus[int]
	closed := bus.subscribe(1)
	bus.closeAll()
	bus.publish(1)
	if _, ok := <-closed.Channel(); ok {
		t.Fatal("closed bus channel")
	}
	late := bus.subscribe(1)
	if _, ok := <-late.Channel(); ok {
		t.Fatal("late channel")
	}
	var defaultBus eventBus[int]
	defaultSub := defaultBus.subscribe()
	defaultBus.publish(1)
	if got := <-defaultSub.Channel(); got != 1 {
		t.Fatalf("default bus=%d", got)
	}
	defaultBus.closeAll()
	defaultBus.closeAll()
	if n, err := (&logWriter{dst: failingWriter{}}).Write([]byte("x")); n != 0 || err == nil {
		t.Fatal("writer error")
	}
	logs := s.SubscribeLogs(SubscriptionOptions{Buffer: 1})
	if _, err := (&logWriter{svc: s, dst: io.Discard, level: "info"}).Write([]byte("line")); err != nil {
		t.Fatal(err)
	}
	if got := <-logs.Channel(); got.Message != "line" {
		t.Fatalf("log=%+v", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write") }

func itoa(v int) string { return strconv.Itoa(v) }
