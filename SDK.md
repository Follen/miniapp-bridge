# miniapp-bridge SDK Usage Guide

This document is the practical usage guide for the public Go SDK
(`github.com/Follen/miniapp-bridge/sdk`). The detailed contract reference
lives in [`docs/sdk.md`](docs/sdk.md).

- Platform: Windows amd64 (WMPF 25297 is the live-verified target; other
  historical address data is compatibility data only)
- Latest release: **v0.0.8**
- Minimum Go: the `go` directive in `go.mod` (Go 1.23 or newer)
- Ports: upstream (miniapp) `9421`, CDP WebSocket `62000`

## 1. Install

```powershell
go get github.com/Follen/miniapp-bridge/sdk@v0.0.8
```

To test against a local checkout:

```go
replace github.com/Follen/miniapp-bridge => D:/path/to/miniapp-bridge
```

The runtime loads `miniapp-frida.dll` (Frida core 17.3.2, native ABI 1.1).
Put the DLL and its `manifest.json` next to your executable, or point
`Options.NativePath` at an absolute DLL path. On Windows the DLL is
published in the `native-v17.3.2-abi1.1` GitHub Release and can be
prepared/validated with `sdk.PrepareNativeRuntime` / `sdk.CheckNativeRuntime`.

## 2. Minimal service

```go
svc, err := sdk.New(sdk.Options{})
if err != nil { return err }
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
if err := svc.Start(ctx); err != nil { return err }
defer svc.Close(context.Background())
```

`Start` opens both listeners, then discovers the WMPF target, attaches
Frida, and loads the embedded Agent. Zero options use the default ports
and the default native starter.

## 3. End-to-end example (self-bootstrapping)

The bridge automatically sends `Runtime.enable` through the empty
`jscontext_id` bootstrap route as soon as the miniapp connects (and again
after every reconnect). You do **not** need to manually enable the Runtime
domain or open a separate WebSocket:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Follen/miniapp-bridge/sdk"
)

