package app

import (
	"bytes"
	"context"
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
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrClosed                  = errors.New("app is closed")
	ErrAlreadyStarted          = errors.New("app is already started")
	ErrInvalidCDPPayload       = errors.New("invalid CDP payload")
	ErrNoContext               = errors.New("no JavaScript context is selected")
	ErrUnknownContext          = errors.New("unknown JavaScript context")
	ErrMessageTooLarge         = errors.New("WebSocket message exceeds the configured limit")
	ErrQueueBytes              = errors.New("client outbound queue byte limit exceeded")
	ErrSubscriptionLimit       = errors.New("CDP subscription limit reached")
	ErrCDPRequestAmbiguous     = errors.New("CDP request ID is fenced by a previous controller generation")
	ErrCDPUpstreamDisconnected = errors.New("WMPF upstream disconnected before the CDP response arrived")
)

// Observer is an optional, non-blocking hook used by the public SDK.
type Observer struct {
	OnCDP        func([]byte)
	OnContext    func(ContextEvent)
	OnConnection func(ConnectionEvent)
	OnError      func(RuntimeError)
}

type ContextEvent struct {
	Kind    string
	Context bridgecontext.Context
}

type ConnectionEvent struct {
	Kind       string
	Connected  bool
	Generation uint64
}

// RuntimeError is a bounded, structured record of an asynchronous listener,
// reader, or writer failure.
type RuntimeError struct {
	Component  string
	Generation uint64
	Message    string
	At         time.Time
}

// ConnectionSnapshot exposes owner generations and rejected/stale traffic
// without granting callers ownership of live transports.
type ConnectionSnapshot struct {
	UpstreamConnected  bool
	CDPConnected       bool
	UpstreamGeneration uint64
	CDPGeneration      uint64
	RejectedUpstream   uint64
	RejectedCDP        uint64
	RejectedOrigin     uint64
	StaleDrops         uint64
}

type App struct {
	DebugPort, CDPPort      int
	Log                     *logging.Logger
	DebugHub, CDPHub        *proxy.Hub
	Contexts                *bridgecontext.Registry
	Requests                *cdp.Correlator
	Recorder                capture.FrameRecorder
	recorderMu              sync.RWMutex
	debugSrv, cdpSrv        *http.Server
	debugLn, cdpLn          net.Listener
	serverMu                sync.Mutex
	serveWG                 sync.WaitGroup
	started                 bool
	closeOnce               sync.Once
	closeErr                error
	closing                 atomic.Bool
	connMu                  sync.RWMutex
	debugOwner              *wsClient
	cdpOwner                *wsClient
	debugClaimed            bool
	cdpClaimed              bool
	debugGeneration         uint64
	cdpGeneration           uint64
	connectionWG            sync.WaitGroup
	rejectedUpstream        atomic.Uint64
	rejectedCDP             atomic.Uint64
	rejectedOrigin          atomic.Uint64
	staleDrops              atomic.Uint64
	dispatchMu              sync.Mutex
	subscriptions           map[string]uint64
	cdpResponseFences       map[string]cdpResponseFence
	cdpResponseFenceBlocked bool
	observerMu              sync.RWMutex
	observer                Observer
	runtimeErrMu            sync.RWMutex
	runtimeErrors           []RuntimeError
	replayMu                sync.Mutex
	replayCancel            context.CancelFunc
	replayID                uint64
	replayWG                sync.WaitGroup
	seq                     atomic.Uint32
	listen                  func(string, string) (net.Listener, error)
	serve                   func(*http.Server, net.Listener) error
}
type wsClient struct {
	conn            websocketConnection
	initOnce        sync.Once
	sendMu          sync.Mutex
	stopOnce        sync.Once
	closeOnce       sync.Once
	outbound        chan outboundMessage
	done            chan struct{}
	writerDone      chan struct{}
	closeErr        error
	closed          atomic.Bool
	typeID          int
	queueSize       int
	queueBytes      int64
	queueByteLimit  int64
	maxMessageBytes int64
	generation      uint64
	network         bool
	onError         func(error)
}

