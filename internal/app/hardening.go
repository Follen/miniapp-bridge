package app

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/gorilla/websocket"
)

type readLimitSetter interface {
	SetReadLimit(limit int64)
}

type rejectionBody struct {
	Error rejectionError `json:"error"`
}

type rejectionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func rejectWebSocket(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rejectionBody{Error: rejectionError{Code: code, Message: message}})
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (a *App) allowedCDPOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	if origin == "devtools://devtools" || origin == "chrome-devtools://devtools" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	return u.Port() == "" || u.Port() == strconv.Itoa(a.CDPPort)
}

func (a *App) reserveOwner(kind string) bool {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if a.closing.Load() {
		return false
	}
	if kind == "upstream" {
		if a.debugClaimed || a.debugOwner != nil {
			return false
		}
		a.debugClaimed = true
		return true
	}
	if a.cdpClaimed || a.cdpOwner != nil {
		return false
	}
	a.cdpClaimed = true
	return true
}

func (a *App) releaseReservation(kind string) {
	a.connMu.Lock()
	if kind == "upstream" {
		a.debugClaimed = false
	} else {
		a.cdpClaimed = false
	}
	a.connMu.Unlock()
}

func (a *App) installOwner(kind string, client *wsClient) bool {
	a.dispatchMu.Lock()
	defer a.dispatchMu.Unlock()
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if kind == "upstream" {
		a.debugClaimed = false
		if a.closing.Load() || a.debugOwner != nil {
			return false
		}
		// A new upstream transport cannot emit responses from the old one.
		a.clearCDPResponseFencesLocked()
		a.debugGeneration++
		client.generation = a.debugGeneration
		client.onError = func(err error) { a.reportRuntimeError("upstream-writer", client.generation, err) }
		a.debugOwner = client
		a.DebugHub.Add(client)
		seededContexts := a.Contexts.List()
		seededSelection, seeded := a.Contexts.Selected()
		a.Contexts.BeginGeneration(client.generation)
		for _, item := range seededContexts {
			_ = a.Contexts.UpsertForGeneration(client.generation, item)
		}
		if seeded {
			_, _ = a.Contexts.SelectForGeneration(client.generation, seededSelection.ID)
		}
	} else {
		a.cdpClaimed = false
		if a.closing.Load() || a.cdpOwner != nil {
			return false
		}
		a.cdpGeneration++
		client.generation = a.cdpGeneration
		client.onError = func(err error) { a.reportRuntimeError("cdp-writer", client.generation, err) }
		a.cdpOwner = client
		a.CDPHub.Add(client)
	}
	a.connectionWG.Add(1)
	return true
}

func (a *App) handleDebugWebSocket(w http.ResponseWriter, r *http.Request) {
	if a.closing.Load() {
		rejectWebSocket(w, http.StatusServiceUnavailable, "app_closing", "the bridge is shutting down")
		return
	}
	if !isLoopbackRequest(r) {
		a.rejectedUpstream.Add(1)
		rejectWebSocket(w, http.StatusForbidden, "loopback_required", "upstream WebSocket requires a loopback peer")
		return
	}
	if !a.reserveOwner("upstream") {
		a.rejectedUpstream.Add(1)
		rejectWebSocket(w, http.StatusConflict, "owner_exists", "an upstream owner is already connected")
		return
	}
	c, err := localUpgrader.Upgrade(w, r, nil)
	if err != nil {
		a.releaseReservation("upstream")
		return
	}
	if setter, ok := any(c).(readLimitSetter); ok {
		setter.SetReadLimit(websocketMaxMessageBytes)
	}
	client := newWSClient(c, websocket.BinaryMessage)
	client.network = true
	if !a.installOwner("upstream", client) {
		_ = c.Close()
		return
	}
	if observer := a.observerSnapshot(); observer.OnConnection != nil {
		observer.OnConnection(ConnectionEvent{Kind: "upstream", Connected: true, Generation: client.generation})
	}
	a.Log.Info("[miniapp] miniapp client connected")
	go a.readDebug(client)
}

func (a *App) handleCDPWebSocket(w http.ResponseWriter, r *http.Request) {
	if a.closing.Load() {
		rejectWebSocket(w, http.StatusServiceUnavailable, "app_closing", "the bridge is shutting down")
		return
	}
	if !isLoopbackRequest(r) {
		a.rejectedCDP.Add(1)
		rejectWebSocket(w, http.StatusForbidden, "loopback_required", "CDP WebSocket requires a loopback peer")
		return
	}
	if !a.allowedCDPOrigin(r.Header.Get("Origin")) {
		a.rejectedOrigin.Add(1)
		rejectWebSocket(w, http.StatusForbidden, "origin_not_allowed", "CDP WebSocket Origin is not allowed")
		return
	}
	if !a.reserveOwner("cdp") {
		a.rejectedCDP.Add(1)
		rejectWebSocket(w, http.StatusConflict, "owner_exists", "a CDP controller is already connected")
		return
	}
	c, err := localUpgrader.Upgrade(w, r, nil)
	if err != nil {
		a.releaseReservation("cdp")
		return
	}
	if setter, ok := any(c).(readLimitSetter); ok {
		setter.SetReadLimit(websocketMaxMessageBytes)
	}
	client := newWSClient(c, websocket.TextMessage)
	client.network = true
	if !a.installOwner("cdp", client) {
		_ = c.Close()
		return
	}
	if observer := a.observerSnapshot(); observer.OnConnection != nil {
		observer.OnConnection(ConnectionEvent{Kind: "cdp", Connected: true, Generation: client.generation})
	}
	a.Log.Info("[cdp] CDP client connected")
	go a.readCDP(client)
}

