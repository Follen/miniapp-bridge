//go:build !windows || !frida

package sdk

// Unsupported builds retain the Go proxy so callers can use replay and a
// separately managed upstream. An explicit NativePath is rejected by Start.
func defaultNativeStarter(string, string) NativeStarter { return nil }
