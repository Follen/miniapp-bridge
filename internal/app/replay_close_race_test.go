package app

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Follen/miniapp-bridge/internal/capture"
	"github.com/Follen/miniapp-bridge/internal/logging"
)

func TestReplayAndCloseShareWaitGroupBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.capture")
	recorder, err := capture.Start(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		a := New(0, 0, logging.New(false, false))
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var replayErr, closeErr error
		go func() {
			defer wg.Done()
			<-start
			replayErr = a.ReplayContext(context.Background(), path)
		}()
		go func() {
			defer wg.Done()
			<-start
			closeErr = a.Close(context.Background())
		}()
		close(start)
		wg.Wait()
		if closeErr != nil {
			t.Fatalf("iteration %d Close=%v", i, closeErr)
		}
		if replayErr != nil && !errors.Is(replayErr, ErrClosed) && !errors.Is(replayErr, context.Canceled) {
			t.Fatalf("iteration %d Replay=%v", i, replayErr)
		}
	}
}
