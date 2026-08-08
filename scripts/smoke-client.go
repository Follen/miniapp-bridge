package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *cdpError       `json:"error"`
}

type pendingCall struct {
	method   string
	response chan envelope
}

type receivedFrame struct {
	Sequence int
	Kind     string
	ID       int
	Method   string
}

type receiveExpectation struct {
	Kind   string
	ID     int
	Method string
}

type client struct {
	conn      *websocket.Conn
	nextID    atomic.Int64
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[int]pendingCall
	events    []envelope
	received  []receivedFrame
	readError error
	done      chan struct{}
}

func dial(url string) (*client, error) {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	c := &client{conn: conn, pending: make(map[int]pendingCall), done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *client) readLoop() {
	defer close(c.done)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.mu.Lock()
			c.readError = err
			c.mu.Unlock()
			return
		}
		var msg envelope
		if err := json.Unmarshal(data, &msg); err != nil {
			c.mu.Lock()
			c.readError = fmt.Errorf("decode CDP frame: %w", err)
			c.mu.Unlock()
			return
		}
		c.mu.Lock()
		if msg.ID == nil {
			if msg.Method != "" {
				c.events = append(c.events, msg)
				c.received = append(c.received, receivedFrame{
					Sequence: len(c.received) + 1,
					Kind:     "event",
					Method:   msg.Method,
				})
			}
			c.mu.Unlock()
			continue
		}
		pending := c.pending[*msg.ID]
		c.received = append(c.received, receivedFrame{
			Sequence: len(c.received) + 1,
			Kind:     "response",
			ID:       *msg.ID,
			Method:   pending.method,
		})
		delete(c.pending, *msg.ID)
		c.mu.Unlock()
		if pending.response != nil {
			pending.response <- msg
		}
	}
}

func (c *client) send(method string, params any) (int, <-chan envelope, error) {
	id := int(c.nextID.Add(1))
	body, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return 0, nil, err
	}
	response := make(chan envelope, 1)
	c.mu.Lock()
	c.pending[id] = pendingCall{method: method, response: response}
	c.mu.Unlock()
	c.writeMu.Lock()
	err = c.conn.WriteMessage(websocket.TextMessage, body)
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return 0, nil, err
	}
	return id, response, nil
}

func (c *client) call(method string, params any, timeout time.Duration) (envelope, error) {
	id, response, err := c.send(method, params)
	if err != nil {
		return envelope{}, err
	}
	select {
	case msg := <-response:
		if msg.Error != nil {
			return msg, fmt.Errorf("%s: CDP error %d: %s", method, msg.Error.Code, msg.Error.Message)
		}
		return msg, nil
	case <-time.After(timeout):
		return envelope{}, fmt.Errorf("%s: response id %d timed out", method, id)
	case <-c.done:
		c.mu.Lock()
		err := c.readError
		c.mu.Unlock()
		return envelope{}, fmt.Errorf("%s: connection closed: %w", method, err)
	}
}

func (c *client) callExpectError(method string, params any, timeout time.Duration) error {
	_, response, err := c.send(method, params)
	if err != nil {
		return err
	}
	select {
	case msg := <-response:
		if msg.Error == nil || msg.Error.Code == 0 || msg.Error.Message == "" {
			return fmt.Errorf("%s: expected structured CDP error, got %+v", method, msg)
		}
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("%s: expected error response timed out", method)
	}
}

func (c *client) event(method string, after int, timeout time.Duration) (envelope, int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for i := after; i < len(c.events); i++ {
			if c.events[i].Method == method {
				msg := c.events[i]
				count := len(c.events)
				c.mu.Unlock()
				return msg, count, nil
			}
		}
		readErr := c.readError
		count := len(c.events)
		c.mu.Unlock()
		if readErr != nil {
			return envelope{}, count, readErr
		}
		time.Sleep(20 * time.Millisecond)
	}
	return envelope{}, after, fmt.Errorf("event %s timed out", method)
}

func (c *client) eventCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

func (c *client) receiveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.received)
}

