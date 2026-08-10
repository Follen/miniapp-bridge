package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

type metadataRollbackNative struct {
	mu       sync.Mutex
	closed   int
	closeErr error
	native   NativeStatus
	target   TargetStatus
}

func (n *metadataRollbackNative) Close(context.Context) error {
	n.mu.Lock()
	n.closed++
	err := n.closeErr
	n.mu.Unlock()
	return err
}

func (n *metadataRollbackNative) NativeMetadata() NativeStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.native
}

func (n *metadataRollbackNative) TargetMetadata() TargetStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.target
}

func (n *metadataRollbackNative) closeCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.closed
}

func TestStartRollbackClearsNativeAndTargetAttachmentState(t *testing.T) {
	cleanupErr := errors.New("native rollback close failed")
	native := &metadataRollbackNative{
		closeErr: cleanupErr,
		native:   NativeStatus{Attached: true, Version: "fixture", ABI: 7, Path: "fixture.dll"},
		target:   TargetStatus{Attached: true, Target: Target{PID: 77, ParentPID: 1, Name: "fixture", Version: 25297}},
	}
	path := filepath.Join(t.TempDir(), "invalid-replay.bin")
	writeCaptureFixture(t, path, []byte{0xff})
	s := newSDK(t, Options{
		ReplayPath: path,
		Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
			return native, nil
		},
	})
	err := s.Start(context.Background())
	if !errors.Is(err, ErrCorruptFrame) || !errors.Is(err, cleanupErr) {
		t.Fatalf("startup rollback error=%v", err)
	}
	status := s.Status()
	if status.NativeAttached || status.Native.Attached || status.Target.Attached {
		t.Fatalf("stale attachment status after rollback=%+v", status)
	}
	if status.Target.PID != native.target.Target.PID {
		t.Fatalf("rollback discarded target identity=%+v", status.Target)
	}
	if got := native.closeCount(); got != 1 {
		t.Fatalf("native close count=%d, want 1", got)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("close failed service=%v", err)
	}
}

func TestStartCancellationAfterNativeStartupRollsBackAttachmentState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	native := &metadataRollbackNative{
		native: NativeStatus{Attached: true, Version: "fixture", ABI: 7},
		target: TargetStatus{Attached: true, Target: Target{PID: 78, Name: "fixture", Version: 25297}},
	}
	s := newSDK(t, Options{
		Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
			cancel()
			return native, nil
		},
	})
	err := s.Start(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup error=%v", err)
	}
	status := s.Status()
	if status.NativeAttached || status.Native.Attached || status.Target.Attached {
		t.Fatalf("stale attachment status after cancellation=%+v", status)
	}
	if got := native.closeCount(); got != 1 {
		t.Fatalf("native close count=%d, want 1", got)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("close failed service=%v", err)
	}
}

func TestCloseWrapsNativeErrorAndJoinPreservesBothCauses(t *testing.T) {
	nativeErr := errors.New("native close failed")
	native := &metadataRollbackNative{closeErr: nativeErr}
	s := newSDK(t, Options{
		Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
			return native, nil
		},
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := s.Close(context.Background())
	assertSDKError(t, err, nativeErr, "close", "resources")
	if got := native.closeCount(); got != 1 {
		t.Fatalf("native close count=%d, want 1", got)
	}
	appErr := errors.New("app close failed")
	if err := joinErrors(appErr, nativeErr); !errors.Is(err, appErr) || !errors.Is(err, nativeErr) {
		t.Fatalf("joined close causes=%v", err)
	}
}

func TestPublicValidationErrorsAreStructured(t *testing.T) {
	s := newSDK(t, Options{})
	_, err := s.Send(context.Background(), Request{})
	assertSDKError(t, err, ErrInvalidRequest, "send", "request")
	_, err = s.Send(context.Background(), Request{ID: true, Method: "Runtime.enable"})
	assertSDKError(t, err, ErrInvalidRequest, "send", "request")
	err = s.Notify(Request{})
	assertSDKError(t, err, ErrInvalidRequest, "send", "request")
	var unsupported *json.UnsupportedTypeError
	_, err = s.Send(context.Background(), Request{Method: "Runtime.enable", Params: map[string]any{"bad": make(chan int)}})
	assertSDKError(t, err, ErrInvalidRequest, "send", "request")
	if !errors.As(err, &unsupported) {
		t.Fatalf("send marshal cause was lost: %v", err)
	}
	err = s.Notify(Request{Method: "Runtime.enable", Params: map[string]any{"bad": make(chan int)}})
	assertSDKError(t, err, ErrInvalidRequest, "send", "request")
	if !errors.As(err, &unsupported) {
		t.Fatalf("notify marshal cause was lost: %v", err)
	}
	err = s.SelectContext("missing")
	assertSDKError(t, err, ErrUnknownContext, "select", "context")
}
