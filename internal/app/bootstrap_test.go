package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

// dialDebugUpstream opens a real upstream WebSocket. Reconnects can transiently
// collide with owner release, so a failed dial is retried until the deadline.
func dialDebugUpstream(t *testing.T, port int) *websocket.Conn {
	t.Helper()
	endpoint := fmt.Sprintf("ws://127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("upstream dial failed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// readUpstreamBootstrapFrame reads the next frame the bridge sends to the
// upstream transport and asserts it is the automatic Runtime.enable bootstrap
// with an empty context route. It returns the request id echoed in the payload.
func readUpstreamBootstrapFrame(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	typ, frame, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.BinaryMessage {
		t.Fatalf("bootstrap frame type=%d want binary", typ)
	}
	outer, err := wmpf.DecodeDebugMessage(frame)
	if err != nil {
		t.Fatal(err)
	}
	if outer.Category != wmpf.CategoryChromeDevtools {
		t.Fatalf("bootstrap category=%q want %q", outer.Category, wmpf.CategoryChromeDevtools)
	}
	chrome, err := wmpf.DecodeChrome(outer.Data)
	if err != nil {
		t.Fatal(err)
	}
	if chrome.JSContextID != "" {
		t.Fatalf("bootstrap jscontext_id=%q want empty bootstrap route", chrome.JSContextID)
	}
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal([]byte(chrome.Payload), &request); err != nil {
		t.Fatal(err)
	}
	if request.Method != "Runtime.enable" {
		t.Fatalf("bootstrap method=%q want Runtime.enable", request.Method)
	}
	return string(request.ID)
}

// replyUpstreamCDP writes a CategoryChromeDevtoolsResult frame from the
// simulated upstream transport.
func replyUpstreamCDP(t *testing.T, conn *websocket.Conn, payload string) {
	t.Helper()
	frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
		Category: wmpf.CategoryChromeDevtoolsResult,
		Data:     wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: payload}),
	})
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamConnectAutoEnablesRuntimeDomain(t *testing.T) {
	a := New(freePort(t), freePort(t), logging.New(false, false))
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(context.Background()) })

	conn := dialDebugUpstream(t, a.DebugPort)
	defer conn.Close()
	if id := readUpstreamBootstrapFrame(t, conn); id != "1" {
		t.Fatalf("first bootstrap request id=%q want 1", id)
	}
	// The simulated miniapp answers the automatic enable so the shared
	// correlator resolves the unscoped bootstrap request.
	replyUpstreamCDP(t, conn, `{"id":1,"result":{}}`)
	waitFor(t, func() bool { return a.Requests.Len() == 0 }, "bootstrap request was not resolved")
}

func TestUpstreamReconnectReEnablesRuntimeDomain(t *testing.T) {
	a := New(freePort(t), freePort(t), logging.New(false, false))
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(context.Background()) })

	first := dialDebugUpstream(t, a.DebugPort)
	if id := readUpstreamBootstrapFrame(t, first); id != "1" {
		t.Fatalf("first bootstrap request id=%q want 1", id)
	}
	replyUpstreamCDP(t, first, `{"id":1,"result":{}}`)
	waitFor(t, func() bool { return a.Requests.Len() == 0 }, "first bootstrap request was not resolved")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return a.DebugClientCount() == 0 && !a.ConnectionSnapshot().UpstreamConnected
	}, "upstream owner was not released after disconnect")

	// A new upstream transport must be re-enabled: WMPF stops emitting context
	// events on a fresh connection until Runtime.enable arrives again.
	second := dialDebugUpstream(t, a.DebugPort)
	defer second.Close()
	if id := readUpstreamBootstrapFrame(t, second); id != "2" {
		t.Fatalf("reconnect bootstrap request id=%q want 2", id)
	}
	replyUpstreamCDP(t, second, `{"id":2,"result":{}}`)
	waitFor(t, func() bool { return a.Requests.Len() == 0 }, "reconnect bootstrap request was not resolved")
}

func TestBootstrapUpstreamDomainsSendsExactlyOncePerConnection(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	client := &appCaptureClient{}
	a.DebugHub.Add(client)

	a.bootstrapUpstreamDomains()
	if len(client.messages) != 1 {
		t.Fatalf("bootstrap frames=%d want 1", len(client.messages))
	}
	outer, err := wmpf.DecodeDebugMessage(client.messages[0])
	if err != nil {
		t.Fatal(err)
	}
	if outer.Category != wmpf.CategoryChromeDevtools {
		t.Fatalf("bootstrap category=%q", outer.Category)
	}
	chrome, err := wmpf.DecodeChrome(outer.Data)
	if err != nil {
		t.Fatal(err)
	}
	if chrome.JSContextID != "" || chrome.Payload != `{"id":1,"method":"Runtime.enable"}` {
		t.Fatalf("bootstrap chrome=%+v", chrome)
	}
	if a.Requests.Len() != 1 {
		t.Fatalf("bootstrap pending=%d want 1", a.Requests.Len())
	}
	// The automatic request resolves through the normal upstream response path.
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: `{"id":1,"result":{}}`}))
	if a.Requests.Len() != 0 {
		t.Fatalf("bootstrap request was not resolved: %d pending", a.Requests.Len())
	}

	// A closing app must not emit another enable.
	a.closing.Store(true)
	a.bootstrapUpstreamDomains()
	if len(client.messages) != 1 {
		t.Fatalf("closing app sent %d bootstrap frames", len(client.messages))
	}
	a.closing.Store(false)
	a.bootstrapUpstreamDomains()
	if len(client.messages) != 2 {
		t.Fatalf("second bootstrap frames=%d want 2", len(client.messages))
	}
	if a.Requests.Len() != 1 {
		t.Fatalf("second bootstrap pending=%d want 1", a.Requests.Len())
	}
}
