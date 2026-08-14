// Package sdk exposes the stable public Go API for miniapp-bridge.
package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Follen/miniapp-bridge/internal/app"
	"github.com/Follen/miniapp-bridge/internal/capture"
	bridgecdp "github.com/Follen/miniapp-bridge/internal/cdp"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultDebugPort           = 9421
	DefaultCDPPort             = 62000
	DefaultPendingRequestLimit = 1024
	DefaultRequestTimeout      = 30 * time.Second
	DefaultShutdownTimeout     = 15 * time.Second
	DefaultRecoveryAttempts    = 6
	DefaultRecoveryBaseDelay   = 100 * time.Millisecond
	DefaultRecoveryMaxDelay    = 2 * time.Second
	MaxSubscriberBuffer        = 4096
	MaxPendingRequestLimit     = 65536
	MaxRequestTimeout          = 5 * time.Minute
	MaxShutdownTimeout         = 2 * time.Minute
	MaxRecoveryAttempts        = 20
	MaxRecoveryDelay           = time.Minute
)

var (
	ErrClosed                = errors.New("miniapp bridge is closed")
	ErrAlreadyStarted        = errors.New("miniapp bridge is already started")
	ErrNotStarted            = errors.New("miniapp bridge is not started")
	ErrNotRunning            = errors.New("miniapp bridge is not running")
	ErrInvalidOptions        = errors.New("invalid miniapp bridge options")
	ErrInvalidState          = ErrNotRunning
	ErrNoUpstream            = errors.New("WMPF upstream is not connected")
	ErrDuplicateID           = errors.New("CDP request ID is already pending")
	ErrDuplicateRequestID    = ErrDuplicateID
	ErrUnknownRequestID      = errors.New("CDP response ID is not pending")
	ErrUnknownRequest        = ErrUnknownRequestID
	ErrInvalidRequest        = errors.New("invalid CDP request")
	ErrNoContext             = errors.New("no JavaScript context is selected")
	ErrUnknownContext        = errors.New("JavaScript context is unknown")
	ErrUpstreamDisconnected  = errors.New("WMPF upstream disconnected")
	ErrTimeout               = errors.New("miniapp bridge operation timed out")
	ErrProtocol              = errors.New("WMPF protocol error")
	ErrCorruptFrame          = errors.New("corrupt WMPF frame")
	ErrSlowSubscriber        = errors.New("subscriber queue is full")
	ErrNativeUnavailable     = errors.New("native runtime is unavailable")
	ErrNativeVersionMismatch = errors.New("native runtime version mismatch")
	ErrNativeABIMismatch     = errors.New("native runtime ABI mismatch")
	ErrNativeExportMissing   = errors.New("native runtime export missing")
	ErrNativeLoad            = errors.New("native runtime load failed")
	ErrResourceExhausted     = errors.New("miniapp bridge resource budget exhausted")
	ErrTooManyPending        = errors.New("too many pending CDP requests")
)

var structuredRequestSequence atomic.Uint64

type Error struct {
	Op, Component string
	Err           error
}

