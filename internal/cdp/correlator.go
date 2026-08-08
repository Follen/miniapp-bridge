package cdp

import (
	"errors"
	"fmt"
	"sync"
)

var ErrUnknownRequest = errors.New("unknown CDP request")

type Request struct {
	ID     any
	Method string
	Params map[string]any
}

type Response struct {
	ID     any
	Result map[string]any
	Error  *Error
}

type Error struct {
	Code    int
	Message string
	Data    any
}

type Correlator struct {
	mu      sync.Mutex
	pending map[string]Request
}

func NewCorrelator() *Correlator { return &Correlator{pending: make(map[string]Request)} }

func key(id any) string {
	switch v := id.(type) {
	case string:
		return "s:" + v
	case float64:
		return "n:" + stringify(v)
	case int:
		return "n:" + stringify(v)
	case int64:
		return "n:" + stringify(v)
	default:
		return "x:" + stringify(v)
	}
}
func stringify(v any) string { return fmt.Sprintf("%v", v) }

func (c *Correlator) Add(r Request) { c.mu.Lock(); c.pending[key(r.ID)] = r; c.mu.Unlock() }

func (c *Correlator) Resolve(r Response) (Request, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := key(r.ID)
	req, ok := c.pending[k]
	if !ok {
		return Request{}, ErrUnknownRequest
	}
	delete(c.pending, k)
	return req, nil
}

func (c *Correlator) Cancel(id any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := key(id)
	if _, ok := c.pending[k]; ok {
		delete(c.pending, k)
		return true
	}
	return false
}
func (c *Correlator) Len() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.pending) }
