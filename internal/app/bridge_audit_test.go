package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func auditDial(t *testing.T, port int) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", port), nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func auditRejectedDial(t *testing.T, port int, wantCode string) {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", port), nil)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("second WebSocket connection unexpectedly succeeded")
	}
	if err == nil || response == nil || response.StatusCode != http.StatusConflict {
		t.Fatalf("rejected dial err=%v status=%v", err, response)
	}
	defer response.Body.Close()
	var body rejectionBody
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != wantCode {
		t.Fatalf("rejection code=%q want %q", body.Error.Code, wantCode)
	}
}

func auditRead(t *testing.T, conn *websocket.Conn, messageType int) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	typ, body, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if typ != messageType {
		t.Fatalf("message type=%d want %d", typ, messageType)
	}
	return body
}

func auditWaitForCDPClients(t *testing.T, app *App, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for app.CDPClientCount() != want {
		if time.Now().After(deadline) {
			t.Fatalf("CDP client count=%d want %d", app.CDPClientCount(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAuditBridgeBroadcastOrderContextRoutingReconnectAndCorruptRecovery(t *testing.T) {
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

	debug1 := auditDial(t, dp)
	defer debug1.Close()
	auditRejectedDial(t, dp, "owner_exists")
	// The bridge self-bootstraps the Runtime domain on every upstream connect.
	// Drain and answer that frame (and wait for its broadcast) before the CDP
	// controller connects, so no unexpected frame reaches cdp1 later.
	bootstrap := auditRead(t, debug1, websocket.BinaryMessage)
	outer, err := wmpf.DecodeDebugMessage(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	chrome, err := wmpf.DecodeChrome(outer.Data)
	if err != nil {
		t.Fatal(err)
	}
	var bootstrapRequest struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal([]byte(chrome.Payload), &bootstrapRequest); err != nil {
		t.Fatal(err)
	}
	if chrome.JSContextID != "" || bootstrapRequest.Method != "Runtime.enable" {
		t.Fatalf("bootstrap chrome=%+v request=%+v", chrome, bootstrapRequest)
	}
	bootstrapReply := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
		Category: wmpf.CategoryChromeDevtoolsResult,
		Data:     wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: `{"id":` + string(bootstrapRequest.ID) + `,"result":{}}`}),
	})
	if err := debug1.WriteMessage(websocket.BinaryMessage, bootstrapReply); err != nil {
		t.Fatal(err)
	}
	deadlineBootstrap := time.Now().Add(2 * time.Second)
	for a.Requests.Len() != 0 {
		if time.Now().After(deadlineBootstrap) {
			t.Fatalf("bootstrap request was not resolved: %d pending", a.Requests.Len())
		}
		time.Sleep(time.Millisecond)
	}
	cdp1 := auditDial(t, cp)
	defer cdp1.Close()
	auditRejectedDial(t, cp, "owner_exists")
	auditWaitForCDPClients(t, a, 1)

	contextData, err := wmpf.EncodeCategory(wmpf.CategoryConnectJsContext, wmpf.JsContext{ID: "ctx-audit"})
	if err != nil {
		t.Fatal(err)
	}
	contextFrame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryConnectJsContext, Data: contextData})
	if err := debug1.WriteMessage(websocket.BinaryMessage, contextFrame); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if selected, ok := a.Contexts.Selected(); ok && selected.ID == "ctx-audit" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connectJsContext did not select its context")
		}
		time.Sleep(time.Millisecond)
	}

	requests := []string{
		`{"id":101,"method":"Runtime.enable"}`,
		`{"id":"debug-2","method":"Debugger.enable"}`,
	}
	for i, request := range requests {
		if err := cdp1.WriteMessage(websocket.TextMessage, []byte(request)); err != nil {
			t.Fatal(err)
		}
		frame := auditRead(t, debug1, websocket.BinaryMessage)
		outer, err := wmpf.DecodeDebugMessage(frame)
		if err != nil {
			t.Fatalf("decode outer: %v", err)
		}
		if outer.Seq != uint32(i+2) {
			t.Fatalf("seq=%d want %d", outer.Seq, i+2)
		}
		chrome, err = wmpf.DecodeChrome(outer.Data)
		if err != nil {
			t.Fatalf("decode chrome: %v", err)
		}
		if chrome.Payload != request || chrome.JSContextID != "ctx-audit" || chrome.OpID > 100 {
			t.Fatalf("chrome=%+v", chrome)
		}
	}

	if err := debug1.WriteMessage(websocket.BinaryMessage, []byte{0xff}); err != nil {
		t.Fatal(err)
	}
	events := []string{
		`{"method":"Runtime.executionContextCreated","params":{"context":{"id":1}}}`,
		`{"id":101,"error":{"code":-32000,"message":"boom","data":{"detail":1}}}`,
	}
	for _, payload := range events {
		frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
			Category: wmpf.CategoryChromeDevtoolsResult,
			Data:     wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: payload}),
		})
		if err := debug1.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range events {
		if got := string(auditRead(t, cdp1, websocket.TextMessage)); got != want {
			t.Fatalf("message[%d]=%q want %q", i, got, want)
		}
	}
	if a.Requests.Len() != 1 {
		t.Fatalf("pending requests=%d want 1", a.Requests.Len())
	}

	if err := cdp1.Close(); err != nil {
		t.Fatal(err)
	}
	auditWaitForCDPClients(t, a, 0)
	if a.Requests.Len() != 0 {
		t.Fatalf("pending requests survived controller disconnect: %d", a.Requests.Len())
	}
	cdp1 = auditDial(t, cp)
	defer cdp1.Close()
	auditWaitForCDPClients(t, a, 1)
	payload := `{"method":"Debugger.paused","params":{}}`
	frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
		Category: wmpf.CategoryChromeDevtoolsResult,
		Data:     wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: payload}),
	})
	if err := debug1.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatal(err)
	}
	if got := string(auditRead(t, cdp1, websocket.TextMessage)); got != payload {
		t.Fatalf("reconnected got %q want %q", got, payload)
	}
	snapshot := a.ConnectionSnapshot()
	if snapshot.UpstreamGeneration != 1 || snapshot.CDPGeneration != 2 || snapshot.RejectedUpstream != 1 || snapshot.RejectedCDP != 1 {
		t.Fatalf("connection snapshot=%+v", snapshot)
	}
}
