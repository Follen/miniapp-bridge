package cdp

import (
	"errors"
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(duration time.Duration) { c.now = c.now.Add(duration) }

func TestCorrelatorOptionsUseSafeDefaults(t *testing.T) {
	for name, correlator := range map[string]*Correlator{
		"legacy constructor": NewCorrelator(),
		"zero options":       NewCorrelatorWithOptions(CorrelatorOptions{}),
		"invalid options": NewCorrelatorWithOptions(CorrelatorOptions{
			MaxPending: -1,
			PendingTTL: -time.Second,
		}),
		"oversized options": NewCorrelatorWithOptions(CorrelatorOptions{
			MaxPending: MaxPendingCapacity + 1,
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if correlator.MaxPending() != DefaultMaxPending {
				t.Fatalf("MaxPending()=%d want %d", correlator.MaxPending(), DefaultMaxPending)
			}
			if correlator.PendingTTL() != DefaultPendingTTL {
				t.Fatalf("PendingTTL()=%v want %v", correlator.PendingTTL(), DefaultPendingTTL)
			}
		})
	}
}

func TestCorrelatorCapacityRejectsNewEntriesWithoutDisplacingPending(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	correlator := NewCorrelatorWithOptions(CorrelatorOptions{
		MaxPending: 2,
		PendingTTL: time.Minute,
		Now:        clock.Now,
	})
	if correlator.MaxPending() != 2 || correlator.PendingTTL() != time.Minute {
		t.Fatalf("custom options: max=%d ttl=%v", correlator.MaxPending(), correlator.PendingTTL())
	}

	if err := correlator.TryAdd(Request{ID: 1, Method: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := correlator.TryAdd(Request{ID: 2, Method: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := correlator.TryAdd(Request{ID: 3, Method: "rejected"}); !errors.Is(err, ErrPendingLimit) {
		t.Fatalf("TryAdd over limit error=%v", err)
	}

	// The legacy Add method remains source compatible. At the safety limit it
	// preserves existing requests instead of evicting one unpredictably.
	correlator.Add(Request{ID: 4, Method: "legacy-rejected"})
	if correlator.Len() != 2 {
		t.Fatalf("pending=%d want 2", correlator.Len())
	}
	for id, method := range map[int]string{1: "first", 2: "second"} {
		request, err := correlator.Resolve(Response{ID: id})
		if err != nil || request.Method != method {
			t.Fatalf("Resolve(%d): request=%+v err=%v", id, request, err)
		}
	}
	for _, id := range []int{3, 4} {
		if _, err := correlator.Resolve(Response{ID: id}); !errors.Is(err, ErrUnknownRequest) {
			t.Fatalf("Resolve(%d) error=%v", id, err)
		}
	}
}

func TestCorrelatorReplacementRefreshesTTLAtCapacity(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	correlator := NewCorrelatorWithOptions(CorrelatorOptions{
		MaxPending: 1,
		PendingTTL: time.Minute,
		Now:        clock.Now,
	})
	if err := correlator.TryAdd(Request{ID: "same", Method: "old"}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(59 * time.Second)
	if err := correlator.TryAdd(Request{ID: "same", Method: "new"}); err != nil {
		t.Fatalf("replacement at limit: %v", err)
	}
	clock.Advance(2 * time.Second)
	request, err := correlator.Resolve(Response{ID: "same"})
	if err != nil || request.Method != "new" {
		t.Fatalf("replacement did not refresh TTL: request=%+v err=%v", request, err)
	}
}

func TestCorrelatorTTLBoundaryAndDeterministicPruning(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	correlator := NewCorrelatorWithOptions(CorrelatorOptions{
		MaxPending: 2,
		PendingTTL: time.Minute,
		Now:        clock.Now,
	})
	if err := correlator.TryAdd(Request{ID: "expires", Method: "old"}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute - time.Nanosecond)
	if correlator.Len() != 1 {
		t.Fatalf("request expired before TTL boundary: pending=%d", correlator.Len())
	}
	clock.Advance(time.Nanosecond)
	if correlator.Len() != 0 {
		t.Fatalf("request remained at TTL boundary: pending=%d", correlator.Len())
	}
	if _, err := correlator.Resolve(Response{ID: "expires"}); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("expired request resolved: %v", err)
	}

	if err := correlator.TryAdd(Request{ID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := correlator.TryAdd(Request{ID: 2}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if err := correlator.TryAdd(Request{ID: 3}); err != nil {
		t.Fatalf("expired entries were not pruned before capacity check: %v", err)
	}
	if correlator.Cancel(1) {
		t.Fatal("Cancel reported an expired request")
	}
	if expired := correlator.PruneExpired(); expired != 0 {
		t.Fatalf("PruneExpired()=%d after normal-operation pruning", expired)
	}
	if correlator.Len() != 1 {
		t.Fatalf("pending=%d want 1", correlator.Len())
	}
	clock.Advance(time.Minute)
	if expired := correlator.PruneExpired(); expired != 1 {
		t.Fatalf("PruneExpired()=%d want 1", expired)
	}
}

func TestCorrelatorOwnerGenerationIsolationAndDrain(t *testing.T) {
	correlator := NewCorrelatorWithOptions(CorrelatorOptions{
		MaxPending: 8,
		PendingTTL: time.Minute,
	})
	for _, test := range []struct {
		owner      string
		generation uint64
		method     string
	}{
		{owner: "controller", generation: 1, method: "generation-one"},
		{owner: "controller", generation: 2, method: "generation-two"},
		{owner: "upstream", generation: 1, method: "other-owner"},
	} {
		if err := correlator.TryAddFor(test.owner, test.generation, Request{ID: 7, Method: test.method}); err != nil {
			t.Fatalf("TryAddFor(%q, %d): %v", test.owner, test.generation, err)
		}
	}
	correlator.Add(Request{ID: 7, Method: "legacy-scope"})

	if _, err := correlator.ResolveFor("controller", 3, Response{ID: 7}); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("wrong generation error=%v", err)
	}
	if _, err := correlator.ResolveFor("unknown", 1, Response{ID: 7}); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("wrong owner error=%v", err)
	}
	if correlator.Len() != 4 || correlator.LenFor("controller", 1) != 1 {
		t.Fatalf("pending=%d generation-one=%d", correlator.Len(), correlator.LenFor("controller", 1))
	}

	drained := correlator.DrainFor("controller", 1)
	if len(drained) != 1 || drained[0].Method != "generation-one" {
		t.Fatalf("DrainFor returned %+v", drained)
	}
	if correlator.Len() != 3 || correlator.LenFor("controller", 1) != 0 {
		t.Fatalf("pending=%d generation-one=%d", correlator.Len(), correlator.LenFor("controller", 1))
	}
	if correlator.CancelFor("controller", 1, 7) {
		t.Fatal("CancelFor canceled a drained generation")
	}
	if !correlator.CancelFor("upstream", 1, 7) {
		t.Fatal("CancelFor did not cancel the exact owner generation")
	}

	request, err := correlator.ResolveFor("controller", 2, Response{ID: 7})
	if err != nil || request.Method != "generation-two" {
		t.Fatalf("ResolveFor generation two: request=%+v err=%v", request, err)
	}
	legacy, err := correlator.Resolve(Response{ID: 7})
	if err != nil || legacy.Method != "legacy-scope" {
		t.Fatalf("Resolve legacy scope: request=%+v err=%v", legacy, err)
	}
}

func TestCorrelatorScopedOperationsPruneExpiredEntries(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	correlator := NewCorrelatorWithOptions(CorrelatorOptions{
		MaxPending: 2,
		PendingTTL: time.Second,
		Now:        clock.Now,
	})
	if err := correlator.TryAddFor("owner", 1, Request{ID: 1}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if got := correlator.LenFor("owner", 1); got != 0 {
		t.Fatalf("LenFor()=%d want 0", got)
	}
	if drained := correlator.DrainFor("owner", 1); len(drained) != 0 {
		t.Fatalf("DrainFor returned expired requests: %+v", drained)
	}
	if err := correlator.TryAddFor("owner", 2, Request{ID: 1}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if _, err := correlator.ResolveFor("owner", 2, Response{ID: 1}); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("ResolveFor expired request error=%v", err)
	}
}
