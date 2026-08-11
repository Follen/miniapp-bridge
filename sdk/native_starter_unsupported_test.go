//go:build !windows || !frida

package sdk

import "testing"

func TestDefaultNativeStarterUnsupportedBuild(t *testing.T) {
	if starter := defaultNativeStarter("", ""); starter != nil {
		t.Fatal("unsupported build selected a native starter")
	}
}
