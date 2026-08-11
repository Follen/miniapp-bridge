//go:build windows && frida

package frida

/*
#cgo windows CFLAGS: -I${SRCDIR} -I${SRCDIR}/shim
#include "loader_windows.h"
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

// The runtime asset is the pinned frida-core 17.3.2 build formerly stored in
// runtime-17.3.2; it is loaded by the opaque Windows loader at runtime.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/cgo"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/Follen/miniapp-bridge/internal/process"
)

type NativeLoadCode int

const (
	NativeLoadOK NativeLoadCode = iota
	NativeLoadFailure
	NativeLoadConflict
	NativeLoadExportMissing
	NativeLoadVersionMismatch
	NativeLoadABIMismatch
)

// NativeLoadError preserves the loader's stable failure class and detailed
// Win32/export/version message without exposing a native handle.
type NativeLoadError struct {
	Code NativeLoadCode
	Err  error
}

func (e *NativeLoadError) Error() string { return e.Err.Error() }
func (e *NativeLoadError) Unwrap() error { return e.Err }

type NativeDevice struct {
	ptr                    *C.mb_device
	mu                     sync.Mutex
	closing                bool
	closed                 bool
	handler                func(Message)
	messageQueue           chan Message
	messageQueueBytes      uint64
	controlQueue           []Message
	controlQueueBytes      uint64
	droppedMessages        uint64
	droppedControlMessages uint64
	controlWake            chan struct{}
	messageStop            chan struct{}
	messageDone            chan struct{}
	closeDone              chan struct{}
	sessions               map[*NativeSession]struct{}
}

const (
	maxNativeMessageQueue      = 256
	maxNativeMessageQueueBytes = 8 << 20
	maxNativeControlQueue      = 64
	maxNativeControlQueueBytes = 1 << 20
)

type NativeSession struct {
	device   *NativeDevice
	ptr      *C.mb_session
	h        cgo.Handle
	mu       sync.Mutex
	closed   bool
	scripts  map[*NativeScript]struct{}
	terminal atomic.Bool
}

type NativeScript struct {
	session *NativeSession
	ptr     *C.mb_script
	closed  bool
}

// nativeFailure is nil in production. Tests use it to exercise C failure
// cleanup paths that require an external Frida/device failure.
var nativeFailure func(string) error

var nativeDeviceOpen = func(errorOut **C.char) *C.mb_device {
	return C.mb_device_open(errorOut)
}

var nativeRuntimePath = func() (string, error) {
	if value := os.Getenv("MINIAPP_BRIDGE_NATIVE_PATH"); value != "" {
		return filepath.Abs(value)
	}
	exe, err := nativeExecutable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "miniapp-frida.dll"), nil
}

var nativeExecutable = os.Executable

const maxNativeZlibOutput = 256 << 20

func loadNativeRuntime() error {
	path, err := nativeRuntimePath()
	if err != nil {
		return fmt.Errorf("frida: native runtime path: %w", err)
	}
	wide, err := syscall.UTF16FromString(path)
	if err != nil {
		return fmt.Errorf("frida: native runtime path: %w", err)
	}
	var cErr *C.char
	var loadCode C.int
	if C.mb_native_load((*C.wchar_t)(unsafe.Pointer(&wide[0])), &cErr, &loadCode) == 0 {
		return fmt.Errorf("frida: native runtime: %w", &NativeLoadError{Code: NativeLoadCode(loadCode), Err: nativeError(cErr)})
	}
	return nil
}

func retainNativeRuntime() (func(), error) {
	if C.mb_native_retain_loaded() != 0 {
		return func() { C.mb_native_release() }, nil
	}
	if err := loadNativeRuntime(); err != nil {
		return nil, err
	}
	return func() { C.mb_native_release() }, nil
}

func nativeZlibInput(data []byte) (*C.uint8_t, func()) {
	if len(data) == 0 {
		return nil, func() {}
	}
	value := C.CBytes(data)
	return (*C.uint8_t)(value), func() { C.free(value) }
}

// ZlibCompress uses the pinned zlib implementation exported by the loaded
// native runtime. The returned bytes are copied before the DLL is released.
func ZlibCompress(data []byte) ([]byte, error) {
	if len(data) > maxNativeZlibOutput {
		return nil, fmt.Errorf("frida: zlib input exceeds %d bytes", maxNativeZlibOutput)
	}
	release, err := retainNativeRuntime()
	if err != nil {
		return nil, err
	}
	defer release()
	input, freeInput := nativeZlibInput(data)
	defer freeInput()
	var output *C.uint8_t
	var outputSize C.size_t
	var cErr *C.char
	if C.mb_zlib_compress(input, C.size_t(len(data)), &output, &outputSize, &cErr) == 0 {
		return nil, fmt.Errorf("frida: zlib compress: %w", nativeError(cErr))
	}
	defer C.mb_bytes_free(output)
	return nativeZlibOutputBytes(unsafe.Pointer(output), uint64(outputSize), maxNativeZlibOutput)
}

// ZlibDecompress validates the caller's expected size when non-zero and
// always enforces a bounded allocation for corrupt or hostile frames.
func ZlibDecompress(data []byte, expectedSize int) ([]byte, error) {
	return ZlibDecompressWithLimit(data, expectedSize, maxNativeZlibOutput)
}

// ZlibDecompressWithLimit is the bounded variant used by protocol layers that
// have a tighter per-message budget than the native runtime ceiling. The
// limit is passed through to the C shim before any native allocation occurs.
func ZlibDecompressWithLimit(data []byte, expectedSize, maxOutput int) ([]byte, error) {
	if maxOutput <= 0 || maxOutput > maxNativeZlibOutput || len(data) > maxOutput || expectedSize < 0 || expectedSize > maxOutput {
		return nil, fmt.Errorf("frida: zlib input or expected output exceeds %d bytes", maxOutput)
	}
	release, err := retainNativeRuntime()
	if err != nil {
		return nil, err
	}
	defer release()
	input, freeInput := nativeZlibInput(data)
	defer freeInput()
	var output *C.uint8_t
	var outputSize C.size_t
	var cErr *C.char
	if C.mb_zlib_decompress(input, C.size_t(len(data)), C.size_t(expectedSize), C.size_t(maxOutput), &output, &outputSize, &cErr) == 0 {
		return nil, fmt.Errorf("frida: zlib decompress: %w", nativeError(cErr))
	}
	defer C.mb_bytes_free(output)
	return nativeZlibOutputBytes(unsafe.Pointer(output), uint64(outputSize), uint64(maxOutput))
}

func nativeZlibOutputBytes(output unsafe.Pointer, outputSize, maxOutput uint64) ([]byte, error) {
	if outputSize > maxOutput {
		return nil, fmt.Errorf("frida: zlib output exceeds %d bytes", maxOutput)
	}
	return C.GoBytes(output, C.int(outputSize)), nil
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
	if err := loadNativeRuntime(); err != nil {
		return nil, err
	}
	var cErr *C.char
	var ptr *C.mb_device
	if forcedNativeFailure("open-c") == nil {
		ptr = nativeDeviceOpen(&cErr)
	}
	if ptr == nil {
		C.mb_native_release()
		return nil, fmt.Errorf("frida: local device: %w", nativeError(cErr))
	}
	device := &NativeDevice{
		ptr: ptr, messageQueue: make(chan Message, maxNativeMessageQueue),
		messageStop: make(chan struct{}), messageDone: make(chan struct{}),
		controlWake: make(chan struct{}, 1), closeDone: make(chan struct{}),
		sessions: make(map[*NativeSession]struct{}),
	}
	go device.runMessageQueue()
	return device, nil
}

func (d *NativeDevice) Enumerate(ctx context.Context) ([]process.Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.closing {
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
	if d.closed || d.closing {
		return nil, errors.New("frida: device closed")
	}
	ns := &NativeSession{device: d, scripts: make(map[*NativeScript]struct{})}
	ns.h = cgo.NewHandle(ns)
	var cErr *C.char
	ns.ptr = C.mb_device_attach_go(d.ptr, C.uint32_t(pid), C.uintptr_t(ns.h), &cErr)
	if ns.ptr == nil {
		ns.h.Delete()
		return nil, fmt.Errorf("frida: attach %d: %w", pid, nativeError(cErr))
	}
	if d.sessions == nil {
		d.sessions = make(map[*NativeSession]struct{})
	}
	d.sessions[ns] = struct{}{}
	return ns, nil
}

func (d *NativeDevice) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	if d.closing {
		done := d.closeDone
		d.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil
	}
	d.closing = true
	ptr := d.ptr
	d.ptr = nil
	stop, done, closeDone := d.messageStop, d.messageDone, d.closeDone
	sessions := make([]*NativeSession, 0, len(d.sessions))
	for session := range d.sessions {
		sessions = append(sessions, session)
	}
	d.mu.Unlock()
	var cleanup []error
	for _, session := range sessions {
		if err := session.Detach(); err != nil {
			cleanup = append(cleanup, err)
		}
	}
	C.mb_device_close(ptr)
	d.mu.Lock()
	d.closed = true
	if stop != nil {
		close(stop)
	}
	if closeDone != nil {
		close(closeDone)
	}
	d.mu.Unlock()
	if done != nil {
		<-done
	}
	C.mb_native_release()
	return errors.Join(cleanup...)
}

func ShutdownRuntime() { C.mb_runtime_shutdown() }

func (d *NativeDevice) SetMessageHandler(handler func(Message)) {
	d.mu.Lock()
	d.handler = handler
	d.mu.Unlock()
}

func (s *NativeSession) LoadScript(source string) (Script, error) {
	if s == nil || s.terminal.Load() {
		return nil, errors.New("frida: session detached")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.terminal.Load() || s.ptr == nil {
		return nil, errors.New("frida: session closed")
	}
	cs := C.CString(source)
	defer C.free(unsafe.Pointer(cs))
	var cErr *C.char
	ptr := C.mb_session_load_script_go(s.ptr, cs, C.uintptr_t(s.h), &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("frida: load script: %w", nativeError(cErr))
	}
	script := &NativeScript{session: s, ptr: ptr}
	if s.scripts == nil {
		s.scripts = make(map[*NativeScript]struct{})
	}
	s.scripts[script] = struct{}{}
	return script, nil
}

func (s *NativeSession) Detach() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.terminal.Store(true)
	var cleanup []error
	for script := range s.scripts {
		if script.closed {
			delete(s.scripts, script)
			continue
		}
		script.closed = true
		var cErr *C.char
		ok := C.mb_script_unload(script.ptr, &cErr)
		script.ptr = nil
		delete(s.scripts, script)
		if ok == 0 || forcedNativeFailure("unload") != nil {
			cleanup = append(cleanup, fmt.Errorf("frida: unload: %w", nativeError(cErr)))
		}
	}
	var cErr *C.char
	ok := C.mb_session_detach(s.ptr, &cErr)
	s.ptr = nil
	if s.h != 0 {
		s.h.Delete()
		s.h = 0
	}
	if s.device != nil {
		s.device.mu.Lock()
		delete(s.device.sessions, s)
		s.device.mu.Unlock()
	}
	if ok == 0 || forcedNativeFailure("detach") != nil {
		cleanup = append(cleanup, fmt.Errorf("frida: detach: %w", nativeError(cErr)))
	}
	return errors.Join(cleanup...)
}

func (s *NativeScript) Unload() error {
	if s == nil || s.session == nil {
		return nil
	}
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var cErr *C.char
	ok := C.mb_script_unload(s.ptr, &cErr)
	s.ptr = nil
	delete(s.session.scripts, s)
	if ok == 0 || forcedNativeFailure("unload") != nil {
		return fmt.Errorf("frida: unload: %w", nativeError(cErr))
	}
	return nil
}

func (s *NativeScript) Post(payload []byte) error {
	if s == nil || s.session == nil {
		return errors.New("frida: script unloaded")
	}
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if s.closed || s.session.closed || s.session.terminal.Load() || s.ptr == nil {
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
	s, ok := nativeSessionFromHandle(uintptr(handle))
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
	if isControlMessage(m) {
		s.device.dispatchControl(m)
	} else if !s.terminal.Load() {
		s.device.dispatch(m)
	}
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
	if s, ok := nativeSessionFromHandle(uintptr(handle)); ok {
		s.terminal.Store(true)
		s.device.dispatchControl(Message{Type: fmt.Sprintf("detached:%d", int(reason))})
	}
}

func nativeSessionFromHandle(handle uintptr) (session *NativeSession, ok bool) {
	if handle == 0 {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			session, ok = nil, false
		}
	}()
	session, ok = cgo.Handle(handle).Value().(*NativeSession)
	return session, ok
}

func invokeFridaDetachedForTest(handle uintptr, reason int) {
	goFridaDetached(C.uintptr_t(handle), C.int(reason))
}

func (d *NativeDevice) dispatch(m Message) {
	size := nativeMessageBytes(m)
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	queue := d.messageQueue
	if queue != nil && (size > maxNativeMessageQueueBytes || d.messageQueueBytes > maxNativeMessageQueueBytes-size) {
		d.droppedMessages++
		d.mu.Unlock()
		return
	}
	if queue != nil {
		select {
		case queue <- m:
			d.messageQueueBytes += size
			d.mu.Unlock()
			return
		default:
			d.droppedMessages++
			d.mu.Unlock()
			return
		}
	}
	d.mu.Unlock()
	d.deliverMessage(m)
}

func isControlMessage(message Message) bool {
	return strings.HasPrefix(message.Type, "detached:") || message.Type == "fatal" || message.Type == "error"
}

func (d *NativeDevice) dispatchControl(m Message) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	if d.controlWake == nil {
		d.mu.Unlock()
		d.deliverMessage(m)
		return
	}
	size := nativeMessageBytes(m)
	if len(d.controlQueue) >= maxNativeControlQueue ||
		size > maxNativeControlQueueBytes ||
		d.controlQueueBytes > maxNativeControlQueueBytes-size {
		/* Keep control delivery reliable without allowing an unbounded slice. */
		d.droppedControlMessages++
		d.mu.Unlock()
		d.deliverMessage(m)
		return
	}
	d.controlQueue = append(d.controlQueue, m)
	d.controlQueueBytes += size
	wake := d.controlWake
	d.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (d *NativeDevice) nextControl() (Message, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.controlQueue) == 0 {
		return Message{}, false
	}
	message := d.controlQueue[0]
	d.controlQueue[0] = Message{}
	d.controlQueue = d.controlQueue[1:]
	size := nativeMessageBytes(message)
	if size >= d.controlQueueBytes {
		d.controlQueueBytes = 0
	} else {
		d.controlQueueBytes -= size
	}
	return message, true
}

