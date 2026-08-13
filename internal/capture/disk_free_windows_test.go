//go:build windows

package capture

import "testing"

func TestPlatformDiskFreeBytesErrors(t *testing.T) {
	if _, err := platformDiskFreeBytes("bad\x00path"); err == nil {
		t.Fatal("NUL path accepted")
	}
	if _, err := platformDiskFreeBytes(`Z:\this-volume-does-not-exist\`); err == nil {
		t.Fatal("missing volume accepted")
	}
}
