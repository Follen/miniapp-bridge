package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Follen/miniapp-bridge/internal/capture"
	"github.com/Follen/miniapp-bridge/internal/cdp"
	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/proxy"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
	"math"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrClosed            = errors.New("app is closed")
	ErrAlreadyStarted    = errors.New("app is already started")
	ErrInvalidCDPPayload = errors.New("invalid CDP payload")
	ErrNoContext         = errors.New("no JavaScript context is selected")
	ErrUnknownContext    = errors.New("unknown JavaScript context")
)

// Observer is an optional, non-blocking hook used by the public SDK.
type Observer struct {
	OnCDP        func([]byte)
	OnContext    func(ContextEvent)
	OnConnection func(ConnectionEvent)
}

type ContextEvent struct {
	Kind    string
	Context bridgecontext.Context
}

type ConnectionEvent struct {
	Kind      string
	Connected bool
}

type App struct {
	DebugPort, CDPPort int
	Log                *logging.Logger
	DebugHub, CDPHub   *proxy.Hub
	Contexts           *bridgecontext.Registry
	Requests           *cdp.Correlator
	Recorder           *capture.Recorder
	recorderMu         sync.RWMutex
	debugSrv, cdpSrv   *http.Server
	debugLn, cdpLn     net.Listener
	serverMu           sync.Mutex
	serveWG            sync.WaitGroup
	started            bool
	closeOnce          sync.Once
	closeErr           error
	closing            atomic.Bool
	connMu             sync.RWMutex
	dispatchMu         sync.Mutex
	observerMu         sync.RWMutex
	observer           Observer
	replayMu           sync.Mutex
	replayCancel       context.CancelFunc
	replayID           uint64
	replayWG           sync.WaitGroup
	seq                atomic.Uint32
	listen             func(string, string) (net.Listener, error)
	serve              func(*http.Server, net.Listener) error
}
type wsClient struct {
	conn       websocketConnection
	initOnce   sync.Once
	sendMu     sync.Mutex
	stopOnce   sync.Once
	closeOnce  sync.Once
	outbound   chan []byte
	done       chan struct{}
	writerDone chan struct{}
	closeErr   error
	closed     atomic.Bool
	typeID     int
	queueSize  int
}

type websocketConnection interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

const (
	websocketWriteTimeout      = 5 * time.Second
	websocketOutboundQueueSize = 256
)

func newWSClient(conn websocketConnection, typeID int) *wsClient {
	return &wsClient{conn: conn, typeID: typeID, queueSize: websocketOutboundQueueSize}
}

func (c *wsClient) initialize() {
	c.initOnce.Do(func() {
		queueSize := c.queueSize
		if queueSize <= 0 {
			queueSize = websocketOutboundQueueSize
		}
		c.outbound = make(chan []byte, queueSize)
		c.done = make(chan struct{})
		c.writerDone = make(chan struct{})
		go c.writeLoop()
	})
}

func (c *wsClient) Send(b []byte) error {
	c.initialize()
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed.Load() {
		return ErrClosed
	}
	message := append([]byte(nil), b...)
	select {
	case c.outbound <- message:
		return nil
	default:
		return proxy.ErrClientBackpressure
	}
}

func (c *wsClient) writeMessage(b []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout)); err != nil {
		return err
	}
	return c.conn.WriteMessage(c.typeID, b)
}

func (c *wsClient) writeLoop() {
	defer close(c.writerDone)
	for {
		select {
		case <-c.done:
			return
		default:
		}
		select {
		case <-c.done:
			return
		case message := <-c.outbound:
			if err := c.writeMessage(message); err != nil {
				c.stop()
				c.closeTransport()
				return
			}
		}
	}
}

func (c *wsClient) stop() {
	c.sendMu.Lock()
	c.closed.Store(true)
	c.stopOnce.Do(func() { close(c.done) })
	c.sendMu.Unlock()
}

func (c *wsClient) closeTransport() {
	c.closeOnce.Do(func() {
		_ = c.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"),
			time.Now().Add(250*time.Millisecond))
		c.closeErr = c.conn.Close()
	})
}

