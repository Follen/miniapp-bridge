package cdp

import "testing"

func TestCorrelator(t *testing.T) {
	c := NewCorrelator()
	c.Add(Request{ID: 1, Method: "Runtime.enable"})
	if c.Len() != 1 {
		t.Fatal()
	}
	r, err := c.Resolve(Response{ID: 1})
	if err != nil || r.Method != "Runtime.enable" {
		t.Fatalf("%v %#v", err, r)
	}
	if _, err := c.Resolve(Response{ID: 1}); err != ErrUnknownRequest {
		t.Fatal(err)
	}
}
