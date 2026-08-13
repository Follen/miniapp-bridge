package sdk

import (
	"errors"
	"testing"
	"time"
)

func TestResourceOptionsRejectConfiguredOversize(t *testing.T) {
	cases := []Options{
		{SubscriberBuffer: -1},
		{SubscriberBuffer: MaxSubscriberBuffer + 1},
		{PendingRequestLimit: MaxPendingRequestLimit + 1},
		{RequestTimeout: MaxRequestTimeout + time.Nanosecond},
		{ShutdownTimeout: MaxShutdownTimeout + time.Nanosecond},
	}
	for i, options := range cases {
		if _, err := New(options); !errors.Is(err, ErrInvalidOptions) {
			t.Errorf("case %d error=%v, want ErrInvalidOptions", i, err)
		}
	}
}

func TestSubscriptionBufferIsBounded(t *testing.T) {
	service, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	subscription := service.SubscribeLogs(SubscriptionOptions{Buffer: MaxSubscriberBuffer + 1})
	defer subscription.Close()
	if got := cap(subscription.ch); got != MaxSubscriberBuffer {
		t.Fatalf("subscription capacity=%d, want %d", got, MaxSubscriberBuffer)
	}
}
