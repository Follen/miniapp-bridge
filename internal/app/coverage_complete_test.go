package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"miniapp-bridge/internal/capture"
	"miniapp-bridge/internal/logging"
	"miniapp-bridge/internal/wmpf"
)

func TestCoverageAppFrameAndReplayErrors(t *testing.T) {
	var logs synchronizedBuffer
	a := New(0, 0, logging.NewWithWriters(true, true, &logs, &logs))
	if err := a.Replay(filepath.Join(t.TempDir(), "missing.capture")); err == nil {
		t.Fatal("missing replay succeeded")
	}
	if a.handleDebugFrame(websocket.CloseMessage, nil) {
		t.Fatal("control frame accepted as debug data")
	}
	if a.handleCDPFrame(websocket.PingMessage, nil, nil) {
		t.Fatal("control frame accepted as CDP data")
	}
	if _, ok := a.decodeCategoryData(wmpf.CategoryPing, "not bytes"); ok {
		t.Fatal("non-byte category data accepted")
	}
	if _, ok := a.decodeCategoryData(wmpf.CategoryAddJsContext, []byte{0xff}); ok {
		t.Fatal("malformed category data accepted")
	}
	a.handleUnwrappedDebug(wmpf.Unwrapped{Category: wmpf.CategoryPing, Data: "not bytes"})
	a.handleDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryPing, CompressAlgo: wmpf.CompressZlib, Data: []byte("bad-zlib")})

	recorder, err := capture.Start(filepath.Join(t.TempDir(), "capture.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	a.SetRecorder(recorder)
	if !a.handleDebugFrame(websocket.BinaryMessage, []byte{0xff}) {
		t.Fatal("binary frame was not consumed")
	}
	if !strings.Contains(logs.String(), "[capture] write:") || !strings.Contains(logs.String(), "miniapp client err") || !strings.Contains(logs.String(), "[miniapp] decode:") || !strings.Contains(logs.String(), "[miniapp] category:") {
		t.Fatalf("missing diagnostic logs: %s", logs.String())
	}

	noContext := New(0, 0, logging.New(false, false))
	if !noContext.handleCDPFrame(websocket.BinaryMessage, []byte(`{"method":"Runtime.enable"}`), nil) {
		t.Fatal("binary CDP frame rejected")
	}
}

func TestCoverageAppStartHandlersServeAndClose(t *testing.T) {
	boom := errors.New("listen failed")
	fail := New(0, 0, logging.New(false, false))
	fail.listen = func(string, string) (net.Listener, error) { return nil, boom }
	if err := fail.Start(); !errors.Is(err, boom) {
		t.Fatalf("debug listen error=%v", err)
	}

	var logs synchronizedBuffer
	serveFail := New(freePort(t), freePort(t), logging.NewWithWriters(true, true, &logs, &logs))
	var calls atomic.Int32
	serveFail.serve = func(_ *http.Server, l net.Listener) error { calls.Add(1); _ = l.Close(); return boom }
	if err := serveFail.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatalf("serve calls=%d logs=%s", calls.Load(), logs.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = serveFail.Close(ctx)

	dp, cp := freePort(t), freePort(t)
	a := New(dp, cp, logging.New(false, false))
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{dp, cp} {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d", port))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("HTTP status=%d", resp.StatusCode)
		}
	}
	a.closing.Store(true)
	for _, port := range []int{dp, cp} {
		conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", port), nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, _, _ = conn.ReadMessage()
		_ = conn.Close()
	}
	a.closing.Store(false)
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "closed-by-app.capture")
	r, err := capture.Start(path)
	if err != nil {
		t.Fatal(err)
	}
	withRecorder := New(0, 0, logging.New(false, false))
	withRecorder.SetRecorder(r)
	if err := withRecorder.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Write([]byte("closed")); !errors.Is(err, capture.ErrRecorderClosed) {
		t.Fatalf("recorder remained open: %v", err)
	}
}