func main() {
	svc, err := sdk.New(sdk.Options{
		DebugPort:  9421,
		CDPPort:    62000,
		NativePath: "D:/path/to/miniapp-frida.dll", // absolute, or omit for default discovery
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "new:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(1)
	}
	defer svc.Close(context.Background())

	// Wait for the miniapp to connect to the debug port (open or reload it
	// after the Frida attach). The bridge self-bootstraps Runtime.enable,
	// so contexts populate without any manual enable.
	upDeadline := time.Now().Add(4 * time.Minute)
	for !svc.Status().Connections.Upstream {
		if time.Now().After(upDeadline) {
			fmt.Fprintln(os.Stderr, "no miniapp connected")
			os.Exit(1)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Poll until the automatic enable produced execution contexts.
	ctxDeadline := time.Now().Add(3 * time.Minute)
	var selected string
	for {
		contexts := svc.Contexts()
		if len(contexts) > 0 {
			fmt.Printf("contexts=%v\n", contexts)
			selected = contexts[0].ID
			break
		}
		if time.Now().After(ctxDeadline) {
			fmt.Fprintln(os.Stderr, "contexts never appeared")
			os.Exit(1)
		}
		time.Sleep(200 * time.Millisecond)
	}

	if err := svc.SelectContext(selected); err != nil {
		fmt.Fprintln(os.Stderr, "select:", err)
		os.Exit(1)
	}
	response, err := svc.Send(ctx, sdk.Request{
		Method: "Runtime.evaluate",
		Params: map[string]any{"expression": "1+1", "returnByValue": true},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "evaluate:", err)
		os.Exit(1)
	}
	fmt.Printf("result=%v error=%v\n", response.Result, response.Error)
}
```

Live-verified on 2026-08-14 (WMPF 25297, WeChat 146.84.28, Tencent Docs):
the miniapp connects, the bridge auto-sends `Runtime.enable` (id 1, empty
`jscontext_id`), the miniapp emits `Runtime.executionContextCreated` for its
contexts, `Contexts()` fills in, and `Runtime.evaluate("1+1")` returns
`value: 2` without any manual enable.

## 4. Requests

```go
// Structured request: the SDK generates a process-unique integer id.
resp, err := svc.Send(ctx, sdk.Request{
	ID:     1, // optional; defaults to a process-unique integer
	Method: "Runtime.evaluate",
	Params: map[string]any{"expression": "1 + 1", "returnByValue": true},
})

// Raw JSON request: caller ids are preserved exactly.
resp, err = svc.SendRaw(ctx, []byte(`{"id":7,"method":"Runtime.evaluate","params":{"expression":"1+1","returnByValue":true}}`))

// Explicit context routing.
resp, err = svc.Send(ctx, sdk.Request{
	Method: "Runtime.evaluate",
	Params: map[string]any{"expression": "1 + 1", "returnByValue": true},
	Route:  sdk.Route{JSContextID: "2"},
})

// Fire-and-forget notification (no id, no response).
err = svc.Notify(sdk.Request{Method: "Runtime.runIfWaitingForDebugger"})
```

Id conventions:

- The WMPF miniapp debug endpoint **rejects non-integer CDP ids**
  (`-32600 Message must have integer 'id' property`), so the SDK default
  for structured sends is a process-unique integer. `SendRaw` still
  preserves a caller-supplied string or JSON numeric id, but a string id
  will be rejected by a real WMPF miniapp.
- Empty `Route` snapshots the selected context; with no context selected it
  uses the empty `jscontext_id` bootstrap route (this is what the automatic
  enable uses). An unknown explicit context returns `ErrUnknownContext`.
- Numeric ids are correlated by normalized decimal text (no `float64`), so
  ids beyond `2^53`, `uint64` maximum, fractions, and huge exponents work.
- Duplicate pending ids fail with `ErrDuplicateRequestID`.

## 5. Automatic bootstrap details

- One `Runtime.enable` is sent per upstream connection through the normal
  outbound path (empty `jscontext_id`).
- Reconnects install a new upstream generation and trigger a fresh enable
  with an incrementing id.
- The automatic request is registered in a **private correlator scope**;
  its response is resolved by the bridge and **swallowed** (never broadcast
  to CDP clients or the SDK), so it can never satisfy or be confused with a
  client request that reused the same id.
- A later explicit `Runtime.enable` from your code is idempotent.

## 6. Events

```go
cdp := svc.SubscribeCDP(sdk.SubscriptionOptions{Buffer: 128})
defer cdp.Close()
go func() {
	for event := range cdp.Channel() {
		fmt.Printf("cdp method=%s payload=%s\n", event.Method, event.Payload)
		if event.Response != nil {
			fmt.Printf("response id=%v result=%v error=%+v\n", event.Response.ID, event.Response.Result, event.Response.Error)
		}
	}
}()

contexts := svc.SubscribeContexts()
defer contexts.Close()
go func() {
	for event := range contexts.Channel() {
		fmt.Printf("context %s id=%s target=%s\n", event.Kind, event.Context.ID, event.Context.Target)
	}
}()
```

`SubscribeLogs` and `SubscribeStatus` provide the same pattern. Each
subscription uses an independent bounded queue; a full queue drops only
that subscriber and `sub.Err()` reports `sdk.ErrSlowSubscriber`.

## 7. Contexts and capture

```go
contexts := svc.Contexts()
if len(contexts) != 0 { _ = svc.SelectContext(contexts[0].ID) }
if err := svc.StartRecording("capture.bin"); err != nil { return err }
defer svc.StopRecording()
if err := svc.Replay(ctx, "capture.bin"); err != nil { return err }
```

Context lists are deterministic. Context ids from CDP are preserved as
decimal strings (`"2"`, `"6"`, `"7"`...). On upstream disconnect the
registry clears and publishes one removal event per context in the same
deterministic order.

## 8. Native target management

```go
targets, err := svc.Discover(ctx)
if err != nil { return err }
if len(targets) > 0 {
	if err := svc.Attach(ctx, targets[0].PID); err != nil { return err }
	defer svc.Detach(context.Background())
}
```

`Discover` reads one CIM snapshot (PID, parent PID, name, path, WMPF
version). `Attach` re-enumerates and validates the exact process identity
before unloading a previous session. The bridge normally attaches
automatically at `Start`; these calls are for explicit re-targeting.

## 9. Errors

```go
resp, err := svc.Send(ctx, sdk.Request{Method: "Runtime.evaluate"})
if errors.Is(err, sdk.ErrNoUpstream) {
	// no miniapp is connected
} else if errors.Is(err, sdk.ErrTimeout) {
	// request timed out
} else if errors.Is(err, sdk.ErrClosed) {
	// service closed
}
if errors.Is(err, sdk.ErrNoContext) {
	// no JavaScript context selected (bootstrap has not produced one yet)
}
var structured *sdk.Error
if errors.As(err, &structured) {
	fmt.Printf("op=%s component=%s cause=%v\n", structured.Op, structured.Component, structured.Err)
}
```

Use `errors.Is` for sentinels (`ErrClosed`, `ErrNoUpstream`, `ErrNoContext`,
`ErrUnknownContext`, `ErrDuplicateRequestID`, `ErrTimeout`,
`ErrUpstreamDisconnected`, `ErrSlowSubscriber`, native errors...) and
`errors.As` for `*sdk.Error` / `*sdk.NativeRuntimeError` details.

## 10. Notes

- Build a consumer Windows executable with
  `CGO_ENABLED=1 go build -tags frida`; untagged builds keep the Go
  proxy/replay surface but do not attach a native target.
- The module never exposes internal packages, cgo pointers, Frida handles,
  or WebSocket objects; import only `github.com/Follen/miniapp-bridge/sdk`.
- The SDK keeps the reference bridge semantics: ports, startup order, WMPF
  protobuf/zlib framing, CDP conversion, request/event ordering, context
  routing, Agent embedding, and reverse-order shutdown.

See [`docs/sdk.md`](docs/sdk.md) for the full contract and
[`docs/verification.md`](docs/verification.md) for the verification commands.