func (c *wsClient) Close() error {
	c.initialize()
	c.stop()
	// Closing the transport interrupts a writer blocked in WriteMessage.
	c.closeTransport()
	<-c.writerDone
	return c.closeErr
}
func New(dp, cp int, l *logging.Logger) *App {
	return &App{DebugPort: dp, CDPPort: cp, Log: l, DebugHub: proxy.NewHub(), CDPHub: proxy.NewHub(), Contexts: bridgecontext.NewRegistry(), Requests: cdp.NewCorrelator(), listen: net.Listen, serve: func(s *http.Server, l net.Listener) error { return s.Serve(l) }}
}
func (a *App) SetRecorder(r *capture.Recorder) {
	a.recorderMu.Lock()
	a.Recorder = r
	a.recorderMu.Unlock()
}
func (a *App) SwapRecorder(r *capture.Recorder) *capture.Recorder {
	a.recorderMu.Lock()
	old := a.Recorder
	a.Recorder = r
	a.recorderMu.Unlock()
	return old
}
func (a *App) TakeRecorder() *capture.Recorder {
	a.recorderMu.Lock()
	r := a.Recorder
	a.Recorder = nil
	a.recorderMu.Unlock()
	return r
}
func (a *App) SetObserver(observer Observer) {
	a.observerMu.Lock()
	a.observer = observer
	a.observerMu.Unlock()
}
func (a *App) observerSnapshot() Observer {
	a.observerMu.RLock()
	defer a.observerMu.RUnlock()
	return a.observer
}
func (a *App) Replay(path string) error {
	return a.ReplayContext(context.Background(), path)
}
func (a *App) ReplayContext(ctx context.Context, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	replayCtx, cancel := context.WithCancel(ctx)
	a.replayMu.Lock()
	if a.closing.Load() {
		a.replayMu.Unlock()
		cancel()
		return ErrClosed
	}
	if a.replayCancel != nil {
		a.replayCancel()
	}
	a.replayID++
	replayID := a.replayID
	a.replayCancel = cancel
	a.replayWG.Add(1)
	a.replayMu.Unlock()
	done := make(chan struct{})
	var replayErr error
	go func() {
		defer a.replayWG.Done()
		defer close(done)
		defer func() {
			a.replayMu.Lock()
			if a.replayID == replayID {
				a.replayCancel = nil
			}
			a.replayMu.Unlock()
		}()
		replayErr = capture.ReplayEachContext(replayCtx, path, func(f []byte) error {
			m, e := wmpf.DecodeDebugMessage(f)
			if e != nil {
				a.Log.Error("[capture] replay:", e)
				return nil
			}
			a.handleDebugMessage(m)
			return nil
		})
	}()
	<-done
	return replayErr
}
func (a *App) Start() error {
	a.serverMu.Lock()
	defer a.serverMu.Unlock()
	if a.closing.Load() {
		return ErrClosed
	}
	if a.started {
		return ErrAlreadyStarted
	}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	a.debugSrv = &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", a.DebugPort), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := up.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		a.connMu.RLock()
		if a.closing.Load() {
			a.connMu.RUnlock()
			_ = c.Close()
			return
		}
		x := newWSClient(c, websocket.BinaryMessage)
		a.DebugHub.Add(x)
		a.connMu.RUnlock()
		if observer := a.observerSnapshot(); observer.OnConnection != nil {
			observer.OnConnection(ConnectionEvent{Kind: "upstream", Connected: true})
		}
		a.Log.Info("[miniapp] miniapp client connected")
		go a.readDebug(x)
	})}
	a.cdpSrv = &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", a.CDPPort), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := up.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		a.connMu.RLock()
		if a.closing.Load() {
			a.connMu.RUnlock()
			_ = c.Close()
			return
		}
		x := newWSClient(c, websocket.TextMessage)
		a.CDPHub.Add(x)
		a.connMu.RUnlock()
		if observer := a.observerSnapshot(); observer.OnConnection != nil {
			observer.OnConnection(ConnectionEvent{Kind: "cdp", Connected: true})
		}
		a.Log.Info("[cdp] CDP client connected")
		go a.readCDP(x)
	})}
	debugLn, err := a.listen("tcp", a.debugSrv.Addr)
	if err != nil {
		return err
	}
	cdpLn, err := a.listen("tcp", a.cdpSrv.Addr)
	if err != nil {
		_ = debugLn.Close()
		return err
	}
	a.debugLn = debugLn
	a.cdpLn = cdpLn
	a.started = true
	a.serveWG.Add(2)
	go func() {
		defer a.serveWG.Done()
		e := a.serve(a.debugSrv, debugLn)
		if e != nil && e != http.ErrServerClosed && !errors.Is(e, net.ErrClosed) {
			a.Log.Error(e)
		}
	}()
	go func() {
		defer a.serveWG.Done()
		e := a.serve(a.cdpSrv, cdpLn)
		if e != nil && e != http.ErrServerClosed && !errors.Is(e, net.ErrClosed) {
			a.Log.Error(e)
		}
	}()
	a.Log.Info(fmt.Sprintf("[server] debug server running on ws://127.0.0.1:%d", a.DebugPort))
	a.Log.Info("[server] debug server waiting for miniapp to connect...")
	a.Log.Info(fmt.Sprintf("[server] proxy server running on ws://127.0.0.1:%d", a.CDPPort))
	a.Log.Info(fmt.Sprintf("[server] link: devtools://devtools/bundled/inspector.html?ws=127.0.0.1:%d", a.CDPPort))
	return nil
}
func (a *App) readDebug(c *wsClient) {
	defer func() {
		a.connMu.Lock()
		a.DebugHub.Remove(c)
		lastUpstream := a.DebugHub.Count() == 0
		if lastUpstream {
			a.dispatchMu.Lock()
			a.ClearRequests()
			a.dispatchMu.Unlock()
		}
		a.connMu.Unlock()
		_ = c.Close()
		if observer := a.observerSnapshot(); observer.OnConnection != nil {
			observer.OnConnection(ConnectionEvent{Kind: "upstream", Connected: false})
		}
		a.Log.Info("[miniapp] miniapp client disconnected")
	}()
	for {
		typ, b, e := c.conn.ReadMessage()
		if e != nil {
			return
		}
		a.handleDebugFrame(typ, b)
	}
}

