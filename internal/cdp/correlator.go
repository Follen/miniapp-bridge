package cdp

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
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
	mu      sync.RWMutex
	pending map[string]Request
}

func NewCorrelator() *Correlator { return &Correlator{pending: make(map[string]Request)} }

var jsonNumberPattern = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)

func key(id any) string {
	value, _ := IDKey(id)
	return value
}

// IDKey returns a stable key for string and JSON-number request IDs. Numeric
// keys retain every decimal digit and normalize equivalent forms such as
// 1, 1.0, and 1e0 without converting through float64.
func IDKey(id any) (string, bool) {
	if value, ok := id.(string); ok {
		return "s:" + value, true
	}
	if value, ok := numericIDKey(id); ok {
		return "n:" + value, true
	}
	return "x:" + stringify(id), false
}

func numericIDKey(id any) (string, bool) {
	switch id.(type) {
	case json.Number, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
	default:
		return "", false
	}
	encoded, err := json.Marshal(id)
	if err != nil {
		return "", false
	}
	return canonicalJSONNumber(string(encoded))
}

func canonicalJSONNumber(encoded string) (string, bool) {
	match := jsonNumberPattern.FindStringSubmatch(encoded)
	if match == nil {
		return "", false
	}
	digits := strings.TrimLeft(match[2]+match[3], "0")
	if digits == "" {
		return "0e0", true
	}
	scale := new(big.Int)
	if match[4] != "" {
		// The JSON-number expression above guarantees a valid signed decimal.
		// SetString therefore cannot fail for this match.
		_, _ = scale.SetString(match[4], 10)
	}
	scale.Sub(scale, big.NewInt(int64(len(match[3]))))
	for strings.HasSuffix(digits, "0") {
		digits = strings.TrimSuffix(digits, "0")
		scale.Add(scale, big.NewInt(1))
	}
	return match[1] + digits + "e" + scale.String(), true
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

// Drain atomically removes and returns every pending request.
func (c *Correlator) Drain() []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	drained := make([]Request, 0, len(c.pending))
	for _, request := range c.pending {
		drained = append(drained, request)
	}
	c.pending = make(map[string]Request)
	return drained
}

// Clear atomically removes every pending request and returns the number removed.
func (c *Correlator) Clear() int { return len(c.Drain()) }

func (c *Correlator) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.pending)
}
