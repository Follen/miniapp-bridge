package sdk

import (
	"context"

	"github.com/Follen/miniapp-bridge/internal/process"
)

// TargetAttacher is an optional native session capability. Implementations
// remain internal to the application and never expose native handles.
type TargetAttacher interface {
	AttachTarget(context.Context, Target) error
}

type TargetDetacher interface {
	DetachTarget(context.Context) error
}

// TargetMetadata is an optional native session capability. It exposes the
// automatically attached process as values only, without a native handle.
type TargetMetadata interface {
	TargetMetadata() TargetStatus
}

func (s *Service) refreshTargetMetadataLocked(native NativeSession) {
	metadata, ok := native.(TargetMetadata)
	if !ok {
		return
	}
	s.mu.Unlock()
	target := metadata.TargetMetadata()
	s.mu.Lock()
	s.status.Target = target
}

func (s *Service) Discover(ctx context.Context) ([]Target, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	items, err := (process.TasklistFinder{}).Find(ctx)
	if err != nil {
		return nil, &Error{Op: "discover", Component: "process", Err: err}
	}
	out := make([]Target, 0, len(items))
	for _, item := range items {
		out = append(out, Target{PID: item.PID, ParentPID: item.ParentPID, Name: item.Name, Path: item.Path, Version: item.Version})
	}
	return out, nil
}

func (s *Service) Attach(ctx context.Context, target Target) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	s.mu.Lock()
	native := s.native
	state := s.state
	s.mu.Unlock()
	if state != StateRunning {
		return ErrNotRunning
	}
	attacher, ok := native.(TargetAttacher)
	if !ok {
		return ErrNativeUnavailable
	}
	if err := attacher.AttachTarget(ctx, target); err != nil {
		s.mu.Lock()
		if _, ok := native.(NativeMetadata); ok {
			s.refreshNativeMetadataFromSessionLocked(native)
		}
		s.status.Target.Attached = s.nativeAttached
		s.mu.Unlock()
		s.publishStatus()
		return &Error{Op: "attach", Component: "native", Err: err}
	}
	s.mu.Lock()
	s.refreshNativeMetadataLocked(native, true)
	s.status.Target = TargetStatus{Attached: true, Target: target}
	s.mu.Unlock()
	s.publishStatus()
	return nil
}

func (s *Service) Detach(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	s.mu.Lock()
	native := s.native
	attached := s.nativeAttached
	state := s.state
	s.mu.Unlock()
	if state != StateRunning {
		return ErrNotRunning
	}
	if native == nil || !attached {
		return nil
	}
	detacher, ok := native.(TargetDetacher)
	if !ok {
		return ErrNativeUnavailable
	}
	if err := detacher.DetachTarget(ctx); err != nil {
		s.mu.Lock()
		if _, ok := native.(NativeMetadata); ok {
			s.refreshNativeMetadataFromSessionLocked(native)
		}
		s.status.Target.Attached = s.nativeAttached
		s.mu.Unlock()
		s.publishStatus()
		return &Error{Op: "detach", Component: "native", Err: err}
	}
	s.mu.Lock()
	s.refreshNativeMetadataLocked(native, false)
	s.status.Target.Attached = false
	s.mu.Unlock()
	s.publishStatus()
	return nil
}