func (a *App) handleDebugFrame(typ int, b []byte) bool {
	if typ != websocket.BinaryMessage && typ != websocket.TextMessage {
		return false
	}
	a.Log.Main("[miniapp] client received raw message (hex):", hex.EncodeToString(b))
	a.dispatchMu.Lock()
	defer a.dispatchMu.Unlock()
	a.recordFrameLocked(capture.DirectionUpstream, b)
	m, e := wmpf.DecodeDebugMessage(b)
	if e != nil {
		a.Log.Error("[miniapp] miniapp client err:", e)
		return true
	}
	a.handleDebugMessageLocked(m)
	return true
}

func (a *App) recordFrameLocked(direction capture.Direction, frame []byte) {
	a.recorderMu.RLock()
	defer a.recorderMu.RUnlock()
	recorder := a.Recorder
	if recorder != nil {
		if err := recorder.WriteFrame(direction, time.Now().UTC(), frame); err != nil {
			a.Log.Error("[capture] write:", err)
		}
	}
}

func (a *App) handleDebugMessage(message wmpf.DebugMessage) {
	a.dispatchMu.Lock()
	defer a.dispatchMu.Unlock()
	a.handleDebugMessageLocked(message)
}

func (a *App) handleDebugMessageLocked(message wmpf.DebugMessage) {
	u, err := wmpf.UnwrapDebugMessage(message)
	if err != nil {
		a.Log.Error("[miniapp] decode:", err)
		return
	}
	a.Log.Main("[miniapp] [DEBUG] decoded data:", u)
	a.handleUnwrappedDebug(u)
}

