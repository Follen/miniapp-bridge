# Public Go SDK

Module path: `github.com/Follen/miniapp-bridge`
Package: `github.com/Follen/miniapp-bridge/sdk`

The SDK and `miniapp-bridge` CLI use the same Service implementation. The
public package does not expose internal packages, cgo pointers, Frida handles,
or WebSocket objects.

## Minimal service

```go
svc, err := sdk.New(sdk.Options{})
if err != nil { return err }
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
if err := svc.Start(ctx); err != nil { return err }
defer svc.Close(context.Background())
```

Zero options preserve `127.0.0.1:9421` for WMPF upstream and
`127.0.0.1:62000` for the CDP WebSocket. `Start` returns after listeners and
the configured native starter are ready. On Windows builds with native support,
zero options discover the target, attach Frida, and load the embedded Agent.
The binary bundles 47 historical address configurations. The current
live-supported target is WMPF 25297 on Windows amd64; the other configurations
are compatibility data, not a production support claim.
`Options.AddressConfigDir` is an explicit per-Service override. Cancelling the
lifetime context starts an orderly asynchronous close. `Close` is safe before
Start, concurrently, repeatedly, and after a caller timeout.

On Windows, `Discover` reads one CIM process snapshot and returns each target's
PID, parent PID, process name, executable path, and WMPF version. A discovered
target therefore carries the metadata required by `Attach` without a second
caller-side lookup. `Attach` still re-enumerates the requested PID and checks
every supplied field plus the process start-time/host identity before unloading
the previous session; its published target status uses the revalidated metadata.

## CDP and events

```go
sub := svc.SubscribeCDP(sdk.SubscriptionOptions{Buffer: 128})
defer sub.Close()
if _, err := svc.Send(ctx, sdk.Request{Method: "Runtime.enable"}); err != nil {
	return err
}
response, err := svc.Send(ctx, sdk.Request{
	Method: "Runtime.evaluate",
	Params: map[string]any{"expression": "1 + 1", "returnByValue": true},
})
if err != nil { return err }
_ = response
```

An empty route snapshots the selected context at dispatch time. When no context
has been discovered yet, it preserves the reference bridge bootstrap behavior
and sends an empty `jscontext_id`; this allows `Runtime.enable` to produce the
initial `Runtime.executionContextCreated` event. An explicit route overrides the
selection, and an unknown explicit context returns `ErrUnknownContext`.

The bridge also sends one automatic `Runtime.enable` through that empty
bootstrap route whenever an upstream (miniapp) transport connects, including
after a reconnect, so `Contexts()` and `Status().Contexts` populate without
any manual client enable. The automatic request resolves through the shared CDP
correlator, and a later explicit `Runtime.enable` from a client is idempotent.
`SendRawRoute` provides the same routing for raw JSON.
`Runtime.executionContextCreated`, `Runtime.executionContextDestroyed`, and
`Runtime.executionContextsCleared` update the same registry as WMPF's private
context messages; numeric CDP context IDs are preserved as decimal strings.
Structured requests receive a process-unique `sdk-*` ID. `SendRaw` preserves a
valid string or JSON numeric caller ID and rejects duplicate pending IDs. Numeric
IDs are correlated from their normalized decimal value without conversion through
`float64`; this includes integers beyond `2^53`, `uint64` maximum, fractions, and
legal exponents outside IEEE-754 range. Structured numbers exposed through
`CDPEvent.Params`, `Response.Result`, and `CDPError.Data` are `json.Number` values,
so their original JSON text remains available. Responses, CDP errors,
notifications, and events retain the reference ordering. Pending
waiters are terminated with `ErrUpstreamDisconnected` or `ErrClosed` when the
connection/service ends.

Log, status, CDP, and context subscriptions use independent bounded queues.
Publishing never waits for a subscriber. A full queue disconnects only that
subscription; `sub.Err()` returns `sdk.ErrSlowSubscriber`.

## Contexts and capture

```go
contexts := svc.Contexts()
if len(contexts) != 0 { _ = svc.SelectContext(contexts[0].ID) }
if err := svc.StartRecording("capture.bin"); err != nil { return err }
defer svc.StopRecording()
if err := svc.Replay(ctx, "capture.bin"); err != nil { return err }
```

