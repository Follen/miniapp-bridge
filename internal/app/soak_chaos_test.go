package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/proxy"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

const deterministicSoakCycles = 16

func TestSingleOwnerReconnectDeterministicSoak(t *testing.T) {
	debugPort, cdpPort := freePort(t), freePort(t)
	bridge := New(debugPort, cdpPort, logging.New(false, false))
	if err := bridge.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := bridge.Close(ctx); err != nil {
			t.Errorf("close soak bridge: %v", err)
		}
	})

	for cycle := 1; cycle <= deterministicSoakCycles; cycle++ {
		upstream := auditDial(t, debugPort)
		// Each fresh upstream transport triggers one automatic Runtime.enable;
		// drain and answer it so the bootstrap request resolves and the frames
		// below stay deterministic.
		bootstrap := auditRead(t, upstream, websocket.BinaryMessage)
		outer, err := wmpf.DecodeDebugMessage(bootstrap)
		if err != nil {
			t.Fatalf("cycle %d bootstrap decode: %v", cycle, err)
		}
		chrome, err := wmpf.DecodeChrome(outer.Data)
		if err != nil {
			t.Fatalf("cycle %d bootstrap chrome: %v", cycle, err)
		}
		var bootstrapRequest struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal([]byte(chrome.Payload), &bootstrapRequest); err != nil {
			t.Fatalf("cycle %d bootstrap payload: %v", cycle, err)
		}
		if chrome.JSContextID != "" || bootstrapRequest.Method != "Runtime.enable" {
			t.Fatalf("cycle %d bootstrap chrome=%+v", cycle, chrome)
		}
		if id := string(bootstrapRequest.ID); id != strconv.Itoa(cycle) {
			t.Fatalf("cycle %d bootstrap id=%q want %d", cycle, id, cycle)
		}
		bootstrapReply := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
			Category: wmpf.CategoryChromeDevtoolsResult,
			Data:     wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: fmt.Sprintf(`{"id":%d,"result":{}}`, cycle)}),
		})
		if err := upstream.WriteMessage(websocket.BinaryMessage, bootstrapReply); err != nil {
			t.Fatalf("cycle %d bootstrap reply: %v", cycle, err)
		}
		controller := auditDial(t, cdpPort)
		waitForConnectionCounts(t, bridge, 1, 1)
		contextID := fmt.Sprintf("soak-context-%d", cycle)
		bridge.Contexts.Upsert(bridgecontext.Context{ID: contextID})
		bridge.Contexts.Select(contextID)
		auditRejectedDial(t, debugPort, "owner_exists")
		auditRejectedDial(t, cdpPort, "owner_exists")

		payload := fmt.Sprintf(`{"method":"Runtime.evaluate","params":{"expression":"%d"}}`, cycle)
		if err := controller.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatalf("cycle %d CDP write: %v", cycle, err)
		}
		frame := auditRead(t, upstream, websocket.BinaryMessage)
		outer, err = wmpf.DecodeDebugMessage(frame)
		if err != nil {
			t.Fatalf("cycle %d outer decode: %v", cycle, err)
		}
		chrome, err = wmpf.DecodeChrome(outer.Data)
		if err != nil {
			t.Fatalf("cycle %d CDP decode: %v", cycle, err)
		}
		if chrome.Payload != payload {
			t.Fatalf("cycle %d payload=%q want=%q", cycle, chrome.Payload, payload)
		}

		closeWebSocketNormally(t, controller)
		closeWebSocketNormally(t, upstream)
		waitForConnectionCounts(t, bridge, 0, 0)
		// Owner release and generation teardown run in the reader goroutines;
		// poll so the leak assertion observes the settled state.
		waitFor(t, func() bool {
			return bridge.Requests.Len() == 0 && bridge.Contexts.Len() == 0
		}, fmt.Sprintf("cycle %d retained requests/contexts=%d/%d", cycle, bridge.Requests.Len(), bridge.Contexts.Len()))
	}

	snapshot := bridge.ConnectionSnapshot()
	if snapshot.UpstreamGeneration != deterministicSoakCycles || snapshot.CDPGeneration != deterministicSoakCycles {
		t.Fatalf("generations upstream/CDP=%d/%d want=%d/%d", snapshot.UpstreamGeneration, snapshot.CDPGeneration, deterministicSoakCycles, deterministicSoakCycles)
	}
	if snapshot.RejectedUpstream != deterministicSoakCycles || snapshot.RejectedCDP != deterministicSoakCycles {
		t.Fatalf("rejections upstream/CDP=%d/%d want=%d/%d", snapshot.RejectedUpstream, snapshot.RejectedCDP, deterministicSoakCycles, deterministicSoakCycles)
	}
	if snapshot.UpstreamConnected || snapshot.CDPConnected {
		t.Fatalf("owners remained connected: %+v", snapshot)
	}
}

