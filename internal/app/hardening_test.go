package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/cdp"
	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

type hardeningWriteErrorConnection struct {
	started chan struct{}
}

func (*hardeningWriteErrorConnection) ReadMessage() (int, []byte, error) {
	return 0, nil, net.ErrClosed
}
func (c *hardeningWriteErrorConnection) WriteMessage(int, []byte) error {
	if c.started != nil {
		close(c.started)
		c.started = nil
	}
	return errors.New("hardening write failure")
}
func (*hardeningWriteErrorConnection) WriteControl(int, []byte, time.Time) error { return nil }
func (*hardeningWriteErrorConnection) SetWriteDeadline(time.Time) error          { return nil }
func (*hardeningWriteErrorConnection) Close() error                              { return nil }

type hardeningControlThenCloseConnection struct {
	reads atomic.Int32
}

func (c *hardeningControlThenCloseConnection) ReadMessage() (int, []byte, error) {
	if c.reads.Add(1) == 1 {
		return websocket.PingMessage, nil, nil
	}
	return 0, nil, net.ErrClosed
}
func (*hardeningControlThenCloseConnection) WriteMessage(int, []byte) error { return nil }
func (*hardeningControlThenCloseConnection) WriteControl(int, []byte, time.Time) error {
	return nil
}
func (*hardeningControlThenCloseConnection) SetWriteDeadline(time.Time) error { return nil }
func (*hardeningControlThenCloseConnection) Close() error                     { return nil }

func TestCDPOriginAllowlistAndSingleOwner(t *testing.T) {
	a := New(freePort(t), freePort(t), logging.New(false, false))
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close(context.Background()) }()

	endpoint := fmt.Sprintf("ws://127.0.0.1:%d", a.CDPPort)
	header := http.Header{"Origin": []string{"https://evil.example"}}
	conn, response, err := websocket.DefaultDialer.Dial(endpoint, header)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("disallowed Origin connected")
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("Origin rejection err=%v response=%v", err, response)
	}
	var rejection rejectionBody
	if decodeErr := json.NewDecoder(response.Body).Decode(&rejection); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	_ = response.Body.Close()
	if rejection.Error.Code != "origin_not_allowed" {
		t.Fatalf("Origin rejection=%+v", rejection)
	}

	owner := auditDial(t, a.CDPPort)
	defer owner.Close()
	auditRejectedDial(t, a.CDPPort, "owner_exists")
	if got := a.CDPClientCount(); got != 1 {
		t.Fatalf("CDP owners=%d want 1", got)
	}
	if got := a.ConnectionSnapshot().RejectedOrigin; got != 1 {
		t.Fatalf("Origin rejects=%d want 1", got)
	}

	_ = owner.Close()
	auditWaitForCDPClients(t, a, 0)
	allowedHeader := http.Header{"Origin": []string{"devtools://devtools"}}
	allowed, _, err := websocket.DefaultDialer.Dial(endpoint, allowedHeader)
	if err != nil {
		t.Fatal(err)
	}
	_ = allowed.Close()
}

func TestAppRejectsClosingConnectionsBeforeUpgrade(t *testing.T) {
	a := New(freePort(t), freePort(t), logging.New(false, false))
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	a.closing.Store(true)
	defer func() { _ = a.Close(context.Background()) }()
	for _, port := range []int{a.DebugPort, a.CDPPort} {
		conn, response, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d", port), nil)
		if conn != nil {
			_ = conn.Close()
			t.Fatalf("closing app accepted port %d", port)
		}
		if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("closing port %d err=%v response=%v", port, err, response)
		}
		var body rejectionBody
		if decodeErr := json.NewDecoder(response.Body).Decode(&body); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		_ = response.Body.Close()
		if body.Error.Code != "app_closing" {
			t.Fatalf("closing rejection=%+v", body)
		}
	}
}

func TestWSClientBoundsQueueBytesAndMessages(t *testing.T) {
	connection := newQueuedTestConnection(true)
	client := &wsClient{
		conn: connection, typeID: websocket.TextMessage, queueSize: 2,
		queueByteLimit: 3, maxMessageBytes: 4,
	}
	if err := client.Send([]byte("12")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-connection.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	if err := client.Send([]byte("34")); !errors.Is(err, ErrQueueBytes) {
		t.Fatalf("queue byte error=%v", err)
	}
	if err := client.Send([]byte("12345")); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("message limit error=%v", err)
	}
	_ = client.Close()
}