func (e *Error) Error() string {
	if e.Component == "" {
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("%s %s: %v", e.Op, e.Component, e.Err)
}
func (e *Error) Unwrap() error { return e.Err }

type State string

const (
	StateNew      State = "new"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateStopped  State = "stopped"
	StateFailed   State = "failed"
)

type Options struct {
	DebugPort, CDPPort     int
	RecordPath, ReplayPath string
	NativePath             string
	AddressConfigDir       string
	DebugMain, DebugFrida  bool
	SubscriberBuffer       int
	PendingRequestLimit    int
	RequestTimeout         time.Duration
	ShutdownTimeout        time.Duration
	RecoveryAttempts       int
	RecoveryBaseDelay      time.Duration
	RecoveryMaxDelay       time.Duration
	Stdout, Stderr         io.Writer
	Native                 NativeStarter
}

type NativeStarter func(context.Context, func(LogEvent)) (NativeSession, error)

type NativeSession interface{ Close(context.Context) error }

// NativeMetadata is an optional capability implemented by native sessions.
// It keeps runtime identity observable without exposing native handles.
type NativeMetadata interface{ NativeMetadata() NativeStatus }

// Target is a process candidate returned by Discover. It contains no native handle.
type Target struct {
	PID       uint32
	ParentPID uint32
	Name      string
	Path      string
	Version   int
}

type NativeStatus struct {
	Attached bool
	Version  string
	ABI      uint32
	Path     string
}

type TargetStatus struct {
	Attached bool
	Target
}

type RecordingStatus struct {
	Active bool
	Path   string
}

type ConnectionStatus struct {
	DebugClients int
	CDPClients   int
	Upstream     bool
}

type Status struct {
	State                    State
	Health                   HealthStatus
	Ready                    bool
	Metrics                  MetricsSnapshot
	DebugPort, CDPPort       int
	DebugClients, CDPClients int
	Contexts                 []JSContext
	SelectedContext          string
	NativeAttached           bool
	Native                   NativeStatus
	Target                   TargetStatus
	Recording                RecordingStatus
	Connections              ConnectionStatus
	StartedAt, StoppedAt     time.Time
	Err                      error
}

type JSContext struct{ ID, Target string }
type LogEvent struct {
	Time           time.Time
	Level, Message string
}
type ContextEvent struct {
	Kind    string
	Context JSContext
}
type CDPEvent struct {
	Time     time.Time
	Payload  []byte
	ID       any
	Method   string
	Params   map[string]any
	Response *Response
	Err      error
}

// Route selects the JavaScript context for one protocol operation. An empty
// ID snapshots the currently selected context at dispatch time.
type Route struct{ JSContextID string }

type Request struct {
	ID     any
	Method string
	Params map[string]any
	Route  Route
}
type Response struct {
	ID     any
	Result map[string]any
	Error  *CDPError
}

// CDPRequest and CDPResponse are stable protocol-specific names retained as
// aliases so existing Request/Response users remain source-compatible.
type CDPRequest = Request
type CDPResponse = Response
type CDPError struct {
	Code    int
	Message string
	Data    any
}

type Service struct {
	mu                 sync.Mutex
	resourceMu         sync.Mutex
	state              State
	startDone          chan struct{}
	startErr           error
	closeDone          chan struct{}
	closeErr           error
	ctx                context.Context
	cancel             context.CancelFunc
	startCancel        context.CancelFunc
	app                *app.App
	nativeLog          *logging.Logger
	native             NativeSession
	nativePath         string
	nativeAttached     bool
	nativeStarter      NativeStarter
	recordPath         string
	replayPath         string
	upstreamOnline     bool
	status             Status
	pending            map[string]chan pendingResult
	pendingIDs         map[string]any
	logs               eventBus[LogEvent]
	statuses           eventBus[Status]
	cdpEvents          eventBus[CDPEvent]
	contexts           eventBus[ContextEvent]
	pendingLimit       int
	requestTimeout     time.Duration
	shutdownTimeout    time.Duration
	recoveryAttempts   int
	recoveryBaseDelay  time.Duration
	recoveryMaxDelay   time.Duration
	recoveryCancel     context.CancelFunc
	recoveryGeneration uint64
	recoveryActive     bool
	recoveryWG         sync.WaitGroup
	stopRequested      bool
	listenersReady     bool
	nativeReady        bool
	listenerFailed     bool
	upstreamSeen       bool
	health             HealthStatus
	metrics            MetricsSnapshot
}

type pendingResult struct {
	response Response
	err      error
}

func New(o Options) (*Service, error) {
	if o.DebugPort == 0 {
		o.DebugPort = DefaultDebugPort
	}
	if o.CDPPort == 0 {
		o.CDPPort = DefaultCDPPort
	}
	if o.DebugPort < 1 || o.DebugPort > 65535 || o.CDPPort < 1 || o.CDPPort > 65535 {
		return nil, &Error{Op: "new", Component: "options", Err: fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalidOptions)}
	}
	if o.SubscriberBuffer < 0 || o.PendingRequestLimit < 0 || o.RequestTimeout < 0 || o.ShutdownTimeout < 0 ||
		o.RecoveryAttempts < 0 || o.RecoveryBaseDelay < 0 || o.RecoveryMaxDelay < 0 {
		return nil, &Error{Op: "new", Component: "options", Err: fmt.Errorf("%w: resource limits and timeouts must not be negative", ErrInvalidOptions)}
	}
	if o.SubscriberBuffer > MaxSubscriberBuffer ||
		o.PendingRequestLimit > MaxPendingRequestLimit ||
		o.RequestTimeout > MaxRequestTimeout ||
		o.ShutdownTimeout > MaxShutdownTimeout ||
		o.RecoveryAttempts > MaxRecoveryAttempts || o.RecoveryBaseDelay > MaxRecoveryDelay || o.RecoveryMaxDelay > MaxRecoveryDelay {
		return nil, &Error{Op: "new", Component: "options", Err: fmt.Errorf("%w: resource limits and timeouts exceed configured maximums", ErrInvalidOptions)}
	}
	if o.SubscriberBuffer == 0 {
		o.SubscriberBuffer = 64
	}
	if o.PendingRequestLimit == 0 {
		o.PendingRequestLimit = DefaultPendingRequestLimit
	}
	if o.RequestTimeout == 0 {
		o.RequestTimeout = DefaultRequestTimeout
	}
	if o.ShutdownTimeout == 0 {
		o.ShutdownTimeout = DefaultShutdownTimeout
	}
	if o.RecoveryAttempts == 0 {
		o.RecoveryAttempts = DefaultRecoveryAttempts
	}
	if o.RecoveryBaseDelay == 0 {
		o.RecoveryBaseDelay = DefaultRecoveryBaseDelay
	}
	if o.RecoveryMaxDelay == 0 {
		o.RecoveryMaxDelay = DefaultRecoveryMaxDelay
	}
	if o.RecoveryMaxDelay < o.RecoveryBaseDelay {
		return nil, &Error{Op: "new", Component: "options", Err: fmt.Errorf("%w: recovery maximum delay must not be smaller than base delay", ErrInvalidOptions)}
	}
	stdout, stderr := o.Stdout, o.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	stdout = &lockedWriter{dst: stdout}
	stderr = &lockedWriter{dst: stderr}
	s := &Service{state: StateNew, pending: make(map[string]chan pendingResult), pendingIDs: make(map[string]any)}
	s.pendingLimit, s.requestTimeout, s.shutdownTimeout = o.PendingRequestLimit, o.RequestTimeout, o.ShutdownTimeout
	s.recoveryAttempts, s.recoveryBaseDelay, s.recoveryMaxDelay = o.RecoveryAttempts, o.RecoveryBaseDelay, o.RecoveryMaxDelay
	s.nativeStarter, s.recordPath, s.replayPath, s.nativePath = o.Native, o.RecordPath, o.ReplayPath, o.NativePath
	if s.nativeStarter == nil {
		s.nativeStarter = defaultNativeStarter(o.NativePath, o.AddressConfigDir)
	}
	s.logs.size, s.statuses.size, s.cdpEvents.size, s.contexts.size = o.SubscriberBuffer, o.SubscriberBuffer, o.SubscriberBuffer, o.SubscriberBuffer
	s.cdpEvents.clone = cloneCDPEvent
	s.statuses.clone = cloneStatus
	s.app = app.New(o.DebugPort, o.CDPPort, logging.NewWithWriters(o.DebugMain, o.DebugFrida, &logWriter{svc: s, level: "info", dst: stdout}, &logWriter{svc: s, level: "error", dst: stderr}))
	// Native messages publish directly to the SDK bus. This second logger only
	// preserves the CLI streams and Frida debug gate, avoiding duplicate events.
	s.nativeLog = logging.NewWithWriters(false, o.DebugFrida, stdout, stderr)
	s.app.SetObserver(app.Observer{OnCDP: s.observeCDP, OnContext: s.observeContext, OnConnection: s.observeConnection, OnError: s.observeRuntimeError})
	s.status = Status{State: StateNew, DebugPort: o.DebugPort, CDPPort: o.CDPPort}
	s.health = HealthStatus{State: HealthStarting, Since: time.Now()}
	if o.RecordPath != "" {
		s.status.Recording.Path = o.RecordPath
	}
	return s, nil
}

