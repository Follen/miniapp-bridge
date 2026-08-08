//go:build windows && frida

package frida

/*
#cgo windows CFLAGS: -I${SRCDIR}/shim
#cgo windows LDFLAGS: -L${SRCDIR}/../../third_party/frida/runtime-17.3.2 -lminiapp-frida
#include "miniapp_frida.h"
#include <stdlib.h>

extern void goFridaMessage(uintptr_t handle, char *message, uint8_t *data, size_t size);
extern void goFridaDetached(uintptr_t handle, int reason);
static mb_session *mb_device_attach_go(mb_device *device, uint32_t pid, uintptr_t handle, char **error) {
  return mb_device_attach(device, pid, handle, goFridaDetached, error);
}
static mb_script *mb_session_load_script_go(mb_session *session, const char *source, uintptr_t handle, char **error) {
  return mb_session_load_script(session, source, handle, goFridaMessage, error);
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/cgo"
	"sync"
	"unsafe"

	"miniapp-bridge/internal/process"
)

type NativeDevice struct {
	ptr     *C.mb_device
	mu      sync.Mutex
	closed  bool
	handler func(Message)
}

type NativeSession struct {
	device *NativeDevice
	ptr    *C.mb_session
	h      cgo.Handle
	mu     sync.Mutex
	closed bool
}

type NativeScript struct {
	ptr    *C.mb_script
	mu     sync.Mutex
	closed bool
}

// nativeFailure is nil in production. Tests use it to exercise C failure
// cleanup paths that require an external Frida/device failure.
var nativeFailure func(string) error

var nativeDeviceOpen = func(errorOut **C.char) *C.mb_device {
	return C.mb_device_open(errorOut)
}

func nilNativeDeviceOpen(**C.char) *C.mb_device { return nil }

func forcedNativeFailure(operation string) error {
	if nativeFailure == nil {
		return nil
	}
	return nativeFailure(operation)
}

func nativeError(value *C.char) error {
	if value == nil {
		return errors.New("frida operation failed")
	}
	message := C.GoString(value)
	C.mb_error_free(value)
	return errors.New(message)
}

func NewNativeDevice() (*NativeDevice, error) {
	if err := forcedNativeFailure("open"); err != nil {
		return nil, fmt.Errorf("frida: local device: %w", err)
	}
	var cErr *C.char
	var ptr *C.mb_device
	if forcedNativeFailure("open-c") == nil {
		ptr = nativeDeviceOpen(&cErr)
	}
	if ptr == nil {
		return nil, fmt.Errorf("frida: local device: %w", nativeError(cErr))
	}
	return &NativeDevice{ptr: ptr}, nil
}

func (d *NativeDevice) Enumerate(ctx context.Context) ([]process.Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("frida: device closed")
	}
	var items *C.mb_process
	var count C.size_t
	var cErr *C.char
	if C.mb_device_enumerate(d.ptr, &items, &count, &cErr) == 0 || forcedNativeFailure("enumerate") != nil {
		C.mb_processes_free(items, count)
		return nil, fmt.Errorf("frida: enumerate: %w", nativeError(cErr))
	}
	defer C.mb_processes_free(items, count)
	native := unsafe.Slice(items, int(count))
	result := make([]process.Process, 0, len(native))
	for _, item := range native {
		path := C.GoString(item.path)
		result = append(result, process.Process{PID: uint32(item.pid), ParentPID: uint32(item.ppid), Name: C.GoString(item.name), Path: path, Version: process.ParseVersion(path)})
	}
	return result, nil
}

func (d *NativeDevice) Attach(pid uint32) (Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("frida: device closed")
	}
	ns := &NativeSession{device: d}
	ns.h = cgo.NewHandle(ns)
	var cErr *C.char
	ns.ptr = C.mb_device_attach_go(d.ptr, C.uint32_t(pid), C.uintptr_t(ns.h), &cErr)
	if ns.ptr == nil {
		ns.h.Delete()
		return nil, fmt.Errorf("frida: attach %d: %w", pid, nativeError(cErr))
	}
	return ns, nil
}

func (d *NativeDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	C.mb_device_close(d.ptr)
	d.ptr = nil
	return nil
}

func ShutdownRuntime() { C.mb_runtime_shutdown() }

func (d *NativeDevice) SetMessageHandler(handler func(Message)) {
	d.mu.Lock()
	d.handler = handler
	d.mu.Unlock()
}

func (s *NativeSession) LoadScript(source string) (Script, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("frida: session closed")
	}
	cs := C.CString(source)
	defer C.free(unsafe.Pointer(cs))
	var cErr *C.char
	ptr := C.mb_session_load_script_go(s.ptr, cs, C.uintptr_t(s.h), &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("frida: load script: %w", nativeError(cErr))
	}
	return &NativeScript{ptr: ptr}, nil
}

func (s *NativeSession) Detach() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var cErr *C.char
	ok := C.mb_session_detach(s.ptr, &cErr)
	s.ptr = nil
	s.h.Delete()
	if ok == 0 || forcedNativeFailure("detach") != nil {
		return fmt.Errorf("frida: detach: %w", nativeError(cErr))
	}
	return nil
}

func (s *NativeScript) Unload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var cErr *C.char
	ok := C.mb_script_unload(s.ptr, &cErr)
	s.ptr = nil
	if ok == 0 || forcedNativeFailure("unload") != nil {
		return fmt.Errorf("frida: unload: %w", nativeError(cErr))
	}
	return nil
}

func (s *NativeScript) Post(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("frida: script unloaded")
	}
	text := C.CString(string(payload))
	defer C.free(unsafe.Pointer(text))
	var cErr *C.char
	if C.mb_script_post(s.ptr, text, &cErr) == 0 || forcedNativeFailure("post") != nil {
		return fmt.Errorf("frida: post: %w", nativeError(cErr))
	}
	return nil
}

//export goFridaMessage
func goFridaMessage(handle C.uintptr_t, message *C.char, data *C.uint8_t, size C.size_t) {
	if handle == 0 || message == nil {
		return
	}
	s, ok := cgo.Handle(handle).Value().(*NativeSession)
	if !ok {
		return
	}
	m := Message{Type: C.GoString(message)}
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal([]byte(m.Type), &envelope) == nil && envelope.Type != "" {
		m.Type = envelope.Type
		if len(envelope.Payload) != 0 && string(envelope.Payload) != "null" {
			var text string
			if json.Unmarshal(envelope.Payload, &text) == nil {
				m.Payload = []byte(text)
			} else {
				m.Payload = append([]byte(nil), envelope.Payload...)
			}
		}
	}
	if size > 0 && data != nil {
		m.Data = C.GoBytes(unsafe.Pointer(data), C.int(size))
	}
	s.device.dispatch(m)
}

func invokeFridaMessageForTest(handle uintptr, message string, data []byte) {
	text := C.CString(message)
	defer C.free(unsafe.Pointer(text))
	var bytes unsafe.Pointer
	if len(data) != 0 {
		bytes = C.CBytes(data)
		defer C.free(bytes)
	}
	goFridaMessage(C.uintptr_t(handle), text, (*C.uint8_t)(bytes), C.size_t(len(data)))
}

//export goFridaDetached
func goFridaDetached(handle C.uintptr_t, reason C.int) {
	if handle == 0 {
		return
	}
	if s, ok := cgo.Handle(handle).Value().(*NativeSession); ok {
		s.device.dispatch(Message{Type: fmt.Sprintf("detached:%d", int(reason))})
	}
}

func invokeFridaDetachedForTest(handle uintptr, reason int) {
	goFridaDetached(C.uintptr_t(handle), C.int(reason))
}

func (d *NativeDevice) dispatch(m Message) {
	d.mu.Lock()
	handler := d.handler
	d.mu.Unlock()
	if handler != nil {
		handler(m)
	}
}
