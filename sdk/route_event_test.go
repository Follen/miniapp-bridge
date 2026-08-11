package sdk

import (
	"context"
	"errors"
	"testing"
	"time"

	bridgeapp "github.com/Follen/miniapp-bridge/internal/app"
	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
)

type routeFrameClient struct{ frames chan []byte }

func (c *routeFrameClient) Send(frame []byte) error {
	c.frames <- append([]byte(nil), frame...)
	return nil
}

func TestTranslateAppErrorCoversPublicErrorModel(t *testing.T) {
	for _, test := range []struct {
		internal error
		public   error
	}{
		{bridgeapp.ErrClosed, ErrClosed},
		{bridgeapp.ErrInvalidCDPPayload, ErrInvalidRequest},
		{bridgeapp.ErrNoContext, ErrNoContext},
		{bridgeapp.ErrUnknownContext, ErrUnknownContext},
	} {
		if err := translateAppError(test.internal); !errors.Is(err, test.public) {
			t.Fatalf("translate %v=%v, want %v", test.internal, err, test.public)
		}
	}
	passthrough := errors.New("passthrough")
	if got := translateAppError(passthrough); got != passthrough {
		t.Fatalf("passthrough=%v", got)
	}
}
func (*routeFrameClient) Close() error { return nil }

func readRouteFrame(t *testing.T, client *routeFrameClient) wmpf.ChromeDevtools {
	t.Helper()
	select {
	case frame := <-client.frames:
		outer, err := wmpf.DecodeDebugMessage(frame)
		if err != nil {
			t.Fatal(err)
		}
		chrome, err := wmpf.DecodeChrome(outer.Data)
		if err != nil {
			t.Fatal(err)
		}
		return chrome
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for routed CDP frame")
		return wmpf.ChromeDevtools{}
	}
}

func TestPublicRequestRoutesAndErrors(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	client := &routeFrameClient{frames: make(chan []byte, 8)}
	s.app.DebugHub.Add(client)
	s.app.Contexts.Upsert(bridgecontext.Context{ID: "selected"})
	s.app.Contexts.Upsert(bridgecontext.Context{ID: "explicit"})
	if !s.app.Contexts.Select("selected") {
		t.Fatal("failed to select context")
	}

	done := make(chan error, 1)
	go func() {
		_, err := s.Send(context.Background(), Request{ID: 41, Method: "Runtime.enable", Route: Route{JSContextID: "explicit"}})
		done <- err
	}()
	if got := readRouteFrame(t, client).JSContextID; got != "explicit" {
		t.Fatalf("explicit route=%q", got)
	}
	s.observeCDP([]byte(`{"id":41,"result":{}}`))
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if err := s.Notify(Request{Method: "Debugger.enable"}); err != nil {
		t.Fatal(err)
	}
	if got := readRouteFrame(t, client).JSContextID; got != "selected" {
		t.Fatalf("selected route=%q", got)
	}
	if _, err := s.SendRawRoute(context.Background(), []byte(`{"method":"Runtime.runIfWaitingForDebugger"}`), Route{JSContextID: "explicit"}); err != nil {
		t.Fatal(err)
	}
	if got := readRouteFrame(t, client).JSContextID; got != "explicit" {
		t.Fatalf("raw route=%q", got)
	}

	_, err := s.SendRawRoute(context.Background(), []byte(`{"method":"Runtime.enable"}`), Route{JSContextID: "missing"})
	if !errors.Is(err, ErrUnknownContext) {
		t.Fatalf("unknown context error=%v", err)
	}
	var structured *Error
	if !errors.As(err, &structured) || structured.Component != "route" {
		t.Fatalf("structured route error=%+v", structured)
	}

	s.app.Contexts.Remove("selected")
	s.app.Contexts.Remove("explicit")
	if _, err := s.SendRaw(context.Background(), []byte(`{"method":"Runtime.enable"}`)); !errors.Is(err, ErrNoContext) {
		t.Fatalf("no context error=%v", err)
	}
}

func TestCDPSubscribersReceiveIndependentDeepCopies(t *testing.T) {
	s := newSDK(t, Options{SubscriberBuffer: 4})
	first := s.SubscribeCDP()
	second := s.SubscribeCDP()
	defer first.Close()
	defer second.Close()

	s.observeCDP([]byte(`{"id":9,"result":{"nested":{"values":[1,2]}},"error":{"code":-1,"message":"bad","data":{"detail":["a"]}}}`))
	a := <-first.Channel()
	b := <-second.Channel()
	a.Payload[0] = 'x'
	a.Response.Result["nested"].(map[string]any)["values"].([]any)[0] = float64(99)
	a.Response.Error.Data.(map[string]any)["detail"].([]any)[0] = "changed"
	if b.Payload[0] == 'x' || b.Response.Result["nested"].(map[string]any)["values"].([]any)[0] != float64(1) || b.Response.Error.Data.(map[string]any)["detail"].([]any)[0] != "a" {
		t.Fatal("CDP response data was shared between subscribers")
	}

	bytes := []byte{1, 2, 3}
	s.cdpEvents.publish(CDPEvent{Params: map[string]any{"bytes": bytes}})
	a = <-first.Channel()
	b = <-second.Channel()
	a.Params["bytes"].([]byte)[0] = 9
	if b.Params["bytes"].([]byte)[0] != 1 || bytes[0] != 1 {
		t.Fatal("CDP byte data was shared between subscribers or publisher")
	}
}
