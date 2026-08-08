package context

import "testing"

func TestCoverageRegistryListEmptyAndValues(t *testing.T) {
	r := NewRegistry()
	if got := r.List(); len(got) != 0 {
		t.Fatalf("empty list=%v", got)
	}
	r.Upsert(Context{ID: "a", Target: "A"})
	r.Upsert(Context{ID: "b", Target: "B"})
	got := r.List()
	if len(got) != 2 {
		t.Fatalf("list=%v", got)
	}
}
