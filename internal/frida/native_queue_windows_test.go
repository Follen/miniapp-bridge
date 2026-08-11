//go:build windows && frida

package frida

import (
	"fmt"
	"reflect"
	"runtime/cgo"
	"testing"
	"time"
)

func TestNativeMessageQueueDeterministicDrainAndPanicIsolation(t *testing.T) {
	var received []string
	device := &NativeDevice{
		handler: func(message Message) {
			received = append(received, message.Type)
			if message.Type == "panic" {
				panic("handler failure")
			}
		},
		messageQueue: make(chan Message, 3),
	}
	device.messageQueue <- Message{Type: "first"}
	device.messageQueue <- Message{Type: "panic"}
	device.messageQueue <- Message{Type: "last"}
	device.drainMessageQueue()
	if want := []string{"first", "panic", "last"}; !reflect.DeepEqual(received, want) {
		t.Fatalf("drained order=%v want=%v", received, want)
	}
	if len(device.messageQueue) != 0 {
		t.Fatalf("queue still contains %d messages", len(device.messageQueue))
	}
}

func TestNativeMessageQueueClosedStopUsesDrainPath(t *testing.T) {
	received := make(chan Message, 1)
	device := &NativeDevice{
		handler:      func(message Message) { received <- message },
		messageQueue: make(chan Message, 1),
		messageStop:  make(chan struct{}),
		messageDone:  make(chan struct{}),
	}
	device.messageQueue <- Message{Type: "queued-before-stop"}
	close(device.messageStop)
	go device.runMessageQueue()
	<-device.messageDone
	if message := <-received; message.Type != "queued-before-stop" {
		t.Fatalf("drained message=%+v", message)
	}
}

func TestNativeMessageFallbackHandlerPanicIsIsolated(t *testing.T) {
	device := &NativeDevice{handler: func(Message) { panic("fallback handler failure") }}
	device.dispatch(Message{Type: "fallback"})
}

func TestNativeControlQueueIsReliableWhenNormalQueueFull(t *testing.T) {
	received := make(chan Message, 2)
	device := &NativeDevice{
		handler:      func(message Message) { received <- message },
		messageQueue: make(chan Message, 1),
		controlWake:  make(chan struct{}, 1),
		messageStop:  make(chan struct{}),
		messageDone:  make(chan struct{}),
	}
	device.messageQueue <- Message{Type: "normal"}
	device.dispatchControl(Message{Type: "detached:crashed"})
	close(device.messageStop)
	go device.runMessageQueue()
	select {
	case <-device.messageDone:
	case <-time.After(time.Second):
		t.Fatal("control queue worker did not stop")
	}
	got := []Message{<-received, <-received}
	if got[0].Type != "detached:crashed" || got[1].Type != "normal" {
		t.Fatalf("control/normal delivery=%v", got)
	}
}

