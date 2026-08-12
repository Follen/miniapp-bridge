package proxy

import (
	"bufio"
	"errors"
	"net"
	"sync"
	"time"
)

var (
	ErrClientBackpressure     = errors.New("client outbound queue is full")
	ErrListenerAlreadyStarted = errors.New("listener already started")
	ErrListenerClosed         = errors.New("listener is closed")
)

const (
	listenerErrorBuffer     = 8
	initialAcceptRetryDelay = 5 * time.Millisecond
	maximumAcceptRetryDelay = time.Second
)

type Client interface {
	Send([]byte) error
	Close() error
}

type Hub struct {
	mu          sync.RWMutex
	broadcastMu sync.Mutex
	closeWG     sync.WaitGroup
	clients     map[Client]struct{}
}

func NewHub() *Hub             { return &Hub{clients: make(map[Client]struct{})} }
func (h *Hub) Add(c Client)    { h.mu.Lock(); h.clients[c] = struct{}{}; h.mu.Unlock() }
func (h *Hub) Remove(c Client) { h.mu.Lock(); delete(h.clients, c); h.mu.Unlock() }
func (h *Hub) Count() int      { h.mu.RLock(); defer h.mu.RUnlock(); return len(h.clients) }
func (h *Hub) CloseAll() {
	h.broadcastMu.Lock()
	h.mu.Lock()
	cs := make([]Client, 0, len(h.clients))
	for c := range h.clients {
		cs = append(cs, c)
	}
	h.clients = make(map[Client]struct{})
	h.mu.Unlock()
	for _, c := range cs {
		_ = c.Close()
	}
	h.closeWG.Wait()
	h.broadcastMu.Unlock()
}
func (h *Hub) Broadcast(msg []byte) {
	// Serialize enqueue operations so every client observes the same event order.
	// Real WebSocket clients implement Send as a bounded, non-blocking enqueue.
	h.broadcastMu.Lock()
	defer h.broadcastMu.Unlock()
	h.mu.RLock()
	cs := make([]Client, 0, len(h.clients))
	for c := range h.clients {
		cs = append(cs, c)
	}
	h.mu.RUnlock()
	for _, c := range cs {
		if err := c.Send(msg); err != nil {
			h.mu.Lock()
			_, present := h.clients[c]
			delete(h.clients, c)
			h.mu.Unlock()
			if present {
				// Closing a transport must never delay delivery to healthy clients.
				h.closeWG.Add(1)
				go func() {
					defer h.closeWG.Done()
					_ = c.Close()
				}()
			}
		}
	}
}

type Reconnector struct {
	mu        sync.Mutex
	connected bool
	attempts  int
}

func (r *Reconnector) Connected() bool   { r.mu.Lock(); defer r.mu.Unlock(); return r.connected }
func (r *Reconnector) MarkConnected()    { r.mu.Lock(); r.connected = true; r.attempts = 0; r.mu.Unlock() }
func (r *Reconnector) MarkDisconnected() { r.mu.Lock(); r.connected = false; r.mu.Unlock() }
func (r *Reconnector) NextAttempt() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts++
	return r.attempts
}

type Listener struct {
	Addr    string
	ln      net.Listener
	stop    chan struct{}
	Handler func(net.Conn)
	listen  func(string, string) (net.Listener, error)

	stateMu        sync.Mutex
	started        bool
	closed         bool
	acceptErr      error
	closeErr       error
	acceptWG       sync.WaitGroup
	handlerWG      sync.WaitGroup
	connMu         sync.Mutex
	conns          map[net.Conn]struct{}
	closeOnce      sync.Once
	errorCloseOnce sync.Once
	errors         chan error
}

func NewListener(addr string, h func(net.Conn)) *Listener {
	return &Listener{
		Addr:    addr,
		stop:    make(chan struct{}),
		Handler: h,
		listen:  net.Listen,
		errors:  make(chan error, listenerErrorBuffer),
		conns:   make(map[net.Conn]struct{}),
	}
}

