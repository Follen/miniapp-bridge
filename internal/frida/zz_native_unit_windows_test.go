//go:build windows && frida && !live

package frida

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime/cgo"
	"strings"
	"testing"
	"time"
)

func TestNativeLoadErrorContract(t *testing.T) {
	detail := errors.New("loader detail")
	loadErr := &NativeLoadError{Code: NativeLoadExportMissing, Err: detail}
	if loadErr.Error() != detail.Error() || !errors.Is(loadErr, detail) {
		t.Fatalf("native load error=%v", loadErr)
	}
}

func TestNativeMessageQueueIsNonBlockingAndDrains(t *testing.T) {
	received := make(chan Message, 4)
	device := &NativeDevice{
		handler:      func(message Message) { received <- message },
		messageQueue: make(chan Message, 2),
		messageStop:  make(chan struct{}),
		messageDone:  make(chan struct{}),
	}
	device.dispatch(Message{Type: "first"})
	device.dispatch(Message{Type: "second"})
	close(device.messageStop)
	go device.runMessageQueue()
	<-device.messageDone
	if first, second := <-received, <-received; first.Type != "first" || second.Type != "second" {
		t.Fatalf("queued order=%q,%q", first.Type, second.Type)
	}

	full := &NativeDevice{messageQueue: make(chan Message, 1)}
	full.messageQueue <- Message{Type: "occupied"}
	returned := make(chan struct{})
	go func() {
		full.dispatch(Message{Type: "dropped"})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("full native message queue blocked callback")
	}
	full.closed = true
	full.dispatch(Message{Type: "closed"})

	fallbackCalled := false
	fallback := &NativeDevice{handler: func(Message) { fallbackCalled = true }}
	fallback.dispatch(Message{Type: "fallback"})
	if !fallbackCalled {
		t.Fatal("test fallback handler was not called")
	}
	fallback.SetMessageHandler(nil)
	fallback.dispatch(Message{Type: "ignored"})
	fallback.deliverMessage(Message{Type: "ignored-worker"})
}

