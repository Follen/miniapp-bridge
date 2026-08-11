package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	bridgeapp "github.com/Follen/miniapp-bridge/internal/app"
	"github.com/Follen/miniapp-bridge/internal/capture"
)

type targetMetadataNative struct {
	target TargetStatus
}

func TestStartClosesNativeReturnedWithStartupError(t *testing.T) {
	startupErr := errors.New("native startup failed")
	closeErr := errors.New("native cleanup failed")
	native := &advancedNative{closeErr: closeErr}
	debugPort, cdpPort := sdkFreePort(t), sdkFreePort(t)
	s, err := New(Options{
		DebugPort: debugPort,
		CDPPort:   cdpPort,
		Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
			return native, startupErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	startErr := s.Start(context.Background())
	if !errors.Is(startErr, startupErr) || !errors.Is(startErr, closeErr) {
		t.Fatalf("start error=%v, want startup and cleanup causes", startErr)
	}
	if got := s.Status().State; got != StateFailed {
		t.Fatalf("state=%s, want %s", got, StateFailed)
	}
	native.mu.Lock()
	closed := native.closed
	native.mu.Unlock()
	if closed != 1 {
		t.Fatalf("native close calls=%d, want 1", closed)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("close failed service: %v", err)
	}
}

func (n *targetMetadataNative) Close(context.Context) error  { return nil }
func (n *targetMetadataNative) TargetMetadata() TargetStatus { return n.target }

func TestNativeLogEventIsPublishedOnceWithReferenceStreams(t *testing.T) {
	for _, debug := range []bool{false, true} {
		t.Run(map[bool]string{false: "debug-disabled", true: "debug-enabled"}[debug], func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			s := newSDK(t, Options{
				Stdout:     &stdout,
				Stderr:     &stderr,
				DebugFrida: debug,
				Native: func(_ context.Context, publish func(LogEvent)) (NativeSession, error) {
					publish(LogEvent{Level: "info", Message: "native-info"})
					publish(LogEvent{Level: "error", Message: "native-error"})
					publish(LogEvent{Level: "debug", Message: "native-debug"})
					return &targetMetadataNative{}, nil
				},
			})
			logs := s.SubscribeLogs(SubscriptionOptions{Buffer: 32})
			if err := s.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())

			counts := map[string]int{}
			deadline := time.After(time.Second)
			for len(counts) < 3 {
				select {
				case event := <-logs.Channel():
					if strings.HasPrefix(event.Message, "native-") {
						counts[event.Message]++
					}
				case <-deadline:
					t.Fatalf("native log events=%v", counts)
				}
			}
			for _, message := range []string{"native-info", "native-error", "native-debug"} {
				if counts[message] != 1 {
					t.Fatalf("%s event count=%d", message, counts[message])
				}
			}
			if got := strings.Count(stdout.String(), "native-info"); got != 1 {
				t.Fatalf("info stdout count=%d output=%q", got, stdout.String())
			}
			if got := strings.Count(stderr.String(), "native-error"); got != 1 {
				t.Fatalf("error stderr count=%d output=%q", got, stderr.String())
			}
			wantDebug := 0
			if debug {
				wantDebug = 1
			}
			if got := strings.Count(stdout.String(), "native-debug"); got != wantDebug {
				t.Fatalf("debug stdout count=%d want=%d output=%q", got, wantDebug, stdout.String())
			}
			if strings.Contains(stderr.String(), "native-debug") || strings.Contains(stdout.String(), "native-error") {
				t.Fatalf("native log used wrong stream stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestExactRequestIDsAndCorrelation(t *testing.T) {
	var target struct{}
	if err := decodeJSONNumber([]byte(`{`), &target); err == nil {
		t.Fatal("invalid JSON number payload was accepted")
	}
	if err := decodeJSONNumber([]byte(`[]`), &target); err == nil {
		t.Fatal("incompatible JSON number target was accepted")
	}
	if key := idKey(math.NaN()); validRequestID(math.NaN()) || key != "x:NaN" {
		t.Fatalf("NaN numeric ID key=%q valid=%v", key, validRequestID(math.NaN()))
	}
	if id := json.Number("1e999999999999999999999"); validRequestID(id) {
		t.Fatalf("overflowing numeric ID was accepted: %s", id)
	}
	if key := idKey(true); key != "x:true" {
		t.Fatalf("fallback ID key=%q", key)
	}
	if key := idKey(json.Number("-0.0")); key != "n:0e0" {
		t.Fatalf("zero ID key=%q", key)
	}
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	selectSDKContext(t, s, "exact-id")
	upstream := &routeFrameClient{frames: make(chan []byte, 16)}
	s.app.DebugHub.Add(upstream)
	defer s.app.DebugHub.Remove(upstream)

	tests := []struct {
		name     string
		request  func() (Response, error)
		response string
		want     any
	}{
		{"above-safe-integer", func() (Response, error) {
			return s.SendRaw(context.Background(), []byte(`{"id":9007199254740993,"method":"Runtime.enable"}`))
		}, `{"id":9007199254740993,"result":{}}`, json.Number("9007199254740993")},
		{"max-uint64", func() (Response, error) {
			return s.Send(context.Background(), Request{ID: uint64(math.MaxUint64), Method: "Runtime.enable"})
		}, `{"id":18446744073709551615,"result":{}}`, json.Number("18446744073709551615")},
		{"json-number", func() (Response, error) {
			return s.Send(context.Background(), Request{ID: json.Number("9007199254740995"), Method: "Runtime.enable"})
		}, `{"id":9007199254740995,"result":{}}`, json.Number("9007199254740995")},
		{"string", func() (Response, error) {
			return s.Send(context.Background(), Request{ID: "9007199254740995", Method: "Runtime.enable"})
		}, `{"id":"9007199254740995","result":{}}`, "9007199254740995"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			done := make(chan pendingResult, 1)
			go func() {
				response, err := test.request()
				done <- pendingResult{response: response, err: err}
			}()
			_ = readRouteFrame(t, upstream)
			s.observeCDP([]byte(test.response))
			result := <-done
			if result.err != nil || result.response.ID != test.want {
				t.Fatalf("response=%+v err=%v want ID=%v (%T)", result.response, result.err, test.want, test.want)
			}
		})
	}

	first := make(chan error, 1)
	go func() {
		_, err := s.SendRaw(context.Background(), []byte(`{"id":1.0,"method":"Runtime.enable"}`))
		first <- err
	}()
	_ = readRouteFrame(t, upstream)
	if _, err := s.Send(context.Background(), Request{ID: json.Number("1e0"), Method: "Runtime.enable"}); !errors.Is(err, ErrDuplicateRequestID) {
		t.Fatalf("canonical duplicate=%v", err)
	}
	s.observeCDP([]byte(`{"id":1,"result":{}}`))
	if err := <-first; err != nil {
		t.Fatal(err)
	}

	before := s.app.Requests.Len()
	if err := s.Notify(Request{Method: "Runtime.enable"}); err != nil {
		t.Fatal(err)
	}
	_ = readRouteFrame(t, upstream)
	if got := s.app.Requests.Len(); got != before {
		t.Fatalf("notification pending=%d want=%d", got, before)
	}
	sub := s.SubscribeCDP()
	defer sub.Close()
	s.observeCDP([]byte(`{"id":"unknown","result":{}}`))
	if event := <-sub.Channel(); !errors.Is(event.Err, ErrUnknownRequestID) {
		t.Fatalf("unknown response event=%+v", event)
	}
}

func TestSendWithoutInitialUpstreamFailsImmediately(t *testing.T) {
	s := newSDK(t, Options{})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	selectSDKContext(t, s, "no-upstream")

	for _, send := range []func() error{
		func() error {
			_, err := s.Send(context.Background(), Request{ID: 1, Method: "Runtime.enable"})
			return err
		},
		func() error {
			_, err := s.SendRaw(context.Background(), []byte(`{"id":2,"method":"Runtime.enable"}`))
			return err
		},
	} {
		started := time.Now()
		if err := send(); !errors.Is(err, ErrNoUpstream) {
			t.Fatalf("send without upstream=%v", err)
		}
		if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
			t.Fatalf("send without upstream took %s", elapsed)
		}
	}
	if len(s.pending) != 0 || s.app.Requests.Len() != 0 {
		t.Fatalf("no-upstream pending sdk=%d app=%d", len(s.pending), s.app.Requests.Len())
	}
}

func TestStartClosesNativeSessionReturnedWithError(t *testing.T) {
	startupErr := errors.New("native starter returned a session and an error")
	native := &lifecycleNative{}
	debugPort, cdpPort := sdkFreePort(t), sdkFreePort(t)
	recordPath := filepath.Join(t.TempDir(), "startup-failure.bin")
	s, err := New(Options{
		DebugPort:  debugPort,
		CDPPort:    cdpPort,
		RecordPath: recordPath,
		Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
			return native, startupErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); !errors.Is(err, startupErr) {
		t.Fatalf("Start error=%v", err)
	}
	if status := s.Status(); status.State != StateFailed || !errors.Is(status.Err, startupErr) {
		t.Fatalf("failed status=%+v", status)
	}
	native.mu.Lock()
	closed := native.closed
	native.mu.Unlock()
	if closed != 1 {
		t.Fatalf("native close calls=%d want 1", closed)
	}
	reopened, err := capture.Start(recordPath)
	if err != nil {
		t.Fatalf("recording path remained locked after native rollback: %v", err)
	}
	if err := reopened.Write([]byte("after native rollback")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	frames, err := capture.Replay(recordPath)
	if err != nil || len(frames) != 1 || string(frames[0]) != "after native rollback" {
		t.Fatalf("reopened recording=%q err=%v", frames, err)
	}
	for _, port := range []int{debugPort, cdpPort} {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Fatalf("port %d was not released: %v", port, err)
		}
		_ = listener.Close()
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	native.mu.Lock()
	closed = native.closed
	native.mu.Unlock()
	if closed != 1 {
		t.Fatalf("native close calls after Close=%d want 1", closed)
	}

	closeErr := errors.New("native cleanup error")
	closingNative := &advancedNative{closeErr: closeErr}
	s, err = New(Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
		return closingNative, startupErr
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); !errors.Is(err, startupErr) || !errors.Is(err, closeErr) {
		t.Fatalf("joined startup/cleanup error=%v", err)
	}
}

func TestPendingCleanupAcrossTerminalPaths(t *testing.T) {
	newRunning := func(t *testing.T) (*Service, *routeFrameClient) {
		t.Helper()
		s := newSDK(t, Options{})
		if err := s.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		selectSDKContext(t, s, "cleanup")
		upstream := &routeFrameClient{frames: make(chan []byte, 8)}
		s.app.DebugHub.Add(upstream)
		return s, upstream
	}
	assertEmpty := func(t *testing.T, s *Service) {
		t.Helper()
		s.mu.Lock()
		sdkPending := len(s.pending)
		idPending := len(s.pendingIDs)
		s.mu.Unlock()
		if sdkPending != 0 || idPending != 0 || s.app.Requests.Len() != 0 {
			t.Fatalf("pending sdk=%d IDs=%d app=%d", sdkPending, idPending, s.app.Requests.Len())
		}
	}

	t.Run("caller cancellation", func(t *testing.T) {
		s, upstream := newRunning(t)
		defer s.Close(context.Background())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := s.Send(ctx, Request{ID: "cancel", Method: "Runtime.enable"}); done <- err }()
		_ = readRouteFrame(t, upstream)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
		assertEmpty(t, s)
	})

	t.Run("exact numeric cancellation", func(t *testing.T) {
		s, upstream := newRunning(t)
		defer s.Close(context.Background())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := s.SendRaw(ctx, []byte(`{"id":18446744073709551615,"method":"Runtime.enable"}`))
			done <- err
		}()
		_ = readRouteFrame(t, upstream)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("exact numeric cancel error=%v", err)
		}
		assertEmpty(t, s)
	})

	t.Run("timeout", func(t *testing.T) {
		s, upstream := newRunning(t)
		defer s.Close(context.Background())
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := s.Send(ctx, Request{ID: "timeout", Method: "Runtime.enable"}); done <- err }()
		_ = readRouteFrame(t, upstream)
		if err := <-done; !errors.Is(err, ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error=%v", err)
		}
		assertEmpty(t, s)
	})

	t.Run("send failure", func(t *testing.T) {
		s, _ := newRunning(t)
		defer s.Close(context.Background())
		if err := s.SelectContext("cleanup"); err != nil {
			t.Fatal(err)
		}
		if err := s.app.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		s.upstreamOnline = true
		s.mu.Unlock()
		if _, err := s.Send(context.Background(), Request{ID: "send-failure", Method: "Runtime.enable"}); !errors.Is(err, ErrClosed) {
			t.Fatalf("send failure=%v", err)
		}
		assertEmpty(t, s)
	})

	t.Run("disconnect", func(t *testing.T) {
		s, upstream := newRunning(t)
		defer s.Close(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := s.Send(context.Background(), Request{ID: "disconnect", Method: "Runtime.enable"})
			done <- err
		}()
		_ = readRouteFrame(t, upstream)
		s.app.DebugHub.Remove(upstream)
		s.observeConnection(bridgeapp.ConnectionEvent{Kind: "upstream", Connected: false})
		if err := <-done; !errors.Is(err, ErrUpstreamDisconnected) {
			t.Fatalf("disconnect error=%v", err)
		}
		assertEmpty(t, s)
	})

	t.Run("close", func(t *testing.T) {
		s, upstream := newRunning(t)
		done := make(chan error, 1)
		go func() {
			_, err := s.Send(context.Background(), Request{ID: "close", Method: "Runtime.enable"})
			done <- err
		}()
		_ = readRouteFrame(t, upstream)
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, ErrClosed) {
			t.Fatalf("close error=%v", err)
		}
		assertEmpty(t, s)
	})
}