type outboundMessage struct {
	frames [][]byte
	bytes  int64
}

type websocketConnection interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

const (
	websocketWriteTimeout       = 5 * time.Second
	websocketOutboundQueueSize  = 256
	websocketMaxMessageBytes    = int64(8 << 20)
	websocketOutboundQueueBytes = int64(16 << 20)
	maxRuntimeErrors            = 64
	maxCDPSubscriptions         = 64
	cdpServerErrorCode          = -32000
)

func newWSClient(conn websocketConnection, typeID int) *wsClient {
	return &wsClient{
		conn: conn, typeID: typeID, queueSize: websocketOutboundQueueSize,
		queueByteLimit: websocketOutboundQueueBytes, maxMessageBytes: websocketMaxMessageBytes,
	}
}

func (c *wsClient) initialize() {
	c.initOnce.Do(func() {
		queueSize := c.queueSize
		if queueSize <= 0 {
			queueSize = websocketOutboundQueueSize
		}
		c.outbound = make(chan outboundMessage, queueSize)
		c.done = make(chan struct{})
		c.writerDone = make(chan struct{})
		go c.writeLoop()
	})
}

func (c *wsClient) Send(b []byte) error {
	return c.SendBatch([][]byte{b})
}

func (c *wsClient) SendBatch(messages [][]byte) error {
	c.initialize()
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed.Load() {
		return ErrClosed
	}
	if len(messages) == 0 {
		return nil
	}
	messageLimit := c.maxMessageBytes
	if messageLimit <= 0 {
		messageLimit = websocketMaxMessageBytes
	}
	byteLimit := c.queueByteLimit
	if byteLimit <= 0 {
		byteLimit = websocketOutboundQueueBytes
	}
	frames := make([][]byte, len(messages))
	var totalBytes int64
	for index, message := range messages {
		messageBytes := int64(len(message))
		if messageBytes > messageLimit {
			return ErrMessageTooLarge
		}
		if messageBytes > byteLimit-c.queueBytes-totalBytes {
			return ErrQueueBytes
		}
		frames[index] = append([]byte(nil), message...)
		totalBytes += messageBytes
	}
	message := outboundMessage{frames: frames, bytes: totalBytes}
	select {
	case c.outbound <- message:
		c.queueBytes += totalBytes
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
			var err error
			for _, frame := range message.frames {
				if err = c.writeMessage(frame); err != nil {
					break
				}
			}
			c.sendMu.Lock()
			c.queueBytes -= message.bytes
			c.sendMu.Unlock()
			if err != nil {
				if c.onError != nil && !errors.Is(err, net.ErrClosed) {
					c.onError(err)
				}
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
	return &App{DebugPort: dp, CDPPort: cp, Log: l, DebugHub: proxy.NewHub(), CDPHub: proxy.NewHub(), Contexts: bridgecontext.NewRegistry(), Requests: cdp.NewCorrelator(), subscriptions: make(map[string]uint64), cdpResponseFences: make(map[string]cdpResponseFence), listen: net.Listen, serve: func(s *http.Server, l net.Listener) error { return s.Serve(l) }}
}
func (a *App) SetRecorder(r capture.FrameRecorder) {
	a.recorderMu.Lock()
	a.Recorder = r
	a.recorderMu.Unlock()
}
func (a *App) SwapRecorder(r capture.FrameRecorder) capture.FrameRecorder {
	a.recorderMu.Lock()
	old := a.Recorder
	a.Recorder = r
	a.recorderMu.Unlock()
	return old
}
func (a *App) TakeRecorder() capture.FrameRecorder {
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
	a.debugSrv = &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", a.DebugPort),
		Handler:           http.HandlerFunc(a.handleDebugWebSocket),
		ReadHeaderTimeout: 5 * time.Second,
	}
	a.cdpSrv = &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", a.CDPPort),
		Handler:           http.HandlerFunc(a.handleCDPWebSocket),
		ReadHeaderTimeout: 5 * time.Second,
	}
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
			a.reportRuntimeError("upstream-listener", 0, e)
		}
	}()
	go func() {
		defer a.serveWG.Done()
		e := a.serve(a.cdpSrv, cdpLn)
		if e != nil && e != http.ErrServerClosed && !errors.Is(e, net.ErrClosed) {
			a.reportRuntimeError("cdp-listener", 0, e)
		}
	}()
	a.Log.Info(fmt.Sprintf("[server] debug server running on ws://127.0.0.1:%d", a.DebugPort))
	a.Log.Info("[server] debug server waiting for miniapp to connect...")
	a.Log.Info(fmt.Sprintf("[server] proxy server running on ws://127.0.0.1:%d", a.CDPPort))
	a.Log.Info(fmt.Sprintf("[server] link: devtools://devtools/bundled/inspector.html?ws=127.0.0.1:%d", a.CDPPort))
	return nil
}
func (a *App) readDebug(c *wsClient) {
	if c.generation != 0 {
		defer a.connectionWG.Done()
	}
	defer func() {
		a.dispatchMu.Lock()
		ownerReleased := a.releaseOwner("upstream", c)
		if ownerReleased || c.generation == 0 {
			if ownerReleased && c.generation != 0 {
				a.failCurrentControllerRequestsLocked()
				// Legacy callers may have registered unscoped requests. Once
				// the upstream owner is gone there is no response path for them.
				a.ClearRequests()
				a.clearCDPResponseFencesLocked()
			} else {
				a.ClearRequests()
			}
			if ownerReleased {
				_, _ = a.Contexts.EndGeneration(c.generation)
			} else {
				a.clearContextsLocked()
			}
			clear(a.subscriptions)
		}
		a.dispatchMu.Unlock()
		if ownerReleased {
			a.finishOwnerRelease("upstream", c)
		}
		_ = c.Close()
		if ownerReleased || c.generation == 0 {
			if observer := a.observerSnapshot(); observer.OnConnection != nil {
				observer.OnConnection(ConnectionEvent{Kind: "upstream", Connected: false, Generation: c.generation})
			}
			a.Log.Info("[miniapp] miniapp client disconnected")
		}
	}()
	for {
		typ, b, e := c.conn.ReadMessage()
		if e != nil {
			if shouldReportWebSocketError(e) {
				a.reportRuntimeError("upstream-reader", c.generation, e)
			}
			return
		}
		if !a.handleDebugFrameForClient(c, typ, b) {
			if !a.ownerCurrent("upstream", c) {
				return
			}
		}
	}
}

