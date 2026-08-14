package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

func TestAuditConcurrentCDPDispatchHasStrictGlobalSequence(t *testing.T) {
	dp, cp := freePort(t), freePort(t)
	a := New(dp, cp, logging.New(false, false))
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.Close(ctx)
	}()

	debug := auditDial(t, dp)
	defer debug.Close()
	deadline := time.Now().Add(time.Second)
	for a.DebugClientCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if count := a.DebugClientCount(); count != 1 {
		t.Fatalf("debug client count=%d want 1", count)
	}
	// The upstream connect self-bootstraps Runtime.enable and consumes the
	// first outgoing sequence number; drain it so every frame below is a
	// SendCDP payload with a stable, shifted sequence.
	bootstrap := auditRead(t, debug, websocket.BinaryMessage)
	bootstrapOuter, err := wmpf.DecodeDebugMessage(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapChrome, err := wmpf.DecodeChrome(bootstrapOuter.Data)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrapOuter.Seq != 1 || bootstrapChrome.JSContextID != "" || bootstrapChrome.Payload != `{"id":1,"method":"Runtime.enable"}` {
		t.Fatalf("bootstrap outer=%+v chrome=%+v", bootstrapOuter, bootstrapChrome)
	}
	const clientCount = 4
	const perClient = 32
	var group sync.WaitGroup
	for clientID := 0; clientID < clientCount; clientID++ {
		group.Add(1)
		go func(clientID int) {
			defer group.Done()
			for j := 0; j < perClient; j++ {
				body := fmt.Sprintf(`{"id":%d,"method":"Runtime.evaluate","params":{"client":%d,"n":%d}}`, clientID*perClient+j, clientID, j)
				if err := a.SendCDP([]byte(body)); err != nil {
					t.Errorf("client %d write %d: %v", clientID, j, err)
					return
				}
			}
		}(clientID)
	}
	group.Wait()

	seen := make(map[string]bool, clientCount*perClient)
	for seq := 1; seq <= clientCount*perClient; seq++ {
		frame := auditRead(t, debug, websocket.BinaryMessage)
		outer, err := wmpf.DecodeDebugMessage(frame)
		if err != nil {
			t.Fatalf("frame %d decode: %v", seq, err)
		}
		if int(outer.Seq) != seq+1 {
			t.Fatalf("frame position %d has seq %d", seq, outer.Seq)
		}
		chrome, err := wmpf.DecodeChrome(outer.Data)
		if err != nil {
			t.Fatalf("frame %d chrome decode: %v", seq, err)
		}
		seen[chrome.Payload] = true
	}
	if len(seen) != clientCount*perClient {
		t.Fatalf("unique payloads=%d want %d", len(seen), clientCount*perClient)
	}
}
