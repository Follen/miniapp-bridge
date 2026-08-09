# Public Go SDK

## Module and package boundary

The module path is `github.com/Follen/miniapp-bridge`. The only supported external package in this change is `github.com/Follen/miniapp-bridge/sdk`. Public declarations must not contain `internal` package types, cgo pointers, GLib objects, Frida handles, or WebSocket implementation objects. The command in `cmd/miniapp-bridge` is a thin adapter over the same SDK Service and never owns a second protocol implementation.

## Construction and lifecycle

`New(Options) (*Service, error)` validates options and allocates in-memory state only. It does not listen, load a DLL, attach, register process-global signals, or exit the process. Zero options preserve ports `127.0.0.1:9421` and `127.0.0.1:62000`, automatic target discovery/attach, embedded address configuration, and the reference startup order.

`Start(ctx)` is safe to call concurrently. One call performs listener creation, upstream/CDP serving, target discovery, Frida attach, and Agent load; concurrent callers wait for that same result. It returns only after all required startup stages are ready. A running repeated Start is idempotent. Cancellation before readiness rolls back every acquired resource and returns a context error. After readiness, cancellation of the lifetime context starts asynchronous orderly Close. Starting a stopped or failed Service returns a typed closed/state error.

`Close(ctx)` is safe to call before Start, concurrently, repeatedly, and after a timeout. The first call initiates one internal shutdown; each caller independently waits with its own context. A caller timeout only ends its wait and never abandons resource cleanup. The final close result is stored and returned to later callers. Shutdown order is: reject new commands, close client writers and fail pending requests, close listeners/servers, stop replay and recording, unload Agent script, detach session, close device, release native runtime, publish terminal status, then close subscription channels.

## Public data and errors

The package exposes versioned DTOs for `Options`, `Status`, `State`, `Target`, `TargetStatus`, `ConnectionStatus`, `NativeStatus`, `JSContext`, `RecordingStatus`, `LogEvent`, `CDPRequest`, `CDPResponse`, `CDPEvent`, `CDPError`, and `Route`. `Status()` and all list methods return copies with deterministic context ordering. Errors use sentinel values plus structured wrappers with `Unwrap`, including invalid options/state, closed/not started, no upstream/context, duplicate/unknown request, disconnect, timeout, slow subscriber, protocol/corrupt frame, and native missing/arch/hash/version/ABI/export/load failures.

## Target and context operations

The Service provides `Discover(ctx)`, `Attach(ctx, Target)`, and `Detach(ctx)` with explicit state transitions and idempotent detach. It provides `Contexts()`, `SelectedContext()`, and `SelectContext(id)`. Context add/connect/remove messages update the registry in the same serialized dispatcher as protocol traffic. Lists are stable; deleting the selected context chooses a deterministic remaining context or no selection. A request may specify a context route; an explicit route overrides the selected context, while an empty route snapshots the selected context at dispatch time.

## CDP requests, events, and subscriptions

The Service provides structured request/response methods and raw JSON methods. Structured calls allocate process-unique monotonic IDs and decode result/error separately. Raw calls preserve valid string or numeric IDs, reject malformed requests and duplicate pending IDs, and do not add notifications to the pending table. Responses resolve the correct waiter, including errors and unknown IDs; upstream disconnect, Service Close, and caller cancellation terminate waiters with typed errors. All WebSocket and SDK traffic uses one serialized protocol dispatcher so WMPF sequence and observable event order remain compatible with the reference.

Logs, status snapshots, and CDP events are delivered through subscription handles with independent bounded queues. Publishing is non-blocking and never runs subscriber code. Queue overflow disconnects only that subscription and reports `ErrSlowSubscriber`; cancellation and repeated Close are idempotent, preserve per-subscription order, and close channels only after the final error/status is observable. No slow client or subscriber can block native callbacks, protocol routing, another client, or Service Close.

## WMPF and capture behavior

The existing structured Protobuf schema and reflection/golden codecs remain authoritative for outer transport, debug messages, ChromeDevtools payloads, CustomMessage, context messages, unknown fields, and nested messages. Compression flags continue to select zlib 1.3.1 behavior. Invalid/truncated frames return diagnostics, preserve raw bytes where possible, and never crash the Service. Recording writes exact raw frames plus direction/timestamp metadata with bounded frame validation. Recording start/stop is synchronized with dispatch. Replay is synchronous from the caller perspective, accepts context cancellation, validates the complete capture before submission, and submits frames through the same dispatcher in original order.

## Native runtime and platform boundary

Native integration is hidden behind a minimal C ABI and a Go loader. Windows amd64 loads `miniapp-frida.dll` from the executable directory or an explicit absolute path using `LoadLibraryExW`/`GetProcAddress`; it never searches CWD, PATH, registry, or global installations. Go validates PE architecture and SHA-256 before load, then checks ABI, Frida core version, native version, zlib version, and every required export. C pointers remain inside `internal/frida`; callbacks copy message bytes and enqueue work instead of running SDK subscribers on the GLib/Frida callback stack.

The pinned runtime is Frida core 17.3.2 with ABI version 1 and the matching zlib 1.3.1 implementation. Runtime loading is reference-counted and serialized; conflicting native paths return a typed error. The last Service releases script, session, device, native runtime, and DLL only after all callback work has drained. `NativeVersion`, `CheckNativeRuntime`, and `PrepareNativeRuntime` expose version and diagnostics without exposing handles. Prepare downloads a pinned ZIP into an atomic per-version cache, verifies ZIP/DLL hashes and manifest/export set, rejects zip-slip and partial files, and supports verified offline hits and explicit offline misses.

## CLI, configuration, and packaging

The CLI parses flags, creates SDK Options, handles SIGINT/SIGTERM, prints structured status/errors, and maps returned errors to existing exit codes. It does not call `os.Exit` or `log.Fatal` from SDK/core code, own native handles, or duplicate shutdown logic. Address configurations are embedded with deterministic version selection and can be overridden by an explicit directory. Release tooling produces `miniapp-frida-native-<version>-windows-amd64.zip` containing the DLL, manifest, licenses, third-party notices, and `SHA256SUMS`; DLL binaries are excluded from the source module.

## Compatibility and verification

The bridge retains the exact listener addresses, startup sequence, WMPF upstream and CDP WebSocket directions, ID correlation, event broadcast, error propagation, context routing, Agent message metadata, and graceful shutdown semantics of the fixed reference commit. Verification must include external-module compilation, full unit and race coverage, protocol differential fixtures, fake-proxy behavior, slow-consumer isolation, native loader/download matrices, Windows native build, and a live matrix covering DevTools initialization plus representative Runtime, Debugger, Page, DOM, Network, Console, Performance, exception, breakpoint, context, reconnect, long-payload, concurrency, and shutdown cases. Any environment-dependent gap is explicitly listed in the final verification report.
