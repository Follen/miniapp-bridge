package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
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

func TestCoverageGateCDPRequestBootstrapsWithoutContext(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	connection := newQueuedTestConnection(false)
	controller := newWSClient(connection, websocket.TextMessage)
	controller.network = true
	if !a.installOwner("cdp", controller) {
		t.Fatal("install controller")
	}
	upstreamOwner := &wsClient{generation: 1}
	a.debugOwner = upstreamOwner
	a.debugGeneration = upstreamOwner.generation
	upstream := &routeCaptureClient{}
	a.DebugHub.Add(upstream)
	if !a.handleCDPFrame(websocket.TextMessage, []byte(`{"id":7,"method":"Runtime.enable"}`), controller) {
		t.Fatal("CDP frame was not handled")
	}
	if got := a.Requests.Len(); got != 1 {
		t.Fatalf("bootstrap request pending=%d want 1", got)
	}
	if messages := connection.snapshot(); len(messages) != 0 {
		t.Fatalf("bootstrap request produced local error=%q", messages)
	}
	frames := upstream.snapshot()
	if len(frames) != 1 {
		t.Fatalf("bootstrap upstream frames=%d want 1", len(frames))
	}
	outer, err := wmpf.DecodeDebugMessage(frames[0])
	if err != nil {
		t.Fatal(err)
	}
	chrome, err := wmpf.DecodeChrome(outer.Data)
	if err != nil {
		t.Fatal(err)
	}
	if outer.Category != wmpf.CategoryChromeDevtools || chrome.JSContextID != "" || chrome.Payload != `{"id":7,"method":"Runtime.enable"}` {
		t.Fatalf("bootstrap frame category=%q chrome=%+v", outer.Category, chrome)
	}
	a.readCDP(controller)
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
	var upgraderMu sync.Mutex
	t.Cleanup(func() {
		upgraderMu.Lock()
		localUpgrader = originalUpgrader
		upgraderMu.Unlock()
	})

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
			upgraderMu.Lock()
			localUpgrader = originalUpgrader
			originChecked := make(chan struct{})
			localUpgrader.CheckOrigin = func(*http.Request) bool {
				a.closing.Store(true)
				close(originChecked)
				return true
			}
			upgraderMu.Unlock()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgraderMu.Lock()
				defer upgraderMu.Unlock()
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
