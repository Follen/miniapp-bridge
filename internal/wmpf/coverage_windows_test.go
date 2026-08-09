//go:build windows && frida

package wmpf

import "testing"

func TestCoverageWindowsZlibFailure(t *testing.T) {
	_, err := zlibDecompress(nil)
	requireError(t, err)
}
