package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/capture"
	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

type categoryAuditClient struct {
	mu   sync.Mutex
	data [][]byte
}

func (c *categoryAuditClient) Send(message []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = append(c.data, append([]byte(nil), message...))
	return nil
}
func (c *categoryAuditClient) Close() error { return nil }
func (c *categoryAuditClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.data)
}

func TestAuditUpstreamCategoryRoutingMatchesReference(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	sink := &categoryAuditClient{}
	a.CDPHub.Add(sink)

	ignored := []struct {
		category string
		value    any
	}{
		{wmpf.CategorySetupContext, wmpf.SetupContext{}},
		{wmpf.CategoryCallInterface, wmpf.CallInterface{}},
		{wmpf.CategoryCallInterfaceResult, wmpf.CallInterfaceResult{}},
		{wmpf.CategoryEvaluateJavascript, wmpf.EvaluateJavascript{}},
		{wmpf.CategoryEvaluateJavascriptResult, wmpf.EvaluateJavascriptResult{}},
		{wmpf.CategoryBreakpoint, wmpf.Breakpoint{}},
		{wmpf.CategoryPing, wmpf.Ping{}},
		{wmpf.CategoryPong, wmpf.Pong{}},
		{wmpf.CategoryDomOp, wmpf.DomOp{}},
		{wmpf.CategoryDomEvent, wmpf.DomEvent{}},
		{wmpf.CategoryNetworkDebugAPI, wmpf.NetworkDebugAPI{}},
		{wmpf.CategoryChromeDevtools, wmpf.ChromeDevtools{Payload: `{"id":1}`}},
		{wmpf.CategoryCustomMessage, wmpf.CustomMessage{}},
	}
	for _, tc := range ignored {
		data, err := wmpf.EncodeCategory(tc.category, tc.value)
		if err != nil {
			t.Fatalf("encode %s: %v", tc.category, err)
		}
		a.handleDebugMessage(wmpf.DebugMessage{Category: tc.category, Data: data})
	}
	if got := sink.count(); got != 0 {
		t.Fatalf("non-result upstream categories were broadcast to CDP: %d", got)
	}

	result, err := wmpf.EncodeCategory(wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: `{"method":"Runtime.consoleAPICalled"}`})
	if err != nil {
		t.Fatal(err)
	}
	a.handleDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryChromeDevtoolsResult, Data: result})
	if got := sink.count(); got != 1 {
		t.Fatalf("chromeDevtoolsResult broadcasts=%d want 1", got)
	}

	unknown := wmpf.DebugMessage{Category: "unknown-category", Data: []byte{0xff}}
	a.handleDebugMessage(unknown)
	if got := sink.count(); got != 1 {
		t.Fatalf("unknown category changed CDP broadcast count to %d", got)
	}
}

func TestAuditJsContextAddRemoveConnectSelectRouting(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	var contextEvents []ContextEvent
	a.SetObserver(Observer{OnContext: func(event ContextEvent) { contextEvents = append(contextEvents, event) }})
	add := func(category string, value wmpf.JsContext) {
		t.Helper()
		data, err := wmpf.EncodeCategory(category, value)
		if err != nil {
			t.Fatal(err)
		}
		a.handleDebugMessage(wmpf.DebugMessage{Category: category, Data: data})
	}
	add(wmpf.CategoryAddJsContext, wmpf.JsContext{ID: "ctx-a", Name: "main"})
	add(wmpf.CategoryAddJsContext, wmpf.JsContext{ID: "ctx-b", Name: "worker"})
	if selected, ok := a.Contexts.Selected(); !ok || selected.ID != "ctx-a" {
		t.Fatalf("first add selected=%+v ok=%v", selected, ok)
	}
	add(wmpf.CategoryConnectJsContext, wmpf.JsContext{ID: "ctx-b"})
	if selected, ok := a.Contexts.Selected(); !ok || selected.ID != "ctx-b" {
		t.Fatalf("connect selected=%+v ok=%v", selected, ok)
	}
	add(wmpf.CategoryRemoveJsContext, wmpf.JsContext{ID: "ctx-b"})
	if selected, ok := a.Contexts.Selected(); !ok || selected.ID != "ctx-a" {
		t.Fatalf("remove fallback selected=%+v ok=%v", selected, ok)
	}
	if event := contextEvents[len(contextEvents)-1]; event.Kind != "removed" || event.Context != (bridgecontext.Context{ID: "ctx-b", Target: "worker"}) {
		t.Fatalf("remove context event=%+v", event)
	}
	add(wmpf.CategoryConnectJsContext, wmpf.JsContext{ID: "ctx-new"})
	if selected, ok := a.Contexts.Selected(); !ok || selected.ID != "ctx-new" {
		t.Fatalf("unknown connect selected=%+v ok=%v", selected, ok)
	}
	if got, ok := a.Contexts.Get("ctx-new"); !ok || got.Target != "" {
		t.Fatalf("unknown connect context=%+v ok=%v", got, ok)
	}
}

