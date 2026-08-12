package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/capture"
	"github.com/Follen/miniapp-bridge/internal/cdp"
	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/proxy"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

type appCaptureClient struct {
	mu       sync.Mutex
	messages [][]byte
	closed   int
}

func (c *appCaptureClient) Send(b []byte) error {
	c.mu.Lock()
	c.messages = append(c.messages, append([]byte(nil), b...))
	c.mu.Unlock()
	return nil
}

func (c *appCaptureClient) Close() error {
	c.mu.Lock()
	c.closed++
	c.mu.Unlock()
	return nil
}

func (c *appCaptureClient) last() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		return nil
	}
	return append([]byte(nil), c.messages[len(c.messages)-1]...)
}

func appReplayFile(t *testing.T, frames ...[]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "frames.capture")
	r, err := capture.Start(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		if err := r.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func appContextFrame(t *testing.T, category, id string) []byte {
	t.Helper()
	data, err := wmpf.EncodeCategory(category, wmpf.JsContext{ID: id, Name: id})
	if err != nil {
		t.Fatal(err)
	}
	return wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: category, Data: data})
}

func TestCoverageSDKRecorderObserverAndCounts(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	first, err := capture.Start(filepath.Join(t.TempDir(), "first.capture"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := capture.Start(filepath.Join(t.TempDir(), "second.capture"))
	if err != nil {
		t.Fatal(err)
	}
	a.SetRecorder(first)
	if got := a.SwapRecorder(second); got != first {
		t.Fatalf("SwapRecorder returned %p want %p", got, first)
	}
	if got := a.TakeRecorder(); got != second {
		t.Fatalf("TakeRecorder returned %p want %p", got, second)
	}
	if a.TakeRecorder() != nil {
		t.Fatal("TakeRecorder did not clear recorder")
	}
	_ = first.Close()
	_ = second.Close()

	var eventsMu sync.Mutex
	var events []ContextEvent
	var cdpEvents [][]byte
	a.SetObserver(Observer{
		OnContext: func(event ContextEvent) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		},
		OnCDP: func(payload []byte) {
			eventsMu.Lock()
			cdpEvents = append(cdpEvents, append([]byte(nil), payload...))
			eventsMu.Unlock()
		},
	})
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryAddJsContext, wmpf.JsContext{ID: "ctx", Name: "main"}))
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryConnectJsContext, wmpf.JsContext{ID: "ctx"}))
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryRemoveJsContext, wmpf.JsContext{ID: "ctx"}))
	// Removing an unknown context exercises the no-op observer path.
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryRemoveJsContext, wmpf.JsContext{ID: "missing"}))
	// Connecting an unknown context creates and selects it.
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryConnectJsContext, wmpf.JsContext{ID: "new"}))
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: `{"method":"Runtime.executionContextCreated"}`}))
	eventsMu.Lock()
	if len(events) != 4 || events[0].Kind != "added" || events[1].Kind != "selected" || events[2].Kind != "removed" || events[3].Kind != "selected" {
		t.Fatalf("context events=%+v", events)
	}
	if len(cdpEvents) != 1 || string(cdpEvents[0]) != `{"method":"Runtime.executionContextCreated"}` {
		t.Fatalf("cdp events=%q", cdpEvents)
	}
	eventsMu.Unlock()

	debug := &appCaptureClient{}
	cdp := &appCaptureClient{}
	a.DebugHub.Add(debug)
	a.CDPHub.Add(cdp)
	if a.DebugClientCount() != 1 || a.CDPClientCount() != 1 {
		t.Fatalf("counts debug=%d cdp=%d", a.DebugClientCount(), a.CDPClientCount())
	}
	a.DebugHub.Remove(debug)
	a.CDPHub.Remove(cdp)
	if a.DebugClientCount() != 0 || a.CDPClientCount() != 0 {
		t.Fatalf("counts after remove debug=%d cdp=%d", a.DebugClientCount(), a.CDPClientCount())
	}
}

func TestUpstreamCDPPayloadValidation(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	client := &appCaptureClient{}
	a.CDPHub.Add(client)
	var observed [][]byte
	var runtimeErrors []RuntimeError
	a.SetObserver(Observer{
		OnCDP: func(payload []byte) {
			observed = append(observed, append([]byte(nil), payload...))
		},
		OnError: func(event RuntimeError) {
			runtimeErrors = append(runtimeErrors, event)
		},
	})
	if err := a.Requests.TryAdd(cdp.Request{ID: 1, Method: "Runtime.enable"}); err != nil {
		t.Fatal(err)
	}

	for _, payload := range []string{"", "{", "[]", "null", `"scalar"`} {
		a.handleUnwrappedDebugForGeneration(
			mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: payload}),
			7,
		)
	}
	if got := len(client.messages); got != 0 {
		t.Fatalf("invalid payloads broadcast %d messages", got)
	}
	if len(observed) != 0 {
		t.Fatalf("invalid payloads reached observer: %q", observed)
	}
	if a.Requests.Len() != 1 {
		t.Fatalf("invalid payloads consumed pending request: %d", a.Requests.Len())
	}
	if len(runtimeErrors) != 5 {
		t.Fatalf("runtime errors=%d want 5", len(runtimeErrors))
	}
	for _, event := range runtimeErrors {
		if event.Component != "upstream-cdp-payload" || event.Generation != 7 || !strings.Contains(event.Message, ErrInvalidCDPPayload.Error()) {
			t.Fatalf("runtime error=%+v", event)
		}
	}

	eventPayload := `{"method":"Runtime.executionContextCreated","params":{}}`
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: eventPayload}))
	responsePayload := `{"id":1,"result":{}}`
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: responsePayload}))
	if got := len(client.messages); got != 2 || string(client.messages[0]) != eventPayload || string(client.messages[1]) != responsePayload {
		t.Fatalf("valid broadcasts=%q", client.messages)
	}
	if len(observed) != 2 || string(observed[0]) != eventPayload || string(observed[1]) != responsePayload {
		t.Fatalf("valid observer payloads=%q", observed)
	}
	if a.Requests.Len() != 0 {
		t.Fatalf("valid response left pending requests=%d", a.Requests.Len())
	}
}