func (s *Service) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	switch s.state {
	case StateRunning:
		s.mu.Unlock()
		return nil
	case StateStarting:
		done := s.startDone
		s.mu.Unlock()
		select {
		case <-done:
			s.mu.Lock()
			err := s.startErr
			s.mu.Unlock()
			return err
		case <-ctx.Done():
			return contextOperationError("start", ctx.Err())
		}
	case StateStopping, StateStopped:
		s.mu.Unlock()
		return ErrClosed
	case StateFailed:
		s.mu.Unlock()
		return ErrClosed
	}
	s.state, s.status.State, s.startDone = StateStarting, StateStarting, make(chan struct{})
	s.stopRequested = false
	s.listenersReady, s.nativeReady, s.listenerFailed = false, false, false
	s.upstreamOnline, s.upstreamSeen = false, false
	s.setHealthLocked(HealthStarting, "")
	done := s.startDone
	startupCtx, startupCancel := context.WithCancel(ctx)
	s.startCancel = startupCancel
	s.mu.Unlock()
	s.publishStatus()

	err := s.start(startupCtx)
	startupCancel()
	if err != nil {
		err = s.rollbackStart(err)
	}
	// A caller-provided path requires a platform native starter. Unsupported
	// builds return a typed diagnostic instead of silently ignoring the path.
	if err == nil {
		s.mu.Lock()
		pathWithoutStarter := s.nativePath != "" && s.nativeStarter == nil
		s.mu.Unlock()
		if pathWithoutStarter {
			err = &Error{Op: "start", Component: "native", Err: ErrNativeUnavailable}
		}
		if err != nil {
			err = s.rollbackStart(err)
		}
	}
	s.mu.Lock()
	s.startCancel = nil
	closing := s.stopRequested || s.state == StateStopping || s.state == StateStopped
	if closing && err == nil {
		err = ErrClosed
	}
	s.startErr = err
	if closing {
		s.state, s.status.State = StateStopped, StateStopped
		s.status.StoppedAt = time.Now()
		s.setHealthLocked(HealthStopped, "")
	} else if err != nil {
		s.state, s.status.State, s.status.Err = StateFailed, StateFailed, err
		s.setHealthLocked(HealthFailed, "start")
	} else {
		s.ctx, s.cancel = context.WithCancel(ctx)
		s.state, s.status.State, s.status.StartedAt = StateRunning, StateRunning, time.Now()
		s.refreshRunningHealthLocked()
		go func() { <-s.ctx.Done(); _ = s.Close(context.Background()) }()
	}
	close(done)
	s.mu.Unlock()
	s.publishStatus()
	return contextOperationError("start", err)
}

func (s *Service) start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.app.Start(); err != nil {
		return &Error{Op: "start", Component: "listeners", Err: err}
	}
	s.mu.Lock()
	s.listenersReady = true
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Open option-backed recording only after the listener is ready. New stays
	// allocation-only, and a caller may have installed an explicit recorder
	// before Start.
	s.mu.Lock()
	needRecorder := s.recordPath != "" && !s.status.Recording.Active
	recordPath := s.recordPath
	s.mu.Unlock()
	if needRecorder {
		recorder, err := capture.StartSegmented(recordPath, capture.SegmentOptions{})
		if err != nil {
			return &Error{Op: "start", Component: "record", Err: err}
		}
		s.app.SetRecorder(recorder)
		s.mu.Lock()
		s.status.Recording = RecordingStatus{Active: true, Path: recordPath}
		s.mu.Unlock()
		s.publishStatus()
	}
	if s.nativeStarter != nil {
		native, err := s.nativeStarter(ctx, func(event LogEvent) {
			if event.Time.IsZero() {
				event.Time = time.Now()
			}
			s.logs.publish(event)
			switch event.Level {
			case "error":
				s.nativeLog.Error(event.Message)
			case "debug":
				s.nativeLog.Frida(event.Message)
			default:
				s.nativeLog.Info(event.Message)
			}
		})
		if err != nil {
			if native != nil {
				if closeErr := native.Close(context.Background()); closeErr != nil {
					err = errors.Join(err, closeErr)
				}
			}
			return &Error{Op: "start", Component: "native", Err: err}
		}
		s.mu.Lock()
		s.native = native
		s.refreshNativeMetadataLocked(native, native != nil)
		s.refreshTargetMetadataLocked(native)
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.nativeReady = true
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.replayPath != "" {
		if err := s.replay(ctx, s.replayPath); err != nil {
			return &Error{Op: "start", Component: "replay", Err: err}
		}
	}
	return nil
}

