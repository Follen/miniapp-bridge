package cdp

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxPending bounds pending CDP requests when no custom limit is
	// configured. A non-positive CorrelatorOptions.MaxPending uses this value.
	DefaultMaxPending = 4096
	// MaxPendingCapacity prevents a custom option from making the correlator
	// an unbounded memory sink. Values outside [1, MaxPendingCapacity] use the
	// safe default in NewCorrelatorWithOptions.
	MaxPendingCapacity = 65536
	// DefaultPendingTTL bounds how long an unanswered CDP request is retained.
	// A non-positive CorrelatorOptions.PendingTTL uses this value.
	DefaultPendingTTL = 5 * time.Minute
)

var (
	ErrUnknownRequest = errors.New("unknown CDP request")
	ErrPendingLimit   = errors.New("cdp pending request limit reached")
)

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

// CorrelatorOptions configures resource bounds. Zero and negative limits use
// the exported safe defaults. Now defaults to time.Now and primarily enables
// deterministic callers and tests without a cleanup goroutine.
type CorrelatorOptions struct {
	MaxPending int
	PendingTTL time.Duration
	Now        func() time.Time
}

type ownerGeneration struct {
	owner      string
	generation uint64
}

type pendingKey struct {
	ownerGeneration
	id string
}

type pendingRequest struct {
	request   Request
	expiresAt time.Time
}

type Correlator struct {
	mu         sync.Mutex
	pending    map[pendingKey]pendingRequest
	maxPending int
	pendingTTL time.Duration
	now        func() time.Time
}

func NewCorrelator() *Correlator { return NewCorrelatorWithOptions(CorrelatorOptions{}) }

func NewCorrelatorWithOptions(options CorrelatorOptions) *Correlator {
	if options.MaxPending <= 0 || options.MaxPending > MaxPendingCapacity {
		options.MaxPending = DefaultMaxPending
	}
	if options.PendingTTL <= 0 {
		options.PendingTTL = DefaultPendingTTL
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Correlator{
		pending:    make(map[pendingKey]pendingRequest),
		maxPending: options.MaxPending,
		pendingTTL: options.PendingTTL,
		now:        options.Now,
	}
}

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

// MaxPending returns the configured pending request capacity.
func (c *Correlator) MaxPending() int { return c.maxPending }

// PendingTTL returns the configured pending request lifetime.
func (c *Correlator) PendingTTL() time.Duration { return c.pendingTTL }

// Add preserves the original no-result API. New call sites should use TryAdd
// so a capacity rejection can be returned to the requester.
func (c *Correlator) Add(request Request) { _ = c.TryAdd(request) }

// TryAdd adds a request in the legacy, unscoped owner generation.
func (c *Correlator) TryAdd(request Request) error {
	return c.TryAddFor("", 0, request)
}

// TryAddFor adds a request owned by an exact connection generation. Reusing
// the same request ID in that generation replaces and refreshes only that
// entry; other owners and generations remain isolated.
func (c *Correlator) TryAddFor(owner string, generation uint64, request Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.pruneExpiredLocked(now)
	pendingKey := scopedKey(owner, generation, request.ID)
	if _, exists := c.pending[pendingKey]; !exists && len(c.pending) >= c.maxPending {
		return ErrPendingLimit
	}
	c.pending[pendingKey] = pendingRequest{
		request:   request,
		expiresAt: now.Add(c.pendingTTL),
	}
	return nil
}

func (c *Correlator) Resolve(response Response) (Request, error) {
	return c.ResolveFor("", 0, response)
}

// ResolveFor resolves and removes a request only from the exact owner
// generation supplied by the caller.
func (c *Correlator) ResolveFor(owner string, generation uint64, response Response) (Request, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneExpiredLocked(c.now())
	pendingKey := scopedKey(owner, generation, response.ID)
	pending, ok := c.pending[pendingKey]
	if !ok {
		return Request{}, ErrUnknownRequest
	}
	delete(c.pending, pendingKey)
	return pending.request, nil
}

func (c *Correlator) Cancel(id any) bool {
	return c.CancelFor("", 0, id)
}

// CancelFor cancels a request only from the exact owner generation supplied.
func (c *Correlator) CancelFor(owner string, generation uint64, id any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneExpiredLocked(c.now())
	pendingKey := scopedKey(owner, generation, id)
	if _, ok := c.pending[pendingKey]; ok {
		delete(c.pending, pendingKey)
		return true
	}
	return false
}

// Drain atomically removes and returns every pending request.
func (c *Correlator) Drain() []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneExpiredLocked(c.now())
	drained := make([]Request, 0, len(c.pending))
	for _, pending := range c.pending {
		drained = append(drained, pending.request)
	}
	c.pending = make(map[pendingKey]pendingRequest)
	return drained
}

// DrainFor atomically removes and returns pending requests from one exact
// owner generation, leaving all other generations intact.
func (c *Correlator) DrainFor(owner string, generation uint64) []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneExpiredLocked(c.now())
	scope := ownerGeneration{owner: owner, generation: generation}
	drained := make([]Request, 0)
	for pendingKey, pending := range c.pending {
		if pendingKey.ownerGeneration == scope {
			drained = append(drained, pending.request)
			delete(c.pending, pendingKey)
		}
	}
	return drained
}

// Clear atomically removes every pending request and returns the number removed.
func (c *Correlator) Clear() int { return len(c.Drain()) }

func (c *Correlator) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneExpiredLocked(c.now())
	return len(c.pending)
}

// LenFor returns the live pending count for one exact owner generation.
func (c *Correlator) LenFor(owner string, generation uint64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneExpiredLocked(c.now())
	scope := ownerGeneration{owner: owner, generation: generation}
	count := 0
	for pendingKey := range c.pending {
		if pendingKey.ownerGeneration == scope {
			count++
		}
	}
	return count
}

// PruneExpired removes requests whose TTL deadline has been reached and
// returns the number removed.
func (c *Correlator) PruneExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pruneExpiredLocked(c.now())
}

func scopedKey(owner string, generation uint64, id any) pendingKey {
	return pendingKey{
		ownerGeneration: ownerGeneration{owner: owner, generation: generation},
		id:              key(id),
	}
}

func (c *Correlator) pruneExpiredLocked(now time.Time) int {
	pruned := 0
	for pendingKey, pending := range c.pending {
		if !pending.expiresAt.After(now) {
			delete(c.pending, pendingKey)
			pruned++
		}
	}
	return pruned
}
