package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
)

type blockingCloseNative struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (n *blockingCloseNative) Close(context.Context) error {
	n.once.Do(func() { close(n.started) })
	<-n.release
	return nil
}

func TestPublicDTOAliasesAndStructuredErrors(t *testing.T) {
	var request CDPRequest = Request{Method: "Runtime.enable"}
	var response CDPResponse = Response{ID: 1}
	if request.Method == "" || response.ID != 1 {
		t.Fatal("CDP compatibility aliases")
	}
	_, err := New(Options{DebugPort: -1})
	var structured *Error
	if !errors.Is(err, ErrInvalidOptions) || !errors.As(err, &structured) || structured.Component != "options" {
		t.Fatalf("invalid options error=%v", err)
	}
	for _, sentinel := range []error{
		ErrInvalidState, ErrNoUpstream, ErrUnknownRequestID, ErrTimeout,
		ErrProtocol, ErrCorruptFrame,
	} {
		if sentinel == nil || sentinel.Error() == "" {
			t.Fatal("missing public error sentinel")
		}
	}
}

func TestStructuredIDsAreProcessWideAndMonotonic(t *testing.T) {
	first := nextStructuredRequestID()
	second := nextStructuredRequestID()
	parse := func(value string) uint64 {
		t.Helper()
		if !strings.HasPrefix(value, "sdk-") {
			t.Fatalf("structured ID=%q", value)
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(value, "sdk-"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if a, b := parse(first), parse(second); b != a+1 {
		t.Fatalf("IDs are not monotonic: %q %q", first, second)
	}
}

func TestRawRequestIDsAndUnknownResponseDiagnostics(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	selectSDKContext(t, s, "raw-context")
	for _, payload := range []string{
		`{"id":true,"method":"Runtime.enable"}`,
		`{"id":{},"method":"Runtime.enable"}`,
		`{"id":[],"method":"Runtime.enable"}`,
	} {
		if _, err := s.SendRaw(context.Background(), []byte(payload)); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid raw ID %s: %v", payload, err)
		}
	}
	if _, err := s.Send(context.Background(), Request{ID: true, Method: "Runtime.enable"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid structured ID=%v", err)
	}
	client := &routeFrameClient{frames: make(chan []byte, 4)}
	s.app.DebugHub.Add(client)
	firstDone := make(chan error, 1)
	go func() {
		_, err := s.SendRaw(context.Background(), []byte(`{"id":"raw-duplicate","method":"Runtime.enable"}`))
		firstDone <- err
	}()
	_ = readRouteFrame(t, client)
	if _, err := s.SendRaw(context.Background(), []byte(`{"id":"raw-duplicate","method":"Runtime.enable"}`)); !errors.Is(err, ErrDuplicateRequestID) {
		t.Fatalf("raw duplicate=%v", err)
	}
	s.observeCDP([]byte(`{"id":"raw-duplicate","result":{}}`))
	if err := <-firstDone; err != nil {
		t.Fatalf("raw first=%v", err)
	}
	sub := s.SubscribeCDP()
	defer sub.Close()
	s.observeCDP([]byte(`{"id":"unknown","result":{}}`))
	event := <-sub.Channel()
	if !errors.Is(event.Err, ErrUnknownRequestID) {
		t.Fatalf("unknown response diagnostic=%v", event.Err)
	}
}

func TestSelectedTargetRecordingAndStatusCopies(t *testing.T) {
	n := &lifecycleNative{metadata: NativeStatus{Attached: true}}
	s := newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return n, nil }})
	if _, ok := s.SelectedContext(); ok {
		t.Fatal("unexpected selected context")
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	s.app.Contexts.Upsert(bridgecontext.Context{ID: "b", Target: "worker"})
	s.app.Contexts.Upsert(bridgecontext.Context{ID: "a", Target: "main"})
	if err := s.SelectContext("a"); err != nil {
		t.Fatal(err)
	}
	selected, ok := s.SelectedContext()
	if !ok || selected.ID != "a" || selected.Target != "main" {
		t.Fatalf("selected=%+v ok=%v", selected, ok)
	}
	target := Target{PID: 77, Name: "fixture", Version: 1}
	if err := s.Attach(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if got := s.Status().Target; !got.Attached || got.Target.PID != target.PID {
		t.Fatalf("target status=%+v", got)
	}
	path := filepath.Join(t.TempDir(), "capture.bin")
	if err := s.StartRecording(path); err != nil {
		t.Fatal(err)
	}
	if got := s.Status().Recording; !got.Active || got.Path != path {
		t.Fatalf("recording status=%+v", got)
	}
	first := s.Status()
	first.Contexts[0].ID = "mutated"
	if got := s.Status().Contexts[0].ID; got != "a" {
		t.Fatalf("Status returned shared contexts: %q", got)
	}
	a, b := s.SubscribeStatus(), s.SubscribeStatus()
	defer a.Close()
	defer b.Close()
	s.publishStatus()
	left, right := <-a.Channel(), <-b.Channel()
	left.Contexts[0].ID = "subscriber-mutation"
	if right.Contexts[0].ID != "a" {
		t.Fatalf("status subscriptions share contexts: %+v", right.Contexts)
	}
	if err := s.StopRecording(); err != nil {
		t.Fatal(err)
	}
	if s.Status().Recording.Active {
		t.Fatal("recording remained active")
	}
	if err := s.Detach(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().Target.Attached {
		t.Fatal("target remained attached")
	}
	if !validRequestID(json.Number("1")) {
		t.Fatal("json.Number should be a valid request ID")
	}
}

func TestNewRecordPathIsAllocationOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "capture.bin")
	s, err := New(Options{
		DebugPort:  sdkFreePort(t),
		CDPPort:    sdkFreePort(t),
		RecordPath: path,
		Native:     disabledNativeStarter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("New touched record path: %v", err)
	}
	if status := s.Status().Recording; status.Active || status.Path != path {
		t.Fatalf("recording before Start=%+v", status)
	}
	err = s.Start(context.Background())
	var structured *Error
	if err == nil || !errors.As(err, &structured) || structured.Component != "record" {
		t.Fatalf("Start record error=%v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCloseWakesPendingBeforeNativeTeardownAndPublishesTerminalStatus(t *testing.T) {
	native := &blockingCloseNative{started: make(chan struct{}), release: make(chan struct{})}
	s := newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return native, nil }})
	statusSub := s.SubscribeStatus(SubscriptionOptions{Buffer: 16})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	selectSDKContext(t, s, "close-order")
	client := &routeFrameClient{frames: make(chan []byte, 1)}
	s.app.DebugHub.Add(client)
	pending := make(chan error, 1)
	go func() {
		_, err := s.Send(context.Background(), Request{ID: "close-order", Method: "Runtime.enable"})
		pending <- err
	}()
	_ = readRouteFrame(t, client)
	closed := make(chan error, 1)
	go func() { closed <- s.Close(context.Background()) }()
	select {
	case <-native.started:
	case <-time.After(time.Second):
		t.Fatal("native teardown did not start")
	}
	select {
	case err := <-pending:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("pending close error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending request waited for native teardown")
	}
	close(native.release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	var states []State
	for status := range statusSub.Channel() {
		states = append(states, status.State)
	}
	if len(states) == 0 || states[len(states)-1] != StateStopped {
		t.Fatalf("terminal status sequence=%v", states)
	}
}
