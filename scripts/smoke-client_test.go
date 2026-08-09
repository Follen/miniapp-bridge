package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

func fakeCDPServer(t *testing.T) (string, func()) {
	return fakeCDPServerMode(t, "")
}

func fakeCDPServerMode(t *testing.T, mode string) (string, func()) {
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
		breakpointID := "bp-1"
		breakpointSet := false
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
				case strings.HasPrefix(expression, "miniappBridgeMatrix()"):
					if breakpointSet {
						pausedID = request.ID
						write(map[string]any{"method": "Debugger.paused", "params": map[string]any{"reason": "breakpoint", "hitBreakpoints": []string{breakpointID}, "callFrames": []any{map[string]any{"callFrameId": "frame-1", "functionName": "miniappBridgeMatrix"}}}})
						continue
					}
					response["result"] = map[string]any{"result": map[string]any{"value": 42}}
				case strings.HasPrefix(expression, "throw new Error"):
					if mode == "bad-exception" {
						response["result"] = map[string]any{"exceptionDetails": map[string]any{"text": "Uncaught"}}
					} else {
						response["result"] = map[string]any{"exceptionDetails": map[string]any{"text": "Uncaught", "exception": map[string]any{"description": "Error: miniapp-bridge-matrix", "className": "Error"}, "stackTrace": map[string]any{"description": "Error: miniapp-bridge-matrix"}}}
					}
				case strings.HasPrefix(expression, "console.log"):
					write(map[string]any{"method": "Runtime.consoleAPICalled", "params": map[string]any{"type": "log", "args": []any{map[string]any{"type": "string", "value": "miniapp-bridge-matrix-console"}}}})
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
					parts := strings.Split(strings.TrimSpace(expression), " * ")
					if len(parts) == 2 {
						left, leftErr := strconv.Atoi(parts[0])
						right, rightErr := strconv.Atoi(parts[1])
						if leftErr == nil && rightErr == nil {
							value := left * right
							if mode == "bad-concurrent" {
								value = 0
							}
							response["result"] = map[string]any{"result": map[string]any{"value": value}}
						}
					}
					var longValue string
					if json.Unmarshal([]byte(expression), &longValue) == nil && strings.HasPrefix(longValue, "miniapp-bridge-") {
						response["result"] = map[string]any{"result": map[string]any{"value": longValue}}
					}
				}
			case "Debugger.resume":
				write(response)
				if pausedID != 0 {
					write(map[string]any{"method": "Debugger.resumed", "params": map[string]any{}})
					write(map[string]any{"id": pausedID, "result": map[string]any{"result": map[string]any{"value": 42}}})
					pausedID = 0
				}
				continue
			case "Debugger.setBreakpointByUrl":
				breakpointSet = true
				if mode == "bad-breakpoint" {
					response["result"] = map[string]any{}
				} else {
					response["result"] = map[string]any{"breakpointId": breakpointID, "locations": []any{map[string]any{"scriptId": "1", "lineNumber": 0, "columnNumber": 0}}}
				}
			case "Debugger.removeBreakpoint":
				if id, _ := request.Params["breakpointId"].(string); id != breakpointID {
					response["error"] = map[string]any{"code": -32000, "message": "unknown breakpoint"}
				}
				breakpointSet = false
			case "Runtime.getProperties":
				if mode == "bad-properties" {
					response["result"] = map[string]any{"result": []any{map[string]any{"name": "wrong"}}}
				} else {
					response["result"] = map[string]any{"result": []any{
						map[string]any{"name": "alpha", "value": map[string]any{"type": "number", "value": 1}},
						map[string]any{"name": "nested", "value": map[string]any{"type": "object", "description": "Object"}},
					}}
				}
			case "Page.getFrameTree":
				if mode == "bad-frame-tree" {
					response["result"] = map[string]any{"frameTree": map[string]any{}}
				} else {
					response["result"] = map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "frame-1", "url": "https://example.test/"}}}
				}
			case "DOM.getDocument":
				if mode == "bad-dom" {
					response["result"] = map[string]any{"root": map[string]any{}}
				} else {
					response["result"] = map[string]any{"root": map[string]any{"nodeId": 1}}
				}
			case "Network.getCookies":
				if mode == "bad-cookies" {
					response["result"] = map[string]any{"cookies": map[string]any{}}
				} else {
					response["result"] = map[string]any{"cookies": []any{map[string]any{"name": "session", "value": "fixture", "domain": "example.test", "path": "/"}}}
				}
			case "Performance.getMetrics":
				if mode == "bad-metrics" {
					response["result"] = map[string]any{"metrics": []any{map[string]any{"value": 1.0}}}
				} else {
					response["result"] = map[string]any{"metrics": []any{map[string]any{"name": "Timestamp", "value": 1.0}, map[string]any{"name": "Documents", "value": 1.0}}}
				}
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
	if mode != "" {
		url += "?fixture=" + mode
	}
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

func TestMatrixSemanticNegativeFixtures(t *testing.T) {
	for _, fixture := range []struct {
		name string
		want string
	}{
		{name: "bad-properties", want: "Runtime.getProperties missing alpha/nested"},
		{name: "bad-exception", want: "Runtime exception details missing"},
		{name: "bad-breakpoint", want: "setBreakpointByUrl missing breakpointId"},
		{name: "bad-frame-tree", want: "Page.getFrameTree missing frame"},
		{name: "bad-dom", want: "DOM.getDocument missing root nodeId"},
		{name: "bad-cookies", want: "Network.getCookies cookies must be array"},
		{name: "bad-metrics", want: "Performance.getMetrics metric name empty"},
		{name: "bad-concurrent", want: "concurrent Runtime.evaluate"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			url, closeServer := fakeCDPServerMode(t, fixture.name)
			defer closeServer()
			err := runMatrix(url)
			if err == nil || !strings.Contains(err.Error(), fixture.want) {
				t.Fatalf("runMatrix error=%v, want substring %q", err, fixture.want)
			}
		})
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
