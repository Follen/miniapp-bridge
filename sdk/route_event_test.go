package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	bridgeapp "github.com/Follen/miniapp-bridge/internal/app"
	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
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
	if _, err := s.SendRaw(context.Background(), []byte(`{"method":"Runtime.enable"}`)); err != nil {
		t.Fatalf("empty bootstrap route error=%v", err)
	}
	if got := readRouteFrame(t, client).JSContextID; got != "" {
		t.Fatalf("empty bootstrap route=%q", got)
	}

	emptySend := make(chan error, 1)
	go func() {
		_, err := s.Send(context.Background(), Request{ID: 42, Method: "Runtime.enable"})
		emptySend <- err
	}()
	if got := readRouteFrame(t, client).JSContextID; got != "" {
		t.Fatalf("structured empty bootstrap route=%q", got)
	}
	s.observeCDP([]byte(`{"id":42,"result":{}}`))
	if err := <-emptySend; err != nil {
		t.Fatalf("structured empty bootstrap send: %v", err)
	}
}

func TestExecutionContextEventsBootstrapPublicSend(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	debug, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", s.Status().DebugPort), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = debug.Close() })
	if err := debug.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	writeRuntimeEvent := func(payload string) {
		t.Helper()
		frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
			Category: wmpf.CategoryChromeDevtoolsResult,
			Data:     wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: payload}),
		})
		if err := debug.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	waitForContexts := func(want int) []JSContext {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			contexts := s.Contexts()
			if len(contexts) == want {
				return contexts
			}
			if time.Now().After(deadline) {
				t.Fatalf("contexts=%+v want count %d", contexts, want)
			}
			time.Sleep(time.Millisecond)
		}
	}

	writeRuntimeEvent(`{"method":"Runtime.executionContextCreated","params":{"context":{"id":1,"name":"game"}}}`)
	if contexts := waitForContexts(1); contexts[0] != (JSContext{ID: "1", Target: "game"}) {
		t.Fatalf("Contexts()=%+v", contexts)
	}

	type sendResult struct {
		response Response
		err      error
	}
	done := make(chan sendResult, 1)
	go func() {
		response, err := s.Send(context.Background(), Request{ID: 91, Method: "Runtime.evaluate"})
		done <- sendResult{response: response, err: err}
	}()
	messageType, requestFrame, err := debug.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("request message type=%d want binary", messageType)
	}
	outer, err := wmpf.DecodeDebugMessage(requestFrame)
	if err != nil {
		t.Fatal(err)
	}
	request, err := wmpf.DecodeChrome(outer.Data)
	if err != nil {
		t.Fatal(err)
	}
	if request.JSContextID != "1" || request.Payload != `{"id":91,"method":"Runtime.evaluate"}` {
		t.Fatalf("routed request=%+v", request)
	}
	responseFrame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
		Category: wmpf.CategoryChromeDevtoolsResult,
		Data:     wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: `{"id":91,"result":{"value":42}}`}),
	})
	if err := debug.WriteMessage(websocket.BinaryMessage, responseFrame); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.response.Result["value"] != json.Number("42") {
			t.Fatalf("Send response=%+v error=%v", result.response, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not receive its CDP response")
	}

	writeRuntimeEvent(`{"method":"Runtime.executionContextDestroyed","params":{"executionContextId":1}}`)
	waitForContexts(0)
	if err := s.Notify(Request{Method: "Runtime.enable"}); err != nil {
		t.Fatalf("Notify after executionContextDestroyed: %v", err)
	}
	messageType, requestFrame, err = debug.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("bootstrap message type=%d want binary", messageType)
	}
	outer, err = wmpf.DecodeDebugMessage(requestFrame)
	if err != nil {
		t.Fatal(err)
	}
	request, err = wmpf.DecodeChrome(outer.Data)
	if err != nil {
		t.Fatal(err)
	}
	if request.JSContextID != "" {
		t.Fatalf("bootstrap route after destroy=%q want empty", request.JSContextID)
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
	a.Response.Result["nested"].(map[string]any)["values"].([]any)[0] = json.Number("99")
	a.Response.Error.Data.(map[string]any)["detail"].([]any)[0] = "changed"
	if b.Payload[0] == 'x' || b.Response.Result["nested"].(map[string]any)["values"].([]any)[0] != json.Number("1") || b.Response.Error.Data.(map[string]any)["detail"].([]any)[0] != "a" {
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

func TestCDPStructuredValuesPreserveJSONNumbers(t *testing.T) {
	s := newSDK(t, Options{SubscriberBuffer: 4})
	sub := s.SubscribeCDP()
	defer sub.Close()

	s.observeCDP([]byte(`{"method":"Runtime.consoleAPICalled","params":{"large":9007199254740993,"fraction":1.25}}`))
	event := <-sub.Channel()
	if event.Params["large"] != json.Number("9007199254740993") || event.Params["fraction"] != json.Number("1.25") {
		t.Fatalf("event params lost numeric text: %#v", event.Params)
	}

	s.observeCDP([]byte(`{"id":"result","result":{"large":9007199254740995}}`))
	event = <-sub.Channel()
	if event.Response == nil || event.Response.Result["large"] != json.Number("9007199254740995") {
		t.Fatalf("response result lost numeric text: %#v", event.Response)
	}

	s.observeCDP([]byte(`{"id":"error","error":{"code":-32000,"message":"bad","data":{"large":18446744073709551615}}}`))
	event = <-sub.Channel()
	if event.Response == nil || event.Response.Error == nil {
		t.Fatalf("missing error response: %#v", event.Response)
	}
	data, ok := event.Response.Error.Data.(map[string]any)
	if !ok || data["large"] != json.Number("18446744073709551615") {
		t.Fatalf("error data lost numeric text: %#v", event.Response.Error.Data)
	}
}
