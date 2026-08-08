package wmpf

import (
	"bytes"
	"testing"
)

func TestOutgoingDebugEnvelopeMatchesReferencePresence(t *testing.T) {
	frame := EncodeOutgoingDebugMessage(DebugMessage{Seq: 1, Category: CategoryPing, Data: []byte{8, 1}})
	if bytes.Contains(frame, []byte{0x10, 0x00}) {
		t.Fatalf("outgoing frame unexpectedly contains after=0: %x", frame)
	}
	if !bytes.HasSuffix(frame, []byte{0x28, 0x00, 0x30, 0x00}) {
		t.Fatalf("outgoing frame omits explicit compression fields: %x", frame)
	}
}

func TestDebugMessageRoundTrip(t *testing.T) {
	in := DebugMessage{Seq: 7, After: 3, Category: CategoryChromeDevtools, Data: EncodeChrome(ChromeDevtools{OpID: 99, Payload: `{"id":1}`, JSContextID: "ctx"}), CompressAlgo: CompressZlib, OriginalSize: 0}
	packed, size, err := WrapData(in.Data, in.Category, in.CompressAlgo)
	if err != nil {
		t.Fatal(err)
	}
	in.Data, in.OriginalSize = packed, size
	out, err := DecodeDebugMessage(EncodeDebugMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	u, err := UnwrapDebugMessage(out)
	if err != nil {
		t.Fatal(err)
	}
	c, err := DecodeChrome(u.Data.([]byte))
	if err != nil || c.OpID != 99 || c.JSContextID != "ctx" {
		t.Fatalf("decoded chrome=%+v err=%v", c, err)
	}
}

func TestAllCategoryCodecs(t *testing.T) {
	tests := []struct {
		category string
		value    any
	}{
		{CategoryPing, Ping{PingID: 1, Payload: "p"}},
		{CategoryPong, Pong{PingID: 1, NetworkType: 2, Payload: "p"}},
		{CategoryCallInterface, CallInterface{ObjName: "wx", MethodName: "m", MethodArgs: []string{"a"}, CallID: 3}},
		{CategoryCallInterfaceResult, CallInterfaceResult{Ret: "ok", CallID: 3}},
		{CategoryEvaluateJavascript, EvaluateJavascript{Script: "1+1", EvaluateID: 4}},
		{CategoryEvaluateJavascriptResult, EvaluateJavascriptResult{Ret: "2", EvaluateID: 4}},
		{CategoryBreakpoint, Breakpoint{IsHit: true}},
		{CategoryChromeDevtools, ChromeDevtools{OpID: 5, Payload: `{}`}},
		{CategoryAddJsContext, JsContext{ID: "ctx", Name: "main"}},
		{CategoryRemoveJsContext, JsContext{ID: "ctx"}},
		{CategoryConnectJsContext, JsContext{ID: "ctx"}},
		{CategoryCustomMessage, CustomMessage{Method: "m", Payload: "p"}},
	}
	for _, tc := range tests {
		b, err := EncodeCategory(tc.category, tc.value)
		if err != nil {
			t.Fatalf("%s encode: %v", tc.category, err)
		}
		if _, err := DecodeCategory(tc.category, b); err != nil {
			t.Fatalf("%s decode: %v", tc.category, err)
		}
	}
}

func TestUnknownFieldAndCorrupt(t *testing.T) {
	b := EncodeDebugMessage(DebugMessage{Seq: 1})
	b = append(b, 0x78, 0x01)
	if _, err := DecodeDebugMessage(b); err != nil {
		t.Fatal(err)
	}
	bad := []byte{0x22, 0x05, 0x01}
	m, err := DecodeDebugMessage(bad)
	if err == nil || m.Data != nil {
		t.Fatalf("expected corrupt frame error, m=%+v err=%v", m, err)
	}
}

func TestChromeGoldenBytes(t *testing.T) {
	got := EncodeChrome(ChromeDevtools{OpID: 1, Payload: "x", JSContextID: "c"})
	want := []byte{0x08, 0x01, 0x12, 0x01, 'x', 0x1a, 0x01, 'c'}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x want %x", got, want)
	}
}
