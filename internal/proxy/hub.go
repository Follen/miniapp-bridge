package proxy

import (
	"bufio"
	"errors"
	"net"
	"sync"
)

type Client interface {
	Send([]byte) error
	Close() error
}

type Hub struct {
	mu      sync.RWMutex
	clients map[Client]struct{}
}

func NewHub() *Hub             { return &Hub{clients: make(map[Client]struct{})} }
func (h *Hub) Add(c Client)    { h.mu.Lock(); h.clients[c] = struct{}{}; h.mu.Unlock() }
func (h *Hub) Remove(c Client) { h.mu.Lock(); delete(h.clients, c); h.mu.Unlock() }
func (h *Hub) Count() int      { h.mu.RLock(); defer h.mu.RUnlock(); return len(h.clients) }
func (h *Hub) CloseAll() {
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
}
func (h *Hub) Broadcast(msg []byte) {
	h.mu.RLock()
	cs := make([]Client, 0, len(h.clients))
	for c := range h.clients {
		cs = append(cs, c)
	}
	h.mu.RUnlock()
	for _, c := range cs {
		if err := c.Send(msg); err != nil {
			h.Remove(c)
			_ = c.Close()
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
	wg      sync.WaitGroup
	Handler func(net.Conn)
	listen  func(string, string) (net.Listener, error)
}

func NewListener(addr string, h func(net.Conn)) *Listener {
	return &Listener{Addr: addr, stop: make(chan struct{}), Handler: h, listen: net.Listen}
}
func (s *Listener) Start() error {
	if s.ln != nil {
		return errors.New("listener already started")
	}
	ln, err := s.listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.wg.Add(1)
	go s.accept()
	return nil
}
func (s *Listener) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.stop:
				return
			default:
				continue
			}
		}
		if s.Handler != nil {
			go s.Handler(c)
		} else {
			go func() { _, _ = bufio.NewReader(c).ReadBytes('\n'); _ = c.Close() }()
		}
	}
}
func (s *Listener) Close() error {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	s.wg.Wait()
	return nil
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
