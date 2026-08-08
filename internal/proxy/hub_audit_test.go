package proxy

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type auditClient struct {
	mu       sync.Mutex
	messages []string
	closed   int
	fail     bool
}

type auditBlockingClient struct {
	entered chan struct{}
	release chan struct{}
	got     chan struct{}
}

func (c *auditBlockingClient) Send([]byte) error {
	select {
	case <-c.entered:
	default:
		close(c.entered)
	}
	<-c.release
	select {
	case <-c.got:
	default:
		close(c.got)
	}
	return nil
}
func (c *auditBlockingClient) Close() error { return nil }

func (c *auditClient) Send(message []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("send failed")
	}
	c.messages = append(c.messages, string(message))
	return nil
}

func (c *auditClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
	return nil
}

func (c *auditClient) snapshot() ([]string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.messages...), c.closed
}

func TestAuditHubBroadcastOrderAndFailedClientRemoval(t *testing.T) {
	h := NewHub()
	first := &auditClient{}
	second := &auditClient{}
	failed := &auditClient{fail: true}
	h.Add(first)
	h.Add(second)
	h.Add(failed)

	for _, message := range []string{"event-1", "response-2", "event-3"} {
		h.Broadcast([]byte(message))
	}
	want := []string{"event-1", "response-2", "event-3"}
	for name, client := range map[string]*auditClient{"first": first, "second": second} {
		got, closed := client.snapshot()
		if len(got) != len(want) {
			t.Fatalf("%s messages=%v want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s message[%d]=%q want %q", name, i, got[i], want[i])
			}
		}
		if closed != 0 {
			t.Fatalf("%s unexpectedly closed %d times", name, closed)
		}
	}
	if _, closed := failed.snapshot(); closed != 1 {
		t.Fatalf("failed client closed=%d want 1", closed)
	}
	if h.Count() != 2 {
		t.Fatalf("clients=%d want 2", h.Count())
	}
}

func TestAuditReconnectorStateResetsOnlyOnSuccessfulConnect(t *testing.T) {
	var r Reconnector
	if r.Connected() {
		t.Fatal("zero reconnector is connected")
	}
	if got := r.NextAttempt(); got != 1 {
		t.Fatalf("attempt=%d want 1", got)
	}
	if got := r.NextAttempt(); got != 2 {
		t.Fatalf("attempt=%d want 2", got)
	}
	r.MarkConnected()
	if !r.Connected() {
		t.Fatal("connected state not recorded")
	}
	r.MarkDisconnected()
	if r.Connected() {
		t.Fatal("disconnected state not recorded")
	}
	if got := r.NextAttempt(); got != 1 {
		t.Fatalf("attempt after successful connection=%d want 1", got)
	}
}

func TestAuditHubSlowClientRetainsOrderedDelivery(t *testing.T) {
	h := NewHub()
	slow := &auditBlockingClient{entered: make(chan struct{}), release: make(chan struct{}), got: make(chan struct{})}
	fast := &auditClient{}
	h.Add(slow)
	h.Add(fast)
	done := make(chan struct{})
	go func() {
		h.Broadcast([]byte("slow-event"))
		close(done)
	}()
	select {
	case <-slow.entered:
	case <-time.After(time.Second):
		t.Fatal("slow client was not entered")
	}
	close(slow.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("broadcast did not complete after slow client released")
	}
	got, _ := fast.snapshot()
	if len(got) != 1 || got[0] != "slow-event" {
		t.Fatalf("fast client messages=%v", got)
	}
}

func TestAuditHubCloseAllClearsAndClosesClients(t *testing.T) {
	h := NewHub()
	first, second := &auditClient{}, &auditClient{}
	h.Add(first)
	h.Add(second)
	h.CloseAll()
	if h.Count() != 0 {
		t.Fatalf("clients after CloseAll=%d", h.Count())
	}
	_, firstClosed := first.snapshot()
	_, secondClosed := second.snapshot()
	if firstClosed != 1 || secondClosed != 1 {
		t.Fatalf("close counts=%d,%d want 1,1", firstClosed, secondClosed)
	}
	h.CloseAll()
	_, firstClosed = first.snapshot()
	if firstClosed != 1 {
		t.Fatalf("second CloseAll reclosed client=%d", firstClosed)
	}
}