func TestAppDropsStaleGenerationFramesAndBoundsInjectedMessages(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	current := &wsClient{conn: &coverageWebsocketConnection{}, generation: 2}
	stale := &wsClient{conn: &coverageWebsocketConnection{}, generation: 1}
	a.cdpOwner = current
	a.cdpGeneration = 2
	if a.handleCDPFrame(websocket.TextMessage, []byte(`{"id":1,"method":"Runtime.enable"}`), stale) {
		t.Fatal("stale frame was routed")
	}
	if got := a.ConnectionSnapshot().StaleDrops; got != 1 {
		t.Fatalf("stale drops=%d want 1", got)
	}
	if a.handleDebugFrame(websocket.BinaryMessage, []byte(strings.Repeat("x", int(websocketMaxMessageBytes)+1))) {
		t.Fatal("oversized injected upstream frame was accepted")
	}
	if err := a.SendCDP(make([]byte, websocketMaxMessageBytes+1)); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversized SendCDP error=%v", err)
	}
	if got := len(a.RuntimeErrors()); got < 2 {
		t.Fatalf("runtime errors=%d want at least 2", got)
	}
}

func TestHardeningHelpersAndStructuredErrorPaths(t *testing.T) {
	var logs synchronizedBuffer
	a := New(0, 62000, logging.NewWithWriters(false, false, &logs, &logs))
	for _, remote := range []string{"not-an-address", "10.0.0.1:1234"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
		req.RemoteAddr = remote
		if isLoopbackRequest(req) {
			t.Fatalf("non-loopback remote %q accepted", remote)
		}
	}
	for _, origin := range []string{
		":bad", "ftp://127.0.0.1", "https://evil.example", "https://127.0.0.1/path",
		"https://127.0.0.1:1", "https://user@127.0.0.1",
	} {
		if a.allowedCDPOrigin(origin) {
			t.Fatalf("origin %q unexpectedly allowed", origin)
		}
	}
	for _, origin := range []string{"", "devtools://devtools", "chrome-devtools://devtools", "http://localhost", "http://127.0.0.1:62000"} {
		if !a.allowedCDPOrigin(origin) {
			t.Fatalf("origin %q unexpectedly rejected", origin)
		}
	}

	for _, handler := range []func(http.ResponseWriter, *http.Request){a.handleDebugWebSocket, a.handleCDPWebSocket} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
		req.RemoteAddr = "10.0.0.1:1"
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("remote handler status=%d", recorder.Code)
		}
	}

	a.closing.Store(true)
	if a.reserveOwner("upstream") || a.reserveOwner("cdp") {
		t.Fatal("closing app reserved an owner")
	}
	a.closing.Store(false)
	a.debugClaimed = true
	if a.reserveOwner("upstream") {
		t.Fatal("claimed upstream reservation was replaced")
	}
	a.debugClaimed = false
	a.cdpClaimed = true
	if a.reserveOwner("cdp") {
		t.Fatal("claimed CDP reservation was replaced")
	}
	a.releaseReservation("upstream")
	a.releaseReservation("cdp")

	client := &wsClient{conn: &coverageWebsocketConnection{}, generation: 1}
	a.closing.Store(true)
	if a.installOwner("upstream", client) || a.installOwner("cdp", client) {
		t.Fatal("install succeeded while closing")
	}
	a.closing.Store(false)
	a.debugOwner = client
	if a.installOwner("upstream", client) {
		t.Fatal("duplicate upstream install succeeded")
	}
	a.debugOwner = nil
	a.Contexts.Upsert(bridgecontext.Context{ID: "seed", Target: "seed-target"})
	a.Contexts.Select("seed")
	if !a.installOwner("upstream", client) {
		t.Fatal("fresh upstream install failed")
	}
	client.onError(errors.New("upstream callback"))
	a.connectionWG.Done()
	if !a.releaseOwner("upstream", client) {
		t.Fatal("upstream release failed")
	}
	a.finishOwnerRelease("upstream", client)
	cdpClient := &wsClient{conn: &coverageWebsocketConnection{}, generation: 1}
	if !a.installOwner("cdp", cdpClient) {
		t.Fatal("fresh CDP install failed")
	}
	cdpClient.onError(errors.New("cdp callback"))
	a.connectionWG.Done()
	if !a.releaseOwner("cdp", cdpClient) {
		t.Fatal("CDP release failed")
	}
	a.finishOwnerRelease("cdp", cdpClient)
	a.cdpOwner = &wsClient{generation: 9}
	if a.releaseOwner("cdp", cdpClient) {
		t.Fatal("stale CDP release claimed current owner")
	}
	a.cdpOwner = nil

	for _, handler := range []func(http.ResponseWriter, *http.Request){a.handleDebugWebSocket, a.handleCDPWebSocket} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
		req.RemoteAddr = "127.0.0.1:1"
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("upgrade failure status=%d", recorder.Code)
		}
	}

	if !a.ownerCurrent("upstream", &wsClient{}) {
		t.Fatal("zero generation owner was not treated as legacy")
	}
	if ownerCurrent := a.ownerCurrent("upstream", &wsClient{generation: 9}); ownerCurrent {
		t.Fatal("unknown owner was current")
	}
	if shouldReportWebSocketError(nil) || shouldReportWebSocketError(net.ErrClosed) {
		t.Fatal("nil/closed WebSocket errors should be quiet")
	}
	if !shouldReportWebSocketError(errors.New("unexpected")) {
		t.Fatal("unexpected WebSocket error was hidden")
	}

	a.Contexts.Upsert(bridgecontext.Context{ID: "clear-me"})
	a.clearContextsLocked()
	if a.Contexts.Len() != 0 {
		t.Fatal("clearContextsLocked left state")
	}
	var observed atomic.Int32
	a.SetObserver(Observer{OnError: func(RuntimeError) { observed.Add(1) }})
	for i := 0; i < maxRuntimeErrors+2; i++ {
		a.reportRuntimeError("test", uint64(i), errors.New("runtime"))
	}
	if len(a.RuntimeErrors()) != maxRuntimeErrors || observed.Load() != maxRuntimeErrors+2 {
		t.Fatalf("runtime ring len=%d callbacks=%d", len(a.RuntimeErrors()), observed.Load())
	}
	var zero App
	zero.reportRuntimeError("nil", 0, nil)
	zero.reportRuntimeError("nil-logger", 0, errors.New("runtime"))

	a.Requests = cdp.NewCorrelatorWithOptions(cdp.CorrelatorOptions{MaxPending: 1})
	a.Contexts.Upsert(bridgecontext.Context{ID: "ctx"})
	a.Contexts.Select("ctx")
	c := &wsClient{conn: &coverageWebsocketConnection{}, typeID: websocket.TextMessage, generation: 1}
	a.cdpOwner, a.cdpGeneration = c, 1
	a.Requests.Add(cdp.Request{ID: "held", Method: "held"})
	a.dispatchMu.Lock()
	a.sendCDPErrorLocked(c, "id", -1, "direct")
	a.dispatchMu.Unlock()
	a.handleCDPFrame(websocket.TextMessage, []byte(`{"id":"new","method":"Runtime.enable"}`), c)
	if a.Requests.Len() != 1 {
		t.Fatal("pending overflow displaced existing request")
	}

	writeStarted := make(chan struct{})
	failing := &wsClient{conn: &hardeningWriteErrorConnection{started: writeStarted}, typeID: websocket.TextMessage, generation: 1}
	failing.onError = func(err error) { a.reportRuntimeError("test-writer", failing.generation, err) }
	if err := failing.Send([]byte("error")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-writeStarted:
	case <-time.After(time.Second):
		t.Fatal("write failure path did not execute")
	}
	_ = failing.Close()
}

