package logging

import "testing"

func TestNew(t *testing.T) {
	logger := New(true, true)
	if logger == nil || !logger.MainDebug || !logger.FridaDebug {
		t.Fatalf("logger=%+v", logger)
	}
}
