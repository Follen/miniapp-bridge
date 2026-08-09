//go:build windows && frida

package frida

import (
	"reflect"
	"testing"
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
