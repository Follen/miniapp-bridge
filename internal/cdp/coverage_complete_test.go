package cdp

import "testing"

func TestCoverageCorrelatorUnknownIDTypes(t *testing.T) {
	c := NewCorrelator()
	for _, id := range []any{int64(7), true, []byte("x"), struct{ N int }{1}, nil} {
		c.Add(Request{ID: id, Method: "x"})
		if _, err := c.Resolve(Response{ID: id}); err != nil {
			t.Fatalf("id %#v: %v", id, err)
		}
	}
}