func (s *Service) rollbackStart(err error) error {
	return joinErrors(err, s.closeApp(), s.closeNative())
}

func joinErrors(errs ...error) error {
	var nonNil []error
	for _, err := range errs {
		if err != nil {
			nonNil = append(nonNil, err)
		}
	}
	switch len(nonNil) {
	case 0:
		return nil
	case 1:
		return nonNil[0]
	default:
		return errors.Join(nonNil...)
	}
}

func (s *Service) closeNative() error {
	s.mu.Lock()
	native := s.native
	s.native = nil
	s.nativeAttached = false
	s.status.NativeAttached = false
	s.status.Native.Attached = false
	s.status.Target.Attached = false
	s.mu.Unlock()
	if native != nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		return native.Close(ctx)
	}
	return nil
}

func (s *Service) closeApp() error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	err := s.app.Close(ctx)
	s.mu.Lock()
	if s.status.Recording.Active {
		s.status.Recording.Active = false
	}
	s.mu.Unlock()
	return err
}

func (s *Service) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.state == StateStopped {
		err := s.closeErr
		done := s.closeDone
		s.mu.Unlock()
		if done == nil {
			return err
		}
		if ctx.Err() != nil {
			return contextOperationError("close", ctx.Err())
		}
		select {
		case <-done:
			return err
		case <-ctx.Done():
			return contextOperationError("close", ctx.Err())
		}
	}
	if s.state == StateStarting {
		cancel := s.startCancel
		done := s.startDone
		s.stopRequested = true
		s.setHealthLocked(HealthStopping, "")
		s.mu.Unlock()
		s.publishStatus()
		if cancel != nil {
			cancel()
		}
		if ctx.Err() != nil {
			return contextOperationError("close", ctx.Err())
		}
		select {
		case <-done:
			return s.Close(ctx)
		case <-ctx.Done():
			return contextOperationError("close", ctx.Err())
		}
	}
	if s.state == StateStopping {
		done := s.closeDone
		s.mu.Unlock()
		if ctx.Err() != nil {
			return contextOperationError("close", ctx.Err())
		}
		select {
		case <-done:
			s.mu.Lock()
			err := s.closeErr
			s.mu.Unlock()
			return err
		case <-ctx.Done():
			return contextOperationError("close", ctx.Err())
		}
	}
	s.state, s.status.State, s.closeDone = StateStopping, StateStopping, make(chan struct{})
	s.stopRequested = true
	s.setHealthLocked(HealthStopping, "")
	done := s.closeDone
	cancel := s.cancel
	s.mu.Unlock()
	s.publishStatus()
	if cancel != nil {
		cancel()
	}
	go s.shutdown(done)
	if ctx.Err() != nil {
		return contextOperationError("close", ctx.Err())
	}
	select {
	case <-done:
		s.mu.Lock()
		err := s.closeErr
		s.mu.Unlock()
		return err
	case <-ctx.Done():
		return contextOperationError("close", ctx.Err())
	}
}

func (s *Service) shutdown(done chan struct{}) {
	started := time.Now()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancelShutdown()
	s.recoveryWG.Wait()
	s.resourceMu.Lock()
	// Wake SDK callers before network/native teardown so no waiter can remain
	// blocked while the close path is releasing resources.
	s.mu.Lock()
	for key, ch := range s.pending {
		select {
		case ch <- pendingResult{err: ErrClosed}:
		default:
		}
		s.cancelAppRequestLocked(key)
		delete(s.pending, key)
		delete(s.pendingIDs, key)
	}
	s.mu.Unlock()
	err := s.app.Close(shutdownCtx)
	s.mu.Lock()
	native := s.native
	s.native = nil
	s.nativeAttached = false
	s.status.NativeAttached = false
	s.status.Native.Attached = false
	s.status.Target.Attached = false
	s.status.Recording.Active = false
	s.mu.Unlock()
	if native != nil {
		err = joinErrors(err, native.Close(shutdownCtx))
	}
	if err != nil {
		err = &Error{Op: "close", Component: "resources", Err: err}
	}
	s.mu.Lock()
	s.closeErr = err
	s.status.Err = err
	s.status.NativeAttached = false
	s.status.State = StateStopped
	s.status.StoppedAt = time.Now()
	s.state = StateStopped
	duration := time.Since(started)
	if duration <= 0 {
		duration = time.Nanosecond
	}
	s.metrics.ShutdownDuration = duration
	s.setHealthLocked(HealthStopped, "")
	s.mu.Unlock()
	// The terminal snapshot is observable before subscription channels close.
	s.publishStatus()
	s.logs.closeAll()
	s.statuses.closeAll()
	s.cdpEvents.closeAll()
	s.contexts.closeAll()
	s.resourceMu.Unlock()
	close(done)
}

func (s *Service) Status() Status {
	s.mu.Lock()
	out := s.status
	out.Health = s.health
	out.Ready = s.health.Ready
	out.Metrics = s.metrics
	out.Metrics.PendingRequests = len(s.pending)
	out.Metrics.RecoveryAttempts = s.health.RecoveryAttempts
	out.NativeAttached = s.nativeAttached
	out.Native.Attached = out.NativeAttached
	s.mu.Unlock()
	out.DebugClients, out.CDPClients = s.app.DebugClientCount(), s.app.CDPClientCount()
	out.Connections = ConnectionStatus{DebugClients: out.DebugClients, CDPClients: out.CDPClients, Upstream: out.DebugClients > 0}
	out.Metrics.CollectedAt = time.Now()
	out.Metrics.DebugConnections = out.DebugClients
	out.Metrics.CDPConnections = out.CDPClients
	out.Contexts = nil
	out.SelectedContext = ""
	for _, c := range s.app.Contexts.List() {
		out.Contexts = append(out.Contexts, JSContext{ID: c.ID, Target: c.Target})
	}
	out.Metrics.Contexts = len(out.Contexts)
	sort.Slice(out.Contexts, func(i, j int) bool { return out.Contexts[i].ID < out.Contexts[j].ID })
	if c, ok := s.app.Contexts.Selected(); ok {
		out.SelectedContext = c.ID
	}
	return out
}

