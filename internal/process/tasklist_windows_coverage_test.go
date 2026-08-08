//go:build windows

package process

import (
	"context"
	"testing"
	"time"
)

func TestTasklistFinderUsesWindowsTasklist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	processes, err := (TasklistFinder{}).Find(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) == 0 {
		t.Fatal("tasklist returned no parseable processes")
	}
}
