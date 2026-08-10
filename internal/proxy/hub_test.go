package proxy

import (
	"net"
	"testing"
	"time"
)

type tc struct {
	n    int
	fail bool
}

func (c *tc) Send([]byte) error {
	c.n++
	if c.fail {
		return net.ErrClosed
	}
	return nil
}
func (c *tc) Close() error { return nil }
func TestHub(t *testing.T) {
	h := NewHub()
	a, b := &tc{}, &tc{fail: true}
	h.Add(a)
	h.Add(b)
	h.Broadcast([]byte("x"))
	if a.n != 1 || h.Count() != 1 {
		t.Fatalf("%d %d", a.n, h.Count())
	}
}

func TestHubRemove(t *testing.T) {
	h := NewHub()
	client := &tc{}
	h.Add(client)
	h.Remove(client)
	if count := h.Count(); count != 0 {
		t.Fatalf("clients=%d want 0", count)
	}
}
func TestListener(t *testing.T) {
	l := NewListener("127.0.0.1:0", func(c net.Conn) { c.Close() })
	if err := l.Start(); err != nil {
		t.Fatal(err)
	}
	addr := l.ln.Addr().String()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	time.Sleep(10 * time.Millisecond)
	l.Close()
}
