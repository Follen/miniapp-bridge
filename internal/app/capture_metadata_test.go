package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Follen/miniapp-bridge/internal/capture"
	"github.com/Follen/miniapp-bridge/internal/logging"
	"github.com/Follen/miniapp-bridge/internal/wmpf"
	"github.com/gorilla/websocket"
)

func TestCaptureMetadataFollowsBidirectionalDispatchOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.capture")
	recorder, err := capture.Start(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(0, 0, logging.New(false, false))
	a.SetRecorder(recorder)

	contextData, err := wmpf.EncodeCategory(wmpf.CategoryConnectJsContext, wmpf.JsContext{ID: "ctx"})
	if err != nil {
		t.Fatal(err)
	}
	incoming := wmpf.EncodeDebugMessage(wmpf.DebugMessage{Category: wmpf.CategoryConnectJsContext, Data: contextData})
	if !a.handleDebugFrame(websocket.BinaryMessage, incoming) {
		t.Fatal("incoming frame rejected")
	}
	if err := a.SendCDP([]byte(`{"id":1,"method":"Runtime.enable"}`)); err != nil {
		t.Fatal(err)
	}
	if got := a.TakeRecorder(); got != recorder {
		t.Fatalf("TakeRecorder=%p want %p", got, recorder)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	frames, err := capture.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := capture.ReplayMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || len(metadata) != 2 {
		t.Fatalf("frames=%d metadata=%d", len(frames), len(metadata))
	}
	if string(frames[0]) != string(incoming) || metadata[0].Direction != capture.DirectionUpstream || metadata[0].Size != uint32(len(frames[0])) {
		t.Fatalf("incoming frame=%x metadata=%+v", frames[0], metadata[0])
	}
	outer, err := wmpf.DecodeDebugMessage(frames[1])
	if err != nil {
		t.Fatal(err)
	}
	chrome, err := wmpf.DecodeChrome(outer.Data)
	if err != nil {
		t.Fatal(err)
	}
	if chrome.Payload != `{"id":1,"method":"Runtime.enable"}` || chrome.JSContextID != "ctx" || metadata[1].Direction != capture.DirectionDownstream || metadata[1].Size != uint32(len(frames[1])) {
		t.Fatalf("outgoing=%+v metadata=%+v", chrome, metadata[1])
	}
	if metadata[1].Timestamp.Before(metadata[0].Timestamp) {
		t.Fatalf("timestamps out of order: %s then %s", metadata[0].Timestamp, metadata[1].Timestamp)
	}
}

func TestCaptureMetadataRecordsCorruptIncomingFrame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.capture")
	recorder, err := capture.Start(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(0, 0, logging.New(false, false))
	a.SetRecorder(recorder)
	if !a.handleDebugFrame(websocket.BinaryMessage, []byte{0xff}) {
		t.Fatal("corrupt frame was not consumed")
	}
	if err := a.TakeRecorder().Close(); err != nil {
		t.Fatal(err)
	}
	frames, err := capture.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := capture.ReplayMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || len(metadata) != 1 || metadata[0].Direction != capture.DirectionUpstream {
		t.Fatalf("frames=%x metadata=%+v", frames, metadata)
	}
	if _, err := wmpf.DecodeDebugMessage(frames[0]); err == nil {
		t.Fatal("corrupt incoming frame became decodable")
	}
}

func TestCaptureMetadataSendCloseRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close-race.capture")
	recorder, err := capture.Start(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(0, 0, logging.New(false, false))
	a.SetRecorder(recorder)

	var group sync.WaitGroup
	errs := make(chan error, 65)
	for i := 0; i < 64; i++ {
		group.Add(1)
		go func(id int) {
			defer group.Done()
			errs <- a.SendCDP([]byte(fmt.Sprintf(`{"id":%d,"method":"Runtime.enable"}`, id)))
		}(i)
	}
	group.Add(1)
	go func() {
		defer group.Done()
		errs <- a.Close(context.Background())
	}()
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, ErrClosed) {
			t.Fatalf("send/close error=%v", err)
		}
	}
	frames, err := capture.Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := capture.ReplayMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != len(metadata) {
		t.Fatalf("frames=%d metadata=%d", len(frames), len(metadata))
	}
	for i := range metadata {
		if metadata[i].Index != uint64(i) || metadata[i].Direction != capture.DirectionDownstream || metadata[i].Size != uint32(len(frames[i])) {
			t.Fatalf("metadata[%d]=%+v", i, metadata[i])
		}
	}
}
