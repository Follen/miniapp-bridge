//go:build windows && frida

package frida

/*
#cgo windows CFLAGS: -I${SRCDIR} -I${SRCDIR}/shim
#include "loader_windows.h"
#include "loader_windows.inc"
*/
import "C"