func TestWSClientQueueAccountingDeterministicSoak(t *testing.T) {
	for cycle := 0; cycle < deterministicSoakCycles; cycle++ {
		connection := newQueuedTestConnection(true)
		client := &wsClient{
			conn: connection, typeID: websocket.BinaryMessage,
			queueSize: 4, queueByteLimit: 100, maxMessageBytes: 8,
		}
		if cycle%2 == 0 {
			client.queueSize = 8
			client.queueByteLimit = 10
		}

		if err := client.Send([]byte("00")); err != nil {
			t.Fatalf("cycle %d first enqueue: %v", cycle, err)
		}
		select {
		case <-connection.started:
		case <-time.After(time.Second):
			t.Fatalf("cycle %d writer did not block", cycle)
		}
		for _, message := range []string{"11", "22", "33", "44"} {
			if err := client.Send([]byte(message)); err != nil {
				t.Fatalf("cycle %d enqueue %q: %v", cycle, message, err)
			}
		}
		err := client.Send([]byte("55"))
		if cycle%2 == 0 && !errors.Is(err, ErrQueueBytes) {
			t.Fatalf("cycle %d overflow=%v want ErrQueueBytes", cycle, err)
		}
		if cycle%2 != 0 && !errors.Is(err, proxy.ErrClientBackpressure) {
			t.Fatalf("cycle %d overflow=%v want ErrClientBackpressure", cycle, err)
		}

		close(connection.release)
		waitFor(t, func() bool { return len(connection.snapshot()) == 5 }, fmt.Sprintf("cycle %d queue did not drain", cycle))
		waitFor(t, func() bool { return wsClientQueuedBytes(client) == 0 }, fmt.Sprintf("cycle %d byte accounting did not drain", cycle))
		messages := connection.snapshot()
		for index, want := range []string{"00", "11", "22", "33", "44"} {
			if string(messages[index]) != want {
				t.Fatalf("cycle %d message[%d]=%q want=%q", cycle, index, messages[index], want)
			}
		}
		if err := client.Close(); err != nil {
			t.Fatalf("cycle %d close: %v", cycle, err)
		}
	}
}

func TestWSClientWriterFaultAndConcurrentShutdownChaos(t *testing.T) {
	for cycle := 0; cycle < deterministicSoakCycles; cycle++ {
		failure := fmt.Errorf("injected writer failure %d", cycle)
		connection := &deterministicWriterFaultConnection{failure: failure, failDeadline: cycle%2 == 0}
		client := &wsClient{conn: connection, typeID: websocket.TextMessage, queueSize: 2}
		var callbacks atomic.Int32
		client.onError = func(err error) {
			if !errors.Is(err, failure) {
				t.Errorf("cycle %d callback error=%v want=%v", cycle, err, failure)
			}
			callbacks.Add(1)
		}
		if err := client.Send([]byte("trigger")); err != nil {
			t.Fatalf("cycle %d enqueue: %v", cycle, err)
		}
		select {
		case <-client.writerDone:
		case <-time.After(time.Second):
			t.Fatalf("cycle %d writer did not stop after injected failure", cycle)
		}

		const closers = 8
		var group sync.WaitGroup
		group.Add(closers)
		closeErrors := make(chan error, closers)
		for range closers {
			go func() {
				defer group.Done()
				closeErrors <- client.Close()
			}()
		}
		group.Wait()
		close(closeErrors)
		for err := range closeErrors {
			if err != nil {
				t.Fatalf("cycle %d concurrent close: %v", cycle, err)
			}
		}
		if callbacks.Load() != 1 || connection.closeCalls.Load() != 1 {
			t.Fatalf("cycle %d callbacks/closes=%d/%d want=1/1", cycle, callbacks.Load(), connection.closeCalls.Load())
		}
		if err := client.Send([]byte("after failure")); !errors.Is(err, ErrClosed) {
			t.Fatalf("cycle %d post-failure send=%v want ErrClosed", cycle, err)
		}
	}
}

type deterministicWriterFaultConnection struct {
	failure      error
	failDeadline bool
	closeCalls   atomic.Int32
}

func (*deterministicWriterFaultConnection) ReadMessage() (int, []byte, error) {
	return 0, nil, net.ErrClosed
}
func (connection *deterministicWriterFaultConnection) WriteMessage(int, []byte) error {
	return connection.failure
}
func (*deterministicWriterFaultConnection) WriteControl(int, []byte, time.Time) error { return nil }
func (connection *deterministicWriterFaultConnection) SetWriteDeadline(time.Time) error {
	if connection.failDeadline {
		return connection.failure
	}
	return nil
}
func (connection *deterministicWriterFaultConnection) Close() error {
	connection.closeCalls.Add(1)
	return nil
}

func closeWebSocketNormally(t *testing.T, connection *websocket.Conn) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	if err := connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "soak cycle complete"),
		deadline,
	); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForConnectionCounts(t *testing.T, bridge *App, upstream, cdp int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bridge.DebugClientCount() == upstream && bridge.CDPClientCount() == cdp {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("connection counts upstream/CDP=%d/%d want=%d/%d", bridge.DebugClientCount(), bridge.CDPClientCount(), upstream, cdp)
}

func wsClientQueuedBytes(client *wsClient) int64 {
	client.sendMu.Lock()
	defer client.sendMu.Unlock()
	return client.queueBytes
}
