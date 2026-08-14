package sdk

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestUpstreamDisconnectFailsPendingAndReconnects(t *testing.T) {
	s, err := New(Options{DebugPort: sdkFreePort(t), CDPPort: sdkFreePort(t), SubscriberBuffer: 16, Native: disabledNativeStarter})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	selectSDKContext(t, s, "connection-context")
	defer s.Close(context.Background())
	status := s.SubscribeStatus(SubscriptionOptions{Buffer: 16})
	defer status.Close()
	endpoint := fmt.Sprintf("ws://127.0.0.1:%d", s.Status().DebugPort)
	upstream, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	connectDeadline := time.Now().Add(5 * time.Second)
	for !s.Status().Connections.Upstream && time.Now().Before(connectDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !s.Status().Connections.Upstream {
		t.Fatal("upstream websocket handshake completed but service did not register the connection")
	}
	requestDone := make(chan error, 1)
	go func() {
		// The SDK-side upstream flag is updated by the connection observer,
		// which can briefly trail the hub registration on a loaded runner.
		// Retry once so the pending request is established before teardown.
		var err error
		for attempt := 0; attempt < 5; attempt++ {
			_, err = s.Send(context.Background(), Request{ID: "disconnect-me", Method: "Runtime.enable"})
			if !errors.Is(err, ErrNoUpstream) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		requestDone <- err
	}()
	_ = upstream.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := upstream.ReadMessage(); err != nil {
		_ = upstream.Close()
		t.Fatal(err)
	}
	if err := upstream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-requestDone:
		if !errors.Is(err, ErrUpstreamDisconnected) {
			t.Fatalf("pending error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending request was not released after upstream disconnect")
	}
	connected, disconnected := false, false
	deadline := time.After(5 * time.Second)
	for !connected || !disconnected {
		select {
		case event := <-status.Channel():
			if event.Connections.Upstream {
				connected = true
			} else if connected {
				disconnected = true
			}
		case <-deadline:
			t.Fatalf("connection statuses connected=%v disconnected=%v", connected, disconnected)
		}
	}
	reconnected, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = reconnected.Close()
}
