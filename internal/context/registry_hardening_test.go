package context

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestRegistryCapacityBoundary(t *testing.T) {
	for _, capacity := range []int{-1, 0, MaxContextCapacity + 1} {
		if registry, err := NewRegistryWithCapacity(capacity); registry != nil || !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("capacity %d: registry=%v err=%v", capacity, registry, err)
		}
	}

	r, err := NewRegistryWithCapacity(2)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Capacity(); got != 2 {
		t.Fatalf("capacity=%d", got)
	}
	if err := r.TryUpsert(Context{ID: "a", Target: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := r.TryUpsert(Context{ID: "b", Target: "two"}); err != nil {
		t.Fatal(err)
	}
	if err := r.TryUpsert(Context{ID: "c"}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("third insert error=%v", err)
	}
	if got := r.Len(); got != 2 {
		t.Fatalf("len=%d", got)
	}
	if err := r.TryUpsert(Context{ID: "b", Target: "updated"}); err != nil {
		t.Fatalf("update at capacity: %v", err)
	}
	if item, ok := r.Get("b"); !ok || item.Target != "updated" {
		t.Fatalf("updated item=%+v ok=%v", item, ok)
	}

	// The compatibility API remains bounded even though it cannot return an error.
	r.Upsert(Context{ID: "ignored"})
	if got := r.Len(); got != 2 {
		t.Fatalf("legacy upsert exceeded capacity: %d", got)
	}
	if !r.Remove("a") {
		t.Fatal("remove a")
	}
	if err := r.TryUpsert(Context{ID: "c"}); err != nil {
		t.Fatalf("insert after removal: %v", err)
	}
	items := r.List()
	if len(items) != 2 || items[0].ID != "b" || items[1].ID != "c" {
		t.Fatalf("items=%+v", items)
	}

	max, err := NewRegistryWithCapacity(MaxContextCapacity)
	if err != nil || max.Capacity() != MaxContextCapacity {
		t.Fatalf("maximum capacity registry=%v err=%v", max, err)
	}
}

func TestRegistryConcurrentCapacity(t *testing.T) {
	const capacity = 8
	r, err := NewRegistryWithCapacity(capacity)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = r.TryUpsert(Context{ID: fmt.Sprintf("context-%d", id)})
		}(i)
	}
	wg.Wait()
	if got := r.Len(); got != capacity {
		t.Fatalf("len=%d want=%d", got, capacity)
	}
}

