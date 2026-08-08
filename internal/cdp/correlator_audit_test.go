package cdp

import (
	"errors"
	"sync"
	"testing"
)

func TestAuditCorrelatorIDDomainsAndLifecycle(t *testing.T) {
	c := NewCorrelator()
	c.Add(Request{ID: 7, Method: "Runtime.enable"})
	c.Add(Request{ID: "7", Method: "Debugger.enable"})

	numeric, err := c.Resolve(Response{ID: float64(7)})
	if err != nil || numeric.Method != "Runtime.enable" {
		t.Fatalf("numeric JSON ID did not correlate with integer request: request=%+v err=%v", numeric, err)
	}
	if !c.Cancel("7") || c.Cancel("7") {
		t.Fatal("string ID must occupy a separate domain and cancel exactly once")
	}
	if c.Len() != 0 {
		t.Fatalf("pending=%d want 0", c.Len())
	}
	if _, err := c.Resolve(Response{ID: 7}); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("resolved request was not removed: %v", err)
	}
}

func TestAuditCorrelatorConcurrentRequestsResolveExactlyOnce(t *testing.T) {
	const count = 128
	c := NewCorrelator()
	var group sync.WaitGroup
	for i := 0; i < count; i++ {
		group.Add(1)
		go func(id int) {
			defer group.Done()
			c.Add(Request{ID: id, Method: "Runtime.evaluate"})
		}(i)
	}
	group.Wait()
	if c.Len() != count {
		t.Fatalf("pending=%d want %d", c.Len(), count)
	}

	for i := 0; i < count; i++ {
		group.Add(1)
		go func(id int) {
			defer group.Done()
			request, err := c.Resolve(Response{ID: float64(id)})
			if err != nil || request.Method != "Runtime.evaluate" {
				t.Errorf("resolve %d: request=%+v err=%v", id, request, err)
			}
		}(i)
	}
	group.Wait()
	if c.Len() != 0 {
		t.Fatalf("pending=%d want 0", c.Len())
	}
}