func TestNativeNonLiveErrorAndIdempotencyBranches(t *testing.T) {
	if err := nativeError(nil); err == nil {
		t.Fatal("nativeError(nil) returned nil")
	}
	device, err := NewNativeDevice()
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := device.Enumerate(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("enumerate canceled err=%v", err)
	}
	if _, err := device.Attach(^uint32(0)); err == nil {
		t.Fatal("invalid PID attach succeeded")
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := device.Enumerate(context.Background()); err == nil {
		t.Fatal("enumerate after close succeeded")
	}
	if _, err := device.Attach(1); err == nil {
		t.Fatal("attach after close succeeded")
	}
}

func TestNativeInjectedOpenFailuresAndCallbacks(t *testing.T) {
	originalFailure := nativeFailure
	originalOpen := nativeDeviceOpen
	t.Cleanup(func() {
		nativeFailure = originalFailure
		nativeDeviceOpen = originalOpen
	})
	nativeFailure = func(operation string) error {
		if operation == "open" || operation == "open-c" {
			return errors.New("injected open failure")
		}
		return nil
	}
	if _, err := NewNativeDevice(); err == nil {
		t.Fatal("injected open failure was ignored")
	}
	nativeFailure = func(operation string) error {
		if operation == "open-c" {
			return errors.New("injected C open failure")
		}
		return nil
	}
	if _, err := NewNativeDevice(); err == nil {
		t.Fatal("injected C open failure was ignored")
	}
	nativeFailure = nil
	nativeDeviceOpen = nilNativeDeviceOpen
	if _, err := NewNativeDevice(); err == nil {
		t.Fatal("nil C device was accepted")
	}
	nativeDeviceOpen = originalOpen

	var callbackMessages []Message
	callbackDevice := &NativeDevice{handler: func(message Message) { callbackMessages = append(callbackMessages, message) }}
	callbackSession := &NativeSession{device: callbackDevice}
	handle := cgo.NewHandle(callbackSession)
	invokeFridaMessageForTest(uintptr(handle), `{"type":"send","payload":"payload"}`, []byte("xy"))
	invokeFridaMessageForTest(uintptr(handle), `{"type":"send","payload":{"x":1}}`, nil)
	invokeFridaMessageForTest(uintptr(handle), "not-json", nil)
	handle.Delete()
	invokeFridaMessageForTest(0, "ignored", nil)
	wrongHandle := cgo.NewHandle(42)
	invokeFridaMessageForTest(uintptr(wrongHandle), "ignored", nil)
	wrongHandle.Delete()
	invokeFridaDetachedForTest(0, 1)
	detachedHandle := cgo.NewHandle(callbackSession)
	invokeFridaDetachedForTest(uintptr(detachedHandle), 2)
	detachedHandle.Delete()
	if len(callbackMessages) != 4 {
		t.Fatalf("callback messages=%d, want 4", len(callbackMessages))
	}
	if callbackMessages[0].Type != "send" || string(callbackMessages[0].Payload) != "payload" || string(callbackMessages[0].Data) != "xy" {
		t.Fatalf("first callback=%+v", callbackMessages[0])
	}
}

func TestNativeScriptNilAndEmptyBranches(t *testing.T) {
	var nilScript *NativeScript
	if err := nilScript.Unload(); err != nil {
		t.Fatalf("nil script unload=%v", err)
	}
	if err := nilScript.Post(nil); err == nil {
		t.Fatal("nil script post unexpectedly succeeded")
	}
	if err := (&NativeScript{}).Unload(); err != nil {
		t.Fatalf("empty script unload=%v", err)
	}
	if err := (&NativeScript{}).Post(nil); err == nil {
		t.Fatal("empty script post unexpectedly succeeded")
	}
}

func TestNativeNonLiveSessionAndScriptLifecycle(t *testing.T) {
	command := exec.Command("ping.exe", "-n", "60", "127.0.0.1")
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	pid := uint32(command.Process.Pid)

	device, err := NewNativeDevice()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = device.Close() })
	originalFailure := nativeFailure
	t.Cleanup(func() { nativeFailure = originalFailure })
	nativeFailure = func(operation string) error {
		if operation == "enumerate" {
			return errors.New("injected enumerate failure")
		}
		return nil
	}
	if _, err := device.Enumerate(context.Background()); err == nil {
		t.Fatal("injected enumerate failure was ignored")
	}
	nativeFailure = nil
	processes, err := device.Enumerate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, process := range processes {
		if process.PID == pid {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("fixture process %d is absent from Frida enumeration", pid)
	}

	messages := make(chan Message, 4)
	device.SetMessageHandler(func(message Message) { messages <- message })
	sessionValue, err := device.Attach(pid)
	if err != nil {
		t.Fatal(err)
	}
	session := sessionValue.(*NativeSession)
	if _, err := session.LoadScript("this is not valid javascript }"); err == nil {
		t.Fatal("invalid script load succeeded")
	}
	session.scripts = nil
	scriptValue, err := session.LoadScript(`send("ready"); recv(function () {});`)
	if err != nil {
		t.Fatal(err)
	}
	script := scriptValue.(*NativeScript)
	select {
	case message := <-messages:
		if message.Type != "send" || string(message.Payload) != "ready" {
			t.Fatalf("Agent message=%+v", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for generic Frida Agent message")
	}
	if err := script.Post([]byte(`{"type":"input","payload":"ok"}`)); err != nil {
		t.Fatal(err)
	}
	nativeFailure = func(operation string) error {
		if operation == "post" {
			return errors.New("injected post failure")
		}
		return nil
	}
	if err := script.Post([]byte(`{"type":"input"}`)); err == nil {
		t.Fatal("injected post failure was ignored")
	}
	nativeFailure = nil
	if err := script.Unload(); err != nil {
		t.Fatal(err)
	}
	if err := script.Unload(); err != nil {
		t.Fatal(err)
	}
	if err := script.Post(nil); err == nil {
		t.Fatal("post after unload succeeded")
	}
	if err := session.Detach(); err != nil {
		t.Fatal(err)
	}
	if err := session.Detach(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.LoadScript("send(1)"); err == nil {
		t.Fatal("load after detach succeeded")
	}

	errorSessionValue, err := device.Attach(pid)
	if err != nil {
		t.Fatal(err)
	}
	errorSession := errorSessionValue.(*NativeSession)
	unloadFailureValue, err := errorSession.LoadScript(`send("unload-failure")`)
	if err != nil {
		t.Fatal(err)
	}
	nativeFailure = func(operation string) error {
		if operation == "unload" {
			return errors.New("injected unload failure")
		}
		return nil
	}
	if err := unloadFailureValue.Unload(); err == nil {
		t.Fatal("injected script unload failure was ignored")
	}
	nativeFailure = nil
	activeValue, err := errorSession.LoadScript(`send("detach-owned")`)
	if err != nil {
		t.Fatal(err)
	}
	active := activeValue.(*NativeScript)
	closed := &NativeScript{session: errorSession, closed: true}
	errorSession.scripts[closed] = struct{}{}
	nativeFailure = func(operation string) error {
		if operation == "unload" || operation == "detach" {
			return errors.New("injected native failure")
		}
		return nil
	}
	err = errorSession.Detach()
	nativeFailure = nil
	if err == nil || !strings.Contains(err.Error(), "frida: unload:") || !strings.Contains(err.Error(), "frida: detach:") {
		t.Fatalf("detach did not aggregate native cleanup failures: %v", err)
	}
	if err := active.Unload(); err != nil {
		t.Fatalf("script unload after failed session detach=%v", err)
	}
}

func TestZZNativeRuntimeShutdown(t *testing.T) {
	ShutdownRuntime()
}
