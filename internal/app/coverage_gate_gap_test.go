package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/gorilla/websocket"
)

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
			localUpgrader.CheckOrigin = func(*http.Request) bool {
				a.closing.Store(true)
				return true
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.handler(a, w, r)
			}))
			defer server.Close()
			headers := http.Header{}
			if tc.origin != "" {
				headers.Set("Origin", tc.origin)
			}
			url := "ws" + strings.TrimPrefix(server.URL, "http")
			conn, _, err := websocket.DefaultDialer.Dial(url, headers)
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil && (a.debugOwner != nil || a.cdpOwner != nil) {
				t.Fatal("owner installed after closing race")
			}
			if a.debugOwner != nil || a.cdpOwner != nil {
				t.Fatal("owner installed after failed install")
			}
		})
	}
}
