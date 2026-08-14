package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

func sendContext(t *testing.T, upstream *websocket.Conn, category, id string) {
	t.Helper()
	data, err := wmpf.EncodeCategory(category, wmpf.JsContext{ID: id, Name: id})
	if err != nil {
		t.Fatal(err)
	}
	frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: category, Data: data})
	if err := upstream.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatal(err)
	}
}

func readOutgoingChrome(t *testing.T, upstream *websocket.Conn) (wmpf.DebugMessage, wmpf.ChromeDevtools) {
	t.Helper()
	frame := auditRead(t, upstream, websocket.BinaryMessage)
	outer, err := wmpf.DecodeDebugMessage(frame)
	if err != nil {
		t.Fatal(err)
	}
	chrome, err := wmpf.DecodeChrome(outer.Data)
	if err != nil {
		t.Fatal(err)
	}
	return outer, chrome
}

func TestSimulatedEndToEndCDPMatrix(t *testing.T) {
	dp, cp := freePort(t), freePort(t)
	a := New(dp, cp, logging.New(false, false))
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.Close(ctx)
	})
	upstream := auditDial(t, dp)
	defer upstream.Close()
	// The bridge self-bootstraps Runtime.enable on upstream connect. Drain and
	// answer it (waiting for the broadcast) before any CDP client connects.
	bootstrapOuter, bootstrapChrome := readOutgoingChrome(t, upstream)
	if bootstrapOuter.Seq != 1 || bootstrapChrome.JSContextID != "" || !strings.Contains(bootstrapChrome.Payload, "Runtime.enable") {
		t.Fatalf("bootstrap outer=%+v chrome=%+v", bootstrapOuter, bootstrapChrome)
	}
	var bootstrapRequest struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal([]byte(bootstrapChrome.Payload), &bootstrapRequest); err != nil || bootstrapRequest.Method != "Runtime.enable" {
		t.Fatalf("bootstrap payload=%q err=%v", bootstrapChrome.Payload, err)
	}
	if err := upstream.WriteMessage(websocket.BinaryMessage, wmpf.EncodeDebugMessage(wmpf.DebugMessage{
		Category: wmpf.CategoryChromeDevtoolsResult,
		Data:     wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: `{"id":` + string(bootstrapRequest.ID) + `,"result":{}}`}),
	})); err != nil {
		t.Fatal(err)
	}
	deadlineBootstrap := time.Now().Add(time.Second)
	for a.Requests.Len() != 0 {
		if time.Now().After(deadlineBootstrap) {
			t.Fatalf("bootstrap request was not resolved: %d pending", a.Requests.Len())
		}
		time.Sleep(time.Millisecond)
	}
	clientA := auditDial(t, cp)
	defer clientA.Close()
	auditRejectedDial(t, cp, "owner_exists")
	auditWaitForCDPClients(t, a, 1)

	sendContext(t, upstream, wmpf.CategoryAddJsContext, "ctx-main")
	sendContext(t, upstream, wmpf.CategoryAddJsContext, "ctx-worker")
	sendContext(t, upstream, wmpf.CategoryConnectJsContext, "ctx-main")
	deadline := time.Now().Add(time.Second)
	for {
		selected, ok := a.Contexts.Selected()
		if ok && selected.ID == "ctx-main" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("main context not selected")
		}
		time.Sleep(time.Millisecond)
	}

	commands := []string{"Runtime.enable", "Debugger.enable", "Page.enable", "DOM.enable", "Network.enable", "Console.enable", "Performance.enable"}
	for i, method := range commands {
		request := fmt.Sprintf(`{"id":%d,"method":"%s","params":{"matrix":true}}`, i+1, method)
		if err := clientA.WriteMessage(websocket.TextMessage, []byte(request)); err != nil {
			t.Fatal(err)
		}
		outer, chrome := readOutgoingChrome(t, upstream)
		if outer.Seq != uint32(i+2) || outer.Category != wmpf.CategoryChromeDevtools || chrome.Payload != request || chrome.JSContextID != "ctx-main" {
			t.Fatalf("command %s outer=%+v chrome=%+v", method, outer, chrome)
		}
	}

	longValue := strings.Repeat("x", 256<<10)
	notification := `{"method":"Runtime.runIfWaitingForDebugger","params":{"value":"` + longValue + `"}}`
	if err := clientA.WriteMessage(websocket.TextMessage, []byte(notification)); err != nil {
		t.Fatal(err)
	}
	_, longChrome := readOutgoingChrome(t, upstream)
	if longChrome.Payload != notification || longChrome.JSContextID != "ctx-main" {
		t.Fatal("long notification changed in transit")
	}
	if got := a.Requests.Len(); got != len(commands) {
		t.Fatalf("notification affected pending count=%d", got)
	}

	sendContext(t, upstream, wmpf.CategoryConnectJsContext, "ctx-worker")
	deadline = time.Now().Add(time.Second)
	for {
		selected, ok := a.Contexts.Selected()
		if ok && selected.ID == "ctx-worker" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker context not selected")
		}
		time.Sleep(time.Millisecond)
	}
	workerRequest := `{"id":"worker-1","method":"Runtime.evaluate","params":{"expression":"throw new Error('matrix')"}}`
	if err := clientA.WriteMessage(websocket.BinaryMessage, []byte(workerRequest)); err != nil {
		t.Fatal(err)
	}
	_, workerChrome := readOutgoingChrome(t, upstream)
	if workerChrome.Payload != workerRequest || workerChrome.JSContextID != "ctx-worker" {
		t.Fatalf("worker route=%+v", workerChrome)
	}

	payloads := []string{
		`{"method":"Runtime.executionContextCreated","params":{"context":{"id":11}}}`,
		`{"method":"Runtime.consoleAPICalled","params":{"type":"log"}}`,
		`{"method":"Debugger.scriptParsed","params":{"scriptId":"1"}}`,
		`{"method":"Debugger.paused","params":{"callFrames":[]}}`,
		`{"id":"worker-1","error":{"code":-32000,"message":"matrix error"}}`,
		`{"id":99999,"result":{"unknown":true}}`,
	}
	for _, payload := range payloads {
		frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryChromeDevtoolsResult, Data: wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: payload})})
		if err := upstream.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range payloads {
		if got := string(auditRead(t, clientA, websocket.TextMessage)); got != want {
			t.Fatalf("broadcast[%d]=%q want %q", i, got, want)
		}
	}
	if got := a.Requests.Len(); got != len(commands) {
		t.Fatalf("response correlation pending=%d", got)
	}

	if err := upstream.WriteMessage(websocket.BinaryMessage, []byte{0xff}); err != nil {
		t.Fatal(err)
	}
	clientA.Close()
	auditWaitForCDPClients(t, a, 0)
	clientB := auditDial(t, cp)
	defer clientB.Close()
	auditWaitForCDPClients(t, a, 1)
	reconnectEvent := `{"method":"Network.loadingFinished","params":{"requestId":"r1"}}`
	frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryChromeDevtoolsResult, Data: wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: reconnectEvent})})
	if err := upstream.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	body := auditRead(t, clientB, websocket.TextMessage)
	if err := json.Unmarshal(body, &env); err != nil || env["method"] != "Network.loadingFinished" {
		t.Fatalf("reconnect event=%s err=%v", body, err)
	}
}
