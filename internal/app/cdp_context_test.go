package app

import (
	"reflect"
	"testing"

	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

func TestCDPRuntimeContextEventsRegisterRouteAndRemove(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	const generation = 7
	a.Contexts.BeginGeneration(generation)
	debug := &routeCaptureClient{}
	a.DebugHub.Add(debug)
	var observed []string
	a.SetObserver(Observer{
		OnContext: func(event ContextEvent) { observed = append(observed, "context:"+event.Kind) },
		OnCDP:     func([]byte) { observed = append(observed, "cdp") },
	})

	created := `{"method":"Runtime.executionContextCreated","params":{"context":{"id":1,"name":"game"}}}`
	a.handleUnwrappedDebugForGeneration(
		mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: created}),
		generation,
	)

	contexts := a.Contexts.List()
	want := bridgecontext.Context{ID: "1", Target: "game"}
	if len(contexts) != 1 || contexts[0] != want {
		t.Fatalf("contexts=%+v want [%+v]", contexts, want)
	}
	if selected, ok := a.Contexts.Selected(); !ok || selected != want {
		t.Fatalf("selected=%+v ok=%v want %+v", selected, ok, want)
	}
	if wantOrder := []string{"context:added", "cdp"}; !reflect.DeepEqual(observed, wantOrder) {
		t.Fatalf("observer order=%v want %v", observed, wantOrder)
	}
	if err := a.SendCDPRoute([]byte(`{"id":1,"method":"Runtime.evaluate"}`), ""); err != nil {
		t.Fatalf("SendCDPRoute after executionContextCreated: %v", err)
	}
	frames := debug.snapshot()
	if len(frames) != 1 {
		t.Fatalf("routed frames=%d want 1", len(frames))
	}
	_, routed := decodeRouteFrame(t, frames[0])
	if routed.JSContextID != "1" {
		t.Fatalf("routed context=%q want 1", routed.JSContextID)
	}

	destroyed := `{"method":"Runtime.executionContextDestroyed","params":{"executionContextId":1}}`
	a.handleUnwrappedDebugForGeneration(
		mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: destroyed}),
		generation,
	)
	if contexts := a.Contexts.List(); len(contexts) != 0 {
		t.Fatalf("contexts after destroy=%+v", contexts)
	}
	if err := a.SendCDPRoute([]byte(`{"id":2,"method":"Runtime.evaluate"}`), ""); err != nil {
		t.Fatalf("SendCDPRoute after executionContextDestroyed: %v", err)
	}
	frames = debug.snapshot()
	if len(frames) != 2 {
		t.Fatalf("routed frames after destroy=%d want 2", len(frames))
	}
	_, routed = decodeRouteFrame(t, frames[1])
	if routed.JSContextID != "" {
		t.Fatalf("bootstrap route after destroy=%q want empty", routed.JSContextID)
	}
}

func TestCDPRuntimeContextIDPreservesNumericLexeme(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	const id = "9007199254740993"
	created := `{"method":"Runtime.executionContextCreated","params":{"context":{"id":` + id + `,"name":"precise"}}}`
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: created}))

	context, ok := a.Contexts.Get(id)
	if !ok || context != (bridgecontext.Context{ID: id, Target: "precise"}) {
		t.Fatalf("precise context=%+v ok=%v", context, ok)
	}

	destroyed := `{"method":"Runtime.executionContextDestroyed","params":{"executionContextId":` + id + `}}`
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: destroyed}))
	if _, ok := a.Contexts.Get(id); ok {
		t.Fatalf("precise context %s survived destroy", id)
	}

	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: `{"method":"Runtime.executionContextDestroyed","params":{}}`}))
}

func TestCDPRuntimeContextEventGenerationAndCapacityIsolation(t *testing.T) {
	registry, err := bridgecontext.NewRegistryWithCapacity(1)
	if err != nil {
		t.Fatal(err)
	}
	a := New(0, 0, logging.New(false, false))
	a.Contexts = registry
	a.Contexts.BeginGeneration(4)
	if err := a.Contexts.UpsertForGeneration(4, bridgecontext.Context{ID: "private"}); err != nil {
		t.Fatal(err)
	}
	cdpClient := &routeCaptureClient{}
	a.CDPHub.Add(cdpClient)

	stale := `{"method":"Runtime.executionContextCreated","params":{"context":{"id":1,"name":"stale"}}}`
	a.handleUnwrappedDebugForGeneration(
		mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: stale}),
		3,
	)
	if got := len(cdpClient.snapshot()); got != 0 {
		t.Fatalf("stale event broadcasts=%d want 0", got)
	}
	if a.ConnectionSnapshot().StaleDrops != 1 {
		t.Fatalf("stale drops=%d want 1", a.ConnectionSnapshot().StaleDrops)
	}

	overflow := `{"method":"Runtime.executionContextCreated","params":{"context":{"id":2,"name":"overflow"}}}`
	a.handleUnwrappedDebugForGeneration(
		mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: overflow}),
		4,
	)
	frames := cdpClient.snapshot()
	if len(frames) != 1 || string(frames[0]) != overflow {
		t.Fatalf("capacity event broadcasts=%q", frames)
	}
	if _, exists := a.Contexts.Get("2"); exists {
		t.Fatal("capacity-limited context was registered")
	}
	runtimeErrors := a.RuntimeErrors()
	if len(runtimeErrors) != 2 || runtimeErrors[0].Component != "upstream-reader" || runtimeErrors[1].Component != "context-registry" {
		t.Fatalf("runtime errors=%+v", runtimeErrors)
	}
}