func (c *client) expectReceiveOrder(after int, expected ...receiveExpectation) ([]int, error) {
	c.mu.Lock()
	frames := append([]receivedFrame(nil), c.received...)
	c.mu.Unlock()
	if after < 0 || after > len(frames) {
		return nil, fmt.Errorf("receive-order checkpoint %d outside 0..%d", after, len(frames))
	}
	sequences := make([]int, 0, len(expected))
	cursor := after
	for _, want := range expected {
		found := false
		for cursor < len(frames) {
			frame := frames[cursor]
			cursor++
			if frame.Kind != want.Kind || frame.Method != want.Method || want.ID != 0 && frame.ID != want.ID {
				continue
			}
			sequences = append(sequences, frame.Sequence)
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("receive order missing %s %s id=%d after sequence %d", want.Kind, want.Method, want.ID, after)
		}
	}
	return sequences, nil
}

func (c *client) close() { _ = c.conn.Close() }

func runLink(url string) error {
	c, err := dial(url)
	if err != nil {
		return err
	}
	defer c.close()
	for _, method := range []string{"Runtime.enable", "Debugger.enable"} {
		if _, err := c.call(method, map[string]any{}, 15*time.Second); err != nil {
			return err
		}
	}
	response, err := c.call("Runtime.evaluate", map[string]any{"expression": "1+1", "returnByValue": true}, 15*time.Second)
	if err != nil {
		return err
	}
	var result struct {
		Result struct {
			Value float64 `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil || result.Result.Value != 2 {
		return fmt.Errorf("Runtime.evaluate result=%s: %v", response.Result, err)
	}
	fmt.Printf("link-smoke: Runtime.enable=true Debugger.enable=true Runtime.evaluate=true events=%d\n", c.eventCount())
	return nil
}

func runMatrix(url string) error {
	c, err := dial(url)
	if err != nil {
		return err
	}
	defer c.close()

	initializers := []struct {
		method string
		params any
	}{
		{"Runtime.enable", map[string]any{}},
		{"Debugger.enable", map[string]any{}},
		{"Debugger.setPauseOnExceptions", map[string]any{"state": "none"}},
		{"Debugger.setAsyncCallStackDepth", map[string]any{"maxDepth": 32}},
		{"Runtime.setAsyncCallStackDepth", map[string]any{"maxDepth": 32}},
		{"Page.enable", map[string]any{}},
		{"DOM.enable", map[string]any{}},
		{"Network.enable", map[string]any{}},
		{"Console.enable", map[string]any{}},
		{"Performance.enable", map[string]any{}},
	}
	methods := make([]string, 0, len(initializers))
	orderAssertions := 0
	initializerCheckpoint := c.receiveCount()
	initializerOrder := make([]receiveExpectation, 0, len(initializers))
	for _, init := range initializers {
		response, err := c.call(init.method, init.params, 15*time.Second)
		if err != nil {
			return err
		}
		initializerOrder = append(initializerOrder, receiveExpectation{Kind: "response", ID: *response.ID, Method: init.method})
		methods = append(methods, init.method)
	}
	if _, err := c.expectReceiveOrder(initializerCheckpoint, initializerOrder...); err != nil {
		return fmt.Errorf("initializer %w", err)
	}
	orderAssertions += len(initializerOrder)

	objectCheckpoint := c.receiveCount()
	object, err := c.call("Runtime.evaluate", map[string]any{
		"expression": "({alpha: 1, nested: {beta: 'matrix'}})", "returnByValue": false,
	}, 15*time.Second)
	if err != nil {
		return err
	}
	var objectResult struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(object.Result, &objectResult); err != nil || objectResult.Result.ObjectID == "" {
		return fmt.Errorf("Runtime object missing objectId: %s: %v", object.Result, err)
	}
	properties, err := c.call("Runtime.getProperties", map[string]any{
		"objectId": objectResult.Result.ObjectID, "ownProperties": true,
	}, 15*time.Second)
	if err != nil {
		return err
	}
	if _, err := c.expectReceiveOrder(objectCheckpoint,
		receiveExpectation{Kind: "response", ID: *object.ID, Method: "Runtime.evaluate"},
		receiveExpectation{Kind: "response", ID: *properties.ID, Method: "Runtime.getProperties"},
	); err != nil {
		return fmt.Errorf("object %w", err)
	}
	orderAssertions += 2

	exceptionCheckpoint := c.receiveCount()
	exception, err := c.call("Runtime.evaluate", map[string]any{
		"expression": "throw new Error('miniapp-bridge-matrix')", "returnByValue": true,
	}, 15*time.Second)
	if err != nil {
		return err
	}
	var exceptionResult struct {
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(exception.Result, &exceptionResult); err != nil || len(exceptionResult.ExceptionDetails) == 0 {
		return fmt.Errorf("Runtime exception details missing: %s: %v", exception.Result, err)
	}
	if _, err := c.expectReceiveOrder(exceptionCheckpoint,
		receiveExpectation{Kind: "response", ID: *exception.ID, Method: "Runtime.evaluate"},
	); err != nil {
		return fmt.Errorf("exception %w", err)
	}
	orderAssertions++

	consoleAfter := c.eventCount()
	consoleCheckpoint := c.receiveCount()
	consoleResponse, err := c.call("Runtime.evaluate", map[string]any{
		"expression": "console.log('miniapp-bridge-matrix-console')", "returnByValue": true,
	}, 15*time.Second)
	if err != nil {
		return err
	}
	if _, _, err := c.event("Runtime.consoleAPICalled", consoleAfter, 10*time.Second); err != nil {
		return err
	}
	consoleOrder, err := c.expectReceiveOrder(consoleCheckpoint,
		receiveExpectation{Kind: "event", Method: "Runtime.consoleAPICalled"},
		receiveExpectation{Kind: "response", ID: *consoleResponse.ID, Method: "Runtime.evaluate"},
	)
	if err != nil {
		return fmt.Errorf("console %w", err)
	}
	orderAssertions += len(consoleOrder)

	scriptAfter := c.eventCount()
	scriptCheckpoint := c.receiveCount()
	scriptResponse, err := c.call("Runtime.evaluate", map[string]any{
		"expression": "function miniappBridgeMatrix(){ return 42; }\n//# sourceURL=miniapp-bridge-matrix.js",
	}, 15*time.Second)
	if err != nil {
		return err
	}
	if _, _, err := c.event("Debugger.scriptParsed", scriptAfter, 10*time.Second); err != nil {
		return err
	}
	scriptOrder, err := c.expectReceiveOrder(scriptCheckpoint,
		receiveExpectation{Kind: "event", Method: "Debugger.scriptParsed"},
		receiveExpectation{Kind: "response", ID: *scriptResponse.ID, Method: "Runtime.evaluate"},
	)
	if err != nil {
		return fmt.Errorf("script %w", err)
	}
	orderAssertions += len(scriptOrder)

	pausedAfter := c.eventCount()
	pauseCheckpoint := c.receiveCount()
	_, pausedResponse, err := c.send("Runtime.evaluate", map[string]any{
		"expression": "debugger; miniappBridgeMatrix()", "returnByValue": true,
	})
	if err != nil {
		return err
	}
	paused, _, err := c.event("Debugger.paused", pausedAfter, 10*time.Second)
	if err != nil {
		return err
	}
	var pausedParams struct {
		CallFrames []json.RawMessage `json:"callFrames"`
	}
	if err := json.Unmarshal(paused.Params, &pausedParams); err != nil || len(pausedParams.CallFrames) == 0 {
		return fmt.Errorf("Debugger.paused callFrames missing: %s: %v", paused.Params, err)
	}
	resumeResponse, err := c.call("Debugger.resume", map[string]any{}, 15*time.Second)
	if err != nil {
		return err
	}
	var pausedEvaluation envelope
	select {
	case response := <-pausedResponse:
		if response.Error != nil {
			return fmt.Errorf("paused Runtime.evaluate: %s", response.Error.Message)
		}
		pausedEvaluation = response
	case <-time.After(15 * time.Second):
		return errors.New("paused Runtime.evaluate did not complete after resume")
	}
	pauseOrder, err := c.expectReceiveOrder(pauseCheckpoint,
		receiveExpectation{Kind: "event", Method: "Debugger.paused"},
		receiveExpectation{Kind: "response", ID: *resumeResponse.ID, Method: "Debugger.resume"},
		receiveExpectation{Kind: "response", ID: *pausedEvaluation.ID, Method: "Runtime.evaluate"},
	)
	if err != nil {
		return fmt.Errorf("pause-resume %w", err)
	}
	orderAssertions += len(pauseOrder)

	checks := []struct {
		method string
		params any
	}{
		{"Page.getFrameTree", map[string]any{}},
		{"DOM.getDocument", map[string]any{"depth": 1}},
		{"Network.getCookies", map[string]any{}},
		{"Performance.getMetrics", map[string]any{}},
	}
	for _, check := range checks {
		checkpoint := c.receiveCount()
		response, err := c.call(check.method, check.params, 15*time.Second)
		if err != nil {
			return err
		}
		if _, err := c.expectReceiveOrder(checkpoint,
			receiveExpectation{Kind: "response", ID: *response.ID, Method: check.method},
		); err != nil {
			return fmt.Errorf("%s %w", check.method, err)
		}
		orderAssertions++
		methods = append(methods, check.method)
	}

	longCheckpoint := c.receiveCount()
	longValue := strings.Repeat("miniapp-bridge-", 32768)
	longExpression, _ := json.Marshal(longValue)
	longResult, err := c.call("Runtime.evaluate", map[string]any{
		"expression": string(longExpression), "returnByValue": true,
	}, 30*time.Second)
	if err != nil {
		return err
	}
	var longResponse struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(longResult.Result, &longResponse); err != nil || longResponse.Result.Value != longValue {
		return fmt.Errorf("long Runtime.evaluate mismatch: got=%d want=%d err=%v", len(longResponse.Result.Value), len(longValue), err)
	}
	if _, err := c.expectReceiveOrder(longCheckpoint,
		receiveExpectation{Kind: "response", ID: *longResult.ID, Method: "Runtime.evaluate"},
	); err != nil {
		return fmt.Errorf("long-message %w", err)
	}
	orderAssertions++

	const concurrent = 16
	concurrentCheckpoint := c.receiveCount()
	var wg sync.WaitGroup
	errCh := make(chan error, concurrent)
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			response, err := c.call("Runtime.evaluate", map[string]any{
				"expression": fmt.Sprintf("%d * %d", i, i), "returnByValue": true,
			}, 15*time.Second)
			if err == nil && len(response.Result) == 0 {
				err = fmt.Errorf("concurrent Runtime.evaluate %d has empty result", i)
			}
			if err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		return err
	}
	c.mu.Lock()
	concurrentResponses := 0
	concurrentIDs := make(map[int]struct{}, concurrent)
	for _, frame := range c.received[concurrentCheckpoint:] {
		if frame.Kind == "response" && frame.Method == "Runtime.evaluate" {
			concurrentResponses++
			concurrentIDs[frame.ID] = struct{}{}
		}
	}
	c.mu.Unlock()
	if concurrentResponses != concurrent || len(concurrentIDs) != concurrent {
		return fmt.Errorf("concurrent receive order responses=%d unique-ids=%d want=%d", concurrentResponses, len(concurrentIDs), concurrent)
	}
	orderAssertions += concurrent

	errorCheckpoint := c.receiveCount()
	if err := c.callExpectError("MiniAppBridge.invalidMethod", map[string]any{}, 15*time.Second); err != nil {
		return err
	}
	if _, err := c.expectReceiveOrder(errorCheckpoint,
		receiveExpectation{Kind: "response", Method: "MiniAppBridge.invalidMethod"},
	); err != nil {
		return fmt.Errorf("error-response %w", err)
	}
	orderAssertions++

	if _, _, err := c.event("Runtime.executionContextCreated", 0, 10*time.Second); err != nil {
		return err
	}
	sort.Strings(methods)
	firstEventCount := c.eventCount()
	c.close()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		return errors.New("first CDP connection did not close")
	}

	reconnected, err := dial(url)
	if err != nil {
		return fmt.Errorf("CDP reconnect: %w", err)
	}
	defer reconnected.close()
	reconnectCheckpoint := reconnected.receiveCount()
	reconnectEnable, err := reconnected.call("Runtime.enable", map[string]any{}, 15*time.Second)
	if err != nil {
		return fmt.Errorf("CDP reconnect Runtime.enable: %w", err)
	}
	reconnectEvaluate, err := reconnected.call("Runtime.evaluate", map[string]any{
		"expression": "6 * 7", "returnByValue": true,
	}, 15*time.Second)
	if err != nil {
		return fmt.Errorf("CDP reconnect Runtime.evaluate: %w", err)
	}
	if _, err := reconnected.expectReceiveOrder(reconnectCheckpoint,
		receiveExpectation{Kind: "response", ID: *reconnectEnable.ID, Method: "Runtime.enable"},
		receiveExpectation{Kind: "response", ID: *reconnectEvaluate.ID, Method: "Runtime.evaluate"},
	); err != nil {
		return fmt.Errorf("reconnect %w", err)
	}
	orderAssertions += 2

	fmt.Printf("live-cdp-matrix: domains=Runtime,Debugger,Page,DOM,Network,Console,Performance init=%d objects=true exceptions=true console=true scripts=true pause-resume=true callframes=true long-bytes=%d concurrent=%d error-response=true contexts=true reconnect=true events=%d receive-order=true order-assertions=%d console-seq=%d<%d script-seq=%d<%d pause-seq=%d<%d<%d received=%d reconnect-received=%d\n",
		len(initializers), len(longValue), concurrent, firstEventCount, orderAssertions,
		consoleOrder[0], consoleOrder[1], scriptOrder[0], scriptOrder[1], pauseOrder[0], pauseOrder[1], pauseOrder[2],
		c.receiveCount(), reconnected.receiveCount())
	return nil
}

func runInteraction(url string) error {
	c, err := dial(url)
	if err != nil {
		return err
	}
	defer c.close()
	for _, method := range []string{"Runtime.enable", "DOM.enable"} {
		if _, err := c.call(method, map[string]any{}, 15*time.Second); err != nil {
			return err
		}
	}

	setup := `(() => {
  document.getElementById('__miniapp_bridge_input_probe')?.remove();
  const host = document.createElement('div');
  host.id = '__miniapp_bridge_input_probe';
  host.style.cssText = 'position:fixed;left:12px;top:12px;width:160px;height:120px;z-index:2147483647;background:#fff';
  const button = document.createElement('button');
  button.id = '__miniapp_bridge_click_probe';
  button.style.cssText = 'display:block;width:120px;height:48px;margin:0;padding:0';
  button.textContent = 'probe';
  const input = document.createElement('input');
  input.id = '__miniapp_bridge_key_probe';
  input.style.cssText = 'display:block;width:120px;height:32px;margin-top:8px;padding:0';
  globalThis.__miniappBridgeInputClickCount = 0;
  button.addEventListener('click', () => globalThis.__miniappBridgeInputClickCount++);
  host.append(button, input);
  document.body.append(host);
  return true;
})()`
	if _, err := c.call("Runtime.evaluate", map[string]any{"expression": setup, "returnByValue": true}, 15*time.Second); err != nil {
		return err
	}
	defer func() {
		_, _ = c.call("Runtime.evaluate", map[string]any{
			"expression":    "document.getElementById('__miniapp_bridge_input_probe')?.remove(); delete globalThis.__miniappBridgeInputClickCount;",
			"returnByValue": true,
		}, 5*time.Second)
	}()

	document, err := c.call("DOM.getDocument", map[string]any{"depth": 1}, 15*time.Second)
	if err != nil {
		return err
	}
	var documentResult struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if err := json.Unmarshal(document.Result, &documentResult); err != nil || documentResult.Root.NodeID == 0 {
		return fmt.Errorf("interaction DOM root missing: %s: %v", document.Result, err)
	}
	query, err := c.call("DOM.querySelector", map[string]any{
		"nodeId": documentResult.Root.NodeID, "selector": "#__miniapp_bridge_click_probe",
	}, 15*time.Second)
	if err != nil {
		return err
	}
	var queryResult struct {
		NodeID int `json:"nodeId"`
	}
	if err := json.Unmarshal(query.Result, &queryResult); err != nil || queryResult.NodeID == 0 {
		return fmt.Errorf("interaction click node missing: %s: %v", query.Result, err)
	}
	box, err := c.call("DOM.getBoxModel", map[string]any{"nodeId": queryResult.NodeID}, 15*time.Second)
	if err != nil {
		return err
	}
	var boxResult struct {
		Model struct {
			Content []float64 `json:"content"`
		} `json:"model"`
	}
	if err := json.Unmarshal(box.Result, &boxResult); err != nil || len(boxResult.Model.Content) != 8 {
		return fmt.Errorf("interaction box model invalid: %s: %v", box.Result, err)
	}
	x := (boxResult.Model.Content[0] + boxResult.Model.Content[2] + boxResult.Model.Content[4] + boxResult.Model.Content[6]) / 4
	y := (boxResult.Model.Content[1] + boxResult.Model.Content[3] + boxResult.Model.Content[5] + boxResult.Model.Content[7]) / 4
	for _, event := range []map[string]any{
		{"type": "mouseMoved", "x": x, "y": y},
		{"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1},
		{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1},
	} {
		if _, err := c.call("Input.dispatchMouseEvent", event, 15*time.Second); err != nil {
			return err
		}
	}
	clicks, err := c.call("Runtime.evaluate", map[string]any{
		"expression": "globalThis.__miniappBridgeInputClickCount", "returnByValue": true,
	}, 15*time.Second)
	if err != nil {
		return err
	}
	var clickResult struct {
		Result struct {
			Value float64 `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(clicks.Result, &clickResult); err != nil || clickResult.Result.Value != 1 {
		return fmt.Errorf("mouse click did not reach DOM target: %s: %v", clicks.Result, err)
	}

	if _, err := c.call("Runtime.evaluate", map[string]any{
		"expression": "document.getElementById('__miniapp_bridge_key_probe').focus()", "returnByValue": true,
	}, 15*time.Second); err != nil {
		return err
	}
	for _, event := range []map[string]any{
		{"type": "rawKeyDown", "key": "A", "code": "KeyA", "windowsVirtualKeyCode": 65, "nativeVirtualKeyCode": 65},
		{"type": "char", "key": "A", "code": "KeyA", "text": "A", "unmodifiedText": "A", "windowsVirtualKeyCode": 65, "nativeVirtualKeyCode": 65},
		{"type": "keyUp", "key": "A", "code": "KeyA", "windowsVirtualKeyCode": 65, "nativeVirtualKeyCode": 65},
	} {
		if _, err := c.call("Input.dispatchKeyEvent", event, 15*time.Second); err != nil {
			return err
		}
	}
	value, err := c.call("Runtime.evaluate", map[string]any{
		"expression": "document.getElementById('__miniapp_bridge_key_probe').value", "returnByValue": true,
	}, 15*time.Second)
	if err != nil {
		return err
	}
	var valueResult struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(value.Result, &valueResult); err != nil || valueResult.Result.Value != "A" {
		return fmt.Errorf("keyboard input did not reach DOM target: %s: %v", value.Result, err)
	}

	fmt.Printf("interaction-live: Input.dispatchMouseEvent=true click-count=1 Input.dispatchKeyEvent=true input-value=A coordinates=%.1f,%.1f\n", x, y)
	return nil
}

func main() {
	url := flag.String("url", "ws://127.0.0.1:62000", "CDP WebSocket URL")
	mode := flag.String("mode", "link", "validation mode: link, matrix, or interaction")
	flag.Parse()
	var err error
	switch *mode {
	case "link":
		err = runLink(*url)
	case "matrix":
		err = runMatrix(*url)
	case "interaction":
		err = runInteraction(*url)
	default:
		err = fmt.Errorf("unknown mode %q", *mode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