func nativeMessageBytes(message Message) uint64 {
	return uint64(len(message.Type)) + uint64(len(message.Payload)) + uint64(len(message.Data))
}

func (d *NativeDevice) accountNormalDequeue(message Message) {
	size := nativeMessageBytes(message)
	d.mu.Lock()
	if size >= d.messageQueueBytes {
		d.messageQueueBytes = 0
	} else {
		d.messageQueueBytes -= size
	}
	d.mu.Unlock()
}

func (d *NativeDevice) runMessageQueue() {
	defer close(d.messageDone)
	for {
		if message, ok := d.nextControl(); ok {
			d.deliverMessage(message)
			continue
		}
		select {
		case <-d.messageStop:
			d.drainMessageQueue()
			return
		default:
		}
		select {
		case message := <-d.messageQueue:
			d.accountNormalDequeue(message)
			d.deliverMessage(message)
		case <-d.controlWake:
			// Control messages are drained at the top of the loop.
		case <-d.messageStop:
			d.drainMessageQueue()
			return
		}
	}
}

func (d *NativeDevice) drainMessageQueue() {
	for {
		if message, ok := d.nextControl(); ok {
			d.deliverMessage(message)
			continue
		}
		select {
		case message := <-d.messageQueue:
			d.accountNormalDequeue(message)
			d.deliverMessage(message)
		default:
			return
		}
	}
}

func (d *NativeDevice) deliverMessage(message Message) {
	d.mu.Lock()
	handler := d.handler
	d.mu.Unlock()
	if handler != nil {
		func() {
			defer func() { _ = recover() }()
			handler(message)
		}()
	}
}