func cloneStatus(in Status) Status {
	out := in
	out.Contexts = append([]JSContext(nil), in.Contexts...)
	return out
}

func (s *Service) refreshNativeMetadataLocked(native NativeSession, attached bool) {
	s.updateNativeMetadataLocked(native, attached, false)
}

func (s *Service) refreshNativeMetadataFromSessionLocked(native NativeSession) {
	s.updateNativeMetadataLocked(native, false, true)
}

func (s *Service) updateNativeMetadataLocked(native NativeSession, attached, useReported bool) {
	effectiveAttached := attached
	metadata, hasMetadata := native.(NativeMetadata)
	var reported NativeStatus
	if hasMetadata {
		// NativeSession is caller-supplied code. Keep the surrounding state
		// transition serialized, but never invoke it while Service.mu is held.
		s.mu.Unlock()
		reported = metadata.NativeMetadata()
		s.mu.Lock()
	}
	if hasMetadata {
		status := reported
		if useReported {
			effectiveAttached = status.Attached
		} else {
			status.Attached = attached
		}
		s.status.Native = status
	} else {
		s.status.Native.Attached = effectiveAttached
	}
	if s.status.Native.Path == "" {
		s.status.Native.Path = s.nativePath
	}
	s.nativeAttached = effectiveAttached
	s.status.NativeAttached = effectiveAttached
	s.status.Native.Attached = effectiveAttached
	s.status.Target.Attached = effectiveAttached
}

func (s *Service) SelectContext(id string) error {
	s.mu.Lock()
	closed := s.state == StateStopping || s.state == StateStopped || s.state == StateFailed
	s.mu.Unlock()
	if closed {
		return &Error{Op: "select", Component: "context", Err: ErrClosed}
	}
	if !s.app.Contexts.Select(id) {
		return &Error{Op: "select", Component: "context", Err: fmt.Errorf("%w: %s", ErrUnknownContext, id)}
	}
	selected, _ := s.app.Contexts.Get(id)
	s.observeContext(app.ContextEvent{Kind: "selected", Context: selected})
	return nil
}
func (s *Service) Contexts() []JSContext { return s.Status().Contexts }

func (s *Service) SelectedContext() (JSContext, bool) {
	selected, ok := s.app.Contexts.Selected()
	if !ok {
		return JSContext{}, false
	}
	return JSContext{ID: selected.ID, Target: selected.Target}, true
}

func (s *Service) StartRecording(path string) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	s.mu.Lock()
	if s.state == StateStopping || s.state == StateStopped || s.state == StateFailed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.mu.Unlock()
	r, err := capture.StartSegmented(path, capture.SegmentOptions{})
	if err != nil {
		return err
	}
	old := s.app.SwapRecorder(r)
	s.mu.Lock()
	s.status.Recording = RecordingStatus{Active: true, Path: path}
	s.mu.Unlock()
	s.publishStatus()
	if old != nil {
		return old.Close()
	}
	return nil
}
func (s *Service) StopRecording() error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	r := s.app.TakeRecorder()
	s.mu.Lock()
	s.status.Recording.Active = false
	s.mu.Unlock()
	s.publishStatus()
	if r == nil {
		return nil
	}
	return r.Close()
}
func (s *Service) Replay(ctx context.Context, path string) error {
	return s.replay(ctx, path)
}

func (s *Service) replay(ctx context.Context, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	err := capture.ReplayEachContext(ctx, path, func(frame []byte) error {
		message, err := wmpf.DecodeDebugMessage(frame)
		if err != nil {
			return errors.Join(ErrProtocol, ErrCorruptFrame, err)
		}
		if _, err := wmpf.UnwrapDebugMessage(message); err != nil {
			return errors.Join(ErrProtocol, ErrCorruptFrame, err)
		}
		return nil
	})
	if err != nil {
		return translateReplayError(err)
	}
	if err := s.app.ReplayContext(ctx, path); err != nil {
		return translateReplayError(err)
	}
	return nil
}

func translateReplayError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return contextOperationError("replay", err)
	}
	component := "capture"
	underlying := err
	if errors.Is(err, app.ErrClosed) {
		component = "service"
		underlying = errors.Join(ErrClosed, err)
	} else if errors.Is(err, ErrProtocol) {
		component = "protocol"
	} else if errors.Is(err, capture.ErrFrameTooLarge) ||
		errors.Is(err, capture.ErrCaptureTooLarge) ||
		errors.Is(err, capture.ErrTooManyFrames) ||
		errors.Is(err, capture.ErrCorruptRecord) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		underlying = errors.Join(ErrCorruptFrame, err)
	}
	return &Error{Op: "replay", Component: component, Err: underlying}
}

func (s *Service) Send(ctx context.Context, req Request) (Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req.Method == "" {
		return Response{}, &Error{Op: "send", Component: "request", Err: ErrInvalidRequest}
	}
	if req.ID == nil {
		req.ID = nextStructuredRequestID()
	} else if !validRequestID(req.ID) {
		return Response{}, &Error{Op: "send", Component: "request", Err: ErrInvalidRequest}
	}
	payload, err := json.Marshal(struct {
		ID     any            `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params,omitempty"`
	}{req.ID, req.Method, req.Params})
	if err != nil {
		return Response{}, &Error{Op: "send", Component: "request", Err: errors.Join(ErrInvalidRequest, err)}
	}
	return s.sendPayload(ctx, payload, req.Route, req.ID, true)
}