func (a *App) handleDebugFrame(typ int, b []byte) bool {
	return a.handleDebugFrameForClient(nil, typ, b)
}

func (a *App) handleDebugFrameForClient(c *wsClient, typ int, b []byte) bool {
	if typ != websocket.BinaryMessage && typ != websocket.TextMessage {
		return false
	}
	if c != nil && !a.ownerCurrent("upstream", c) {
		a.recordStaleDrop("upstream", c.generation)
		return false
	}
	if int64(len(b)) > websocketMaxMessageBytes {
		a.reportRuntimeError("upstream-reader", func() uint64 {
			if c == nil {
				return 0
			}
			return c.generation
		}(), ErrMessageTooLarge)
		return false
	}
	a.Log.Main("[miniapp] client received raw message:", logging.PayloadSummary(b))
	a.dispatchMu.Lock()
	defer a.dispatchMu.Unlock()
	a.recordFrameLocked(capture.DirectionUpstream, b)
	m, e := wmpf.DecodeDebugMessage(b)
	if e != nil {
		a.Log.Error("[miniapp] miniapp client err:", e)
		return true
	}
	generation := uint64(0)
	if c != nil {
		generation = c.generation
	}
	a.handleDebugMessageLockedForGeneration(m, generation)
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
	a.handleDebugMessageLockedForGeneration(message, 0)
}

