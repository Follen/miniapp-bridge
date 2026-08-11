package context

import (
	"errors"
	"fmt"
	"sync"
)

const (
	// DefaultMaxContexts bounds registries created through NewRegistry.
	DefaultMaxContexts = 256
	// MaxContextCapacity is the largest configurable registry capacity.
	MaxContextCapacity = 4096
)

var (
	ErrInvalidCapacity    = errors.New("invalid context registry capacity")
	ErrCapacityExceeded   = errors.New("context registry capacity exceeded")
	ErrGenerationMismatch = errors.New("context registry generation mismatch")
)

type Context struct {
	ID     string
	Target string
}

type Registry struct {
	mu               sync.RWMutex
	items            map[string]Context
	order            []string
	selected         string
	capacity         int
	generation       uint64
	generationActive bool
}

func NewRegistry() *Registry { return newRegistry(DefaultMaxContexts) }

// NewRegistryWithCapacity creates a registry with an explicit context limit.
func NewRegistryWithCapacity(capacity int) (*Registry, error) {
	if capacity < 1 || capacity > MaxContextCapacity {
		return nil, fmt.Errorf("%w: %d (must be between 1 and %d)", ErrInvalidCapacity, capacity, MaxContextCapacity)
	}
	return newRegistry(capacity), nil
}

func newRegistry(capacity int) *Registry {
	return &Registry{items: make(map[string]Context), capacity: capacity}
}

func (r *Registry) initializeLocked() {
	if r.items == nil {
		r.items = make(map[string]Context)
	}
	if r.capacity == 0 {
		r.capacity = DefaultMaxContexts
	}
}

func (r *Registry) Upsert(c Context) {
	_ = r.TryUpsert(c)
}

// TryUpsert inserts or updates a context. Updates remain permitted at
// capacity; only a new ID can return ErrCapacityExceeded.
func (r *Registry) TryUpsert(c Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.upsertLocked(c)
}

func (r *Registry) upsertLocked(c Context) error {
	r.initializeLocked()
	if _, exists := r.items[c.ID]; !exists {
		if len(r.items) >= r.capacity {
			return ErrCapacityExceeded
		}
		r.order = append(r.order, c.ID)
	}
	r.items[c.ID] = c
	if r.selected == "" {
		r.selected = c.ID
	}
	return nil
}

func (r *Registry) Remove(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.removeLocked(id)
}

func (r *Registry) removeLocked(id string) bool {
	if _, ok := r.items[id]; !ok {
		return false
	}
	delete(r.items, id)
	for i, candidate := range r.order {
		if candidate == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	if r.selected == id {
		r.selected = ""
		if len(r.order) > 0 {
			r.selected = r.order[0]
		}
	}
	return true
}

func (r *Registry) Select(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.selectLocked(id)
}

func (r *Registry) selectLocked(id string) bool {
	if _, ok := r.items[id]; !ok {
		return false
	}
	r.selected = id
	return true
}

func (r *Registry) Selected() (Context, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.items[r.selected]
	return c, ok
}
func (r *Registry) Get(id string) (Context, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.items[id]
	return c, ok
}
func (r *Registry) List() []Context {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Context, 0, len(r.items))
	for _, id := range r.order {
		if c, ok := r.items[id]; ok {
			out = append(out, c)
		}
	}
	return out
}

// Len returns the number of registered contexts.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// Capacity returns the maximum number of contexts accepted by this registry.
func (r *Registry) Capacity() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.capacity == 0 {
		return DefaultMaxContexts
	}
	return r.capacity
}

// Clear atomically removes all contexts and the current selection.
func (r *Registry) Clear() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clearLocked()
}

func (r *Registry) clearLocked() int {
	removed := len(r.items)
	r.items = make(map[string]Context)
	r.order = nil
	r.selected = ""
	return removed
}

// BeginGeneration clears state from any previous owner and activates
// generation. Calling it again for the active generation is idempotent.
func (r *Registry) BeginGeneration(generation uint64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.generationActive && r.generation == generation {
		return 0
	}
	removed := r.clearLocked()
	r.generation = generation
	r.generationActive = true
	return removed
}

// CurrentGeneration reports the active owner generation, if any.
func (r *Registry) CurrentGeneration() (uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.generation, r.generationActive
}

func (r *Registry) matchesGenerationLocked(generation uint64) bool {
	return r.generationActive && r.generation == generation
}

// UpsertForGeneration applies an upsert only to the active owner generation.
func (r *Registry) UpsertForGeneration(generation uint64, c Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.matchesGenerationLocked(generation) {
		return ErrGenerationMismatch
	}
	return r.upsertLocked(c)
}

// RemoveForGeneration removes a context only from the active owner generation.
func (r *Registry) RemoveForGeneration(generation uint64, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.matchesGenerationLocked(generation) {
		return false, ErrGenerationMismatch
	}
	return r.removeLocked(id), nil
}

// SelectForGeneration changes selection only for the active owner generation.
func (r *Registry) SelectForGeneration(generation uint64, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.matchesGenerationLocked(generation) {
		return false, ErrGenerationMismatch
	}
	return r.selectLocked(id), nil
}

// SelectedForGeneration snapshots selection only for the active owner generation.
func (r *Registry) SelectedForGeneration(generation uint64) (Context, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.matchesGenerationLocked(generation) {
		return Context{}, false, ErrGenerationMismatch
	}
	c, ok := r.items[r.selected]
	return c, ok, nil
}

// EndGeneration atomically clears and deactivates an owner generation. Once
// inactive, delayed operations from that generation are rejected.
func (r *Registry) EndGeneration(generation uint64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.matchesGenerationLocked(generation) {
		return 0, ErrGenerationMismatch
	}
	removed := r.clearLocked()
	r.generationActive = false
	return removed, nil
}

type Router struct{ Registry *Registry }

func (rt Router) Route(id string) (Context, bool) {
	if rt.Registry == nil {
		return Context{}, false
	}
	return rt.Registry.Get(id)
}
