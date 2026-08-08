package version

import "testing"

func TestOffsetAndSelect(t *testing.T) {
	c := map[int]AddressConfig{100: {Version: 100, SceneOffsets: []int{1}}, 200: {Version: 200, SceneOffsets: []int{2}}}
	if v, _ := Select(c, 200); v.Version != 200 {
		t.Fatal(v.Version)
	}
	if _, err := Select(c, 150); err == nil {
		t.Fatal("expected exact-version error")
	}
	if n, _ := (AddressConfig{}).Offset("0x10"); n != 16 {
		t.Fatal(n)
	}
}
