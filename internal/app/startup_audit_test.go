package app

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/gorilla/websocket"
)

func TestAuditStartRollsBackDebugListenerWhenCDPBindFails(t *testing.T) {
	t.Parallel()
	debugPort := freePort(t)
	occupied, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", freePort(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	cdpPort := occupied.Addr().(*net.TCPAddr).Port
	a := New(debugPort, cdpPort, logging.New(false, false))
	if err := a.Start(); err == nil {
		t.Fatal("Start succeeded with occupied CDP port")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", debugPort))
	if err != nil {
		t.Fatalf("debug listener was not rolled back: %v", err)
	}
	_ = listener.Close()
}

func TestAuditCloseTerminatesUpgradedWebSockets(t *testing.T) {
	debugPort, cdpPort := freePort(t), freePort(t)
	a := New(debugPort, cdpPort, logging.New(false, false))
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", debugPort), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The bridge self-bootstraps Runtime.enable on upstream connect; consume
	// that frame before asserting the transport terminates on App.Close.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("bootstrap frame was not received: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("WebSocket remained open after App.Close")
	} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatal("WebSocket remained open after App.Close (read timed out)")
	}
}
