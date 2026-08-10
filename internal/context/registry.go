package context

import "sync"

type Context struct {
	ID     string
	Target string
}

type Registry struct {
	mu       sync.RWMutex
	items    map[string]Context
	order    []string
	selected string
}

func NewRegistry() *Registry { return &Registry{items: make(map[string]Context)} }

func (r *Registry) Upsert(c Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[c.ID]; !exists {
		r.order = append(r.order, c.ID)
	}
	r.items[c.ID] = c
	if r.selected == "" {
		r.selected = c.ID
	}
}
func (r *Registry) Remove(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
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

type Router struct{ Registry *Registry }

func (rt Router) Route(id string) (Context, bool) {
	if rt.Registry == nil {
		return Context{}, false
	}
	return rt.Registry.Get(id)
}
