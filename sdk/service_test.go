package sdk

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

var sdkTestPorts = struct {
	sync.Mutex
	used map[int]struct{}
}{used: make(map[int]struct{})}

func sdkFreePort(t *testing.T) int {
	t.Helper()
	for {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		sdkTestPorts.Lock()
		_, reused := sdkTestPorts.used[port]
		if !reused {
			sdkTestPorts.used[port] = struct{}{}
		}
		sdkTestPorts.Unlock()
		if !reused {
			return port
		}
	}
}

type fakeNative struct {
	mu     sync.Mutex
	closed int
}

func (f *fakeNative) Close(context.Context) error { f.mu.Lock(); f.closed++; f.mu.Unlock(); return nil }

func TestServiceLifecycleIsIdempotentAndThreadSafe(t *testing.T) {
	fake := &fakeNative{}
	s, err := New(Options{DebugPort: sdkFreePort(t), CDPPort: sdkFreePort(t), Native: func(context.Context, func(LogEvent)) (NativeSession, error) { return fake, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Status().State; got != StateNew {
		t.Fatalf("initial state=%s", got)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := s.Start(context.Background()); err != nil {
				t.Errorf("repeat start: %v", err)
			}
		}()
	}
	group.Wait()
	if got := s.Status().State; got != StateRunning {
		t.Fatalf("running state=%s", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	group.Add(1)
	go func() { defer group.Done(); _ = s.Close(ctx) }()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	group.Wait()
	if got := s.Status().State; got != StateStopped {
		t.Fatalf("stopped state=%s", got)
	}
	if err := s.Start(context.Background()); err != ErrClosed {
		t.Fatalf("restart err=%v", err)
	}
	fake.mu.Lock()
	closed := fake.closed
	fake.mu.Unlock()
	if closed != 1 {
		t.Fatalf("native closes=%d want 1", closed)
	}
}

func TestServiceCloseBeforeStartAndSubscriptionCancellation(t *testing.T) {
	s, err := New(Options{DebugPort: sdkFreePort(t), CDPPort: sdkFreePort(t), SubscriberBuffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	logs := s.SubscribeLogs()
	if logs.Channel() == nil {
		t.Fatal("nil log subscription")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-logs.Channel(); ok {
		t.Fatal("subscription remained open")
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEventBusSlowSubscriberIsNonBlockingAndDisconnects(t *testing.T) {
	var bus eventBus[int]
	bus.size = 1
	sub := bus.subscribe()
	for i := 0; i < 10000; i++ {
		bus.publish(i)
	}
	select {
	case _, ok := <-sub.Channel():
		if !ok {
			t.Fatal("subscription closed before buffered event")
		}
		if _, ok := <-sub.Channel(); ok {
			t.Fatal("slow subscription remained open after buffered event")
		}
	case <-time.After(time.Second):
		t.Fatal("bounded publisher blocked")
	}
	if !errors.Is(sub.Err(), ErrSlowSubscriber) {
		t.Fatalf("subscription err=%v", sub.Err())
	}
	if err := sub.Close(); err != nil && !errors.Is(err, ErrSlowSubscriber) {
		t.Fatalf("close err=%v", err)
	}
}
