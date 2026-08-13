package sdk

import "testing"

func TestCoverageGateSubscriptionBufferUpperBound(t *testing.T) {
	bus := eventBus[int]{}
	sub := bus.subscribe(MaxSubscriberBuffer + 1)
	if got := cap(sub.ch); got != MaxSubscriberBuffer {
		t.Fatalf("subscription capacity=%d, want %d", got, MaxSubscriberBuffer)
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
}
