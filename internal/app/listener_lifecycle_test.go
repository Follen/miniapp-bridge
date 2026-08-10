package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/logging"
)

type closeSignalListener struct {
	net.Listener
	closed chan struct{}
	once   sync.Once
}

func (listener *closeSignalListener) Close() error {
	err := listener.Listener.Close()
	listener.once.Do(func() { close(listener.closed) })
	return err
}

func TestCloseReleasesListenersBeforeServeStartsAndWaitsForServe(t *testing.T) {
	debugPort, cdpPort := freePort(t), freePort(t)
	bridge := New(debugPort, cdpPort, logging.New(false, false))

	var listenersMu sync.Mutex
	var listeners []*closeSignalListener
	bridge.listen = func(network, address string) (net.Listener, error) {
		underlying, err := net.Listen(network, address)
		if err != nil {
			return nil, err
		}
		listener := &closeSignalListener{Listener: underlying, closed: make(chan struct{})}
		listenersMu.Lock()
		listeners = append(listeners, listener)
		listenersMu.Unlock()
		return listener, nil
	}

	serveEntered := make(chan struct{}, 2)
	releaseServe := make(chan struct{})
	bridge.serve = func(server *http.Server, listener net.Listener) error {
		serveEntered <- struct{}{}
		<-releaseServe
		return server.Serve(listener)
	}
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		<-serveEntered
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- bridge.Close(context.Background()) }()

	listenersMu.Lock()
	owned := append([]*closeSignalListener(nil), listeners...)
	listenersMu.Unlock()
	if len(owned) != 2 {
		t.Fatalf("owned listener count=%d, want 2", len(owned))
	}
	for _, listener := range owned {
		select {
		case <-listener.closed:
		case <-time.After(time.Second):
			t.Fatal("Close did not synchronously release an owned listener")
		}
	}
	for _, port := range []int{debugPort, cdpPort} {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("listener %d was not released: %v", port, err)
		}
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before Serve goroutines exited: %v", err)
	default:
	}

	close(releaseServe)
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
}

func TestStartRejectsRepeatedAndPostCloseCalls(t *testing.T) {
	debugPort, cdpPort := freePort(t), freePort(t)
	bridge := New(debugPort, cdpPort, logging.New(false, false))
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Start(); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("repeated Start error=%v, want ErrAlreadyStarted", err)
	}
	if err := bridge.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Start(); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-Close Start error=%v, want ErrClosed", err)
	}
}

func TestCloseWinsAgainstStartWaitingForServerLock(t *testing.T) {
	debugPort, cdpPort := freePort(t), freePort(t)
	bridge := New(debugPort, cdpPort, logging.New(false, false))
	bridge.serverMu.Lock()
	startDone := make(chan error, 1)
	go func() { startDone <- bridge.Start() }()
	bridge.closing.Store(true)
	bridge.serverMu.Unlock()
	if err := <-startDone; !errors.Is(err, ErrClosed) {
		t.Fatalf("blocked Start error=%v, want ErrClosed", err)
	}
	if err := bridge.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{debugPort, cdpPort} {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("port %d was not released: %v", port, err)
		}
		_ = listener.Close()
	}
}
