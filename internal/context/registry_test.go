package context

import "testing"

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	r.Upsert(Context{ID: "a"})
	r.Upsert(Context{ID: "b"})
	if c, _ := r.Selected(); c.ID != "a" {
		t.Fatal(c)
	}
	if !r.Select("b") {
		t.Fatal()
	}
	r.Remove("b")
	if c, _ := r.Selected(); c.ID != "a" {
		t.Fatal(c)
	}
}

func TestRegistryPreservesInsertionOrderAndDeterministicFallback(t *testing.T) {
	r := NewRegistry()
	r.Upsert(Context{ID: "first", Target: "one"})
	r.Upsert(Context{ID: "second", Target: "two"})
	r.Upsert(Context{ID: "third", Target: "three"})
	r.Upsert(Context{ID: "second", Target: "two-updated"})

	items := r.List()
	if len(items) != 3 || items[0].ID != "first" || items[1].ID != "second" || items[1].Target != "two-updated" || items[2].ID != "third" {
		t.Fatalf("ordered list=%+v", items)
	}
	if !r.Select("third") || !r.Remove("third") {
		t.Fatal("unable to select/remove third")
	}
	if selected, ok := r.Selected(); !ok || selected.ID != "first" {
		t.Fatalf("fallback selected=%+v ok=%v", selected, ok)
	}
	if !r.Remove("first") {
		t.Fatal("unable to remove first")
	}
	if selected, ok := r.Selected(); !ok || selected.ID != "second" {
		t.Fatalf("second fallback=%+v ok=%v", selected, ok)
	}
}