func TestRegistryDefaultCapacityIsEnforced(t *testing.T) {
	r := NewRegistry()
	if got := r.Capacity(); got != DefaultMaxContexts {
		t.Fatalf("capacity=%d", got)
	}
	for i := 0; i < DefaultMaxContexts; i++ {
		if err := r.TryUpsert(Context{ID: fmt.Sprintf("default-%d", i)}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := r.TryUpsert(Context{ID: "one-too-many"}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("overflow error=%v", err)
	}
	if got := r.Len(); got != DefaultMaxContexts {
		t.Fatalf("len=%d", got)
	}
}

func TestRegistryClearAndZeroValue(t *testing.T) {
	var direct Registry
	if err := direct.TryUpsert(Context{ID: "direct"}); err != nil {
		t.Fatal(err)
	}
	if item, ok := direct.Get("direct"); !ok || item.ID != "direct" {
		t.Fatalf("direct zero-value item=%+v ok=%v", item, ok)
	}

	var zero Registry
	if got := zero.Capacity(); got != DefaultMaxContexts {
		t.Fatalf("zero-value capacity=%d", got)
	}
	if removed := zero.Clear(); removed != 0 {
		t.Fatalf("empty clear=%d", removed)
	}
	if err := zero.TryUpsert(Context{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	zero.Upsert(Context{ID: "b"})
	if !zero.Select("b") {
		t.Fatal("select b")
	}
	if removed := zero.Clear(); removed != 2 {
		t.Fatalf("clear=%d", removed)
	}
	if zero.Len() != 0 || len(zero.List()) != 0 {
		t.Fatalf("registry not empty: len=%d list=%+v", zero.Len(), zero.List())
	}
	if selected, ok := zero.Selected(); ok {
		t.Fatalf("selection survived clear: %+v", selected)
	}
	if _, ok := zero.Get("a"); ok {
		t.Fatal("item survived clear")
	}
	if got := zero.Capacity(); got != DefaultMaxContexts {
		t.Fatalf("capacity changed after clear: %d", got)
	}
	zero.Upsert(Context{ID: "fresh"})
	if selected, ok := zero.Selected(); !ok || selected.ID != "fresh" {
		t.Fatalf("selection after reuse=%+v ok=%v", selected, ok)
	}
}

func TestRegistryGenerationIsolation(t *testing.T) {
	r, err := NewRegistryWithCapacity(2)
	if err != nil {
		t.Fatal(err)
	}
	if generation, active := r.CurrentGeneration(); generation != 0 || active {
		t.Fatalf("initial generation=%d active=%v", generation, active)
	}
	if err := r.UpsertForGeneration(1, Context{ID: "stale"}); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("inactive upsert error=%v", err)
	}
	if _, err := r.RemoveForGeneration(1, "stale"); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("inactive remove error=%v", err)
	}
	if _, err := r.SelectForGeneration(1, "stale"); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("inactive select error=%v", err)
	}
	if _, _, err := r.SelectedForGeneration(1); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("inactive selected error=%v", err)
	}
	if _, err := r.EndGeneration(1); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("inactive end error=%v", err)
	}

	r.Upsert(Context{ID: "legacy"})
	if removed := r.BeginGeneration(7); removed != 1 {
		t.Fatalf("begin removed=%d", removed)
	}
	if generation, active := r.CurrentGeneration(); generation != 7 || !active {
		t.Fatalf("generation=%d active=%v", generation, active)
	}
	if err := r.UpsertForGeneration(7, Context{ID: "main", Target: "Main"}); err != nil {
		t.Fatal(err)
	}
	if removed := r.BeginGeneration(7); removed != 0 || r.Len() != 1 {
		t.Fatalf("idempotent begin removed=%d len=%d", removed, r.Len())
	}
	if err := r.UpsertForGeneration(6, Context{ID: "old"}); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("stale upsert error=%v", err)
	}
	if removed, err := r.RemoveForGeneration(6, "main"); removed || !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("stale remove removed=%v err=%v", removed, err)
	}
	if selected, err := r.SelectForGeneration(7, "missing"); selected || err != nil {
		t.Fatalf("missing select selected=%v err=%v", selected, err)
	}
	if selected, err := r.SelectForGeneration(7, "main"); !selected || err != nil {
		t.Fatalf("select main selected=%v err=%v", selected, err)
	}
	if selected, ok, err := r.SelectedForGeneration(7); err != nil || !ok || selected.ID != "main" {
		t.Fatalf("selected=%+v ok=%v err=%v", selected, ok, err)
	}
	if removed, err := r.RemoveForGeneration(7, "missing"); removed || err != nil {
		t.Fatalf("missing remove removed=%v err=%v", removed, err)
	}
	if err := r.UpsertForGeneration(7, Context{ID: "worker"}); err != nil {
		t.Fatal(err)
	}
	if err := r.UpsertForGeneration(7, Context{ID: "overflow"}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("generation capacity error=%v", err)
	}

	if removed := r.BeginGeneration(8); removed != 2 {
		t.Fatalf("generation switch removed=%d", removed)
	}
	if err := r.UpsertForGeneration(7, Context{ID: "late"}); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("old generation upsert error=%v", err)
	}
	if err := r.UpsertForGeneration(8, Context{ID: "next"}); err != nil {
		t.Fatal(err)
	}
	if removed, err := r.EndGeneration(7); removed != 0 || !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("stale end removed=%d err=%v", removed, err)
	}
	if removed, err := r.EndGeneration(8); removed != 1 || err != nil {
		t.Fatalf("end removed=%d err=%v", removed, err)
	}
	if _, active := r.CurrentGeneration(); active {
		t.Fatal("generation remained active")
	}
	if err := r.UpsertForGeneration(8, Context{ID: "delayed"}); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("delayed upsert error=%v", err)
	}
	if r.Len() != 0 {
		t.Fatalf("delayed operation polluted registry: %+v", r.List())
	}
}
