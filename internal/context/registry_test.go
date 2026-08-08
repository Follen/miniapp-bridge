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