func (a *App) handleUnwrappedDebug(u wmpf.Unwrapped) {
	value, ok := a.decodeCategoryData(u.Category, u.Data)
	if !ok {
		return
	}
	switch u.Category {
	case wmpf.CategoryAddJsContext:
		v := value.(wmpf.JsContext)
		ctx := bridgecontext.Context{ID: v.ID, Target: v.Name}
		a.Contexts.Upsert(ctx)
		if observer := a.observerSnapshot(); observer.OnContext != nil {
			observer.OnContext(ContextEvent{Kind: "added", Context: ctx})
		}
	case wmpf.CategoryRemoveJsContext:
		v := value.(wmpf.JsContext)
		if a.Contexts.Remove(v.ID) {
			if observer := a.observerSnapshot(); observer.OnContext != nil {
				observer.OnContext(ContextEvent{Kind: "removed", Context: bridgecontext.Context{ID: v.ID, Target: v.Name}})
			}
		}
	case wmpf.CategoryConnectJsContext:
		v := value.(wmpf.JsContext)
		if _, exists := a.Contexts.Get(v.ID); !exists {
			a.Contexts.Upsert(bridgecontext.Context{ID: v.ID})
		}
		a.Contexts.Select(v.ID)
		if observer := a.observerSnapshot(); observer.OnContext != nil {
			ctx, _ := a.Contexts.Get(v.ID)
			observer.OnContext(ContextEvent{Kind: "selected", Context: ctx})
		}
	case wmpf.CategoryChromeDevtoolsResult:
		v := value.(wmpf.ChromeDevtools)
		var response struct {
			ID     json.RawMessage `json:"id"`
			Result map[string]any  `json:"result"`
			Error  *cdp.Error      `json:"error"`
		}
		if json.Unmarshal([]byte(v.Payload), &response) == nil && len(response.ID) != 0 && string(response.ID) != "null" {
			if id, err := decodeExactJSONValue(response.ID); err == nil {
				_, _ = a.Requests.Resolve(cdp.Response{ID: id, Result: response.Result, Error: response.Error})
			}
		}
		a.CDPHub.Broadcast([]byte(v.Payload))
		if observer := a.observerSnapshot(); observer.OnCDP != nil {
			observer.OnCDP([]byte(v.Payload))
		}
	}
}

func (a *App) decodeCategoryData(category string, data any) (any, bool) {
	raw, ok := data.([]byte)
	if !ok {
		return nil, false
	}
	value, err := wmpf.DecodeCategory(category, raw)
	if err != nil {
		a.Log.Error("[miniapp] category:", err)
		return nil, false
	}
	return value, true
}
func (a *App) readCDP(c *wsClient) {
	defer func() {
		a.CDPHub.Remove(c)
		_ = c.Close()
		if observer := a.observerSnapshot(); observer.OnConnection != nil {
			observer.OnConnection(ConnectionEvent{Kind: "cdp", Connected: false})
		}
		a.Log.Info("[cdp] CDP client disconnected")
	}()
	for {
		typ, b, e := c.conn.ReadMessage()
		if e != nil {
			return
		}
		a.handleCDPFrame(typ, b, c)
	}
}

func (a *App) handleCDPFrame(typ int, b []byte, c *wsClient) bool {
	if typ != websocket.TextMessage && typ != websocket.BinaryMessage {
		return false
	}
	a.dispatchMu.Lock()
	defer a.dispatchMu.Unlock()
	a.sendCDPLocked(string(b), c)
	return true
}

// SendCDP injects a raw CDP payload through the same route used by a WebSocket
// client. It is intentionally narrow so the public SDK never sees wsClient.
func (a *App) SendCDP(payload []byte) error {
	if a.closing.Load() {
		return ErrClosed
	}
	a.dispatchMu.Lock()
	defer a.dispatchMu.Unlock()
	a.sendCDPLocked(string(payload), nil)
	return nil
}