func TestHardeningGenerationAndRoutingBranches(t *testing.T) {
	var logs synchronizedBuffer
	a := New(0, 0, logging.NewWithWriters(false, false, &logs, &logs))
	registry, err := bridgecontext.NewRegistryWithCapacity(1)
	if err != nil {
		t.Fatal(err)
	}
	a.Contexts = registry
	a.Contexts.BeginGeneration(2)
	categoryData := func(category, id string) []byte {
		data, encodeErr := wmpf.EncodeCategory(category, wmpf.JsContext{ID: id, Name: id})
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		return data
	}
	a.handleDebugMessageLocked(wmpf.DebugMessage{Category: wmpf.CategoryPing})
	a.handleUnwrappedDebugForGeneration(wmpf.Unwrapped{
		Category: wmpf.CategoryAddJsContext,
		Data:     categoryData(wmpf.CategoryAddJsContext, "stale-add"),
	}, 1)
	a.handleUnwrappedDebugForGeneration(wmpf.Unwrapped{
		Category: wmpf.CategoryAddJsContext,
		Data:     categoryData(wmpf.CategoryAddJsContext, "current"),
	}, 2)
	a.handleUnwrappedDebugForGeneration(wmpf.Unwrapped{
		Category: wmpf.CategoryAddJsContext,
		Data:     categoryData(wmpf.CategoryAddJsContext, "overflow"),
	}, 2)
	a.handleUnwrappedDebugForGeneration(wmpf.Unwrapped{
		Category: wmpf.CategoryRemoveJsContext,
		Data:     categoryData(wmpf.CategoryRemoveJsContext, "current"),
	}, 1)
	a.handleUnwrappedDebugForGeneration(wmpf.Unwrapped{
		Category: wmpf.CategoryConnectJsContext,
		Data:     categoryData(wmpf.CategoryConnectJsContext, "missing"),
	}, 1)
	a.handleUnwrappedDebugForGeneration(wmpf.Unwrapped{
		Category: wmpf.CategoryConnectJsContext,
		Data:     categoryData(wmpf.CategoryConnectJsContext, "current"),
	}, 1)
	a.handleUnwrappedDebugForGeneration(wmpf.Unwrapped{
		Category: wmpf.CategoryConnectJsContext,
		Data:     categoryData(wmpf.CategoryConnectJsContext, "missing"),
	}, 2)
	if a.ConnectionSnapshot().StaleDrops < 3 {
		t.Fatalf("stale drops=%d", a.ConnectionSnapshot().StaleDrops)
	}

	readerApp := New(0, 0, logging.NewWithWriters(false, false, &logs, &logs))
	currentDebug := &wsClient{generation: 2}
	staleDebug := &wsClient{conn: &hardeningControlThenCloseConnection{}, generation: 1}
	readerApp.debugOwner, readerApp.debugGeneration = currentDebug, 2
	if readerApp.handleDebugFrameForClient(staleDebug, websocket.BinaryMessage, []byte{0xff}) {
		t.Fatal("stale debug frame was routed")
	}
	readerApp.connectionWG.Add(1)
	readerApp.readDebug(staleDebug)
	readerApp.Requests.Add(cdp.Request{ID: "reader"})
	readerApp.readCDP(&wsClient{conn: &coverageWebsocketConnection{}})
	if readerApp.Requests.Len() != 0 {
		t.Fatal("legacy CDP reader did not clear pending")
	}
	if readerApp.handleCDPFrame(websocket.TextMessage, make([]byte, websocketMaxMessageBytes+1), nil) {
		t.Fatal("oversized nil-owner CDP frame was routed")
	}
	if err := readerApp.SendCDPRoute(make([]byte, websocketMaxMessageBytes+1), ""); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversized route error=%v", err)
	}
	if err := readerApp.SendCDPRoute([]byte("null"), ""); !errors.Is(err, ErrInvalidCDPPayload) {
		t.Fatalf("invalid route error=%v", err)
	}

	subscriptions := New(0, 0, logging.NewWithWriters(false, false, &logs, &logs))
	subscriptions.subscriptions = nil
	clientOne := &wsClient{generation: 1}
	clientTwo := &wsClient{generation: 2}
	for _, method := range []string{"", "Runtime.evaluate", "Runtime.enable", "Runtime.enable"} {
		if err := subscriptions.trackSubscriptionLocked(method, clientOne); err != nil {
			t.Fatalf("track %q: %v", method, err)
		}
	}
	if err := subscriptions.trackSubscriptionLocked("Runtime.disable", clientTwo); err != nil {
		t.Fatal(err)
	}
	if _, exists := subscriptions.subscriptions["Runtime"]; !exists {
		t.Fatal("wrong generation disabled subscription")
	}
	if err := subscriptions.trackSubscriptionLocked("Runtime.disable", clientOne); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxCDPSubscriptions; i++ {
		subscriptions.subscriptions[fmt.Sprintf("Domain%d", i)] = 1
	}
	if err := subscriptions.trackSubscriptionLocked("Overflow.enable", clientOne); !errors.Is(err, ErrSubscriptionLimit) {
		t.Fatalf("subscription overflow=%v", err)
	}
	outboundConnection := newQueuedTestConnection(false)
	outboundClient := &wsClient{conn: outboundConnection, typeID: websocket.TextMessage, generation: 1}
	subscriptions.sendCDPToContextLocked(`{"id":"overflow","method":"Overflow.enable"}`, outboundClient, "ctx")
	select {
	case <-outboundConnection.started:
	case <-time.After(time.Second):
		t.Fatal("subscription rejection was not written")
	}
	_ = outboundClient.Close()
	subscriptions.sendCDPErrorLocked(nil, 1, -1, "nil")
	subscriptions.sendCDPErrorLocked(&wsClient{conn: &coverageWebsocketConnection{}}, func() {}, -1, "marshal")
	closedClient := &wsClient{conn: &coverageWebsocketConnection{}, generation: 1}
	closedClient.initialize()
	closedClient.closed.Store(true)
	subscriptions.sendCDPErrorLocked(closedClient, 1, -1, "closed")
	_ = closedClient.Close()

	cancellations := New(0, 0, logging.NewWithWriters(false, false, &logs, &logs))
	cancellations.Requests.Add(cdp.Request{ID: "legacy"})
	if !cancellations.CancelCDPRequest("legacy") {
		t.Fatal("legacy cancellation failed")
	}
	cancellations.cdpOwner = &wsClient{generation: 3}
	cancellations.cdpGeneration = 3
	if err := cancellations.Requests.TryAddFor("controller", 3, cdp.Request{ID: "owned"}); err != nil {
		t.Fatal(err)
	}
	if !cancellations.CancelCDPRequest("owned") {
		t.Fatal("generation cancellation failed")
	}
}
