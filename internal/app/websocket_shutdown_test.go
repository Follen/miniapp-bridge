package app

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/gorilla/websocket"
)

type blockingWebSocketConnection struct {
	writeStarted chan struct{}
	closed       chan struct{}
	startOnce    sync.Once
	closeOnce    sync.Once
	closeCalls   atomic.Int32
	deadlineSet  atomic.Bool
}

func newBlockingWebSocketConnection() *blockingWebSocketConnection {
	return &blockingWebSocketConnection{
		writeStarted: make(chan struct{}),
		closed:       make(chan struct{}),
	}
}

func (c *blockingWebSocketConnection) ReadMessage() (int, []byte, error) {
	return 0, nil, net.ErrClosed
}

func (c *blockingWebSocketConnection) WriteMessage(int, []byte) error {
	c.startOnce.Do(func() { close(c.writeStarted) })
	<-c.closed
	return net.ErrClosed
}

func (c *blockingWebSocketConnection) WriteControl(int, []byte, time.Time) error {
	return nil
}

func (c *blockingWebSocketConnection) SetWriteDeadline(time.Time) error {
	c.deadlineSet.Store(true)
	return nil
}

func (c *blockingWebSocketConnection) Close() error {
	c.closeCalls.Add(1)
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func TestAppCloseInterruptsBlockedWebSocketWrite(t *testing.T) {
	conn := newBlockingWebSocketConnection()
	client := &wsClient{conn: conn, typeID: websocket.BinaryMessage}
	a := New(0, 0, logging.New(false, false))
	a.DebugHub.Add(client)

	sendDone := make(chan error, 1)
	go func() { sendDone <- client.Send([]byte("blocked")) }()

	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("WebSocket write did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	closeDone := make(chan error, 1)
	go func() { closeDone <- a.Close(ctx) }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("App.Close: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("App.Close did not interrupt the blocked WebSocket write: %v", ctx.Err())
	}

	select {
	case err := <-sendDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("blocked send error = %v, want net.ErrClosed", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("blocked WebSocket write was not interrupted by App.Close")
	}
	if !conn.deadlineSet.Load() {
		t.Fatal("WebSocket write deadline was not set")
	}
	if calls := conn.closeCalls.Load(); calls != 1 {
		t.Fatalf("underlying Close calls = %d, want 1", calls)
	}
	if count := a.DebugClientCount(); count != 0 {
		t.Fatalf("debug client count = %d, want 0", count)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("repeated client Close: %v", err)
	}
	if calls := conn.closeCalls.Load(); calls != 1 {
		t.Fatalf("underlying Close calls after repeated close = %d, want 1", calls)
	}
}
