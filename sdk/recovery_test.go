package sdk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Follen/miniapp-bridge/internal/app"
)

func recoveryFixture(t *testing.T, attempts int) (*Service, context.CancelFunc) {
	t.Helper()
	s, err := New(Options{
		RecoveryAttempts:  attempts,
		RecoveryBaseDelay: time.Millisecond,
		RecoveryMaxDelay:  2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.ctx = ctx
	s.cancel = cancel
	s.state = StateRunning
	s.status.State = StateRunning
	s.listenersReady = true
	s.nativeReady = true
	s.upstreamSeen = true
	s.upstreamOnline = false
	s.mu.Unlock()
	return s, func() {
		cancel()
		s.recoveryWG.Wait()
	}
}

func TestUpstreamRecoveryExhaustionAndReconnect(t *testing.T) {
	s, cleanup := recoveryFixture(t, 3)
	defer cleanup()
	s.observeConnection(app.ConnectionEvent{Kind: "upstream", Connected: false})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		health := s.Health()
		if health.State == HealthFailed {
			if health.RecoveryAttempts != 3 || health.LastFailure != "upstream-reconnect-exhausted" {
				t.Fatalf("health=%+v", health)
			}
			if !errors.Is(s.Status().Err, ErrUpstreamDisconnected) {
				t.Fatalf("status error=%v", s.Status().Err)
			}
			s.observeConnection(app.ConnectionEvent{Kind: "upstream", Connected: true})
			if health := s.Health(); health.State != HealthReady || !health.Ready {
				t.Fatalf("reconnected health=%+v", health)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("recovery did not exhaust: %+v", s.Health())
}

func TestUpstreamRecoverySingleAttemptExhaustsImmediately(t *testing.T) {
	s, cleanup := recoveryFixture(t, 1)
	defer cleanup()
	s.observeConnection(app.ConnectionEvent{Kind: "upstream", Connected: false})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if health := s.Health(); health.State == HealthFailed {
			if health.RecoveryAttempts != 1 {
				t.Fatalf("health=%+v", health)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("single recovery attempt did not exhaust: %+v", s.Health())
}

func TestUpstreamRecoveryCancellationAndStaleGeneration(t *testing.T) {
	s, cleanup := recoveryFixture(t, 5)
	defer cleanup()
	s.observeConnection(app.ConnectionEvent{Kind: "upstream", Connected: false})
	s.observeConnection(app.ConnectionEvent{Kind: "upstream", Connected: true})
	attempts := s.Health().RecoveryAttempts
	time.Sleep(10 * time.Millisecond)
	if got := s.Health().RecoveryAttempts; got != attempts {
		t.Fatalf("canceled recovery attempts=%d want %d", got, attempts)
	}

	s.mu.Lock()
	s.recoveryActive = true
	s.startUpstreamRecoveryLocked()
	s.recoveryActive = false
	s.state = StateNew
	s.startUpstreamRecoveryLocked()
	s.state = StateRunning
	s.ctx = nil
	s.startUpstreamRecoveryLocked()
	s.ctx = context.Background()
	s.recoveryGeneration++
	stale := s.recoveryGeneration - 1
	s.recoveryWG.Add(1)
	s.mu.Unlock()
	s.runUpstreamRecovery(context.Background(), stale)
}

func TestRecoveryDelayAndOptions(t *testing.T) {
	if got := recoveryDelay(10*time.Millisecond, time.Second, 0); got != 9*time.Millisecond {
		t.Fatalf("attempt zero delay=%s", got)
	}
	if got := recoveryDelay(10*time.Millisecond, time.Second, 2); got != 22*time.Millisecond {
		t.Fatalf("attempt two delay=%s", got)
	}
	if got := recoveryDelay(10*time.Millisecond, 25*time.Millisecond, 10); got != 25*time.Millisecond {
		t.Fatalf("clamped delay=%s", got)
	}
	if _, err := New(Options{RecoveryBaseDelay: 2 * time.Second, RecoveryMaxDelay: time.Second}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("inverted recovery delays=%v", err)
	}
	if _, err := New(Options{RecoveryAttempts: MaxRecoveryAttempts + 1}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("recovery attempts limit=%v", err)
	}
	if _, err := New(Options{RecoveryAttempts: -1}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("negative recovery attempts=%v", err)
	}
}
