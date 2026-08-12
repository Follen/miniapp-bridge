package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/gorilla/websocket"
)

func TestCoverageGateCDPRequestFailsWithoutUpstream(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	a.Contexts.Upsert(bridgecontext.Context{ID: "ctx"})
	a.Contexts.Select("ctx")
	connection := newQueuedTestConnection(false)
	controller := newWSClient(connection, websocket.TextMessage)
	controller.network = true
	if !a.installOwner("cdp", controller) {
		t.Fatal("install controller")
	}
	if !a.handleCDPFrame(websocket.TextMessage, []byte(`{"id":"missing-upstream","method":"Runtime.enable"}`), controller) {
		t.Fatal("CDP frame was not handled")
	}
	if got := a.Requests.Len(); got != 0 {
		t.Fatalf("pending request leaked without upstream: %d", got)
	}
	waitFor(t, func() bool { return len(connection.snapshot()) == 1 }, "upstream absence error was not written")
	messages := connection.snapshot()
	if len(messages) != 1 {
		t.Fatalf("error response count=%d want 1", len(messages))
	}
	var envelope struct {
		ID    json.RawMessage `json:"id"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(messages[0], &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope.ID) != `"missing-upstream"` || envelope.Error.Message != ErrCDPUpstreamDisconnected.Error() {
		t.Fatalf("unexpected error response=%s", messages[0])
	}
	a.readCDP(controller)
}

func TestCoverageGateCDPRequestFailsWithoutContext(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	connection := newQueuedTestConnection(false)
	controller := newWSClient(connection, websocket.TextMessage)
	controller.network = true
	if !a.installOwner("cdp", controller) {
		t.Fatal("install controller")
	}
	if !a.handleCDPFrame(websocket.TextMessage, []byte(`{"id":7,"method":"Runtime.enable"}`), controller) {
		t.Fatal("CDP frame was not handled")
	}
	if got := a.Requests.Len(); got != 0 {
		t.Fatalf("pending request leaked without context: %d", got)
	}
	waitFor(t, func() bool { return len(connection.snapshot()) == 1 }, "context absence error was not written")
	messages := connection.snapshot()
	if len(messages) != 1 || !strings.Contains(string(messages[0]), ErrNoContext.Error()) {
		t.Fatalf("unexpected context error response=%q", messages)
	}
	a.readCDP(controller)
}

func TestCoverageGateCDPRouteErrorPayloadBranches(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	connection := newQueuedTestConnection(false)
	controller := newWSClient(connection, websocket.TextMessage)
	a.dispatchMu.Lock()
	a.sendCDPErrorForPayloadLocked(nil, `{"id":1}`, ErrNoContext)
	a.sendCDPErrorForPayloadLocked(controller, `not-json`, ErrNoContext)
	a.sendCDPErrorForPayloadLocked(controller, `{"method":"Runtime.enable"}`, ErrNoContext)
	a.sendCDPErrorForPayloadLocked(controller, `{"id":null}`, ErrNoContext)
	a.dispatchMu.Unlock()
	if got := len(connection.snapshot()); got != 0 {
		t.Fatalf("invalid route payloads produced %d responses", got)
	}
}

func TestCoverageGateOwnerSpecificMessageLimits(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	upstream := &wsClient{conn: &coverageWebsocketConnection{}, generation: 3}
	a.debugOwner = upstream
	a.debugGeneration = 3
	if a.handleDebugFrameForClient(upstream, websocket.BinaryMessage, []byte(strings.Repeat("x", int(websocketMaxMessageBytes)+1))) {
		t.Fatal("oversized owner-specific upstream frame was accepted")
	}

	cdpClient := &wsClient{conn: &coverageWebsocketConnection{}, generation: 4}
	a.cdpOwner = cdpClient
	a.cdpGeneration = 4
	if a.handleCDPFrame(websocket.TextMessage, []byte(strings.Repeat("x", int(websocketMaxMessageBytes)+1)), cdpClient) {
		t.Fatal("oversized owner-specific CDP frame was accepted")
	}
}

func TestCoverageGateUpgradeInstallOwnerRace(t *testing.T) {
	originalUpgrader := localUpgrader
	t.Cleanup(func() { localUpgrader = originalUpgrader })

	tests := []struct {
		name    string
		handler func(*App, http.ResponseWriter, *http.Request)
		origin  string
	}{
		{name: "upstream", handler: func(a *App, w http.ResponseWriter, r *http.Request) { a.handleDebugWebSocket(w, r) }},
		{name: "cdp", handler: func(a *App, w http.ResponseWriter, r *http.Request) { a.handleCDPWebSocket(w, r) }, origin: "http://127.0.0.1:62000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := New(0, 62000, logging.New(false, false))
			localUpgrader = originalUpgrader
			originChecked := make(chan struct{})
			localUpgrader.CheckOrigin = func(*http.Request) bool {
				a.closing.Store(true)
				close(originChecked)
				return true
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.handler(a, w, r)
			}))
			headers := http.Header{}
			if tc.origin != "" {
				headers.Set("Origin", tc.origin)
			}
			url := "ws" + strings.TrimPrefix(server.URL, "http")
			conn, _, err := websocket.DefaultDialer.Dial(url, headers)
			if conn != nil {
				_ = conn.Close()
			}
			select {
			case <-originChecked:
			case <-time.After(2 * time.Second):
				t.Fatal("websocket upgrade did not reach CheckOrigin")
			}
			server.Close()
			if err == nil && (a.debugOwner != nil || a.cdpOwner != nil) {
				t.Fatal("owner installed after closing race")
			}
			if a.debugOwner != nil || a.cdpOwner != nil {
				t.Fatal("owner installed after failed install")
			}
		})
	}
}