func TestNativeControlQueueOverflowIsBoundedAndReliable(t *testing.T) {
	received := make(chan Message, 1)
	device := &NativeDevice{
		handler:     func(message Message) { received <- message },
		controlWake: make(chan struct{}, 1),
	}
	for i := 0; i < maxNativeControlQueue+1; i++ {
		device.dispatchControl(Message{Type: fmt.Sprintf("detached:%d", i)})
	}
	device.mu.Lock()
	queued := len(device.controlQueue)
	device.mu.Unlock()
	if queued != maxNativeControlQueue {
		t.Fatalf("control queue length=%d want=%d", queued, maxNativeControlQueue)
	}
	select {
	case message := <-received:
		if message.Type != fmt.Sprintf("detached:%d", maxNativeControlQueue) {
			t.Fatalf("overflow message=%q", message.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("overflow control message was not synchronously delivered")
	}
}

func TestNativeQueuesEnforceByteBudgets(t *testing.T) {
	device := &NativeDevice{
		messageQueue: make(chan Message, maxNativeMessageQueue),
		controlWake:  make(chan struct{}, 1),
		handler:      func(Message) {},
	}
	oversize := Message{Type: "data", Data: make([]byte, maxNativeMessageQueueBytes+1)}
	device.dispatch(oversize)
	device.mu.Lock()
	if len(device.messageQueue) != 0 || device.messageQueueBytes != 0 || device.droppedMessages != 1 {
		t.Fatalf("normal queue state len=%d bytes=%d dropped=%d", len(device.messageQueue), device.messageQueueBytes, device.droppedMessages)
	}
	device.mu.Unlock()

	control := Message{Type: "detached:oversize", Data: make([]byte, maxNativeControlQueueBytes+1)}
	device.dispatchControl(control)
	device.mu.Lock()
	if len(device.controlQueue) != 0 || device.controlQueueBytes != 0 || device.droppedControlMessages != 1 {
		t.Fatalf("control queue state len=%d bytes=%d dropped=%d", len(device.controlQueue), device.controlQueueBytes, device.droppedControlMessages)
	}
	device.mu.Unlock()
}

func TestNativeDetachedCallbackInvalidatesSessionBeforeDelivery(t *testing.T) {
	received := make(chan Message, 1)
	device := &NativeDevice{handler: func(message Message) { received <- message }}
	session := &NativeSession{device: device}
	handle := cgo.NewHandle(session)
	invokeFridaDetachedForTest(uintptr(handle), 7)
	handle.Delete()
	if !session.terminal.Load() {
		t.Fatal("detached callback did not invalidate session")
	}
	if _, err := session.LoadScript("send(1)"); err == nil {
		t.Fatal("detached session accepted a new script")
	}
	select {
	case message := <-received:
		if message.Type != "detached:7" {
			t.Fatalf("control message=%+v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("detached control message was not delivered")
	}
}

func TestNativeSessionFromDeletedHandleIsIgnored(t *testing.T) {
	handle := cgo.NewHandle(42)
	handle.Delete()
	if _, ok := nativeSessionFromHandle(uintptr(handle)); ok {
		t.Fatal("deleted cgo handle was accepted")
	}
}

func TestNativeDefensiveQueueAndHandleBranches(t *testing.T) {
	if _, ok := nativeSessionFromHandle(0); ok {
		t.Fatal("zero handle was accepted")
	}
	closed := &NativeDevice{closed: true}
	closed.dispatchControl(Message{Type: "error"})

	device := &NativeDevice{
		controlQueue:      []Message{{Type: "a"}, {Type: "bb"}},
		controlQueueBytes: 3,
		messageQueue:      make(chan Message, 1),
		handler:           func(Message) {},
	}
	first, ok := device.nextControl()
	if !ok || first.Type != "a" || device.controlQueueBytes != 2 {
		t.Fatalf("first control=%+v ok=%v bytes=%d", first, ok, device.controlQueueBytes)
	}
	device.controlQueue = append(device.controlQueue, Message{Type: "ccc"})
	device.controlQueueBytes += 3
	device.messageQueue <- Message{Type: "normal"}
	device.drainMessageQueue()
	if len(device.controlQueue) != 0 || len(device.messageQueue) != 0 {
		t.Fatalf("queues not drained: control=%d normal=%d", len(device.controlQueue), len(device.messageQueue))
	}
}

func TestNativeDefensiveSessionAndCloseBranches(t *testing.T) {
	if err := ((*NativeSession)(nil)).Detach(); err != nil {
		t.Fatalf("nil session detach=%v", err)
	}
	if _, err := (&NativeSession{}).LoadScript("send(1)"); err == nil {
		t.Fatal("nil-pointer session accepted script")
	}
	if err := (&NativeDevice{closing: true}).Close(); err != nil {
		t.Fatalf("closing device with nil completion=%v", err)
	}
	done := make(chan struct{})
	close(done)
	if err := (&NativeDevice{closing: true, closeDone: done}).Close(); err != nil {
		t.Fatalf("closing device completion=%v", err)
	}
}

func TestNativeZlibOutputInvariant(t *testing.T) {
	if _, err := nativeZlibOutputBytes(nil, 2, 1); err == nil {
		t.Fatal("oversized native output was accepted")
	}
	got, err := nativeZlibOutputBytes(nil, 0, 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty native output=%v err=%v", got, err)
	}
}

func TestNativeControlCallbackPath(t *testing.T) {
	received := make(chan Message, 1)
	device := &NativeDevice{handler: func(message Message) { received <- message }}
	session := &NativeSession{device: device}
	handle := cgo.NewHandle(session)
	invokeFridaMessageForTest(uintptr(handle), "error", nil)
	handle.Delete()
	select {
	case message := <-received:
		if message.Type != "error" {
			t.Fatalf("control message=%+v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("control callback was not delivered")
	}
	session.terminal.Store(true)
	handle = cgo.NewHandle(session)
	invokeFridaMessageForTest(uintptr(handle), "ignored", nil)
	handle.Delete()
	select {
	case message := <-received:
		t.Fatalf("terminal session delivered %+v", message)
	default:
	}
}