func (s *Service) sendPayload(ctx context.Context, payload []byte, route Route, id any, wait bool) (Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.state != StateRunning {
		s.mu.Unlock()
		return Response{}, ErrNotRunning
	}
	if wait && !s.upstreamOnline && s.app.DebugClientCount() == 0 {
		s.mu.Unlock()
		return Response{}, ErrNoUpstream
	}
	if !wait {
		s.mu.Unlock()
		err := s.app.SendCDPRoute(payload, route.JSContextID)
		if err != nil {
			return Response{}, translateAppError(err)
		}
		return Response{}, nil
	}
	key := idKey(id)
	ch := make(chan pendingResult, 1)
	if _, exists := s.pending[key]; exists {
		s.mu.Unlock()
		return Response{}, ErrDuplicateID
	}
	if len(s.pending) >= s.pendingLimit {
		s.mu.Unlock()
		return Response{}, &Error{Op: "send", Component: "pending", Err: errors.Join(ErrResourceExhausted, ErrTooManyPending)}
	}
	s.pending[key] = ch
	s.pendingIDs[key] = id
	s.mu.Unlock()
	sendErr := s.app.SendCDPRoute(payload, route.JSContextID)
	if sendErr != nil {
		s.mu.Lock()
		delete(s.pending, key)
		delete(s.pendingIDs, key)
		s.cancelAppRequest(id)
		s.mu.Unlock()
		return Response{}, translateAppError(sendErr)
	}
	waitCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		waitCtx, cancel = context.WithTimeout(ctx, s.requestTimeout)
	}
	defer cancel()
	select {
	case result := <-ch:
		return result.response, result.err
	case <-waitCtx.Done():
		s.mu.Lock()
		delete(s.pending, key)
		delete(s.pendingIDs, key)
		s.cancelAppRequest(id)
		s.mu.Unlock()
		return Response{}, contextOperationError("send", waitCtx.Err())
	}
}

func translateAppError(err error) error {
	var public error
	switch {
	case errors.Is(err, app.ErrClosed):
		public = ErrClosed
	case errors.Is(err, app.ErrInvalidCDPPayload):
		public = ErrInvalidRequest
	case errors.Is(err, app.ErrNoContext):
		public = ErrNoContext
	case errors.Is(err, app.ErrUnknownContext):
		public = ErrUnknownContext
	default:
		return err
	}
	return &Error{Op: "send", Component: "route", Err: fmt.Errorf("%w: %v", public, err)}
}

func (s *Service) SendRaw(ctx context.Context, payload []byte) (Response, error) {
	return s.SendRawRoute(ctx, payload, Route{})
}

// SendRawRoute sends a raw JSON CDP request with an explicit context route.
func (s *Service) SendRawRoute(ctx context.Context, payload []byte, route Route) (Response, error) {
	var env struct {
		ID     exactJSONID `json:"id"`
		Method string      `json:"method"`
	}
	if err := decodeJSONNumber(payload, &env); err != nil {
		return Response{}, &Error{Op: "send", Component: "request", Err: errors.Join(ErrInvalidRequest, err)}
	}
	if env.Method == "" {
		return Response{}, &Error{Op: "send", Component: "request", Err: ErrInvalidRequest}
	}
	wait := env.ID.present && env.ID.value != nil
	if wait {
		if !validRequestID(env.ID.value) {
			return Response{}, &Error{Op: "send", Component: "request", Err: ErrInvalidRequest}
		}
	}
	return s.sendPayload(ctx, payload, route, env.ID.value, wait)
}
func (s *Service) Notify(req Request) error {
	if req.Method == "" {
		return &Error{Op: "send", Component: "request", Err: ErrInvalidRequest}
	}
	req.ID = nil
	body, err := json.Marshal(struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params,omitempty"`
	}{req.Method, req.Params})
	if err != nil {
		return &Error{Op: "send", Component: "request", Err: errors.Join(ErrInvalidRequest, err)}
	}
	_, err = s.sendPayload(context.Background(), body, req.Route, nil, false)
	return err
}