func TestAuditReplayPreservesValidFrameOrderAcrossCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.bin")
	recorder, err := capture.Start(path)
	if err != nil {
		t.Fatal(err)
	}
	payloads := []string{`{"method":"Runtime.executionContextCreated"}`, `{"method":"Debugger.paused"}`}
	for _, payload := range payloads[:1] {
		frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryChromeDevtoolsResult, Data: wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: payload})})
		if err := recorder.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Write([]byte{0xff}); err != nil {
		t.Fatal(err)
	}
	frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryChromeDevtoolsResult, Data: wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: payloads[1]})})
	if err := recorder.Write(frame); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	a := New(0, 0, logging.New(false, false))
	sink := &categoryAuditClient{}
	a.CDPHub.Add(sink)
	if err := a.Replay(path); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for sink.count() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := sink.count(); got != 2 {
		t.Fatalf("replay valid frame count=%d want 2", got)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for i, want := range payloads {
		if string(sink.data[i]) != want {
			t.Fatalf("replay frame[%d]=%q want %q", i, sink.data[i], want)
		}
	}
}

func TestAuditCDPIDBoundaries(t *testing.T) {
	dp, cp := freePort(t), freePort(t)
	a := New(dp, cp, logging.New(false, false))
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.Close(ctx)
	}()
	debug := auditDial(t, dp)
	defer debug.Close()
	ownerDeadline := time.Now().Add(time.Second)
	for {
		a.connMu.Lock()
		ready := a.debugOwner != nil
		a.connMu.Unlock()
		if ready {
			break
		}
		if time.Now().After(ownerDeadline) {
			t.Fatal("upstream owner was not installed")
		}
		time.Sleep(time.Millisecond)
	}
	// The upstream connect self-bootstraps Runtime.enable; drain and answer it
	// so the pending assertions below count only controller requests.
	bootstrapFrame := auditRead(t, debug, websocket.BinaryMessage)
	bootstrapOuter, err := wmpf.DecodeDebugMessage(bootstrapFrame)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapChrome, err := wmpf.DecodeChrome(bootstrapOuter.Data)
	if err != nil {
		t.Fatal(err)
	}
	var bootstrapRequest struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal([]byte(bootstrapChrome.Payload), &bootstrapRequest); err != nil {
		t.Fatal(err)
	}
	if bootstrapChrome.JSContextID != "" || bootstrapRequest.Method != "Runtime.enable" {
		t.Fatalf("bootstrap chrome=%+v", bootstrapChrome)
	}
	bootstrapReply := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
		Category: wmpf.CategoryChromeDevtoolsResult,
		Data:     wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: `{"id":` + string(bootstrapRequest.ID) + `,"result":{}}`}),
	})
	if err := debug.WriteMessage(websocket.BinaryMessage, bootstrapReply); err != nil {
		t.Fatal(err)
	}
	deadlineBootstrap := time.Now().Add(time.Second)
	for a.Requests.Len() != 0 {
		if time.Now().After(deadlineBootstrap) {
			t.Fatalf("bootstrap request was not resolved: %d pending", a.Requests.Len())
		}
		time.Sleep(time.Millisecond)
	}
	a.Contexts.Upsert(bridgecontext.Context{ID: "audit-context"})
	a.Contexts.Select("audit-context")
	conn := auditDial(t, cp)
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"method":"Runtime.enable"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	if got := a.Requests.Len(); got != 0 {
		t.Fatalf("notification created pending request=%d", got)
	}
	for _, body := range []string{`{"id":7,"method":"Runtime.enable"}`, `{"id":7,"method":"Debugger.enable"}`} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for a.Requests.Len() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := a.Requests.Len(); got != 1 {
		t.Fatalf("duplicate id pending=%d want 1", got)
	}
	unknown, err := wmpf.EncodeCategory(wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: `{"id":999,"result":{}}`})
	if err != nil {
		t.Fatal(err)
	}
	a.handleDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryChromeDevtoolsResult, Data: unknown})
	if got := a.Requests.Len(); got != 1 {
		t.Fatalf("unknown response changed pending=%d", got)
	}
}
