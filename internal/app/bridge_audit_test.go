package app

import (
	"context"
	"fmt"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
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
	debug2 := auditDial(t, dp)
	defer debug2.Close()
	cdp1 := auditDial(t, cp)
	defer cdp1.Close()
	cdp2 := auditDial(t, cp)
	defer cdp2.Close()
	auditWaitForCDPClients(t, a, 2)

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
		for name, debug := range map[string]*websocket.Conn{"debug1": debug1, "debug2": debug2} {
			frame := auditRead(t, debug, websocket.BinaryMessage)
			outer, err := wmpf.DecodeDebugMessage(frame)
			if err != nil {
				t.Fatalf("%s decode outer: %v", name, err)
			}
			if outer.Seq != uint32(i+1) {
				t.Fatalf("%s seq=%d want %d", name, outer.Seq, i+1)
			}
			chrome, err := wmpf.DecodeChrome(outer.Data)
			if err != nil {
				t.Fatalf("%s decode chrome: %v", name, err)
			}
			if chrome.Payload != request || chrome.JSContextID != "ctx-audit" || chrome.OpID > 100 {
				t.Fatalf("%s chrome=%+v", name, chrome)
			}
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
	for name, client := range map[string]*websocket.Conn{"cdp1": cdp1, "cdp2": cdp2} {
		for i, want := range events {
			if got := string(auditRead(t, client, websocket.TextMessage)); got != want {
				t.Fatalf("%s message[%d]=%q want %q", name, i, got, want)
			}
		}
	}
	if a.Requests.Len() != 1 {
		t.Fatalf("pending requests=%d want 1", a.Requests.Len())
	}

	if err := cdp1.Close(); err != nil {
		t.Fatal(err)
	}
	auditWaitForCDPClients(t, a, 1)
	cdp1 = auditDial(t, cp)
	defer cdp1.Close()
	auditWaitForCDPClients(t, a, 2)
	payload := `{"method":"Debugger.paused","params":{}}`
	frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
		Category: wmpf.CategoryChromeDevtoolsResult,
		Data:     wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: payload}),
	})
	if err := debug1.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatal(err)
	}
	for name, client := range map[string]*websocket.Conn{"reconnected": cdp1, "continuous": cdp2} {
		if got := string(auditRead(t, client, websocket.TextMessage)); got != payload {
			t.Fatalf("%s got %q want %q", name, got, payload)
		}
	}
}