Context lists are deterministic. `Status` includes listener ports, client
counts, selected context, native state, recording/lifecycle timestamps, and the
last structured error. Selected and removed context events retain both the
context ID and target name. Upstream generation disconnect clears the registry
and publishes one removal per context in the same deterministic order.

## Native runtime

The pinned runtime is Frida core `17.3.2`, native ABI `1`, native version
`17.3.2-abi1.1`, and `miniapp-frida.dll`. The SDK can validate a DLL or prepare a
verified cache without exposing a handle:

```go
path, err := sdk.PrepareNativeRuntime(ctx, sdk.NativePrepareOptions{
    Offline: false,
    ExpectedArchiveSHA: "<release SHA-256>",
})
if err != nil { return err }
if err := sdk.CheckNativeRuntime(path); err != nil { return err }
status, err := sdk.InspectNativeRuntime(path)
if err != nil { return err }
_ = status
```

Preparation uses a lock, `.partial` download, SHA-256 verification, manifest
and PE amd64 checks, zip-slip rejection, staging extraction, and atomic install.
`Offline: true` accepts only a verified cache. Native releases are published
as `miniapp-frida-native-<version>-windows-amd64.zip` beside a manifest,
licenses, third-party notices, and `SHA256SUMS`; DLLs are not committed to the
Go source module.

Build a consumer's Windows native executable with `CGO_ENABLED=1 go build -tags frida`.
The Module compiles only the Win32 loader and does not require
Frida headers, a Frida import library, zlib headers, `libz`, or `zlib1.dll`.
Put the prepared `miniapp-frida.dll` and `manifest.json` beside that executable.
Untagged builds retain the Go proxy/replay surface but do not auto-discover or
attach a native target.

## Errors and compatibility

Use `errors.Is` for sentinel errors such as `ErrClosed`,
`ErrDuplicateRequestID`, `ErrSlowSubscriber`, `ErrNativeOffline`, and native
hash/architecture/manifest errors. Use `errors.As` for `*sdk.Error` and
`*sdk.NativeRuntimeError` to inspect operation/component/path/version details.

The public SDK follows Go module SemVer. The `github.com/Follen/miniapp-bridge/sdk`
API, structured error types, status values, subscription contracts, request
correlation behavior, native version constants, and documented default ports are
stable within a major version. Breaking SDK changes require a new major module
version; additive options, status fields, event fields, and error details may be
introduced in minor releases without changing existing behavior.

The minimum supported Go version is the `go` directive in `go.mod`; this
release requires Go `1.23` or newer. Patch releases keep the same minimum Go
version unless a security or toolchain fix requires otherwise. A future increase
to the minimum Go version is treated as a compatibility item and is documented
in the release notes before publishing.

The SDK keeps the reference ports, listener-before-attach startup sequence,
WMPF protobuf/zlib framing, CDP conversion, request/event semantics, context
routing, Agent embedding, and reverse-order shutdown. Windows is the first
native platform; other platforms keep the same Go-level abstraction and return
diagnostic native availability errors when no runtime is installed.

## Module boundary and verification

Consumers should import only `github.com/Follen/miniapp-bridge/sdk`; the CLI
and `internal` packages are implementation details. A temporary external Go
module using `replace github.com/Follen/miniapp-bridge => <checkout>` is tested
by `TestExternalModuleImportsOnlySDK`. The test covers both untagged and
`-tags frida` builds, asserts WMPF itself has no cgo files, and inspects the PE
import table for forbidden Frida/zlib DLL imports. The exact command is:

```powershell
go test ./scripts -run TestExternalModuleImportsOnlySDK -count=1 -v
```

Lifecycle, subscriptions, correlation, contexts, capture/replay, and native
manifest error contracts are covered by package tests. See
[`verification.md`](verification.md) for the stable command contract and its
linked Native report containing exact exits, key outputs, acceptance status,
and live result.

Hosted native tests use `-tags frida` and require no WeChat/WMPF process.
Interactive process enumeration, attach, Agent lifecycle, and reattach tests
use `-tags "frida live"` and remain part of the separate Windows live gate.
