package sdk

import "time"

// HealthState describes whether a running Service can accept protocol work.
// It is separate from State so existing lifecycle consumers remain compatible.
type HealthState string

const (
	HealthStarting     HealthState = "starting"
	HealthReady        HealthState = "ready"
	HealthDegraded     HealthState = "degraded"
	HealthReconnecting HealthState = "reconnecting"
	HealthFailed       HealthState = "failed"
	HealthStopping     HealthState = "stopping"
	HealthStopped      HealthState = "stopped"
)

type HealthStatus struct {
	State            HealthState
	Ready            bool
	Since            time.Time
	FirstFailure     string
	LastFailure      string
	RecoveryAttempts uint64
}

type MetricsSnapshot struct {
	CollectedAt          time.Time
	DebugConnections     int
	CDPConnections       int
	PendingRequests      int
	Contexts             int
	RejectedConnections  uint64
	DroppedMessages      uint64
	DecompressionRejects uint64
	RecoveryAttempts     uint64
	ShutdownDuration     time.Duration
}

type DiagnosticSnapshot struct {
	CollectedAt time.Time
	Lifecycle   State
	Health      HealthStatus
	Metrics     MetricsSnapshot
	Native      struct {
		Attached bool
		Version  string
		ABI      uint32
	}
	RecordingActive bool
}

func (s *Service) setHealthLocked(state HealthState, failure string) {
	if s.health.State != state {
		s.health.State = state
		s.health.Since = time.Now()
	}
	s.health.Ready = state == HealthReady
	if failure != "" {
		if s.health.FirstFailure == "" {
			s.health.FirstFailure = failure
		}
		s.health.LastFailure = failure
	}
}

func (s *Service) Health() HealthStatus {
	s.mu.Lock()
	health := s.health
	s.mu.Unlock()
	return health
}

func (s *Service) Ready() bool { return s.Health().Ready }

func (s *Service) Metrics() MetricsSnapshot {
	s.mu.Lock()
	metrics := s.metrics
	metrics.PendingRequests = len(s.pending)
	metrics.RecoveryAttempts = s.health.RecoveryAttempts
	s.mu.Unlock()
	metrics.CollectedAt = time.Now()
	metrics.DebugConnections = s.app.DebugClientCount()
	metrics.CDPConnections = s.app.CDPClientCount()
	metrics.Contexts = len(s.app.Contexts.List())
	connections := s.app.ConnectionSnapshot()
	metrics.RejectedConnections = max(metrics.RejectedConnections,
		connections.RejectedUpstream+connections.RejectedCDP+connections.RejectedOrigin)
	metrics.DroppedMessages = max(metrics.DroppedMessages, connections.StaleDrops)
	return metrics
}

func (s *Service) Diagnostics() DiagnosticSnapshot {
	status := s.Status()
	out := DiagnosticSnapshot{
		CollectedAt:     time.Now(),
		Lifecycle:       status.State,
		Health:          s.Health(),
		Metrics:         s.Metrics(),
		RecordingActive: status.Recording.Active,
	}
	out.Native.Attached = status.Native.Attached
	out.Native.Version = status.Native.Version
	out.Native.ABI = status.Native.ABI
	return out
}
