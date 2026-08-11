package sdk

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Follen/miniapp-bridge/internal/capture"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
)

func TestReplayErrorContract(t *testing.T) {
	t.Run("nil context and valid capture", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "valid.capture")
		writeCaptureFixture(t, path, wmpf.EncodeDebugMessage(wmpf.DebugMessage{}))
		s := newSDK(t, Options{})
		if err := s.Replay(nil, path); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("truncated capture", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "truncated.capture")
		if err := os.WriteFile(path, []byte{0, 0, 0, 2, 0x08}, 0o600); err != nil {
			t.Fatal(err)
		}
		s := newSDK(t, Options{})
		err := s.Replay(context.Background(), path)
		assertSDKError(t, err, ErrCorruptFrame, "replay", "capture")
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("replay did not preserve capture cause: %v", err)
		}
	})

	t.Run("malformed WMPF", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "malformed.capture")
		writeCaptureFixture(t, path, []byte{0x1a, 0x02, 'x'})
		s := newSDK(t, Options{})
		err := s.Replay(context.Background(), path)
		assertSDKError(t, err, ErrProtocol, "replay", "protocol")
		if !errors.Is(err, ErrCorruptFrame) || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("malformed frame error chain=%v", err)
		}
	})

	t.Run("invalid zlib payload", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "zlib.capture")
		frame := wmpf.EncodeDebugMessage(wmpf.DebugMessage{
			Category:     wmpf.CategoryChromeDevtools,
			Data:         []byte("not-zlib"),
			CompressAlgo: wmpf.CompressZlib,
		})
		writeCaptureFixture(t, path, frame)
		s := newSDK(t, Options{})
		err := s.Replay(context.Background(), path)
		assertSDKError(t, err, ErrProtocol, "replay", "protocol")
		if !errors.Is(err, ErrCorruptFrame) {
			t.Fatalf("invalid compressed frame error chain=%v", err)
		}
	})

	t.Run("canceled", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "valid.capture")
		writeCaptureFixture(t, path, wmpf.EncodeDebugMessage(wmpf.DebugMessage{}))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s := newSDK(t, Options{})
		if err := s.Replay(ctx, path); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled replay=%v", err)
		}
	})

	t.Run("closed service", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "valid.capture")
		writeCaptureFixture(t, path, wmpf.EncodeDebugMessage(wmpf.DebugMessage{}))
		s := newSDK(t, Options{})
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		err := s.Replay(context.Background(), path)
		assertSDKError(t, err, ErrClosed, "replay", "service")
	})
}

func TestTranslateReplayErrorContract(t *testing.T) {
	if translateReplayError(nil) != nil {
		t.Fatal("nil replay error was changed")
	}
	for _, sentinel := range []error{
		capture.ErrFrameTooLarge,
		capture.ErrCaptureTooLarge,
		capture.ErrTooManyFrames,
	} {
		err := translateReplayError(sentinel)
		assertSDKError(t, err, ErrCorruptFrame, "replay", "capture")
		if !errors.Is(err, sentinel) {
			t.Fatalf("replay error did not preserve %v: %v", sentinel, err)
		}
	}
}

func TestCloseDeterministicWaitBranches(t *testing.T) {
	t.Run("stopped without completion channel", func(t *testing.T) {
		s := newSDK(t, Options{})
		s.mu.Lock()
		s.state, s.status.State = StateStopped, StateStopped
		s.mu.Unlock()
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("stopped wait timeout", func(t *testing.T) {
		s := newSDK(t, Options{})
		done := make(chan struct{})
		s.mu.Lock()
		s.state, s.status.State, s.closeDone = StateStopped, StateStopped, done
		s.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("stopped timeout=%v", err)
		}
		close(done)
	})

	t.Run("starting wait timeout without cancel", func(t *testing.T) {
		s := newSDK(t, Options{})
		done := make(chan struct{})
		s.mu.Lock()
		s.state, s.status.State, s.startDone, s.startCancel = StateStarting, StateStarting, done, nil
		s.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("starting timeout=%v", err)
		}
		close(done)
	})

	t.Run("stopping wait timeout", func(t *testing.T) {
		s := newSDK(t, Options{})
		done := make(chan struct{})
		s.mu.Lock()
		s.state, s.status.State, s.closeDone = StateStopping, StateStopping, done
		s.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("stopping timeout=%v", err)
		}
		close(done)
	})

	t.Run("new close caller timeout", func(t *testing.T) {
		s := newSDK(t, Options{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("new close timeout=%v", err)
		}
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReplayPathStartUsesPublicErrorContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truncated.capture")
	if err := os.WriteFile(path, []byte{0, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSDK(t, Options{ReplayPath: path})
	err := s.Start(context.Background())
	assertSDKError(t, err, ErrCorruptFrame, "start", "replay")
	if status := s.Status(); status.State != StateFailed || !errors.Is(status.Err, ErrCorruptFrame) {
		t.Fatalf("failed replay status=%+v", status)
	}
}

func TestMalformedRawCDPErrorsAreStructured(t *testing.T) {
	s := newSDK(t, Options{})
	for _, payload := range [][]byte{
		[]byte(`{`),
		[]byte(`[]`),
		[]byte(`{"id":1}`),
		[]byte(`{"id":true,"method":"Runtime.enable"}`),
		[]byte(`{"id":1e9999,"method":"Runtime.enable"}`),
	} {
		_, err := s.SendRaw(context.Background(), payload)
		assertSDKError(t, err, ErrInvalidRequest, "send", "request")
		if string(payload) == "{" {
			var syntax *json.SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("malformed request did not preserve JSON cause: %v", err)
			}
		}
	}

	sub := s.SubscribeCDP()
	defer sub.Close()
	s.observeCDP([]byte(`{`))
	event := <-sub.Channel()
	assertSDKError(t, event.Err, ErrInvalidRequest, "receive", "response")
	var syntax *json.SyntaxError
	if !errors.As(event.Err, &syntax) {
		t.Fatalf("malformed response did not preserve JSON cause: %v", event.Err)
	}
	if string(event.Payload) != "{" {
		t.Fatalf("malformed response payload=%q", event.Payload)
	}
}

func TestUnknownResponseErrorIsStructuredAndPreservesPending(t *testing.T) {
	s := newSDK(t, Options{})
	want := make(chan pendingResult, 1)
	s.pending[idKey("known")] = want
	sub := s.SubscribeCDP()
	defer sub.Close()

	s.observeCDP([]byte(`{"id":"unknown","result":{}}`))
	event := <-sub.Channel()
	assertSDKError(t, event.Err, ErrUnknownRequestID, "receive", "request")
	if _, ok := s.pending[idKey("known")]; !ok {
		t.Fatal("unknown response removed an unrelated pending request")
	}
	select {
	case <-want:
		t.Fatal("unknown response resolved an unrelated pending request")
	default:
	}
}

func assertSDKError(t *testing.T, err, sentinel error, op, component string) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v does not match %v", err, sentinel)
	}
	var structured *Error
	if !errors.As(err, &structured) {
		t.Fatalf("error is not structured: %v", err)
	}
	if structured.Op != op || structured.Component != component {
		t.Fatalf("structured error=%+v, want op=%q component=%q", structured, op, component)
	}
}

func writeCaptureFixture(t *testing.T, path string, frame []byte) {
	t.Helper()
	data := make([]byte, 4+len(frame))
	binary.BigEndian.PutUint32(data, uint32(len(frame)))
	copy(data[4:], frame)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
