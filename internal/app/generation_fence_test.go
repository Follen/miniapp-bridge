package app

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/Follen/miniapp-bridge/internal/cdp"
	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

type cdpErrorEnvelope struct {
	ID    json.RawMessage `json:"id"`
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeCDPErrorEnvelope(t *testing.T, body []byte) cdpErrorEnvelope {
	t.Helper()
	var envelope cdpErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode CDP error %q: %v", body, err)
	}
	return envelope
}

func captureMessageCount(c *appCaptureClient) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

func installTestController(t *testing.T, a *App) (*wsClient, *queuedTestConnection) {
	t.Helper()
	connection := newQueuedTestConnection(false)
	client := newWSClient(connection, websocket.TextMessage)
	if !a.installOwner("cdp", client) {
		t.Fatal("install CDP controller")
	}
	return client, connection
}

func TestControllerGenerationFenceRejectsReuseAndDropsLateResponse(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	a.Contexts.Upsert(bridgecontext.Context{ID: "ctx"})
	a.Contexts.Select("ctx")
	upstream := &appCaptureClient{}
	a.DebugHub.Add(upstream)
	defer a.DebugHub.Remove(upstream)

	first, _ := installTestController(t, a)
	request := []byte(`{"id":1,"method":"Runtime.enable"}`)
	if !a.handleCDPFrame(websocket.TextMessage, request, first) {
		t.Fatal("generation one request was not routed")
	}
	if got := a.Requests.LenFor("controller", first.generation); got != 1 {
		t.Fatalf("generation one pending=%d", got)
	}
	if got := captureMessageCount(upstream); got != 1 {
		t.Fatalf("upstream request count=%d want 1", got)
	}

	// A controller disconnect drains its scoped pending requests into the
	// response fence before the next generation can claim the endpoint.
	a.readCDP(first)
	second, secondConnection := installTestController(t, a)
	if second.generation == first.generation {
		t.Fatalf("controller generation was reused: %d", second.generation)
	}
	if !a.handleCDPFrame(websocket.TextMessage, request, second) {
		t.Fatal("ambiguous generation two request was not handled")
	}
	waitFor(t, func() bool { return len(secondConnection.snapshot()) == 1 }, "ambiguity error was not written")
	ambiguous := decodeCDPErrorEnvelope(t, secondConnection.snapshot()[0])
	if string(ambiguous.ID) != "1" || ambiguous.Error.Code != cdpServerErrorCode || ambiguous.Error.Message != ErrCDPRequestAmbiguous.Error() {
		t.Fatalf("ambiguity error=%+v id=%s", ambiguous.Error, ambiguous.ID)
	}
	if got := a.Requests.LenFor("controller", second.generation); got != 0 {
		t.Fatalf("ambiguous request registered pending=%d", got)
	}
	if got := captureMessageCount(upstream); got != 1 {
		t.Fatalf("ambiguous request reached upstream: count=%d", got)
	}

	var observed atomic.Int32
	a.SetObserver(Observer{OnCDP: func([]byte) { observed.Add(1) }})
	latePayload := `{"id":1,"result":{"source":"generation-one"}}`
	lateData := wmpf.EncodeChrome(wmpf.ChromeDevtools{Payload: latePayload})
	a.dispatchMu.Lock()
	a.handleUnwrappedDebugForGeneration(wmpf.Unwrapped{Category: wmpf.CategoryChromeDevtoolsResult, Data: lateData}, 1)
	a.dispatchMu.Unlock()
	if got := len(secondConnection.snapshot()); got != 1 {
		t.Fatalf("late response reached generation two: messages=%d", got)
	}
	if observed.Load() != 0 {
		t.Fatal("late response reached observer")
	}
	if a.ConnectionSnapshot().StaleDrops == 0 {
		t.Fatal("late response drop was not counted")
	}

	// Consuming the old response removes the ambiguity, so the current
	// controller can retry the same request ID safely.
	if !a.handleCDPFrame(websocket.TextMessage, request, second) {
		t.Fatal("generation two retry was not routed")
	}
	if got := captureMessageCount(upstream); got != 2 {
		t.Fatalf("generation two retry upstream count=%d want 2", got)
	}
	if got := a.Requests.LenFor("controller", second.generation); got != 1 {
		t.Fatalf("generation two retry pending=%d", got)
	}
	a.readCDP(second)
}

