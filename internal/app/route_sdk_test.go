package app

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	bridgecontext "github.com/Follen/miniapp-bridge/internal/context"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
)

type routeCaptureClient struct {
	mu     sync.Mutex
	frames [][]byte
}

func (c *routeCaptureClient) Send(frame []byte) error {
	c.mu.Lock()
	c.frames = append(c.frames, append([]byte(nil), frame...))
	c.mu.Unlock()
	return nil
}

func (*routeCaptureClient) Close() error { return nil }

func (c *routeCaptureClient) snapshot() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.frames))
	for i := range c.frames {
		out[i] = append([]byte(nil), c.frames[i]...)
	}
	return out
}

func decodeRouteFrame(t *testing.T, frame []byte) (wmpf.DebugMessage, wmpf.ChromeDevtools) {
	t.Helper()
	outer, err := wmpf.DecodeDebugMessage(frame)
	if err != nil {
		t.Fatal(err)
	}
	chrome, err := wmpf.DecodeChrome(outer.Data)
	if err != nil {
		t.Fatal(err)
	}
	return outer, chrome
}

func TestSendCDPRouteExplicitAndSelectedFallback(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	a.Contexts.Upsert(bridgecontext.Context{ID: "selected", Target: "main"})
	a.Contexts.Upsert(bridgecontext.Context{ID: "explicit", Target: "worker"})
	if !a.Contexts.Select("selected") {
		t.Fatal("failed to establish selected context")
	}
	client := &routeCaptureClient{}
	a.DebugHub.Add(client)

	if err := a.SendCDPRoute([]byte(`{"id":1,"method":"Runtime.enable"}`), "explicit"); err != nil {
		t.Fatal(err)
	}
	if err := a.SendCDPRoute([]byte(`{"id":2,"method":"Debugger.enable"}`), ""); err != nil {
		t.Fatal(err)
	}
	frames := client.snapshot()
	if len(frames) != 2 {
		t.Fatalf("frames=%d want 2", len(frames))
	}
	_, explicit := decodeRouteFrame(t, frames[0])
	_, fallback := decodeRouteFrame(t, frames[1])
	if explicit.JSContextID != "explicit" {
		t.Fatalf("explicit route=%q", explicit.JSContextID)
	}
	if fallback.JSContextID != "selected" {
		t.Fatalf("fallback route=%q", fallback.JSContextID)
	}
	if selected, ok := a.Contexts.Selected(); !ok || selected.ID != "selected" {
		t.Fatalf("explicit route changed selection: %+v ok=%v", selected, ok)
	}
}

func TestSendCDPRouteRejectsUnknownAndMalformedInputs(t *testing.T) {
	a := New(0, 0, logging.New(false, false))
	a.Contexts.Upsert(bridgecontext.Context{ID: "known"})
	client := &routeCaptureClient{}
	a.DebugHub.Add(client)
	payload := []byte(`{"id":1,"method":"Runtime.enable"}`)

	err := a.SendCDPRoute(payload, "missing")
	if !errors.Is(err, ErrUnknownContext) || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unknown context error=%v", err)
	}
	for _, malformed := range [][]byte{nil, []byte("not-json"), []byte("[]"), []byte("null")} {
		if err := a.SendCDPRoute(malformed, "known"); !errors.Is(err, ErrInvalidCDPPayload) {
			t.Fatalf("payload %q error=%v", malformed, err)
		}
	}
	if frames := client.snapshot(); len(frames) != 0 {
		t.Fatalf("invalid routes broadcast %d frames", len(frames))
	}
	if a.Requests.Len() != 0 || a.seq.Load() != 0 {
		t.Fatalf("invalid routes changed state: pending=%d seq=%d", a.Requests.Len(), a.seq.Load())
	}
	withoutContext := New(0, 0, logging.New(false, false))
	emptyRoute := &routeCaptureClient{}
	withoutContext.DebugHub.Add(emptyRoute)
	if err := withoutContext.SendCDPRoute(payload, ""); err != nil {
		t.Fatalf("empty bootstrap route error=%v", err)
	}
	emptyFrames := emptyRoute.snapshot()
	if len(emptyFrames) != 1 {
		t.Fatalf("empty bootstrap route frames=%d want 1", len(emptyFrames))
	}
	_, empty := decodeRouteFrame(t, emptyFrames[0])
	if empty.JSContextID != "" {
		t.Fatalf("empty bootstrap route context=%q", empty.JSContextID)
	}
	a.closing.Store(true)
	if err := a.SendCDPRoute(payload, "known"); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed route error=%v", err)
	}
}

func TestSendCDPRouteSerializesDispatch(t *testing.T) {
	const requests = 64
	a := New(0, 0, logging.New(false, false))
	a.Contexts.Upsert(bridgecontext.Context{ID: "ctx"})
	client := &routeCaptureClient{}
	a.DebugHub.Add(client)

	var group sync.WaitGroup
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		group.Add(1)
		go func(id int) {
			defer group.Done()
			errs <- a.SendCDPRoute([]byte(fmt.Sprintf(`{"id":%d,"method":"Runtime.evaluate"}`, id)), "ctx")
		}(i)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	frames := client.snapshot()
	if len(frames) != requests {
		t.Fatalf("frames=%d want %d", len(frames), requests)
	}
	for i, frame := range frames {
		outer, chrome := decodeRouteFrame(t, frame)
		if outer.Seq != uint32(i+1) || chrome.JSContextID != "ctx" {
			t.Fatalf("frame[%d] seq=%d context=%q", i, outer.Seq, chrome.JSContextID)
		}
	}
}
