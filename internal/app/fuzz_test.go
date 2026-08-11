package app

import (
	"io"
	"testing"

	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

const appFuzzInputLimit = 64 << 10

func fuzzApp() *App {
	return New(0, 0, logging.NewWithWriters(false, false, io.Discard, io.Discard))
}

func FuzzWebSocketDebugFrameDecode(f *testing.F) {
	contextData, err := wmpf.EncodeCategory(
		wmpf.CategoryAddJsContext,
		wmpf.JsContext{ID: "ctx", Name: "main"},
	)
	if err != nil {
		f.Fatal(err)
	}
	contextFrame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
		Seq: 1, Category: wmpf.CategoryAddJsContext, Data: contextData,
	})
	cdpFrame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
		Seq: 2, Category: wmpf.CategoryChromeDevtoolsResult,
		Data: wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: `{"id":1,"result":{}}`}),
	})
	f.Add(uint8(websocket.BinaryMessage), contextFrame)
	f.Add(uint8(websocket.TextMessage), cdpFrame)
	f.Add(uint8(websocket.BinaryMessage), []byte{0x22, 0x05, 0x01})
	f.Add(uint8(websocket.PingMessage), []byte("ignored"))

	f.Fuzz(func(t *testing.T, messageType uint8, frame []byte) {
		if len(frame) > appFuzzInputLimit {
			t.Skip()
		}
		if message, err := wmpf.DecodeDebugMessage(frame); err == nil && message.CompressAlgo&wmpf.CompressZlib != 0 {
			t.Skip()
		}

		bridge := fuzzApp()
		accepted := bridge.handleDebugFrame(int(messageType), frame)
		wantAccepted := int(messageType) == websocket.BinaryMessage || int(messageType) == websocket.TextMessage
		if accepted != wantAccepted {
			t.Fatalf("message type=%d accepted=%t want=%t", messageType, accepted, wantAccepted)
		}
		if bridge.Contexts.Len() > 1 {
			t.Fatalf("one upstream frame created %d contexts", bridge.Contexts.Len())
		}
		if len(bridge.RuntimeErrors()) > maxRuntimeErrors {
			t.Fatalf("runtime errors exceeded cap: %d", len(bridge.RuntimeErrors()))
		}
	})
}

func FuzzCDPWebSocketFrameDecode(f *testing.F) {
	f.Add(uint8(websocket.TextMessage), []byte(`{"id":1,"method":"Runtime.enable"}`))
	f.Add(uint8(websocket.BinaryMessage), []byte(`{"id":18446744073709551615,"method":"Debugger.enable"}`))
	f.Add(uint8(websocket.TextMessage), []byte(`{"id":"request","method":"Runtime.evaluate","params":{"expression":"1+1"}}`))
	f.Add(uint8(websocket.TextMessage), []byte(`{"id":{},"method":"Runtime.enable"}`))
	f.Add(uint8(websocket.PingMessage), []byte(`{"id":1,"method":"Runtime.enable"}`))
	f.Add(uint8(websocket.TextMessage), []byte(`{`))

	f.Fuzz(func(t *testing.T, messageType uint8, payload []byte) {
		if len(payload) > appFuzzInputLimit {
			t.Skip()
		}
		bridge := fuzzApp()
		bridge.Contexts.Upsert(bridgecontext.Context{ID: "ctx", Target: "main"})
		bridge.Contexts.Select("ctx")

		accepted := bridge.handleCDPFrame(int(messageType), payload, nil)
		wantAccepted := int(messageType) == websocket.TextMessage || int(messageType) == websocket.BinaryMessage
		if accepted != wantAccepted {
			t.Fatalf("message type=%d accepted=%t want=%t", messageType, accepted, wantAccepted)
		}
		if pending := bridge.Requests.Len(); pending > 1 {
			t.Fatalf("one CDP frame registered %d requests", pending)
		}
		if subscriptions := len(bridge.subscriptions); subscriptions > 1 {
			t.Fatalf("one CDP frame registered %d subscriptions", subscriptions)
		}
		if bridge.Requests.Len() == 1 {
			bridge.Requests.Drain()
		}
	})
}