func (a *App) handleDebugMessageLocked(message wmpf.DebugMessage) {
	a.handleDebugMessageLockedForGeneration(message, 0)
}

func (a *App) handleDebugMessageLockedForGeneration(message wmpf.DebugMessage, generation uint64) {
	u, err := wmpf.UnwrapDebugMessage(message)
	if err != nil {
		a.Log.Error("[miniapp] decode:", err)
		a.reportRuntimeError("upstream-decode", generation, err)
		return
	}
	a.Log.Main("[miniapp] [DEBUG] decoded data:", logging.PayloadSummary(u.Raw))
	a.handleUnwrappedDebugForGeneration(u, generation)
}

func (a *App) handleUnwrappedDebug(u wmpf.Unwrapped) {
	a.handleUnwrappedDebugForGeneration(u, 0)
}

func (a *App) handleUnwrappedDebugForGeneration(u wmpf.Unwrapped, generation uint64) {
	value, ok := a.decodeCategoryData(u.Category, u.Data)
	if !ok {
		return
	}
	switch u.Category {
	case wmpf.CategoryAddJsContext:
		v := value.(wmpf.JsContext)
		ctx := bridgecontext.Context{ID: v.ID, Target: v.Name}
		if generation != 0 {
			if err := a.Contexts.UpsertForGeneration(generation, ctx); err != nil {
				if errors.Is(err, bridgecontext.ErrGenerationMismatch) {
					a.recordStaleDrop("upstream", generation)
				} else {
					a.reportRuntimeError("context-registry", generation, err)
				}
				return
			}
		} else {
			a.Contexts.Upsert(ctx)
		}
		if observer := a.observerSnapshot(); observer.OnContext != nil {
			observer.OnContext(ContextEvent{Kind: "added", Context: ctx})
		}
	case wmpf.CategoryRemoveJsContext:
		v := value.(wmpf.JsContext)
		removed := false
		if generation != 0 {
			var err error
			removed, err = a.Contexts.RemoveForGeneration(generation, v.ID)
			if errors.Is(err, bridgecontext.ErrGenerationMismatch) {
				a.recordStaleDrop("upstream", generation)
				return
			}
		} else {
			removed = a.Contexts.Remove(v.ID)
		}
		if removed {
			if observer := a.observerSnapshot(); observer.OnContext != nil {
				observer.OnContext(ContextEvent{Kind: "removed", Context: bridgecontext.Context{ID: v.ID, Target: v.Name}})
			}
		}
	case wmpf.CategoryConnectJsContext:
		v := value.(wmpf.JsContext)
		if _, exists := a.Contexts.Get(v.ID); !exists {
			if generation != 0 {
				if err := a.Contexts.UpsertForGeneration(generation, bridgecontext.Context{ID: v.ID}); err != nil {
					if errors.Is(err, bridgecontext.ErrGenerationMismatch) {
						a.recordStaleDrop("upstream", generation)
					} else {
						a.reportRuntimeError("context-registry", generation, err)
					}
					return
				}
			} else {
				a.Contexts.Upsert(bridgecontext.Context{ID: v.ID})
			}
		}
		if generation != 0 {
			selected, err := a.Contexts.SelectForGeneration(generation, v.ID)
			if errors.Is(err, bridgecontext.ErrGenerationMismatch) {
				a.recordStaleDrop("upstream", generation)
				return
			}
			if !selected {
				return
			}
		} else {
			a.Contexts.Select(v.ID)
		}
		if observer := a.observerSnapshot(); observer.OnContext != nil {
			ctx, _ := a.Contexts.Get(v.ID)
			observer.OnContext(ContextEvent{Kind: "selected", Context: ctx})
		}
	case wmpf.CategoryChromeDevtoolsResult:
		v := value.(wmpf.ChromeDevtools)
		payload := []byte(v.Payload)
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(payload, &envelope); err != nil || envelope == nil {
			a.reportRuntimeError(
				"upstream-cdp-payload",
				generation,
				fmt.Errorf("%w: expected a JSON object (%s)", ErrInvalidCDPPayload, logging.PayloadSummary(payload)),
			)
			return
		}
		var response struct {
			ID     json.RawMessage `json:"id"`
			Result map[string]any  `json:"result"`
			Error  *cdp.Error      `json:"error"`
		}
		if json.Unmarshal(payload, &response) == nil && len(response.ID) != 0 && string(response.ID) != "null" {
			if id, err := decodeExactJSONValue(response.ID); err == nil {
				if fenced, controllerGeneration := a.consumeCDPResponseFenceLocked(id); fenced {
					a.recordStaleDrop("cdp-response", controllerGeneration)
					return
				}
				a.resolveCDPResponse(id, response.Result, response.Error)
			}
		}
		a.CDPHub.Broadcast(payload)
		if observer := a.observerSnapshot(); observer.OnCDP != nil {
			observer.OnCDP(payload)
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

func (a *App) resolveCDPResponse(id any, result map[string]any, responseError *cdp.Error) {
	a.connMu.RLock()
	owner := a.cdpOwner
	generation := a.cdpGeneration
	a.connMu.RUnlock()
	if owner != nil && owner.generation != 0 {
		if _, err := a.Requests.ResolveFor("controller", generation, cdp.Response{ID: id, Result: result, Error: responseError}); err == nil {
			return
		}
	}
	_, _ = a.Requests.Resolve(cdp.Response{ID: id, Result: result, Error: responseError})
}

func (a *App) readCDP(c *wsClient) {
	if c.generation != 0 {
		defer a.connectionWG.Done()
	}
	defer func() {
		a.dispatchMu.Lock()
		ownerReleased := a.releaseOwner("cdp", c)
		if ownerReleased || c.generation == 0 {
			if ownerReleased && c.generation != 0 {
				drained := a.Requests.DrainFor("controller", c.generation)
				// Keep the generation tombstone even when the upstream owner is
				// already absent. A late response from the old transport must not
				// be allowed to satisfy a reused request ID.
				a.addCDPResponseFencesLocked(drained, c.generation)
			} else {
				a.ClearRequests()
			}
			clear(a.subscriptions)
		}
		a.dispatchMu.Unlock()
		if ownerReleased {
			a.finishOwnerRelease("cdp", c)
		}
		_ = c.Close()
		if ownerReleased || c.generation == 0 {
			if observer := a.observerSnapshot(); observer.OnConnection != nil {
				observer.OnConnection(ConnectionEvent{Kind: "cdp", Connected: false, Generation: c.generation})
			}
			a.Log.Info("[cdp] CDP client disconnected")
		}
	}()
	for {
		typ, b, e := c.conn.ReadMessage()
		if e != nil {
			if shouldReportWebSocketError(e) {
				a.reportRuntimeError("cdp-reader", c.generation, e)
			}
			return
		}
		a.handleCDPFrame(typ, b, c)
	}
}

func (a *App) handleCDPFrame(typ int, b []byte, c *wsClient) bool {
	if typ != websocket.TextMessage && typ != websocket.BinaryMessage {
		return false
	}
	if c != nil && !a.ownerCurrent("cdp", c) {
		a.recordStaleDrop("cdp", c.generation)
		return false
	}
	if int64(len(b)) > websocketMaxMessageBytes {
		a.reportRuntimeError("cdp-reader", func() uint64 {
			if c == nil {
				return 0
			}
			return c.generation
		}(), ErrMessageTooLarge)
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
	if int64(len(payload)) > websocketMaxMessageBytes {
		return ErrMessageTooLarge
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
	if int64(len(payload)) > websocketMaxMessageBytes {
		return ErrMessageTooLarge
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
	var requestID any
	hasRequestID := false
	if env.Method != "" && len(env.ID) != 0 && string(env.ID) != "null" {
		if id, err := decodeExactJSONValue(env.ID); err == nil {
			requestID = id
			hasRequestID = true
		}
	}
	if hasRequestID && c != nil && c.generation != 0 {
		if c.network {
			a.connMu.RLock()
			upstreamActive := a.debugOwner != nil
			a.connMu.RUnlock()
			if !upstreamActive {
				a.sendCDPErrorLocked(c, requestID, cdpServerErrorCode, ErrCDPUpstreamDisconnected.Error())
				return
			}
		}
	}
	if hasRequestID && c != nil && c.generation != 0 {
		if fenced, _ := a.cdpResponseFencedLocked(requestID); fenced {
			a.sendCDPErrorLocked(c, requestID, cdpServerErrorCode, ErrCDPRequestAmbiguous.Error())
			return
		}
	}
	if err := a.trackSubscriptionLocked(env.Method, c); err != nil {
		if c != nil && hasRequestID {
			a.sendCDPErrorLocked(c, requestID, cdpServerErrorCode, err.Error())
		}
		return
	}
	if hasRequestID {
		request := cdp.Request{ID: requestID, Method: env.Method, Params: env.Params}
		var addErr error
		if c != nil && c.generation != 0 {
			addErr = a.Requests.TryAddFor("controller", c.generation, request)
		} else {
			addErr = a.Requests.TryAdd(request)
		}
		if addErr != nil {
			if c != nil {
				message := addErr.Error()
				a.sendCDPErrorLocked(c, requestID, cdpServerErrorCode, message)
			}
			return
		}
	}
	opID := uint64(math.Round(100 * rand.Float64()))
	rawPayload := wmpf.ChromeDevtools{OpID: opID, Payload: payload, JSContextID: contextID}
	a.Log.Main("[cdp] outbound payload:", logging.PayloadSummary([]byte(payload)))
	inner := wmpf.EncodeChrome(rawPayload)
	frame := wmpf.EncodeOutgoingDebugMessage(wmpf.DebugMessage{Seq: a.seq.Add(1), Category: wmpf.CategoryChromeDevtools, Data: inner})
	a.recordFrameLocked(capture.DirectionDownstream, frame)
	a.DebugHub.Broadcast(frame)
}

func (a *App) trackSubscriptionLocked(method string, c *wsClient) error {
	if method == "" {
		return nil
	}
	parts := strings.SplitN(method, ".", 2)
	if len(parts) != 2 || (parts[1] != "enable" && parts[1] != "disable") {
		return nil
	}
	generation := uint64(0)
	if c != nil {
		generation = c.generation
	}
	if a.subscriptions == nil {
		a.subscriptions = make(map[string]uint64)
	}
	key := parts[0]
	if parts[1] == "disable" {
		if ownerGeneration, ok := a.subscriptions[key]; ok && ownerGeneration == generation {
			delete(a.subscriptions, key)
		}
		return nil
	}
	if ownerGeneration, ok := a.subscriptions[key]; ok && ownerGeneration == generation {
		return nil
	}
	if len(a.subscriptions) >= maxCDPSubscriptions {
		return ErrSubscriptionLimit
	}
	a.subscriptions[key] = generation
	return nil
}

func (a *App) sendCDPErrorLocked(c *wsClient, id any, code int, message string) {
	if c == nil {
		return
	}
	body, err := marshalCDPError(id, code, message)
	if err == nil {
		if sendErr := c.Send(body); sendErr != nil {
			a.reportRuntimeError("cdp-error-writer", c.generation, sendErr)
		}
	}
}

func marshalCDPError(id any, code int, message string) ([]byte, error) {
	return json.Marshal(struct {
		ID    any `json:"id"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{ID: id, Error: struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}})
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
func (a *App) CancelCDPRequest(id any) bool {
	a.connMu.RLock()
	owner := a.cdpOwner
	generation := a.cdpGeneration
	a.connMu.RUnlock()
	if owner != nil && owner.generation != 0 {
		return a.Requests.CancelFor("controller", generation, id)
	}
	return a.Requests.Cancel(id)
}

// ClearRequests removes every pending CDP request from the internal correlator.
func (a *App) ClearRequests() int { return a.Requests.Clear() }

func (a *App) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
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
		a.connectionWG.Wait()
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
