package proxy

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

type scriptedListener struct {
	mu     sync.Mutex
	calls  int
	closed chan struct{}
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	l.calls++
	call := l.calls
	l.mu.Unlock()
	if call == 1 {
		return nil, errors.New("temporary accept failure")
	}
	<-l.closed
	return nil, net.ErrClosed
}
func (l *scriptedListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}
func (l *scriptedListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestCoverageListenerBranches(t *testing.T) {
	s := NewListener("127.0.0.1:0", nil)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err == nil {
		t.Fatal("duplicate start succeeded")
	}
	c, err := net.Dial("tcp", s.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("line\n"))
	_ = c.Close()
	time.Sleep(10 * time.Millisecond)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	fail := NewListener(occupied.Addr().String(), nil)
	if err := fail.Start(); err == nil {
		t.Fatal("occupied listener started")
	}

	scripted := &scriptedListener{closed: make(chan struct{})}
	recovering := NewListener("unused", nil)
	recovering.listen = func(string, string) (net.Listener, error) { return scripted, nil }
	if err := recovering.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		scripted.mu.Lock()
		calls := scripted.calls
		scripted.mu.Unlock()
		if calls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("accept did not retry")
		}
		time.Sleep(time.Millisecond)
	}
	if err := recovering.Close(); err != nil {
		t.Fatal(err)
	}

	free := func() string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := l.Addr().String()
		l.Close()
		return addr
	}
	a, b, err := startListeners(free(), free(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	b.Close()
	occupiedFirst, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if a, b, err := startListeners(occupiedFirst.Addr().String(), free(), nil, nil); err == nil || a != nil || b != nil {
		t.Fatal("first listener failure not returned")
	}
	occupiedFirst.Close()
	firstAddr := free()
	occupiedSecond, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if a, b, err := startListeners(firstAddr, occupiedSecond.Addr().String(), nil, nil); err == nil || a != nil || b != nil {
		t.Fatal("second listener failure not returned")
	}
	probe, err := net.Listen("tcp", firstAddr)
	if err != nil {
		t.Fatalf("first listener not rolled back: %v", err)
	}
	probe.Close()
	occupiedSecond.Close()

	// Exercise the fixed-address wrapper without depending on those ports being free.
	first, second, err := StartDefaultListeners(nil, nil)
	if err == nil {
		first.Close()
		second.Close()
	}
}
