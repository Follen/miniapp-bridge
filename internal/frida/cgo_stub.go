//go:build frida && !windows

package frida

// frida-core 17.3.2 is currently packaged for Windows. Platform-specific
// implementations stay behind Device, Session, and Script.
