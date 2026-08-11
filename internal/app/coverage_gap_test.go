package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

func TestCoverageGapOwnerSpecificDebugLimit(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	client := &wsClient{conn: &coverageWebsocketConnection{}, generation: 3}
	a.debugOwner = client
	a.debugGeneration = client.generation
	if a.handleDebugFrameForClient(client, websocket.BinaryMessage, []byte(strings.Repeat("x", int(websocketMaxMessageBytes)+1))) {
		t.Fatal("oversized owner-specific upstream frame was accepted")
	}
	if got := a.RuntimeErrors(); len(got) != 1 || got[0].Generation != client.generation {
		t.Fatalf("runtime errors=%+v", got)
	}
}

func TestCoverageGapConnectSelectionRace(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	const generation = uint64(1)
	const id = "race-context"
	a.Contexts.BeginGeneration(generation)
	var selected atomic.Int64
	a.SetObserver(Observer{OnContext: func(event ContextEvent) {
		if event.Kind == "selected" {
			selected.Add(1)
		}
	}})

	data, err := wmpf.EncodeCategory(wmpf.CategoryConnectJsContext, wmpf.JsContext{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var remover sync.WaitGroup
	remover.Add(1)
	go func() {
		defer remover.Done()
		for {
			select {
			case <-stop:
				return
			default:
				a.Contexts.Remove(id)
				runtime.Gosched()
			}
		}
	}()

	const attempts = 500000
	for i := 0; i < attempts; i++ {
		a.handleUnwrappedDebugForGeneration(wmpf.Unwrapped{Category: wmpf.CategoryConnectJsContext, Data: data}, generation)
		if selected.Load() != int64(i+1) {
			break
		}
	}
	close(stop)
	remover.Wait()
	if selected.Load() == attempts {
		t.Fatalf("did not observe a context selection failure after %d attempts", attempts)
	}
}

func TestCoverageGapCDPInstallAfterUpgradeRejected(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	server := httptest.NewServer(http.HandlerFunc(a.handleCDPWebSocket))
	defer server.Close()

	oldCheckOrigin := localUpgrader.CheckOrigin
	localUpgrader.CheckOrigin = func(r *http.Request) bool {
		a.closing.Store(true)
		if oldCheckOrigin != nil {
			return oldCheckOrigin(r)
		}
		return true
	}
	defer func() {
		localUpgrader.CheckOrigin = oldCheckOrigin
		a.closing.Store(false)
	}()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, response, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if conn != nil {
		_ = conn.Close()
	}
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil && conn == nil {
		t.Fatal("CDP upgrade returned neither a connection nor an error")
	}
	deadline := time.Now().Add(time.Second)
	for {
		a.connMu.RLock()
		claimed, owner := a.cdpClaimed, a.cdpOwner
		a.connMu.RUnlock()
		if !claimed && owner == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("CDP owner state did not settle: owner=%p claimed=%v", owner, claimed)
		}
		time.Sleep(time.Millisecond)
	}
	if a.cdpOwner != nil || a.cdpClaimed {
		t.Fatalf("CDP owner state leaked after rejected install: owner=%p claimed=%v", a.cdpOwner, a.cdpClaimed)
	}
	_ = a.Close(context.Background())
}

func TestCoverageGapDebugInstallAfterUpgradeRejected(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	server := httptest.NewServer(http.HandlerFunc(a.handleDebugWebSocket))
	defer server.Close()

	oldCheckOrigin := localUpgrader.CheckOrigin
	localUpgrader.CheckOrigin = func(r *http.Request) bool {
		a.closing.Store(true)
		if oldCheckOrigin != nil {
			return oldCheckOrigin(r)
		}
		return true
	}
	defer func() {
		localUpgrader.CheckOrigin = oldCheckOrigin
		a.closing.Store(false)
	}()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, response, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if conn != nil {
		_ = conn.Close()
	}
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil && conn == nil {
		t.Fatal("upstream upgrade returned neither a connection nor an error")
	}
	deadline := time.Now().Add(time.Second)
	for {
		a.connMu.RLock()
		claimed, owner := a.debugClaimed, a.debugOwner
		a.connMu.RUnlock()
		if !claimed && owner == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("upstream owner state did not settle: owner=%p claimed=%v", owner, claimed)
		}
		time.Sleep(time.Millisecond)
	}
	if a.debugOwner != nil || a.debugClaimed {
		t.Fatalf("upstream owner state leaked after rejected install: owner=%p claimed=%v", a.debugOwner, a.debugClaimed)
	}
	_ = a.Close(context.Background())
}