func (s *Service) observeCDP(payload []byte) {
	var env struct {
		ID     exactJSONID    `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
		Result map[string]any `json:"result"`
		Error  *CDPError      `json:"error"`
	}
	if err := decodeJSONNumber(payload, &env); err != nil {
		s.cdpEvents.publish(CDPEvent{
			Time:    time.Now(),
			Payload: append([]byte(nil), payload...),
			Err:     &Error{Op: "receive", Component: "response", Err: errors.Join(ErrInvalidRequest, err)},
		})
		return
	}
	e := CDPEvent{Time: time.Now(), Payload: append([]byte(nil), payload...), ID: env.ID.value, Method: env.Method, Params: cloneJSONMap(env.Params)}
	var responseTarget chan pendingResult
	var responseResult pendingResult
	if env.ID.value != nil && env.Method == "" {
		resp := Response{ID: env.ID.value, Result: cloneJSONMap(env.Result), Error: cloneCDPError(env.Error)}
		e.Response = &resp
		key := idKey(env.ID.value)
		s.mu.Lock()
		responseTarget = s.pending[key]
		delete(s.pending, key)
		delete(s.pendingIDs, key)
		s.mu.Unlock()
		if responseTarget != nil {
			responseResult = pendingResult{response: resp}
		} else {
			e.Err = &Error{Op: "receive", Component: "request", Err: fmt.Errorf("%w: %v", ErrUnknownRequestID, env.ID.value)}
		}
	}
	s.cdpEvents.publish(e)
	if responseTarget != nil {
		responseTarget <- responseResult
	}
}
func (s *Service) observeContext(e app.ContextEvent) {
	s.contexts.publish(ContextEvent{Kind: e.Kind, Context: JSContext{ID: e.Context.ID, Target: e.Context.Target}})
	s.publishStatus()
}
func (s *Service) observeConnection(e app.ConnectionEvent) {
	if e.Kind == "upstream" {
		online := e.Connected || s.app.DebugClientCount() > 0
		s.mu.Lock()
		s.upstreamOnline = online
		if online {
			s.upstreamSeen = true
			s.stopUpstreamRecoveryLocked()
		}
		if s.state == StateRunning {
			if !online && s.upstreamSeen {
				s.startUpstreamRecoveryLocked()
			}
			s.refreshRunningHealthLocked()
		}
		pending := make([]chan pendingResult, 0, len(s.pending))
		if !online && s.state == StateRunning {
			for key, ch := range s.pending {
				pending = append(pending, ch)
				s.cancelAppRequestLocked(key)
				delete(s.pending, key)
				delete(s.pendingIDs, key)
			}
		}
		s.mu.Unlock()
		if !online {
			for _, ch := range pending {
				select {
				case ch <- pendingResult{err: ErrUpstreamDisconnected}:
				default:
				}
			}
		}
	}
	s.publishStatus()
}

func (s *Service) startUpstreamRecoveryLocked() {
	if s.recoveryActive || s.state != StateRunning {
		return
	}
	s.health.RecoveryAttempts++
	if s.ctx == nil {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.recoveryGeneration++
	generation := s.recoveryGeneration
	s.recoveryActive = true
	s.recoveryCancel = cancel
	s.recoveryWG.Add(1)
	go s.runUpstreamRecovery(ctx, generation)
}

func (s *Service) stopUpstreamRecoveryLocked() {
	if s.recoveryCancel != nil {
		s.recoveryCancel()
	}
	s.recoveryCancel = nil
	s.recoveryActive = false
	s.recoveryGeneration++
}

func (s *Service) runUpstreamRecovery(ctx context.Context, generation uint64) {
	defer s.recoveryWG.Done()
	if s.recoveryAttempts <= 1 {
		s.finishUpstreamRecoveryAttempt(generation, true)
		return
	}
	for attempt := 2; attempt <= s.recoveryAttempts; attempt++ {
		timer := time.NewTimer(recoveryDelay(s.recoveryBaseDelay, s.recoveryMaxDelay, attempt-1))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if !s.finishUpstreamRecoveryAttempt(generation, attempt == s.recoveryAttempts) {
			return
		}
		if attempt == s.recoveryAttempts {
			return
		}
	}
}

func (s *Service) finishUpstreamRecoveryAttempt(generation uint64, exhausted bool) bool {
	s.mu.Lock()
	if generation != s.recoveryGeneration || s.state != StateRunning || s.upstreamOnline {
		s.mu.Unlock()
		return false
	}
	if s.health.RecoveryAttempts < uint64(s.recoveryAttempts) {
		s.health.RecoveryAttempts++
	}
	if exhausted {
		s.recoveryActive = false
		s.recoveryCancel = nil
		s.status.Err = &Error{Op: "recover", Component: "upstream", Err: ErrUpstreamDisconnected}
		s.setHealthLocked(HealthFailed, "upstream-reconnect-exhausted")
	} else {
		s.setHealthLocked(HealthReconnecting, "upstream")
	}
	s.mu.Unlock()
	s.publishStatus()
	return true
}

func recoveryDelay(base, maximum time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for step := 1; step < attempt && delay < maximum; step++ {
		if delay > maximum/2 {
			delay = maximum
		} else {
			delay *= 2
		}
	}
	jitter := delay / 10
	if attempt%2 == 0 {
		delay += jitter
	} else {
		delay -= jitter
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func (s *Service) refreshRunningHealthLocked() {
	if s.state != StateRunning {
		return
	}
	switch {
	case s.listenerFailed:
		s.setHealthLocked(HealthFailed, "")
	case !s.listenersReady || !s.nativeReady:
		s.setHealthLocked(HealthStarting, "")
	case !s.upstreamOnline && s.upstreamSeen:
		s.setHealthLocked(HealthReconnecting, "upstream")
	case !s.upstreamOnline:
		s.setHealthLocked(HealthStarting, "")
	default:
		s.setHealthLocked(HealthReady, "")
	}
}

func (s *Service) observeRuntimeError(event app.RuntimeError) {
	failure := event.Component
	if failure == "" {
		failure = "runtime"
	}
	s.mu.Lock()
	if s.state == StateStopping || s.state == StateStopped {
		s.mu.Unlock()
		return
	}
	if strings.HasSuffix(event.Component, "-listener") {
		s.listenerFailed = true
		s.stopUpstreamRecoveryLocked()
		s.status.Err = &Error{Op: "serve", Component: failure, Err: errors.New(event.Message)}
		s.setHealthLocked(HealthFailed, failure)
	} else if s.state == StateRunning {
		s.setHealthLocked(HealthDegraded, failure)
	}
	s.mu.Unlock()
	s.publishStatus()
}
func (s *Service) publishStatus() { s.statuses.publish(s.Status()) }

type SubscriptionOptions struct{ Buffer int }

func (s *Service) SubscribeLogs(options ...SubscriptionOptions) *Subscription[LogEvent] {
	return s.logs.subscribe(subscriptionBuffer(options))
}
func (s *Service) SubscribeStatus(options ...SubscriptionOptions) *Subscription[Status] {
	return s.statuses.subscribe(subscriptionBuffer(options))
}
func (s *Service) SubscribeCDP(options ...SubscriptionOptions) *Subscription[CDPEvent] {
	return s.cdpEvents.subscribe(subscriptionBuffer(options))
}
func (s *Service) SubscribeContexts(options ...SubscriptionOptions) *Subscription[ContextEvent] {
	return s.contexts.subscribe(subscriptionBuffer(options))
}

func subscriptionBuffer(options []SubscriptionOptions) int {
	if len(options) > 0 && options[0].Buffer > 0 {
		if options[0].Buffer > MaxSubscriberBuffer {
			return MaxSubscriberBuffer
		}
		return options[0].Buffer
	}
	return 0
}

func idKey(id any) string {
	key, _ := bridgecdp.IDKey(id)
	return key
}

// nextStructuredRequestID returns a process-unique integer request id for
// structured sends that omit an explicit caller id. The WMPF miniapp debug
// endpoint rejects non-integer CDP ids ("Message must have integer 'id'
// property"), so the bridge default must stay numeric.
func nextStructuredRequestID() int64 {
	return int64(structuredRequestSequence.Add(1))
}

func validRequestID(id any) bool {
	if _, ok := id.(string); ok {
		return true
	}
	_, ok := bridgecdp.IDKey(id)
	return ok
}

func decodeJSONNumber(payload []byte, target any) error {
	if !json.Valid(payload) {
		// Preserve encoding/json's concrete SyntaxError for errors.Is/As users.
		var discarded any
		return json.Unmarshal(payload, &discarded)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

type exactJSONID struct {
	value   any
	present bool
}

func (id *exactJSONID) UnmarshalJSON(payload []byte) error {
	id.present = true
	return decodeJSONNumber(payload, &id.value)
}

func (s *Service) cancelAppRequest(id any) {
	if id != nil {
		s.app.CancelCDPRequest(id)
	}
}

func (s *Service) cancelAppRequestLocked(key string) {
	s.cancelAppRequest(s.pendingIDs[key])
}

func contextOperationError(op string, err error) error {
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &Error{Op: op, Component: "context", Err: errors.Join(ErrTimeout, err)}
}

type logWriter struct {
	svc   *Service
	level string
	dst   io.Writer
}

type lockedWriter struct {
	mu  sync.Mutex
	dst io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dst.Write(p)
}

func (w *logWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if w.svc != nil {
		w.svc.logs.publish(LogEvent{Time: time.Now(), Level: w.level, Message: string(p)})
	}
	return n, err
}

type Subscription[T any] struct {
	C     <-chan T
	bus   *eventBus[T]
	ch    chan T
	once  sync.Once
	errMu sync.Mutex
	err   error
}

func (s *Subscription[T]) Close() error {
	if s == nil || s.bus == nil {
		return nil
	}
	s.once.Do(func() { s.bus.remove(s) })
	return s.Err()
}
func (s *Subscription[T]) Channel() <-chan T {
	if s == nil {
		return nil
	}
	return s.C
}

// Err returns the terminal reason for the subscription, if any.
func (s *Subscription[T]) Err() error {
	if s == nil {
		return nil
	}
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *Subscription[T]) setErr(err error) {
	s.errMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.errMu.Unlock()
}

type eventBus[T any] struct {
	mu     sync.Mutex
	size   int
	subs   map[*Subscription[T]]struct{}
	closed bool
	clone  func(T) T
}

func (b *eventBus[T]) subscribe(buffer ...int) *Subscription[T] {
	b.mu.Lock()
	defer b.mu.Unlock()
	size := b.size
	if len(buffer) > 0 && buffer[0] > 0 {
		size = buffer[0]
	}
	if size <= 0 {
		size = 64
	}
	if size > MaxSubscriberBuffer {
		size = MaxSubscriberBuffer
	}
	ch := make(chan T, size)
	s := &Subscription[T]{C: ch, bus: b, ch: ch}
	if b.subs == nil {
		b.subs = make(map[*Subscription[T]]struct{})
	}
	if b.closed {
		close(ch)
	} else {
		b.subs[s] = struct{}{}
	}
	return s
}
func (b *eventBus[T]) remove(s *Subscription[T]) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		close(s.ch)
	}
}
func (b *eventBus[T]) publish(v T) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for s := range b.subs {
		value := v
		if b.clone != nil {
			value = b.clone(v)
		}
		select {
		case s.ch <- value:
		default:
			delete(b.subs, s)
			s.setErr(ErrSlowSubscriber)
			close(s.ch)
		}
	}
}

func cloneJSONMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneJSONValue(v)
	}
	return out
}

func cloneJSONValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneJSONMap(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = cloneJSONValue(item)
		}
		return out
	case []byte:
		return append([]byte(nil), x...)
	default:
		return v
	}
}

func cloneCDPError(in *CDPError) *CDPError {
	if in == nil {
		return nil
	}
	out := *in
	out.Data = cloneJSONValue(in.Data)
	return &out
}

func cloneCDPEvent(in CDPEvent) CDPEvent {
	out := in
	out.Payload = append([]byte(nil), in.Payload...)
	out.Params = cloneJSONMap(in.Params)
	if in.Response != nil {
		response := *in.Response
		response.Result = cloneJSONMap(in.Response.Result)
		response.Error = cloneCDPError(in.Response.Error)
		out.Response = &response
	}
	return out
}
func (b *eventBus[T]) closeAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for s := range b.subs {
		close(s.ch)
	}
	b.subs = nil
}
