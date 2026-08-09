package app

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type coverageWebsocketConnection struct {
	deadlineErr error
	writes      atomic.Int32
}

func (*coverageWebsocketConnection) ReadMessage() (int, []byte, error) {
	return 0, nil, errors.New("unused")
}

func (c *coverageWebsocketConnection) WriteMessage(int, []byte) error {
	c.writes.Add(1)
	return nil
}

func (*coverageWebsocketConnection) WriteControl(int, []byte, time.Time) error { return nil }

func (c *coverageWebsocketConnection) SetWriteDeadline(time.Time) error {
	return c.deadlineErr
}

func (*coverageWebsocketConnection) Close() error { return nil }

func TestWSClientSendClosedChecksAndDeadlineFailure(t *testing.T) {
	connection := &coverageWebsocketConnection{}
	client := &wsClient{conn: connection}
	client.initialize()
	client.closed.Store(true)
	if err := client.Send([]byte("first-check")); !errors.Is(err, ErrClosed) {
		t.Fatalf("first closed check error=%v", err)
	}

	deadlineErr := errors.New("set write deadline")
	client.closed.Store(false)
	connection.deadlineErr = deadlineErr
	if err := client.writeMessage([]byte("deadline")); !errors.Is(err, deadlineErr) {
		t.Fatalf("write deadline error=%v", err)
	}
	if connection.writes.Load() != 0 {
		t.Fatalf("WriteMessage called after deadline failure: %d", connection.writes.Load())
	}

	connection.deadlineErr = nil
	if err := client.writeMessage([]byte("success")); err != nil {
		t.Fatal(err)
	}
	if connection.writes.Load() != 1 {
		t.Fatalf("writes=%d want 1", connection.writes.Load())
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWSClientSendObservesStoppedWriter(t *testing.T) {
	client := &wsClient{conn: &coverageWebsocketConnection{}, queueSize: 1}
	client.initialize()
	client.stop()
	<-client.writerDone
	if err := client.Send([]byte("stopped")); !errors.Is(err, ErrClosed) {
		t.Fatalf("stopped writer error=%v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}
