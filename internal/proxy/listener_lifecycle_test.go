package proxy

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type listenerTestError struct {
	message   string
	temporary bool
	timeout   bool
}

func (err *listenerTestError) Error() string   { return err.message }
func (err *listenerTestError) Temporary() bool { return err.temporary }
func (err *listenerTestError) Timeout() bool   { return err.timeout }

type channelListener struct {
	accepted   chan net.Conn
	closed     chan struct{}
	closeOnce  sync.Once
	closeCalls atomic.Int32
	closeErr   error
}

func newChannelListener(closeErr error) *channelListener {
	return &channelListener{
		accepted: make(chan net.Conn),
		closed:   make(chan struct{}),
		closeErr: closeErr,
	}
}

func (listener *channelListener) Accept() (net.Conn, error) {
	select {
	case conn := <-listener.accepted:
		return conn, nil
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func (listener *channelListener) Close() error {
	listener.closeCalls.Add(1)
	listener.closeOnce.Do(func() { close(listener.closed) })
	return listener.closeErr
}

func (*channelListener) Addr() net.Addr { return &net.TCPAddr{} }

type errorListener struct {
	acceptErr error
	closeErr  error
}

func (listener *errorListener) Accept() (net.Conn, error) { return nil, listener.acceptErr }
func (listener *errorListener) Close() error              { return listener.closeErr }
func (*errorListener) Addr() net.Addr                     { return &net.TCPAddr{} }

func TestListenerCloseWaitsForHandlersAndIsConcurrentSafe(t *testing.T) {
	wantCloseErr := errors.New("listener close failed")
	underlying := newChannelListener(wantCloseErr)
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	listener := NewListener("unused", func(conn net.Conn) {
		close(handlerStarted)
		<-releaseHandler
		_ = conn.Close()
	})
	listener.listen = func(string, string) (net.Listener, error) { return underlying, nil }
	if err := listener.Start(); err != nil {
		t.Fatal(err)
	}

	server, client := net.Pipe()
	defer client.Close()
	underlying.accepted <- server
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	const closers = 8
	results := make(chan error, closers)
	for range closers {
		go func() { results <- listener.Close() }()
	}
	select {
	case err := <-results:
		t.Fatalf("Close returned before handler completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseHandler)
	for range closers {
		select {
		case err := <-results:
			if !errors.Is(err, wantCloseErr) {
				t.Fatalf("Close error=%v, want %v", err, wantCloseErr)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent Close did not return")
		}
	}
	if calls := underlying.closeCalls.Load(); calls != 1 {
		t.Fatalf("listener Close calls=%d, want 1", calls)
	}
	if err := listener.Close(); !errors.Is(err, wantCloseErr) {
		t.Fatalf("repeated Close error=%v, want %v", err, wantCloseErr)
	}
}

func TestListenerReportsPermanentAcceptFailureAndReturnsItFromClose(t *testing.T) {
	wantAcceptErr := &listenerTestError{message: "permanent accept failed"}
	wantCloseErr := errors.New("close after accept failed")
	underlying := &errorListener{acceptErr: wantAcceptErr, closeErr: wantCloseErr}
	listener := NewListener("unused", nil)
	listener.listen = func(string, string) (net.Listener, error) { return underlying, nil }
	if err := listener.Start(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-listener.Errors():
		if !errors.Is(err, wantAcceptErr) {
			t.Fatalf("reported error=%v, want %v", err, wantAcceptErr)
		}
	case <-time.After(time.Second):
		t.Fatal("accept error was not reported")
	}
	select {
	case _, ok := <-listener.Errors():
		if ok {
			t.Fatal("error stream remained open after accept loop exited")
		}
	case <-time.After(time.Second):
		t.Fatal("error stream did not close")
	}
	if err := listener.Close(); !errors.Is(err, wantAcceptErr) || !errors.Is(err, wantCloseErr) {
		t.Fatalf("Close error=%v, want both accept and close causes", err)
	}
}

func TestListenerRetriesTransientAcceptFailureAndReportsIt(t *testing.T) {
	transient := &listenerTestError{message: "temporary accept failed", temporary: true}
	underlying := newChannelListener(nil)
	var calls atomic.Int32
	listener := NewListener("unused", func(conn net.Conn) { _ = conn.Close() })
	listener.listen = func(string, string) (net.Listener, error) {
		return &acceptSequenceListener{
			firstErr: transient,
			next:     underlying,
			calls:    &calls,
		}, nil
	}
	if err := listener.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-listener.Errors():
		if !errors.Is(err, transient) {
			t.Fatalf("reported error=%v, want %v", err, transient)
		}
	case <-time.After(time.Second):
		t.Fatal("transient error was not reported")
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("accept loop did not retry transient error")
		}
		time.Sleep(time.Millisecond)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close returned recovered transient error: %v", err)
	}
}

func TestListenerCloseCancelsAcceptRetryBackoff(t *testing.T) {
	transient := &listenerTestError{message: "temporary accept failed", temporary: true}
	underlying := newChannelListener(nil)
	listener := NewListener("unused", nil)
	listener.listen = func(string, string) (net.Listener, error) {
		return &acceptSequenceListener{
			firstErr: transient,
			next:     underlying,
			calls:    new(atomic.Int32),
		}, nil
	}
	if err := listener.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-listener.Errors():
	case <-time.After(time.Second):
		t.Fatal("transient error was not reported")
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close returned canceled retry error: %v", err)
	}
}

type acceptSequenceListener struct {
	firstErr error
	next     net.Listener
	calls    *atomic.Int32
}

func (listener *acceptSequenceListener) Accept() (net.Conn, error) {
	if listener.calls.Add(1) == 1 {
		return nil, listener.firstErr
	}
	return listener.next.Accept()
}

func (listener *acceptSequenceListener) Close() error   { return listener.next.Close() }
func (listener *acceptSequenceListener) Addr() net.Addr { return listener.next.Addr() }

func TestListenerLifecycleHelperBranches(t *testing.T) {
	timeoutErr := &listenerTestError{message: "timeout", timeout: true}
	permanentErr := &listenerTestError{message: "permanent"}
	if !retryableAcceptError(errors.New("unclassified")) {
		t.Fatal("unclassified custom errors must retain retry compatibility")
	}
	if !retryableAcceptError(timeoutErr) {
		t.Fatal("timeout error was not retryable")
	}
	if retryableAcceptError(permanentErr) {
		t.Fatal("permanent net.Error was retryable")
	}
	if retryableAcceptError(net.ErrClosed) {
		t.Fatal("closed listener error was retryable")
	}
	if got := nextAcceptRetryDelay(0); got != initialAcceptRetryDelay {
		t.Fatalf("initial delay=%s", got)
	}
	if got := nextAcceptRetryDelay(initialAcceptRetryDelay); got != 2*initialAcceptRetryDelay {
		t.Fatalf("doubled delay=%s", got)
	}
	if got := nextAcceptRetryDelay(maximumAcceptRetryDelay); got != maximumAcceptRetryDelay {
		t.Fatalf("capped delay=%s", got)
	}

	listener := NewListener("unused", nil)
	for i := 0; i <= listenerErrorBuffer; i++ {
		listener.reportAcceptError(errors.New("queued accept failure"))
	}
	for i := 0; i < listenerErrorBuffer; i++ {
		if err := <-listener.Errors(); err == nil {
			t.Fatal("buffered error was nil")
		}
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := listener.Start(); !errors.Is(err, ErrListenerClosed) {
		t.Fatalf("post-Close Start error=%v, want %v", err, ErrListenerClosed)
	}

	closedUnderlying := newChannelListener(net.ErrClosed)
	closedListener := NewListener("unused", nil)
	closedListener.listen = func(string, string) (net.Listener, error) { return closedUnderlying, nil }
	if err := closedListener.Start(); err != nil {
		t.Fatal(err)
	}
	if err := closedListener.Close(); err != nil {
		t.Fatalf("normal net.ErrClosed close error was returned: %v", err)
	}
}