func TestUpstreamDisconnectFailsControllerPendingWithStructuredError(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	a.Contexts.Upsert(bridgecontext.Context{ID: "ctx"})
	a.Contexts.Select("ctx")
	controller, connection := installTestController(t, a)
	upstream := newWSClient(&coverageWebsocketConnection{}, websocket.BinaryMessage)
	if !a.installOwner("upstream", upstream) {
		t.Fatal("install upstream")
	}

	request := []byte(`{"id":"pending","method":"Runtime.enable"}`)
	if !a.handleCDPFrame(websocket.TextMessage, request, controller) {
		t.Fatal("request was not routed")
	}
	if got := a.Requests.LenFor("controller", controller.generation); got != 1 {
		t.Fatalf("pending before upstream disconnect=%d", got)
	}

	a.readDebug(upstream)
	waitFor(t, func() bool { return len(connection.snapshot()) == 1 }, "disconnect error was not written")
	disconnected := decodeCDPErrorEnvelope(t, connection.snapshot()[0])
	if string(disconnected.ID) != `"pending"` || disconnected.Error.Code != cdpServerErrorCode || disconnected.Error.Message != ErrCDPUpstreamDisconnected.Error() {
		t.Fatalf("disconnect error=%+v id=%s", disconnected.Error, disconnected.ID)
	}
	if got := a.Requests.LenFor("controller", controller.generation); got != 0 {
		t.Fatalf("pending after upstream disconnect=%d", got)
	}

	// The old upstream is gone, so its tombstone is cleared and the same ID
	// can be retried on the next upstream generation.
	nextUpstream := newWSClient(&coverageWebsocketConnection{}, websocket.BinaryMessage)
	if !a.installOwner("upstream", nextUpstream) {
		t.Fatal("install replacement upstream")
	}
	if !a.handleCDPFrame(websocket.TextMessage, request, controller) {
		t.Fatal("post-disconnect retry was not handled")
	}
	if got := a.Requests.LenFor("controller", controller.generation); got != 1 {
		t.Fatalf("post-disconnect retry pending=%d", got)
	}
	a.readDebug(nextUpstream)
	a.readCDP(controller)
}

func TestCDPResponseFenceIsBoundedAndClearedByUpstreamGeneration(t *testing.T) {
	a := &App{}
	a.addCDPResponseFencesLocked([]cdp.Request{{ID: "one"}}, 1)
	if fenced, generation := a.cdpResponseFencedLocked("one"); !fenced || generation != 1 {
		t.Fatalf("fenced=%v generation=%d", fenced, generation)
	}
	if consumed, generation := a.consumeCDPResponseFenceLocked("one"); !consumed || generation != 1 {
		t.Fatalf("consumed=%v generation=%d", consumed, generation)
	}
	if consumed, _ := a.consumeCDPResponseFenceLocked("one"); consumed {
		t.Fatal("response fence consumed twice")
	}

	a.addCDPResponseFencesLocked([]cdp.Request{{ID: "persistent"}}, 2)
	if fenced, _ := a.cdpResponseFencedLocked("persistent"); !fenced {
		t.Fatal("response fence disappeared without a response or upstream generation change")
	}

	requests := make([]cdp.Request, maxCDPResponseFences+1)
	for index := range requests {
		requests[index].ID = index
	}
	a.addCDPResponseFencesLocked(requests, 9)
	if len(a.cdpResponseFences) != 0 || !a.cdpResponseFenceBlocked {
		t.Fatalf("saturated fence entries=%d blocked=%v", len(a.cdpResponseFences), a.cdpResponseFenceBlocked)
	}
	if fenced, generation := a.cdpResponseFencedLocked("any"); !fenced || generation != 0 {
		t.Fatalf("global fence=%v generation=%d", fenced, generation)
	}
	if consumed, generation := a.consumeCDPResponseFenceLocked("any"); !consumed || generation != 0 {
		t.Fatalf("global consume=%v generation=%d", consumed, generation)
	}
	if fenced, _ := a.cdpResponseFencedLocked("any"); !fenced {
		t.Fatal("global response fence was released by one arbitrary response")
	}
	a.clearCDPResponseFencesLocked()
	if fenced, _ := a.cdpResponseFencedLocked("any"); fenced {
		t.Fatal("upstream generation cleanup did not release global fence")
	}
}

func TestGenerationFenceFailureBranchesRemainBounded(t *testing.T) {
	empty := New(0, 0, logging.New(false, false))
	empty.failCurrentControllerRequestsLocked()

	invalid := New(0, 0, logging.New(false, false))
	invalidController := newWSClient(newQueuedTestConnection(false), websocket.TextMessage)
	invalidController.generation = 1
	invalid.cdpOwner = invalidController
	invalid.cdpGeneration = 1
	if err := invalid.Requests.TryAddFor("controller", 1, cdp.Request{ID: func() {}}); err != nil {
		t.Fatal(err)
	}
	invalid.failCurrentControllerRequestsLocked()
	if len(invalid.RuntimeErrors()) != 1 {
		t.Fatalf("marshal failure runtime errors=%d", len(invalid.RuntimeErrors()))
	}

	rejected := New(0, 0, logging.New(false, false))
	rejectedConnection := newQueuedTestConnection(false)
	rejectedController := newWSClient(rejectedConnection, websocket.TextMessage)
	rejectedController.generation = 2
	rejectedController.initialize()
	rejectedController.closed.Store(true)
	rejected.cdpOwner = rejectedController
	rejected.cdpGeneration = 2
	if err := rejected.Requests.TryAddFor("controller", 2, cdp.Request{ID: "closed"}); err != nil {
		t.Fatal(err)
	}
	rejected.failCurrentControllerRequestsLocked()
	waitFor(t, func() bool { return rejectedConnection.closeCalls.Load() == 1 }, "rejected controller was not closed")
	if len(rejected.RuntimeErrors()) != 1 {
		t.Fatalf("batch rejection runtime errors=%d", len(rejected.RuntimeErrors()))
	}

	batchClient := newWSClient(newQueuedTestConnection(false), websocket.TextMessage)
	if err := batchClient.SendBatch(nil); err != nil {
		t.Fatalf("empty batch error=%v", err)
	}
	if err := batchClient.Close(); err != nil {
		t.Fatal(err)
	}
}