// Errors reports accept failures that happen after Start succeeds. The stream
// is bounded; when a consumer falls behind, the oldest pending error is
// replaced so the latest listener failure remains observable.
func (s *Listener) Errors() <-chan error { return s.errors }

func (s *Listener) Start() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.closed {
		return ErrListenerClosed
	}
	if s.started {
		return ErrListenerAlreadyStarted
	}
	ln, err := s.listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.started = true
	s.acceptWG.Add(1)
	go s.accept(ln, s.Handler)
	return nil
}

func (s *Listener) accept(ln net.Listener, handler func(net.Conn)) {
	defer s.acceptWG.Done()
	defer s.closeErrorStream()
	var retryDelay time.Duration
	for {
		c, err := ln.Accept()
		if err != nil {
			if s.stopping() {
				return
			}
			s.reportAcceptError(err)
			if !retryableAcceptError(err) {
				s.stateMu.Lock()
				s.acceptErr = err
				s.stateMu.Unlock()
				return
			}
			retryDelay = nextAcceptRetryDelay(retryDelay)
			select {
			case <-time.After(retryDelay):
			case <-s.stop:
				return
			}
			continue
		}
		retryDelay = 0
		s.connMu.Lock()
		s.conns[c] = struct{}{}
		s.connMu.Unlock()
		s.handlerWG.Add(1)
		go s.handle(c, handler)
	}
}

func (s *Listener) handle(c net.Conn, handler func(net.Conn)) {
	defer s.handlerWG.Done()
	defer func() {
		s.connMu.Lock()
		delete(s.conns, c)
		s.connMu.Unlock()
	}()
	if handler != nil {
		handler(c)
		return
	}
	_, _ = bufio.NewReader(c).ReadBytes('\n')
	_ = c.Close()
}

func (s *Listener) closeConnections() {
	s.connMu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.connMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (s *Listener) stopping() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func retryableAcceptError(err error) bool {
	if errors.Is(err, net.ErrClosed) {
		return false
	}
	var netErr net.Error
	if !errors.As(err, &netErr) {
		// Preserve the previous retry behavior for custom listeners whose errors
		// do not expose net.Error's transient classification.
		return true
	}
	return netErr.Timeout() || netErr.Temporary()
}

func nextAcceptRetryDelay(current time.Duration) time.Duration {
	if current == 0 {
		return initialAcceptRetryDelay
	}
	next := current * 2
	if next > maximumAcceptRetryDelay {
		return maximumAcceptRetryDelay
	}
	return next
}

func (s *Listener) reportAcceptError(err error) {
	select {
	case s.errors <- err:
		return
	default:
	}
	select {
	case <-s.errors:
	default:
	}
	select {
	case s.errors <- err:
	default:
	}
}

func (s *Listener) closeErrorStream() {
	s.errorCloseOnce.Do(func() { close(s.errors) })
}

func (s *Listener) Close() error {
	s.closeOnce.Do(func() {
		s.stateMu.Lock()
		s.closed = true
		close(s.stop)
		ln := s.ln
		s.stateMu.Unlock()

		var listenerErr error
		if ln != nil {
			listenerErr = ln.Close()
			if errors.Is(listenerErr, net.ErrClosed) {
				listenerErr = nil
			}
		}
		s.acceptWG.Wait()
		s.closeConnections()
		s.handlerWG.Wait()
		s.closeErrorStream()

		s.stateMu.Lock()
		s.closeErr = errors.Join(listenerErr, s.acceptErr)
		s.stateMu.Unlock()
	})
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.closeErr
}
func StartDefaultListeners(debug, cdp func(net.Conn)) (*Listener, *Listener, error) {
	return startListeners("127.0.0.1:9421", "127.0.0.1:62000", debug, cdp)
}

func startListeners(debugAddr, cdpAddr string, debug, cdp func(net.Conn)) (*Listener, *Listener, error) {
	a := NewListener(debugAddr, debug)
	if err := a.Start(); err != nil {
		return nil, nil, err
	}
	b := NewListener(cdpAddr, cdp)
	if err := b.Start(); err != nil {
		_ = a.Close()
		return nil, nil, err
	}
	return a, b, nil
}
