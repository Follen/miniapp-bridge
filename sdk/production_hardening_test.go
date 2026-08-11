package sdk

import (
	"context"
	"errors"
	"testing"
	"time"

	bridgeapp "github.com/Follen/miniapp-bridge/internal/app"
	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
)

func TestProductionResourceDefaultsAndValidation(t *testing.T) {
	service := newSDK(t, Options{})
	if service.pendingLimit != DefaultPendingRequestLimit ||
		service.requestTimeout != DefaultRequestTimeout ||
		service.shutdownTimeout != DefaultShutdownTimeout {
		t.Fatalf("resource defaults pending=%d request=%s shutdown=%s", service.pendingLimit, service.requestTimeout, service.shutdownTimeout)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, err := New(Options{PendingRequestLimit: -1}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("negative resource option=%v", err)
	}
}

func TestProductionHealthMetricsAndDiagnostics(t *testing.T) {
	service := newSDK(t, Options{RequestTimeout: 10 * time.Millisecond, ShutdownTimeout: time.Second})
	if service.Ready() || service.Health().State != HealthStarting {
		t.Fatalf("new health=%+v", service.Health())
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.Ready() || service.Status().Health.State != HealthStarting || service.Status().Ready {
		t.Fatalf("waiting health=%+v status=%+v", service.Health(), service.Status())
	}

	service.app.Contexts.Upsert(bridgecontext.Context{ID: "production", Target: "main"})
	service.observeConnection(bridgeapp.ConnectionEvent{Kind: "upstream", Connected: true})
	if !service.Ready() || service.Health().State != HealthReady {
		t.Fatalf("connected health=%+v", service.Health())
	}
	service.observeConnection(bridgeapp.ConnectionEvent{Kind: "upstream", Connected: false})
	health := service.Health()
	if health.State != HealthReconnecting || health.Ready || health.RecoveryAttempts != 1 || health.FirstFailure != "upstream" || health.LastFailure != "upstream" {
		t.Fatalf("reconnecting health=%+v", health)
	}
	service.observeConnection(bridgeapp.ConnectionEvent{Kind: "upstream", Connected: true})
	if !service.Ready() || service.Health().State != HealthReady {
		t.Fatalf("recovered health=%+v", service.Health())
	}

	service.mu.Lock()
	service.metrics.RejectedConnections = 2
	service.metrics.DroppedMessages = 3
	service.metrics.DecompressionRejects = 4
	service.pending["held"] = make(chan pendingResult, 1)
	service.mu.Unlock()
	metrics := service.Metrics()
	if metrics.PendingRequests != 1 || metrics.Contexts != 1 || metrics.RejectedConnections != 2 || metrics.DroppedMessages != 3 || metrics.DecompressionRejects != 4 || metrics.RecoveryAttempts != 1 || metrics.CollectedAt.IsZero() {
		t.Fatalf("metrics=%+v", metrics)
	}
	diagnostics := service.Diagnostics()
	if diagnostics.Lifecycle != StateRunning || diagnostics.Health.State != HealthReady || diagnostics.Metrics.PendingRequests != 1 || diagnostics.CollectedAt.IsZero() {
		t.Fatalf("diagnostics=%+v", diagnostics)
	}

	service.mu.Lock()
	delete(service.pending, "held")
	service.mu.Unlock()
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.Health().State != HealthStopped || service.Metrics().ShutdownDuration <= 0 {
		t.Fatalf("terminal health=%+v metrics=%+v", service.Health(), service.Metrics())
	}
}

func TestProductionListenerFailureIsNeverReady(t *testing.T) {
	service := newSDK(t, Options{})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())

	service.observeConnection(bridgeapp.ConnectionEvent{Kind: "upstream", Connected: true})
	if !service.Ready() {
		t.Fatalf("connected health=%+v", service.Health())
	}
	service.observeRuntimeError(bridgeapp.RuntimeError{Component: "cdp-listener", Message: "serve failed", At: time.Now()})
	if health := service.Health(); health.State != HealthFailed || health.Ready || health.LastFailure != "cdp-listener" {
		t.Fatalf("listener failure health=%+v", health)
	}
	if status := service.Status(); status.Ready || status.Err == nil {
		t.Fatalf("listener failure status=%+v", status)
	}
	service.observeConnection(bridgeapp.ConnectionEvent{Kind: "upstream", Connected: true})
	if health := service.Health(); health.Ready || health.LastFailure != "cdp-listener" {
		t.Fatalf("upstream reconnect masked or rewrote a failed critical listener: %+v", health)
	}
}

func TestProductionRuntimeErrorHealthMatrix(t *testing.T) {
	service := newSDK(t, Options{})
	service.mu.Lock()
	initial := service.health
	service.refreshRunningHealthLocked()
	if service.health != initial {
		service.mu.Unlock()
		t.Fatalf("non-running refresh changed health: before=%+v after=%+v", initial, service.health)
	}
	service.mu.Unlock()
	service.observeRuntimeError(bridgeapp.RuntimeError{})

	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.nativeReady = false
	service.upstreamOnline = true
	service.refreshRunningHealthLocked()
	if service.health.State != HealthStarting || service.health.Ready {
		service.mu.Unlock()
		t.Fatalf("native-not-ready health=%+v", service.health)
	}
	service.nativeReady = true
	service.refreshRunningHealthLocked()
	service.mu.Unlock()
	if !service.Ready() {
		t.Fatalf("restored health=%+v", service.Health())
	}

	service.observeRuntimeError(bridgeapp.RuntimeError{Component: "cdp-writer", Message: "write failed"})
	if health := service.Health(); health.State != HealthDegraded || health.Ready || health.LastFailure != "cdp-writer" {
		t.Fatalf("degraded health=%+v", health)
	}
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.observeRuntimeError(bridgeapp.RuntimeError{Component: "cdp-listener", Message: "late failure"})
	if health := service.Health(); health.State != HealthStopped || health.Ready {
		t.Fatalf("late runtime error changed terminal health=%+v", health)
	}
}

func TestProductionPendingRequestLimit(t *testing.T) {
	service := newSDK(t, Options{PendingRequestLimit: 1})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	service.mu.Lock()
	service.upstreamOnline = true
	service.pending["held"] = make(chan pendingResult, 1)
	service.mu.Unlock()

	_, err := service.Send(context.Background(), Request{ID: "overflow", Method: "Runtime.enable"})
	if !errors.Is(err, ErrResourceExhausted) || !errors.Is(err, ErrTooManyPending) {
		t.Fatalf("pending limit error=%v", err)
	}
	service.mu.Lock()
	delete(service.pending, "held")
	service.mu.Unlock()
}
