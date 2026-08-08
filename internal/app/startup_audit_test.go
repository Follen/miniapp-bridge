package app

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"miniapp-bridge/internal/logging"
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