func TestStatusSequenceAndAutomaticTargetMetadata(t *testing.T) {
	target := TargetStatus{Attached: true, Target: Target{PID: 25297, ParentPID: 1, Name: "fixture", Path: `C:\fixture\app.exe`, Version: 25297}}
	closeGate := make(chan struct{})
	var closeOnce sync.Once
	native := &targetMetadataNative{target: target}
	s := newSDK(t, Options{Native: func(context.Context, func(LogEvent)) (NativeSession, error) {
		return nativeSessionFunc{close: func(context.Context) error {
			closeOnce.Do(func() { close(closeGate) })
			return nil
		}, target: native.target}, nil
	}})
	statuses := s.SubscribeStatus(SubscriptionOptions{Buffer: 16})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Status().Target; got != target {
		t.Fatalf("automatic target=%+v want=%+v", got, target)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-closeGate
	var states []State
	for status := range statuses.Channel() {
		states = append(states, status.State)
	}
	want := []State{StateStarting, StateRunning, StateStopping, StateStopped}
	if len(states) != len(want) {
		t.Fatalf("status sequence=%v want=%v", states, want)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("status sequence=%v want=%v", states, want)
		}
	}
}

type nativeSessionFunc struct {
	close  func(context.Context) error
	target TargetStatus
}

func (n nativeSessionFunc) Close(ctx context.Context) error { return n.close(ctx) }
func (n nativeSessionFunc) TargetMetadata() TargetStatus    { return n.target }
