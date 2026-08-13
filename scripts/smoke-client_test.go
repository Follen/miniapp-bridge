package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func fakeCDPServer(t *testing.T) (string, func()) {
	return fakeCDPServerMode(t, "")
}

func fakeCDPServerMode(t *testing.T, mode string) (string, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var owner atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://"+r.Host {
			http.Error(w, `{"error":{"code":"origin_not_allowed"}}`, http.StatusForbidden)
			return
		}
		if !owner.CompareAndSwap(false, true) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"owner_exists"}}`))
			return
		}
		defer owner.Store(false)
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
		runtimeEnableCalls := 0
		interleaved := mode == "interleaved-events"
		emptyBreakpointLocations := interleaved || mode == "resolved-breakpoint" || mode == "resolved-breakpoint-with-hits" || mode == "bad-breakpoint-resolved"
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
				runtimeEnableCalls++
				if mode == "delayed-context" && runtimeEnableCalls == 1 {
					response["error"] = map[string]any{"code": -32000, "message": "no JavaScript context is selected"}
					break
				}
				if mode == "missing-context" {
					response["error"] = map[string]any{"code": -32000, "message": "no JavaScript context is selected"}
					break
				}
				if mode == "runtime-enable-error" {
					response["error"] = map[string]any{"code": -32000, "message": "runtime unavailable"}
					break
				}
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
						pausedParams := map[string]any{
							"reason": "other",
							"callFrames": []any{map[string]any{
								"callFrameId":  "frame-1",
								"functionName": "miniappBridgeMatrix",
								"location":     map[string]any{"scriptId": "1", "lineNumber": 0, "columnNumber": 0},
							}},
						}
						switch mode {
						case "with-hit-breakpoints", "resolved-breakpoint-with-hits":
							pausedParams["hitBreakpoints"] = []string{breakpointID}
						case "bad-hit-breakpoint":
							pausedParams["hitBreakpoints"] = []string{"other-breakpoint"}
						}
						if interleaved || mode == "bad-paused-event" {
							write(map[string]any{"method": "Debugger.paused", "params": map[string]any{
								"reason": "other",
								"callFrames": []any{map[string]any{
									"callFrameId": "unrelated-frame",
									"location":    map[string]any{"scriptId": "unrelated-script", "lineNumber": 99},
								}},
							}})
							if mode == "bad-paused-event" {
								return
							}
						}
						write(map[string]any{"method": "Debugger.paused", "params": pausedParams})
						if mode == "bad-hit-breakpoint" {
							return
						}
						continue
					}
					response["result"] = map[string]any{"result": map[string]any{"value": 42}}
				case strings.HasPrefix(expression, "throw new Error"):
					if mode == "bad-exception" {
						response["result"] = map[string]any{"exceptionDetails": map[string]any{
							"text":       "Uncaught",
							"exception":  map[string]any{"description": "Error: miniapp-bridge-matrix", "className": "Error"},
							"stackTrace": map[string]any{"description": "Error: miniapp-bridge-matrix"},
						}}
					} else {
						response["result"] = map[string]any{"exceptionDetails": map[string]any{
							"text":      "Uncaught",
							"exception": map[string]any{"description": "Error: miniapp-bridge-matrix", "className": "Error"},
							"stackTrace": map[string]any{"callFrames": []any{map[string]any{
								"functionName": "miniappBridgeMatrixException",
								"scriptId":     "1",
								"url":          "miniapp-bridge-matrix.js",
								"lineNumber":   0,
								"columnNumber": 0,
							}}},
						}}
					}
				case strings.HasPrefix(expression, "console.log"):
					if interleaved || mode == "bad-console-event" {
						write(map[string]any{"method": "Runtime.consoleAPICalled", "params": map[string]any{"type": "log", "args": []any{map[string]any{"type": "string", "value": "unrelated-console-value"}}}})
						write(map[string]any{"id": request.ID + 1000, "result": map[string]any{"result": map[string]any{"value": "wrong-response-id"}}})
						write(response)
						if mode == "bad-console-event" {
							return
						}
						write(map[string]any{"method": "Runtime.consoleAPICalled", "params": map[string]any{"type": "log", "args": []any{map[string]any{"type": "string", "value": "miniapp-bridge-matrix-console"}}}})
						continue
					}
					write(map[string]any{"method": "Runtime.consoleAPICalled", "params": map[string]any{"type": "log", "args": []any{map[string]any{"type": "string", "value": "miniapp-bridge-matrix-console"}}}})
				case strings.Contains(expression, "sourceURL=miniapp-bridge-matrix.js"):
					if interleaved || mode == "bad-script-event" {
						write(map[string]any{"method": "Debugger.scriptParsed", "params": map[string]any{"scriptId": "unrelated", "url": "unrelated.js"}})
						write(response)
						if mode == "bad-script-event" {
							return
						}
						write(map[string]any{"method": "Debugger.scriptParsed", "params": map[string]any{"scriptId": "1", "url": "miniapp-bridge-matrix.js"}})
						continue
					}
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
							if mode == "bad-concurrent" || mode == "bad-reconnect" && expression == "6 * 7" {
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
				if interleaved && pausedID != 0 {
					write(map[string]any{"method": "Debugger.resumed", "params": map[string]any{}})
					write(map[string]any{"id": pausedID, "result": map[string]any{"result": map[string]any{"value": 42}}})
					pausedID = 0
					write(response)
					continue
				}
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
				} else if emptyBreakpointLocations {
					response["result"] = map[string]any{"breakpointId": breakpointID, "locations": []any{}}
					if interleaved {
						write(map[string]any{"method": "Debugger.breakpointResolved", "params": map[string]any{"breakpointId": "unrelated-breakpoint", "location": map[string]any{"scriptId": "unrelated", "lineNumber": 99}}})
					}
					write(response)
					if mode == "bad-breakpoint-resolved" {
						write(map[string]any{"method": "Debugger.breakpointResolved", "params": map[string]any{"breakpointId": "unrelated-breakpoint", "location": map[string]any{"scriptId": "1", "lineNumber": 0}}})
						return
					}
					write(map[string]any{"method": "Debugger.breakpointResolved", "params": map[string]any{"breakpointId": breakpointID, "location": map[string]any{"scriptId": "1", "lineNumber": 0, "columnNumber": 0}}})
					continue
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
						map[string]any{"name": "nested", "value": map[string]any{"type": "object", "objectId": "fake-nested-1"}},
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
				if mode == "bad-metrics-shape" {
					response["result"] = map[string]any{"metrics": map[string]any{}}
				} else if mode == "bad-metrics" {
					response["result"] = map[string]any{"metrics": []any{map[string]any{"value": 1.0}}}
				} else if mode == "empty-metrics" {
					response["result"] = map[string]any{"metrics": []any{}}
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

func TestClientsWaitForSelectedContext(t *testing.T) {
	for _, run := range []struct {
		name string
		fn   func(string) error
	}{
		{name: "link", fn: runLink},
		{name: "matrix", fn: runMatrix},
		{name: "interaction", fn: runInteraction},
	} {
		t.Run(run.name, func(t *testing.T) {
			url, closeServer := fakeCDPServerMode(t, "delayed-context")
			defer closeServer()
			if err := run.fn(url); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeEnableDoesNotRetryOtherErrors(t *testing.T) {
	url, closeServer := fakeCDPServerMode(t, "runtime-enable-error")
	defer closeServer()
	err := runLink(url)
	if err == nil || !strings.Contains(err.Error(), "runtime unavailable") {
		t.Fatalf("runLink error=%v, want runtime unavailable", err)
	}
}

func TestRuntimeEnableContextWaitIsBounded(t *testing.T) {
	url, closeServer := fakeCDPServerMode(t, "missing-context")
	defer closeServer()
	c, err := dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	_, err = c.enableRuntimeWhenContextReady(10 * time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "before timeout") {
		t.Fatalf("enableRuntimeWhenContextReady error=%v, want bounded timeout", err)
	}
}

func TestMatrixAcceptsHitBreakpoints(t *testing.T) {
	for _, fixture := range []string{
		"with-hit-breakpoints",
		"interleaved-events",
		"resolved-breakpoint",
		"resolved-breakpoint-with-hits",
		"empty-metrics",
	} {
		t.Run(fixture, func(t *testing.T) {
			url, closeServer := fakeCDPServerMode(t, fixture)
			defer closeServer()
			if err := runMatrix(url); err != nil {
				t.Fatal(err)
			}
		})
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
		{name: "bad-breakpoint-resolved", want: "Debugger.breakpointResolved"},
		{name: "bad-hit-breakpoint", want: "could not correlate breakpoint"},
		{name: "bad-paused-event", want: "could not correlate breakpoint"},
		{name: "bad-console-event", want: "Runtime.consoleAPICalled marker"},
		{name: "bad-script-event", want: "Debugger.scriptParsed URL"},
		{name: "bad-frame-tree", want: "Page.getFrameTree missing frame"},
		{name: "bad-dom", want: "DOM.getDocument missing root nodeId"},
		{name: "bad-cookies", want: "Network.getCookies cookies must be array"},
		{name: "bad-metrics", want: "Performance.getMetrics metric name empty"},
		{name: "bad-metrics-shape", want: "Performance.getMetrics metrics must be array"},
		{name: "bad-concurrent", want: "concurrent Runtime.evaluate"},
		{name: "bad-reconnect", want: "CDP reconnect Runtime.evaluate result="},
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

func TestMatrixSemanticNegativeFixturesRaceStable(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		t.Run(fmt.Sprintf("attempt-%02d", attempt), func(t *testing.T) {
			url, closeServer := fakeCDPServerMode(t, "bad-script-event")
			defer closeServer()
			err := runMatrix(url)
			if err == nil || !strings.Contains(err.Error(), "Debugger.scriptParsed URL") {
				t.Fatalf("runMatrix error=%v, want semantic script event error", err)
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
