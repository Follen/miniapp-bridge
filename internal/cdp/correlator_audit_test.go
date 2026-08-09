package cdp

import (
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"
)

func TestCorrelatorPreservesAndNormalizesExactNumericIDs(t *testing.T) {
	c := NewCorrelator()
	c.Add(Request{ID: json.Number("9007199254740993"), Method: "Runtime.enable"})
	if !c.Cancel(uint64(9007199254740993)) || c.Len() != 0 {
		t.Fatalf("exact numeric cancellation failed: pending=%d", c.Len())
	}

	c.Add(Request{ID: uint64(math.MaxUint64), Method: "Debugger.enable"})
	request, err := c.Resolve(Response{ID: json.Number("18446744073709551615")})
	if err != nil || request.Method != "Debugger.enable" {
		t.Fatalf("max uint64 correlation: request=%+v err=%v", request, err)
	}

	c.Add(Request{ID: json.Number("1.0"), Method: "Runtime.evaluate"})
	request, err = c.Resolve(Response{ID: json.Number("1e0")})
	if err != nil || request.Method != "Runtime.evaluate" {
		t.Fatalf("equivalent numeric correlation: request=%+v err=%v", request, err)
	}
}

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

func TestCorrelatorDrainAndClearAreAtomic(t *testing.T) {
	c := NewCorrelator()
	for id := 0; id < 64; id++ {
		c.Add(Request{ID: id, Method: "Runtime.evaluate"})
	}
	drained := c.Drain()
	if len(drained) != 64 || c.Len() != 0 {
		t.Fatalf("drained=%d pending=%d", len(drained), c.Len())
	}
	for _, request := range drained {
		if _, err := c.Resolve(Response{ID: request.ID}); !errors.Is(err, ErrUnknownRequest) {
			t.Fatalf("drained request %v still resolved: %v", request.ID, err)
		}
	}

	c.Add(Request{ID: "one"})
	c.Add(Request{ID: "two"})
	if cleared := c.Clear(); cleared != 2 || c.Len() != 0 {
		t.Fatalf("cleared=%d pending=%d", cleared, c.Len())
	}
	if cleared := c.Clear(); cleared != 0 {
		t.Fatalf("second clear=%d want 0", cleared)
	}
}

func TestCorrelatorConcurrentCancelAndDrain(t *testing.T) {
	const count = 256
	c := NewCorrelator()
	for id := 0; id < count; id++ {
		c.Add(Request{ID: id})
	}
	start := make(chan struct{})
	var group sync.WaitGroup
	for id := 0; id < count; id++ {
		group.Add(1)
		go func(id int) {
			defer group.Done()
			<-start
			c.Cancel(id)
		}(id)
	}
	close(start)
	drained := c.Drain()
	group.Wait()
	if c.Len() != 0 {
		t.Fatalf("pending=%d want 0", c.Len())
	}
	if len(drained) > count {
		t.Fatalf("drained=%d exceeds %d", len(drained), count)
	}
}

func TestNumericIDKeyDefensiveBranches(t *testing.T) {
	if _, ok := IDKey(math.NaN()); ok {
		t.Fatal("NaN must be rejected as a request ID")
	}
	if got, ok := canonicalJSONNumber("not-a-json-number"); ok || got != "" {
		t.Fatalf("malformed numeric text accepted: %q, %v", got, ok)
	}
	if got, ok := canonicalJSONNumber("1e+"); ok || got != "" {
		t.Fatalf("incomplete exponent accepted: %q, %v", got, ok)
	}
}
