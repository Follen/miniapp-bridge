package app

import (
	"bytes"
	"context"
	"fmt"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func freePort(t *testing.T) int {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func TestBridgeAndRebind(t *testing.T) {
	dp, cp := freePort(t), freePort(t)
	var logs synchronizedBuffer
	a := New(dp, cp, logging.NewWithWriters(false, false, &logs, &logs))
	if e := a.Start(); e != nil {
		t.Fatal(e)
	}
	d, _, e := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", dp), nil)
	if e != nil {
		t.Fatal(e)
	}
	c, _, e := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", cp), nil)
	if e != nil {
		t.Fatal(e)
	}
	logDeadline := time.Now().Add(time.Second)
	for !strings.Contains(logs.String(), "[miniapp] miniapp client connected") || !strings.Contains(logs.String(), "[cdp] CDP client connected") {
		if time.Now().After(logDeadline) {
			t.Fatalf("connection logs missing: %s", logs.String())
		}
		time.Sleep(time.Millisecond)
	}
	contextData, e := wmpf.EncodeCategory(wmpf.CategoryAddJsContext, wmpf.JsContext{ID: "ctx-1", Name: "main"})
	if e != nil {
		t.Fatal(e)
	}
	if e = d.WriteMessage(websocket.BinaryMessage, wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryAddJsContext, Data: contextData})); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if selected, ok := a.Contexts.Selected(); ok && selected.ID == "ctx-1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("context was not selected")
		}
		time.Sleep(time.Millisecond)
	}
	if e = c.WriteMessage(websocket.TextMessage, []byte(`{"id":1,"method":"Runtime.enable"}`)); e != nil {
		t.Fatal(e)
	}
	typ, frame, e := d.ReadMessage()
	if e != nil || typ != websocket.BinaryMessage {
		t.Fatalf("debug type=%d err=%v", typ, e)
	}
	outer, e := wmpf.DecodeDebugMessage(frame)
	if e != nil || outer.Category != wmpf.CategoryChromeDevtools {
		t.Fatalf("outer=%+v err=%v", outer, e)
	}
	chrome, e := wmpf.DecodeChrome(outer.Data)
	if e != nil || chrome.JSContextID != "ctx-1" {
		t.Fatalf("chrome=%+v err=%v", chrome, e)
	}
	reply := wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryChromeDevtoolsResult, Data: wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: `{"id":1,"result":{}}`})})
	if e = d.WriteMessage(websocket.BinaryMessage, reply); e != nil {
		t.Fatal(e)
	}
	typ, b, e := c.ReadMessage()
	if e != nil || typ != websocket.TextMessage || string(b) != `{"id":1,"result":{}}` {
		t.Fatalf("cdp type=%d body=%s err=%v", typ, b, e)
	}
	if a.Requests.Len() != 0 {
		t.Fatalf("pending requests=%d", a.Requests.Len())
	}
	_ = d.Close()
	_ = c.Close()
	logDeadline = time.Now().Add(time.Second)
	for !strings.Contains(logs.String(), "[miniapp] miniapp client disconnected") || !strings.Contains(logs.String(), "[cdp] CDP client disconnected") {
		if time.Now().After(logDeadline) {
			t.Fatalf("disconnection logs missing: %s", logs.String())
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = a.Close(ctx); e != nil {
		t.Fatal(e)
	}
	bapp := New(dp, cp, logging.New(false, false))
	if e = bapp.Start(); e != nil {
		t.Fatalf("rebind: %v", e)
	}
	_ = bapp.Close(ctx)
}