// SendCDPRoute dispatches a CDP payload to an explicit JavaScript context. An
// empty context ID snapshots the selected context while holding dispatchMu.
func (a *App) SendCDPRoute(payload []byte, jscontextID string) error {
	if a.closing.Load() {
		return ErrClosed
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope == nil {
		return fmt.Errorf("%w: expected a JSON object", ErrInvalidCDPPayload)
	}
	a.dispatchMu.Lock()
	defer a.dispatchMu.Unlock()
	contextID, err := a.routeContextID(jscontextID)
	if err != nil {
		return err
	}
	a.sendCDPToContextLocked(string(payload), nil, contextID)
	return nil
}

func (a *App) sendCDPLocked(payload string, c *wsClient) {
	contextID, _ := a.routeContextID("")
	a.sendCDPToContextLocked(payload, c, contextID)
}

func (a *App) routeContextID(jscontextID string) (string, error) {
	if jscontextID == "" {
		if selected, ok := a.Contexts.Selected(); ok {
			return selected.ID, nil
		}
		return "", ErrNoContext
	}
	if _, ok := a.Contexts.Get(jscontextID); !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownContext, jscontextID)
	}
	return jscontextID, nil
}

func (a *App) sendCDPToContextLocked(payload string, c *wsClient, contextID string) {
	var env struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params map[string]any  `json:"params"`
	}
	_ = json.Unmarshal([]byte(payload), &env)
	if env.Method != "" && len(env.ID) != 0 && string(env.ID) != "null" {
		if id, err := decodeExactJSONValue(env.ID); err == nil {
			a.Requests.Add(cdp.Request{ID: id, Method: env.Method, Params: env.Params})
		}
	}
	opID := uint64(math.Round(100 * rand.Float64()))
	rawPayload := wmpf.ChromeDevtools{OpID: opID, Payload: payload, JSContextID: contextID}
	a.Log.Main(rawPayload)
	inner := wmpf.EncodeChrome(rawPayload)
	frame := wmpf.EncodeOutgoingDebugMessage(wmpf.DebugMessage{Seq: a.seq.Add(1), Category: wmpf.CategoryChromeDevtools, Data: inner})
	a.recordFrameLocked(capture.DirectionDownstream, frame)
	a.DebugHub.Broadcast(frame)
}

func decodeExactJSONValue(payload []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func (a *App) DebugClientCount() int { return a.DebugHub.Count() }
func (a *App) CDPClientCount() int   { return a.CDPHub.Count() }

// CancelCDPRequest removes one pending CDP request from the internal correlator.
func (a *App) CancelCDPRequest(id any) bool { return a.Requests.Cancel(id) }

// ClearRequests removes every pending CDP request from the internal correlator.
func (a *App) ClearRequests() int { return a.Requests.Clear() }

func (a *App) Close(ctx context.Context) error {
	a.closeOnce.Do(func() {
		var out error
		addCloseError := func(err error) {
			if err == nil || errors.Is(err, net.ErrClosed) {
				return
			}
			if out == nil {
				out = err
				return
			}
			out = errors.Join(out, err)
		}
		a.closing.Store(true)
		a.replayMu.Lock()
		if a.replayCancel != nil {
			a.replayCancel()
		}
		a.replayMu.Unlock()
		// Close clients before listeners and capture so no new protocol frame can
		// enter while resources are being released.
		a.connMu.Lock()
		a.DebugHub.CloseAll()
		a.CDPHub.CloseAll()
		a.connMu.Unlock()
		a.serverMu.Lock()
		debugLn, cdpLn := a.debugLn, a.cdpLn
		debugSrv, cdpSrv := a.debugSrv, a.cdpSrv
		if debugSrv != nil {
			addCloseError(debugSrv.Shutdown(ctx))
		}
		if cdpSrv != nil {
			addCloseError(cdpSrv.Shutdown(ctx))
		}
		// Shutdown only knows about listeners that have entered Serve. A custom
		// Serve implementation (or a startup failure) may leave one unregistered.
		// Close those listeners explicitly after Shutdown has had first ownership.
		for _, listener := range []net.Listener{debugLn, cdpLn} {
			if listener != nil {
				addCloseError(listener.Close())
			}
		}
		a.serveWG.Wait()
		a.started = false
		a.serverMu.Unlock()
		a.replayWG.Wait()
		a.dispatchMu.Lock()
		a.ClearRequests()
		recorder := a.TakeRecorder()
		a.dispatchMu.Unlock()
		if recorder != nil {
			addCloseError(recorder.Close())
		}
		a.closeErr = out
	})
	return a.closeErr
}
