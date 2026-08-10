//go:build windows && frida

package frida

import (
	"context"
	"errors"
	"runtime/cgo"
	"strings"
	"testing"
	"time"

	agent "github.com/Follen/miniapp-bridge/frida"
	"github.com/Follen/miniapp-bridge/internal/process"
)

func TestNativeEnumeratesWMPFTargetMetadata(t *testing.T) {
	device, err := NewNativeDevice()
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	processes, err := device.Enumerate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		if strings.EqualFold(process.Name, "WeChatAppEx.exe") {
			t.Logf("pid=%d ppid=%d version=%d path=%s", process.PID, process.ParentPID, process.Version, process.Path)
			if process.ParentPID != 0 && process.Path != "" && process.Version != 0 {
				return
			}
		}
	}
	t.Fatalf("Frida returned %d processes but no complete WeChatAppEx.exe metadata", len(processes))
}

func TestNativeAgentLifecycleAndReattach(t *testing.T) {
	device, err := NewNativeDevice()
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := Bootstrap{Device: device, ConfigDir: "../../configs/addresses", Agent: agent.SourceForConfig}
	session, script, target, err := bootstrap.Attach(context.Background())
	if err != nil {
		device.Close()
		t.Fatal(err)
	}
	if err := script.Unload(); err != nil {
		t.Fatal(err)
	}
	if err := session.Detach(); err != nil {
		t.Fatal(err)
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := NewNativeDevice()
	if err != nil {
		t.Fatal(err)
	}
	reattach, err := second.Attach(target.PID)
	if err != nil {
		second.Close()
		t.Fatal(err)
	}
	if err := reattach.Detach(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

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

func TestNativeReachableErrorAndIdempotencyBranches(t *testing.T) {
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
	processes, err := device.Enumerate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pid, err := process.SelectParent(processes, "WeChatAppEx.exe")
	if err != nil {
		t.Fatal(err)
	}
	session, err := device.Attach(pid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.LoadScript("this is not valid javascript }"); err == nil {
		t.Fatal("invalid script load succeeded")
	}
	messages := make(chan Message, 2)
	device.SetMessageHandler(func(message Message) { messages <- message })
	script, err := session.LoadScript(`send("payload", new Uint8Array([1, 2, 3]).buffer); recv(function () {});`)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		if message.Type != "send" || string(message.Payload) != "payload" || string(message.Data) != string([]byte{1, 2, 3}) {
			t.Fatalf("message=%+v", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Agent message")
	}
	if err := script.Post([]byte(`{"type":"input","payload":"ok"}`)); err != nil {
		t.Fatal(err)
	}
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
	device.SetMessageHandler(nil)
	device.dispatch(Message{Type: "ignored"})
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	if err := device.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := device.Enumerate(context.Background()); err == nil {
		t.Fatal("enumerate after close succeeded")
	}
	if _, err := device.Attach(pid); err == nil {
		t.Fatal("attach after close succeeded")
	}
}

func TestNativeInjectedCFailuresAndCallbacks(t *testing.T) {
	original := nativeFailure
	originalOpen := nativeDeviceOpen
	t.Cleanup(func() { nativeFailure = original })
	t.Cleanup(func() { nativeDeviceOpen = originalOpen })
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
	nativeFailure = func(operation string) error {
		if operation == "enumerate" {
			return errors.New("injected enumerate failure")
		}
		return nil
	}
	device, err := NewNativeDevice()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := device.Enumerate(context.Background()); err == nil {
		t.Fatal("injected enumerate failure was ignored")
	}
	nativeFailure = func(operation string) error {
		if operation == "post" {
			return errors.New("injected post failure")
		}
		return nil
	}
	session, err := device.Attach(^uint32(0))
	if err == nil || session != nil {
		t.Fatal("invalid attach unexpectedly succeeded")
	}
	processes, err := device.Enumerate(context.Background())
	if err != nil {
		nativeFailure = nil
		processes, err = device.Enumerate(context.Background())
	}
	if err != nil {
		t.Fatal(err)
	}
	pid, err := process.SelectParent(processes, "WeChatAppEx.exe")
	if err != nil {
		nativeFailure = nil
		processes, err = device.Enumerate(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		pid, err = process.SelectParent(processes, "WeChatAppEx.exe")
	}
	if err != nil {
		t.Fatal(err)
	}
	session, err = device.Attach(pid)
	if err != nil {
		t.Fatal(err)
	}
	nativeFailure = func(operation string) error {
		if operation == "post" || operation == "unload" || operation == "detach" {
			return errors.New("injected native failure")
		}
		return nil
	}
	script, err := session.LoadScript("send('ready')")
	if err != nil {
		t.Fatal(err)
	}
	if err := script.Post([]byte(`{"type":"test"}`)); err == nil {
		t.Fatal("injected post failure was ignored")
	}
	if err := script.Unload(); err == nil {
		t.Fatal("injected unload failure was ignored")
	}
	if err := session.Detach(); err == nil {
		t.Fatal("injected detach failure was ignored")
	}
	_ = device.Close()
	nativeFailure = nil

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

func TestNativeSessionOwnsScriptsDuringDetach(t *testing.T) {
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

	device, err := NewNativeDevice()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = device.Close() }()
	processes, err := device.Enumerate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pid, err := process.SelectParent(processes, "WeChatAppEx.exe")
	if err != nil {
		t.Fatal(err)
	}
	sessionValue, err := device.Attach(pid)
	if err != nil {
		t.Fatal(err)
	}
	session := sessionValue.(*NativeSession)
	session.scripts = nil
	scriptValue, err := session.LoadScript(`send("detach-owned")`)
	if err != nil {
		t.Fatal(err)
	}
	script := scriptValue.(*NativeScript)
	closedScript := &NativeScript{session: session, closed: true}
	session.scripts[closedScript] = struct{}{}

	originalFailure := nativeFailure
	nativeFailure = func(operation string) error {
		if operation == "unload" {
			return errors.New("injected detach unload failure")
		}
		return nil
	}
	err = session.Detach()
	nativeFailure = originalFailure
	if err == nil || !strings.Contains(err.Error(), "frida: unload:") {
		t.Fatalf("detach did not aggregate script unload failure: %v", err)
	}
	if err := script.Unload(); err != nil {
		t.Fatalf("script unload after session detach=%v", err)
	}
}

func TestZZNativeRuntimeShutdown(t *testing.T) {
	ShutdownRuntime()
}
