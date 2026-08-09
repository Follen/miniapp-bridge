package sdk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bridgeapp "github.com/Follen/miniapp-bridge/internal/app"
)

type reentrantServiceNative struct {
	service       *Service
	metadataCalls int
	closeCalls    int
	mu            sync.Mutex
}

func (n *reentrantServiceNative) NativeMetadata() NativeStatus {
	_ = n.service.Status()
	n.mu.Lock()
	n.metadataCalls++
	n.mu.Unlock()
	return NativeStatus{Version: "fixture", ABI: 1, Path: "fixture.dll"}
}

func (n *reentrantServiceNative) Close(context.Context) error {
	_ = n.service.Status()
	n.mu.Lock()
	n.closeCalls++
	n.mu.Unlock()
	return nil
}

func TestServiceNativeCallbacksMayReenterStatus(t *testing.T) {
	native := &reentrantServiceNative{}
	s := newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
		return native, nil
	}})
	native.service = s

	startDone := make(chan error, 1)
	go func() { startDone <- s.Start(context.Background()) }()
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start deadlocked in NativeMetadata callback")
	}

	statusSub := s.SubscribeStatus()
	closeDone := make(chan error, 1)
	go func() { closeDone <- s.Close(context.Background()) }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked in NativeSession.Close callback")
	}
	if _, ok := <-statusSub.Channel(); ok {
		for range statusSub.Channel() {
		}
	}
	if !s.resourceMu.TryLock() {
		t.Fatal("Close returned before releasing resourceMu")
	}
	s.resourceMu.Unlock()
	native.mu.Lock()
	metadataCalls, closeCalls := native.metadataCalls, native.closeCalls
	native.mu.Unlock()
	if metadataCalls != 1 || closeCalls != 1 {
		t.Fatalf("native calls metadata=%d close=%d", metadataCalls, closeCalls)
	}
}

func TestServiceConcurrentCloseWaitsForSubscriptionTeardown(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	s.statuses.mu.Lock()
	firstDone := make(chan error, 1)
	go func() { firstDone <- s.Close(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		stopped := s.state == StateStopping
		s.mu.Unlock()
		if stopped {
			break
		}
		time.Sleep(time.Millisecond)
	}
	s.mu.Lock()
	stopped := s.state == StateStopping
	s.mu.Unlock()
	if !stopped {
		s.statuses.mu.Unlock()
		t.Fatal("first Close did not publish stopping state")
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- s.Close(context.Background()) }()
	select {
	case err := <-secondDone:
		s.statuses.mu.Unlock()
		t.Fatalf("second Close returned before subscription teardown: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	s.statuses.mu.Unlock()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestServiceDisconnectCannotBeOvertakenByPendingRegistration(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	selectSDKContext(t, s, "disconnect-registration")

	s.observeConnection(bridgeapp.ConnectionEvent{Kind: "upstream", Connected: false})
	done := make(chan error, 1)
	go func() {
		_, err := s.SendRaw(context.Background(), []byte(`{"id":"after-disconnect","method":"Runtime.enable"}`))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNoUpstream) {
			t.Fatalf("Send after disconnect=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Send registered a waiter after disconnect drain")
	}
	s.mu.Lock()
	pending := len(s.pending)
	s.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending after rejected send=%d", pending)
	}
}

func TestServicePublishesResponseEventBeforeWakingWaiter(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	selectSDKContext(t, s, "response-order")
	upstream := &routeFrameClient{frames: make(chan []byte, 1)}
	s.app.DebugHub.Add(upstream)
	defer s.app.DebugHub.Remove(upstream)

	requestDone := make(chan error, 1)
	go func() {
		_, err := s.SendRaw(context.Background(), []byte(`{"id":"response-order","method":"Runtime.enable"}`))
		requestDone <- err
	}()
	waitForPendingRequest(t, s, "response-order")

	s.cdpEvents.mu.Lock()
	observeDone := make(chan struct{})
	go func() {
		s.observeCDP([]byte(`{"id":"response-order","result":{}}`))
		close(observeDone)
	}()
	waitForPendingRemoval(t, s, "response-order")
	select {
	case err := <-requestDone:
		s.cdpEvents.mu.Unlock()
		t.Fatalf("request completed before event publication: %v", err)
	default:
	}
	s.cdpEvents.mu.Unlock()
	select {
	case <-observeDone:
	case <-time.After(time.Second):
		t.Fatal("response observer did not finish")
	}
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
}

func TestServiceStartRecordingRejectsFailedAndStoppingStates(t *testing.T) {
	startupErr := errors.New("fixture startup failure")
	s := newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
		return nil, startupErr
	}})
	if err := s.Start(context.Background()); !errors.Is(err, startupErr) {
		t.Fatalf("Start=%v", err)
	}
	failedPath := filepath.Join(t.TempDir(), "failed.capture")
	if err := s.StartRecording(failedPath); !errors.Is(err, ErrClosed) {
		t.Fatalf("StartRecording after failed Start=%v", err)
	}
	assertRecordingFilesAbsent(t, failedPath)
	if recorder := s.app.TakeRecorder(); recorder != nil {
		_ = recorder.Close()
		t.Fatal("failed service retained recorder")
	}

	s = newSDK(t, Options{})
	s.mu.Lock()
	s.state, s.status.State = StateStopping, StateStopping
	s.mu.Unlock()
	stoppingPath := filepath.Join(t.TempDir(), "stopping.capture")
	if err := s.StartRecording(stoppingPath); !errors.Is(err, ErrClosed) {
		t.Fatalf("StartRecording while stopping=%v", err)
	}
	assertRecordingFilesAbsent(t, stoppingPath)
	if recorder := s.app.TakeRecorder(); recorder != nil {
		_ = recorder.Close()
		t.Fatal("stopping service retained recorder")
	}
}

func assertRecordingFilesAbsent(t *testing.T, path string) {
	t.Helper()
	for _, candidate := range []string{path, path + ".meta.jsonl"} {
		if _, err := os.Stat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected recording left %q: %v", candidate, err)
		}
	}
}

func waitForPendingRequest(t *testing.T, s *Service, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, ok := s.pending[idKey(id)]
		s.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("request %q did not become pending", id)
}

func waitForPendingRemoval(t *testing.T, s *Service, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, ok := s.pending[idKey(id)]
		s.mu.Unlock()
		if !ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("request %q remained pending", id)
}