var localUpgrader = websocket.Upgrader{
	HandshakeTimeout: 5 * time.Second,
	CheckOrigin:      func(*http.Request) bool { return true },
}

func (a *App) ownerCurrent(kind string, client *wsClient) bool {
	if client.generation == 0 {
		return true
	}
	a.connMu.RLock()
	defer a.connMu.RUnlock()
	if kind == "upstream" {
		return a.debugOwner == client && a.debugGeneration == client.generation
	}
	return a.cdpOwner == client && a.cdpGeneration == client.generation
}

func (a *App) releaseOwner(kind string, client *wsClient) bool {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if kind == "upstream" {
		a.DebugHub.Remove(client)
		if a.debugOwner != client || a.debugGeneration != client.generation {
			return false
		}
		a.debugClaimed = true
		a.debugOwner = nil
		return true
	}
	a.CDPHub.Remove(client)
	if a.cdpOwner != client || a.cdpGeneration != client.generation {
		return false
	}
	a.cdpClaimed = true
	a.cdpOwner = nil
	return true
}

func (a *App) finishOwnerRelease(kind string, client *wsClient) {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if kind == "upstream" {
		if a.debugOwner == nil && a.debugGeneration == client.generation {
			a.debugClaimed = false
		}
		return
	}
	if a.cdpOwner == nil && a.cdpGeneration == client.generation {
		a.cdpClaimed = false
	}
}

func (a *App) clearContextsLocked() {
	for _, item := range a.Contexts.List() {
		if a.Contexts.Remove(item.ID) {
			a.notifyContextRemoved(item)
		}
	}
}

func (a *App) notifyContextsRemoved(contexts []bridgecontext.Context) {
	for _, item := range contexts {
		a.notifyContextRemoved(item)
	}
}

func (a *App) notifyContextRemoved(item bridgecontext.Context) {
	if observer := a.observerSnapshot(); observer.OnContext != nil {
		observer.OnContext(ContextEvent{Kind: "removed", Context: item})
	}
}

func shouldReportWebSocketError(err error) bool {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return false
	}
	return !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway)
}

func (a *App) recordStaleDrop(kind string, generation uint64) {
	a.staleDrops.Add(1)
	a.reportRuntimeError(kind+"-reader", generation, errors.New("stale owner generation dropped"))
}

func (a *App) reportRuntimeError(component string, generation uint64, err error) {
	if err == nil {
		return
	}
	message := logging.SanitizeErrorText(err.Error())
	event := RuntimeError{Component: component, Generation: generation, Message: message, At: time.Now().UTC()}
	a.runtimeErrMu.Lock()
	if len(a.runtimeErrors) == maxRuntimeErrors {
		copy(a.runtimeErrors, a.runtimeErrors[1:])
		a.runtimeErrors[len(a.runtimeErrors)-1] = event
	} else {
		a.runtimeErrors = append(a.runtimeErrors, event)
	}
	a.runtimeErrMu.Unlock()
	if observer := a.observerSnapshot(); observer.OnError != nil {
		observer.OnError(event)
	}
	if a.Log != nil {
		a.Log.Error("[", component, "] generation ", generation, ": ", message)
	}
}

func (a *App) RuntimeErrors() []RuntimeError {
	a.runtimeErrMu.RLock()
	defer a.runtimeErrMu.RUnlock()
	return append([]RuntimeError(nil), a.runtimeErrors...)
}

func (a *App) ConnectionSnapshot() ConnectionSnapshot {
	a.connMu.RLock()
	snapshot := ConnectionSnapshot{
		UpstreamConnected:  a.debugOwner != nil,
		CDPConnected:       a.cdpOwner != nil,
		UpstreamGeneration: a.debugGeneration,
		CDPGeneration:      a.cdpGeneration,
	}
	a.connMu.RUnlock()
	snapshot.RejectedUpstream = a.rejectedUpstream.Load()
	snapshot.RejectedCDP = a.rejectedCDP.Load()
	snapshot.RejectedOrigin = a.rejectedOrigin.Load()
	snapshot.StaleDrops = a.staleDrops.Load()
	return snapshot
}
