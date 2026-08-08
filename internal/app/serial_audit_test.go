package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"miniapp-bridge/internal/logging"
	"miniapp-bridge/internal/wmpf"
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
	const clientCount = 4
	const perClient = 32
	clients := make([]*websocket.Conn, clientCount)
	for i := range clients {
		clients[i] = auditDial(t, cp)
		defer clients[i].Close()
	}
	var group sync.WaitGroup
	for clientID, conn := range clients {
		group.Add(1)
		go func(clientID int, conn *websocket.Conn) {
			defer group.Done()
			for j := 0; j < perClient; j++ {
				body := fmt.Sprintf(`{"id":%d,"method":"Runtime.evaluate","params":{"client":%d,"n":%d}}`, clientID*perClient+j, clientID, j)
				if err := conn.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
					t.Errorf("client %d write %d: %v", clientID, j, err)
					return
				}
			}
		}(clientID, conn)
	}
	group.Wait()

	seen := make(map[string]bool, clientCount*perClient)
	for seq := 1; seq <= clientCount*perClient; seq++ {
		frame := auditRead(t, debug, websocket.BinaryMessage)
		outer, err := wmpf.DecodeDebugMessage(frame)
		if err != nil {
			t.Fatalf("frame %d decode: %v", seq, err)
		}
		if int(outer.Seq) != seq {
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
