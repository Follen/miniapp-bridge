package context

import "testing"

func TestAuditRegistryAddConnectRemoveRouteStateMachine(t *testing.T) {
	r := NewRegistry()
	r.Upsert(Context{ID: "main", Target: "Main"})
	r.Upsert(Context{ID: "worker", Target: "Worker"})

	selected, ok := r.Selected()
	if !ok || selected.ID != "main" {
		t.Fatalf("first context was not selected: %+v ok=%v", selected, ok)
	}
	if !r.Select("worker") {
		t.Fatal("known context could not be selected")
	}
	r.Upsert(Context{ID: "main", Target: "Main updated"})
	selected, ok = r.Selected()
	if !ok || selected.ID != "worker" {
		t.Fatalf("updating another context changed selection: %+v ok=%v", selected, ok)
	}
	if r.Select("missing") {
		t.Fatal("unknown context was selected")
	}
	selected, ok = r.Selected()
	if !ok || selected.ID != "worker" {
		t.Fatalf("failed selection changed current context: %+v ok=%v", selected, ok)
	}
	if !r.Remove("worker") {
		t.Fatal("selected context was not removed")
	}
	selected, ok = r.Selected()
	if !ok || selected.ID != "main" || selected.Target != "Main updated" {
		t.Fatalf("remaining context was not selected: %+v ok=%v", selected, ok)
	}
	if r.Remove("missing") {
		t.Fatal("unknown context removal reported success")
	}
	if got, ok := (Router{Registry: r}).Route("main"); !ok || got != selected {
		t.Fatalf("route=%+v ok=%v want %+v", got, ok, selected)
	}
	if _, ok := (Router{}).Route("main"); ok {
		t.Fatal("nil registry routed a context")
	}
}

func TestAuditRegistryRemoveLastContextClearsSelection(t *testing.T) {
	r := NewRegistry()
	r.Upsert(Context{ID: "only"})
	if !r.Remove("only") {
		t.Fatal("remove failed")
	}
	if selected, ok := r.Selected(); ok {
		t.Fatalf("selection survived last removal: %+v", selected)
	}
}