func TestCoverageSDKSendCDPAndClosed(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	client := &appCaptureClient{}
	a.DebugHub.Add(client)
	if err := a.SendCDP([]byte(`{"id":7,"method":"Runtime.enable","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	frame := client.last()
	outer, err := wmpf.DecodeDebugMessage(frame)
	if err != nil {
		t.Fatal(err)
	}
	chrome, err := wmpf.DecodeChrome(outer.Data)
	if err != nil || chrome.Payload != `{"id":7,"method":"Runtime.enable","params":{}}` {
		t.Fatalf("chrome=%+v err=%v", chrome, err)
	}
	if a.Requests.Len() != 1 {
		t.Fatalf("pending=%d want 1", a.Requests.Len())
	}
	a.closing.Store(true)
	if !errors.Is(a.SendCDP([]byte(`{"id":8}`)), ErrClosed) {
		t.Fatal("SendCDP after close did not return ErrClosed")
	}
}

func TestCoverageSDKConnectionObserver(t *testing.T) {
	events := make(chan ConnectionEvent, 8)
	a := New(freePort(t), freePort(t), logging.New(false, false))
	a.SetObserver(Observer{OnConnection: func(event ConnectionEvent) { events <- event }})
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	debug, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", a.DebugPort), nil)
	if err != nil {
		t.Fatal(err)
	}
	cdp, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", a.CDPPort), nil)
	if err != nil {
		_ = debug.Close()
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	deadline := time.After(time.Second)
	for len(seen) < 2 {
		select {
		case event := <-events:
			if event.Connected {
				seen[event.Kind] = true
			}
		case <-deadline:
			_ = debug.Close()
			_ = cdp.Close()
			_ = a.Close(context.Background())
			t.Fatalf("connected events=%v", seen)
		}
	}
	_ = debug.Close()
	_ = cdp.Close()
	disconnected := 0
	deadline = time.After(time.Second)
	for disconnected < 2 {
		select {
		case event := <-events:
			if !event.Connected {
				disconnected++
			}
		case <-deadline:
			_ = a.Close(context.Background())
			t.Fatalf("disconnected events=%d", disconnected)
		}
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageSDKReplayContextCancellationAndClose(t *testing.T) {
	frame := appContextFrame(t, wmpf.CategoryAddJsContext, "replay")
	path := appReplayFile(t, frame, frame)
	a := New(0, 0, logging.New(false, false))
	// Simulate an already registered replay so starting the next replay cancels it.
	var previousCanceled atomic.Bool
	a.replayCancel = func() { previousCanceled.Store(true) }
	if err := a.ReplayContext(nil, path); err != nil {
		t.Fatal(err)
	}
	if !previousCanceled.Load() {
		t.Fatal("new replay did not cancel previous replay")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.ReplayContext(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled replay error=%v", err)
	}
	if err := a.ReplayContext(context.Background(), filepath.Join(t.TempDir(), "missing.capture")); err == nil {
		t.Fatal("missing replay succeeded")
	}
	a.closing.Store(true)
	if !errors.Is(a.ReplayContext(context.Background(), path), ErrClosed) {
		t.Fatal("closed replay did not return ErrClosed")
	}

	// Cancel while a frame is being dispatched; the next loop observes Done.
	a = New(0, 0, logging.New(false, false))
	cancelOnFirst := make(chan struct{})
	var once atomic.Bool
	var replayCancel context.CancelFunc
	a.SetObserver(Observer{OnContext: func(ContextEvent) {
		if once.CompareAndSwap(false, true) {
			close(cancelOnFirst)
			replayCancel()
		}
	}})
	replayCtx, cancelReplay := context.WithCancel(context.Background())
	replayCancel = cancelReplay
	result := make(chan error, 1)
	go func() { result <- a.ReplayContext(replayCtx, path) }()
	select {
	case <-cancelOnFirst:
	case <-time.After(time.Second):
		t.Fatal("replay did not dispatch first frame")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled replay error=%v", err)
	}

	// A pre-existing cancellation function is replaced and invoked by Close.
	a = New(0, 0, logging.New(false, false))
	var canceled atomic.Bool
	a.replayCancel = func() { canceled.Store(true) }
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !canceled.Load() {
		t.Fatal("Close did not cancel active replay")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func mustUnwrappedCategory(t *testing.T, category string, value any) wmpf.Unwrapped {
	t.Helper()
	raw, err := wmpf.EncodeCategory(category, value)
	if err != nil {
		t.Fatal(err)
	}
	return wmpf.Unwrapped{Category: category, Data: raw}
}

var _ proxy.Client = (*appCaptureClient)(nil)
var _ = bridgecontext.Context{}