func TestCDPExecutionContextsClearedRemovesAllRuntimeContexts(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	const generation = 9
	a.Contexts.BeginGeneration(generation)
	a.handleUnwrappedDebugForGeneration(
		mustUnwrappedCategory(t, wmpf.CategoryAddJsContext, wmpf.JsContext{ID: "private", Name: "app-service"}),
		generation,
	)
	for _, payload := range []string{
		`{"method":"Runtime.executionContextCreated","params":{"context":{"id":1,"name":"main"}}}`,
		`{"method":"Runtime.executionContextCreated","params":{"context":{"id":2,"name":"worker"}}}`,
	} {
		a.handleUnwrappedDebugForGeneration(
			mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: payload}),
			generation,
		)
	}
	var removed []bridgecontext.Context
	a.SetObserver(Observer{OnContext: func(event ContextEvent) {
		if event.Kind == "removed" {
			removed = append(removed, event.Context)
		}
	}})

	a.handleUnwrappedDebugForGeneration(
		mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: `{"method":"Runtime.executionContextsCleared"}`}),
		generation,
	)
	contexts := a.Contexts.List()
	if len(contexts) != 0 {
		t.Fatalf("contexts after executionContextsCleared=%+v", contexts)
	}
	wantRemoved := []bridgecontext.Context{
		{ID: "private", Target: "app-service"},
		{ID: "1", Target: "main"},
		{ID: "2", Target: "worker"},
	}
	if !reflect.DeepEqual(removed, wantRemoved) {
		t.Fatalf("removed context events=%+v want %+v", removed, wantRemoved)
	}

	legacy := New(0, 0, logging.New(false, false))
	legacy.Contexts.Upsert(bridgecontext.Context{ID: "legacy"})
	legacy.handleUnwrappedDebug(
		mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: `{"method":"Runtime.executionContextsCleared"}`}),
	)
	if contexts := legacy.Contexts.List(); len(contexts) != 0 {
		t.Fatalf("legacy contexts after executionContextsCleared=%+v", contexts)
	}
}

func TestCDPRuntimeContextLegacyCapacityErrorStillBroadcasts(t *testing.T) {
	registry, err := bridgecontext.NewRegistryWithCapacity(1)
	if err != nil {
		t.Fatal(err)
	}
	registry.Upsert(bridgecontext.Context{ID: "existing"})
	a := New(0, 0, logging.New(false, false))
	a.Contexts = registry
	client := &routeCaptureClient{}
	a.CDPHub.Add(client)
	payload := `{"method":"Runtime.executionContextCreated","params":{"context":{"id":2,"name":"overflow"}}}`
	a.handleUnwrappedDebug(mustUnwrappedCategory(t, wmpf.CategoryChromeDevtoolsResult, wmpf.ChromeDevtools{Payload: payload}))

	if _, ok := a.Contexts.Get("2"); ok {
		t.Fatal("legacy capacity-limited context was registered")
	}
	if frames := client.snapshot(); len(frames) != 1 || string(frames[0]) != payload {
		t.Fatalf("legacy capacity event broadcasts=%q", frames)
	}
	runtimeErrors := a.RuntimeErrors()
	if len(runtimeErrors) != 1 || runtimeErrors[0].Component != "context-registry" || runtimeErrors[0].Generation != 0 {
		t.Fatalf("legacy runtime errors=%+v", runtimeErrors)
	}
}

func TestUpstreamGenerationDisconnectPublishesContextRemoval(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	a.Contexts.Upsert(bridgecontext.Context{ID: "1", Target: "main"})
	a.Contexts.Upsert(bridgecontext.Context{ID: "2", Target: "worker"})
	client := newWSClient(newQueuedTestConnection(false), websocket.BinaryMessage)
	if !a.installOwner("upstream", client) {
		t.Fatal("install upstream owner")
	}

	var removed []bridgecontext.Context
	a.SetObserver(Observer{OnContext: func(event ContextEvent) {
		if event.Kind == "removed" {
			removed = append(removed, event.Context)
		}
	}})
	a.readDebug(client)

	if contexts := a.Contexts.List(); len(contexts) != 0 {
		t.Fatalf("contexts after disconnect=%+v", contexts)
	}
	want := []bridgecontext.Context{{ID: "1", Target: "main"}, {ID: "2", Target: "worker"}}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("disconnect removals=%+v want %+v", removed, want)
	}
	if _, active := a.Contexts.CurrentGeneration(); active {
		t.Fatal("context generation remained active after disconnect")
	}
}
