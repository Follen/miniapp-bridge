package app

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/cdp"
	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/proxy"
	"github.com/gorilla/websocket"
)

type queuedTestConnection struct {
	block      bool
	started    chan struct{}
	release    chan struct{}
	closed     chan struct{}
	startOnce  sync.Once
	closeOnce  sync.Once
	closeCalls atomic.Int32
	mu         sync.Mutex
	messages   [][]byte
}

func newQueuedTestConnection(block bool) *queuedTestConnection {
	return &queuedTestConnection{
		block:   block,
		started: make(chan struct{}),
		release: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (*queuedTestConnection) ReadMessage() (int, []byte, error) { return 0, nil, net.ErrClosed }

func (c *queuedTestConnection) WriteMessage(_ int, message []byte) error {
	c.startOnce.Do(func() { close(c.started) })
	if c.block {
		select {
		case <-c.release:
		case <-c.closed:
			return net.ErrClosed
		}
	}
	c.mu.Lock()
	c.messages = append(c.messages, append([]byte(nil), message...))
	c.mu.Unlock()
	return nil
}

func (*queuedTestConnection) WriteControl(int, []byte, time.Time) error { return nil }
func (*queuedTestConnection) SetWriteDeadline(time.Time) error          { return nil }

func (c *queuedTestConnection) Close() error {
	c.closeCalls.Add(1)
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *queuedTestConnection) snapshot() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([][]byte, len(c.messages))
	for i := range c.messages {
		result[i] = append([]byte(nil), c.messages[i]...)
	}
	return result
}

func waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(message)
}

func TestWSClientQueueIsolatesSlowClientAndPreservesOrder(t *testing.T) {
	hub := proxy.NewHub()
	slowConnection := newQueuedTestConnection(true)
	fastConnection := newQueuedTestConnection(false)
	slow := &wsClient{conn: slowConnection, typeID: websocket.TextMessage, queueSize: 1}
	fast := &wsClient{conn: fastConnection, typeID: websocket.TextMessage, queueSize: 8}
	hub.Add(slow)
	hub.Add(fast)

	first := []byte("event-1")
	hub.Broadcast(first)
	first[0] = 'X'
	select {
	case <-slowConnection.started:
	case <-time.After(time.Second):
		t.Fatal("slow writer did not start")
	}

	hub.Broadcast([]byte("response-2"))
	started := time.Now()
	hub.Broadcast([]byte("event-3"))
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("overflow broadcast blocked for %v", elapsed)
	}
	waitFor(t, func() bool { return hub.Count() == 1 }, "overflowing client was not removed")
	waitFor(t, func() bool { return slowConnection.closeCalls.Load() == 1 }, "overflowing client was not closed")
	waitFor(t, func() bool { return len(fastConnection.snapshot()) == 3 }, "healthy client did not receive all messages")

	got := fastConnection.snapshot()
	want := []string{"event-1", "response-2", "event-3"}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Fatalf("message[%d]=%q want %q", i, got[i], want[i])
		}
	}
	hub.CloseAll()
}

func TestWSClientConcurrentCloseIsIdempotent(t *testing.T) {
	connection := newQueuedTestConnection(true)
	client := &wsClient{conn: connection, typeID: websocket.BinaryMessage, queueSize: 1}
	if err := client.Send([]byte("blocked")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-connection.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}

	const closers = 32
	var group sync.WaitGroup
	errorsSeen := make(chan error, closers)
	for i := 0; i < closers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			errorsSeen <- client.Close()
		}()
	}
	group.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("Close error=%v", err)
		}
	}
	if calls := connection.closeCalls.Load(); calls != 1 {
		t.Fatalf("transport Close calls=%d want 1", calls)
	}
	if err := client.Send([]byte("after close")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send after close error=%v", err)
	}
}

func TestAppRequestCancellationAndClear(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	a.Requests.Add(cdpRequest(1))
	a.Requests.Add(cdpRequest(2))
	if !a.CancelCDPRequest(1) || a.CancelCDPRequest(1) {
		t.Fatal("request cancellation did not remove exactly one request")
	}
	if cleared := a.ClearRequests(); cleared != 1 || a.Requests.Len() != 0 {
		t.Fatalf("cleared=%d pending=%d", cleared, a.Requests.Len())
	}
}

func TestAppPreservesExactJSONRequestIDForCancellation(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	a.Contexts.Upsert(bridgecontext.Context{ID: "exact"})
	a.Contexts.Select("exact")
	upstream := &appCaptureClient{}
	a.DebugHub.Add(upstream)
	defer a.DebugHub.Remove(upstream)

	if err := a.SendCDP([]byte(`{"id":18446744073709551615,"method":"Runtime.enable"}`)); err != nil {
		t.Fatal(err)
	}
	if a.Requests.Len() != 1 || !a.CancelCDPRequest(uint64(^uint64(0))) || a.Requests.Len() != 0 {
		t.Fatalf("exact request cancellation left pending=%d", a.Requests.Len())
	}
}

func TestAppDisconnectAndCloseClearRequests(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	a.Requests.Add(cdpRequest("disconnect"))
	a.readDebug(&wsClient{conn: &coverageWebsocketConnection{}})
	if pending := a.Requests.Len(); pending != 0 {
		t.Fatalf("pending after upstream disconnect=%d", pending)
	}

	a.Requests.Add(cdpRequest("close"))
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending := a.Requests.Len(); pending != 0 {
		t.Fatalf("pending after App.Close=%d", pending)
	}
}

func TestAppOnlyLastUpstreamDisconnectClearsRequests(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	first := &wsClient{conn: &coverageWebsocketConnection{}}
	second := &wsClient{conn: &coverageWebsocketConnection{}}
	a.DebugHub.Add(first)
	a.DebugHub.Add(second)
	a.Requests.Add(cdpRequest("still-live"))

	a.readDebug(first)
	if clients, pending := a.DebugClientCount(), a.Requests.Len(); clients != 1 || pending != 1 {
		t.Fatalf("after first disconnect clients=%d pending=%d want 1,1", clients, pending)
	}
	a.readDebug(second)
	if clients, pending := a.DebugClientCount(), a.Requests.Len(); clients != 0 || pending != 0 {
		t.Fatalf("after last disconnect clients=%d pending=%d want 0,0", clients, pending)
	}
}

func cdpRequest(id any) cdp.Request { return cdp.Request{ID: id, Method: "Runtime.enable"} }
