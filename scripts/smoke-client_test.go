package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

func fakeCDPServer(t *testing.T) (string, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		var writeMu sync.Mutex
		write := func(value any) {
			writeMu.Lock()
			defer writeMu.Unlock()
			if err := conn.WriteJSON(value); err != nil {
				t.Logf("fake CDP write after close: %v", err)
			}
		}
		pausedID := 0
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request struct {
				ID     int            `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if err := json.Unmarshal(data, &request); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			response := map[string]any{"id": request.ID, "result": map[string]any{}}
			switch request.Method {
			case "Runtime.enable":
				write(map[string]any{"method": "Runtime.executionContextCreated", "params": map[string]any{"context": map[string]any{"id": 1}}})
			case "Runtime.evaluate":
				expression, _ := request.Params["expression"].(string)
				switch {
				case expression == "1+1":
					response["result"] = map[string]any{"result": map[string]any{"value": 2}}
				case strings.HasPrefix(expression, "({alpha:"):
					response["result"] = map[string]any{"result": map[string]any{"objectId": "fake-object-1"}}
				case strings.HasPrefix(expression, "throw new Error"):
					response["result"] = map[string]any{"exceptionDetails": map[string]any{"text": "Uncaught"}}
				case strings.HasPrefix(expression, "console.log"):
					write(map[string]any{"method": "Runtime.consoleAPICalled", "params": map[string]any{"type": "log"}})
				case strings.Contains(expression, "sourceURL=miniapp-bridge-matrix.js"):
					write(map[string]any{"method": "Debugger.scriptParsed", "params": map[string]any{"scriptId": "1", "url": "miniapp-bridge-matrix.js"}})
				case strings.HasPrefix(expression, "debugger;"):
					pausedID = request.ID
					write(map[string]any{"method": "Debugger.paused", "params": map[string]any{"callFrames": []any{map[string]any{"callFrameId": "1"}}}})
					continue
				case expression == "globalThis.__miniappBridgeInputClickCount":
					response["result"] = map[string]any{"result": map[string]any{"value": 1}}
				case strings.Contains(expression, "__miniapp_bridge_key_probe').value"):
					response["result"] = map[string]any{"result": map[string]any{"value": "A"}}
				default:
					var longValue string
					if json.Unmarshal([]byte(expression), &longValue) == nil && strings.HasPrefix(longValue, "miniapp-bridge-") {
						response["result"] = map[string]any{"result": map[string]any{"value": longValue}}
					}
				}
			case "Debugger.resume":
				write(response)
				if pausedID != 0 {
					write(map[string]any{"id": pausedID, "result": map[string]any{"result": map[string]any{"value": 42}}})
					pausedID = 0
				}
				continue
			case "DOM.getDocument":
				response["result"] = map[string]any{"root": map[string]any{"nodeId": 1}}
			case "DOM.querySelector":
				response["result"] = map[string]any{"nodeId": 2}
			case "DOM.getBoxModel":
				response["result"] = map[string]any{"model": map[string]any{"content": []float64{0, 0, 100, 0, 100, 100, 0, 100}}}
			case "MiniAppBridge.invalidMethod":
				response = map[string]any{"id": request.ID, "error": map[string]any{"code": -32601, "message": "method not found"}}
			}
			write(response)
		}
	}))
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	return url, server.Close
}

func TestLinkAndMatrixClients(t *testing.T) {
	url, closeServer := fakeCDPServer(t)
	defer closeServer()
	if err := runLink(url); err != nil {
		t.Fatal(err)
	}
	if err := runMatrix(url); err != nil {
		t.Fatal(err)
	}
	if err := runInteraction(url); err != nil {
		t.Fatal(err)
	}
}

func TestExpectReceiveOrder(t *testing.T) {
	c := &client{received: []receivedFrame{
		{Sequence: 1, Kind: "event", Method: "Runtime.executionContextCreated"},
		{Sequence: 2, Kind: "response", ID: 7, Method: "Runtime.enable"},
		{Sequence: 3, Kind: "event", Method: "Debugger.scriptParsed"},
	}}
	sequences, err := c.expectReceiveOrder(0,
		receiveExpectation{Kind: "event", Method: "Runtime.executionContextCreated"},
		receiveExpectation{Kind: "response", ID: 7, Method: "Runtime.enable"},
		receiveExpectation{Kind: "event", Method: "Debugger.scriptParsed"},
	)
	if err != nil || fmt.Sprint(sequences) != "[1 2 3]" {
		t.Fatalf("ordered sequence = %v, %v", sequences, err)
	}
	for _, test := range []struct {
		name  string
		after int
		want  []receiveExpectation
	}{
		{name: "invalid checkpoint", after: 4},
		{name: "reverse order", want: []receiveExpectation{
			{Kind: "response", ID: 7, Method: "Runtime.enable"},
			{Kind: "event", Method: "Runtime.executionContextCreated"},
		}},
		{name: "missing frame", want: []receiveExpectation{{Kind: "response", ID: 8, Method: "Runtime.enable"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := c.expectReceiveOrder(test.after, test.want...); err == nil {
				t.Fatal("expected receive-order error")
			}
		})
	}
}
