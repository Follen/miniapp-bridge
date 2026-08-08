package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"math"
	"math/rand"
	"miniapp-bridge/internal/capture"
	"miniapp-bridge/internal/cdp"
	bridgecontext "miniapp-bridge/internal/context"
	"miniapp-bridge/internal/logging"
	"miniapp-bridge/internal/proxy"
	"miniapp-bridge/internal/wmpf"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type App struct {
	DebugPort, CDPPort int
	Log                *logging.Logger
	DebugHub, CDPHub   *proxy.Hub
	Contexts           *bridgecontext.Registry
	Requests           *cdp.Correlator
	Recorder           *capture.Recorder
	debugSrv, cdpSrv   *http.Server
	closeOnce          sync.Once
	closing            atomic.Bool
	dispatchMu         sync.Mutex
	seq                atomic.Uint32
	listen             func(string, string) (net.Listener, error)
	serve              func(*http.Server, net.Listener) error
}
type wsClient struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	typeID int
}

func (c *wsClient) Send(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(c.typeID, b)
}
func (c *wsClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"),
		time.Now().Add(250*time.Millisecond))
	return c.conn.Close()
}
func New(dp, cp int, l *logging.Logger) *App {
	return &App{DebugPort: dp, CDPPort: cp, Log: l, DebugHub: proxy.NewHub(), CDPHub: proxy.NewHub(), Contexts: bridgecontext.NewRegistry(), Requests: cdp.NewCorrelator(), listen: net.Listen, serve: func(s *http.Server, l net.Listener) error { return s.Serve(l) }}
}
func (a *App) SetRecorder(r *capture.Recorder) { a.Recorder = r }
func (a *App) Replay(path string) error {
	frames, err := capture.Replay(path)
	if err != nil {
		return err
	}
	go func() {
		for _, f := range frames {
			m, e := wmpf.DecodeDebugMessage(f)
			if e != nil {
				a.Log.Error("[capture] replay:", e)
				continue
			}
			a.handleDebugMessage(m)
		}
	}()
	return nil
}
func (a *App) Start() error {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	a.debugSrv = &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", a.DebugPort), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := up.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		if a.closing.Load() {
			_ = c.Close()
			return
		}
		x := &wsClient{conn: c, typeID: websocket.BinaryMessage}
		a.DebugHub.Add(x)
		a.Log.Info("[miniapp] miniapp client connected")
		go a.readDebug(x)
	})}
	a.cdpSrv = &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", a.CDPPort), Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := up.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		if a.closing.Load() {
			_ = c.Close()
			return
		}
		x := &wsClient{conn: c, typeID: websocket.TextMessage}
		a.CDPHub.Add(x)
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
	go func() {
		e := a.serve(a.debugSrv, debugLn)
		if e != nil && e != http.ErrServerClosed {
			a.Log.Error(e)
		}
	}()
	go func() {
		e := a.serve(a.cdpSrv, cdpLn)
		if e != nil && e != http.ErrServerClosed {
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
		a.DebugHub.Remove(c)
		_ = c.Close()
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
	if a.Recorder != nil {
		if err := a.Recorder.Write(b); err != nil {
			a.Log.Error("[capture] write:", err)
		}
	}
	m, e := wmpf.DecodeDebugMessage(b)
	if e != nil {
		a.Log.Error("[miniapp] miniapp client err:", e)
		return true
	}
	a.handleDebugMessage(m)
	return true
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
		a.Contexts.Upsert(bridgecontext.Context{ID: v.ID, Target: v.Name})
	case wmpf.CategoryRemoveJsContext:
		a.Contexts.Remove(value.(wmpf.JsContext).ID)
	case wmpf.CategoryConnectJsContext:
		v := value.(wmpf.JsContext)
		if _, exists := a.Contexts.Get(v.ID); !exists {
			a.Contexts.Upsert(bridgecontext.Context{ID: v.ID})
		}
		a.Contexts.Select(v.ID)
	case wmpf.CategoryChromeDevtoolsResult:
		v := value.(wmpf.ChromeDevtools)
		var response struct {
			ID     any            `json:"id"`
			Result map[string]any `json:"result"`
			Error  *cdp.Error     `json:"error"`
		}
		if json.Unmarshal([]byte(v.Payload), &response) == nil && response.ID != nil {
			_, _ = a.Requests.Resolve(cdp.Response{ID: response.ID, Result: response.Result, Error: response.Error})
		}
		a.CDPHub.Broadcast([]byte(v.Payload))
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
	payload := string(b)
	var env struct {
		ID     any            `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	_ = json.Unmarshal(b, &env)
	a.dispatchMu.Lock()
	if env.Method != "" && env.ID != nil {
		a.Requests.Add(cdp.Request{ID: env.ID, Method: env.Method, Params: env.Params})
	}
	contextID := ""
	if selected, ok := a.Contexts.Selected(); ok {
		contextID = selected.ID
	}
	opID := uint64(math.Round(100 * rand.Float64()))
	rawPayload := wmpf.ChromeDevtools{OpID: opID, Payload: payload, JSContextID: contextID}
	a.Log.Main(rawPayload)
	inner := wmpf.EncodeChrome(rawPayload)
	frame := wmpf.EncodeOutgoingDebugMessage(wmpf.DebugMessage{Seq: a.seq.Add(1), Category: wmpf.CategoryChromeDevtools, Data: inner})
	a.DebugHub.Broadcast(frame)
	a.dispatchMu.Unlock()
	return true
}
func (a *App) Close(ctx context.Context) error {
	var out error
	a.closeOnce.Do(func() {
		a.closing.Store(true)
		if a.Recorder != nil {
			out = a.Recorder.Close()
		}
		a.DebugHub.CloseAll()
		a.CDPHub.CloseAll()
		if a.debugSrv != nil {
			if err := a.debugSrv.Shutdown(ctx); out == nil {
				out = err
			}
		}
		if a.cdpSrv != nil {
			if e := a.cdpSrv.Shutdown(ctx); out == nil {
				out = e
			}
		}
	})
	return out
}
