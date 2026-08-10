//go:build windows && !frida

package frida

/*
// The dynamic loader is compiled only with the frida tag. The default Windows
// build deliberately keeps this package free of native handles.
*/
import "C"
