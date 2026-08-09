# Verification Record

Date: 2026-08-09 (Windows amd64 checkout)
Module: `github.com/Follen/miniapp-bridge`
Reference: `evi0s/WMPFDebugger` commit `2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d`

This record describes commands run in the `comet/public-go-sdk` worktree. A
command is listed as passing only when it completed in this worktree with exit
status `0`.

## Commands Run

| Command | Exit | Observed result |
|---|---:|---|
| `go mod tidy` | 0 | Module path and imports resolve without dependency changes. |
| `go test ./... -count=1` | 0 | All packages, SDK example, native validation tests, protocol fixtures, and simulated proxy tests passed. |
| `go vet ./...` | 0 | No diagnostics. |
| `go test -race ./... -count=1` | 0 | All non-tagged packages passed under the race detector. |
| `go test ./scripts -run TestExternalModuleImportsOnlySDK -count=1 -v` | 0 | A temporary external module compiled through `replace` and imported only `github.com/Follen/miniapp-bridge/sdk`. |
| `go test -tags frida ./internal/frida -run TestNativeAgentLifecycleAndReattach -count=1 -v` | 0 | The pinned runtime attached to the current WMPF 25297 host, loaded/unloaded the embedded Agent, detached, and reattached. |
| `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/coverage-gate.ps1` | 0 | Unit, internal, smoke-runner, SDK, and tagged internal statement profiles each reported `100.0%`; tagged race and vet passed. Final line: `coverage_gate=100% reference behaviors; internal_go_statements=100.0%; sdk_go_statements=100.0%; smoke_runner_go_statements=100.0%; unit/race/tagged-race/vet=passed`. |
| `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-windows.ps1` | 0 | Rebuilt zlib and the Frida shim, ran tagged race tests, built `dist/miniapp-bridge.exe`, and copied `dist/miniapp-frida.dll`. |
| `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/native-release.ps1` | 0 | Produced the reproducible versioned Windows amd64 ZIP (fixed entry order and timestamps), embedded manifest/licenses/notices, and `SHA256SUMS`; archive contents and hashes were checked twice. |
| `scripts/native-prepare.ps1 -Offline` with the generated ZIP and expected SHA-256 | 0 | Atomic install completed; installed DLL SHA-256 matched `05CF2B66A6A031E813FEB1C0A895A1272A68770233C3F270272243F48D11E846`. |
| `go test ./scripts -run Zlib -count=1` and concurrent offline zlib builds | 0 | Isolated object directories prevent concurrent `ar.exe` cleanup races; all builds produced zlib `1.3.1` and library SHA-256 `9DE45B674DA1FC9F11D3E1833CFC6FA98AE27468D5F0E222556E65FC9B950D2A`. |
| `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/smoke-windows.ps1 -UpstreamWaitSeconds 180 -CDPMode all` | 1 | Listeners started and Frida attached to PID `8996`, version `25297`; no miniapp renderer opened the WMPF upstream socket before the 180-second deadline. Cleanup completed, both ports rebound successfully, and the script did not emit `smoke-success=true`. |
| `git diff --check` | 0 | No whitespace errors. |

Build artifacts from this run:

- `dist/miniapp-bridge.exe`: SHA-256 `387ACA4EF33AF541BD6CBF10F6CB942F2997AD05972EF070C98D0DEAA393844E`
- `dist/miniapp-frida.dll`: size `67,788,288`; SHA-256 `05CF2B66A6A031E813FEB1C0A895A1272A68770233C3F270272243F48D11E846`
- `dist/native/miniapp-frida-native-17.3.2-abi1-windows-amd64.zip`: size `27,922,807`; SHA-256 `1597ADCC6B3B13B5BCBA910904046AB7D2E1E3D73AE16961C73E400373BDE87A`

The coverage gate and native build are deterministic local evidence. The live
run proves listener startup, native attach/Agent lifecycle, graceful bridge
exit, and immediate port rebinding. It does not prove the CDP matrix because no
miniapp upstream connected during the bounded wait.

## Covered Behavior

- Reference protobuf schema, field numbers, nested messages, unknown fields,
  compression flags, zlib framing, malformed-frame recovery, and golden/
  differential fixtures.
- CDP request IDs, structured and raw requests, responses/errors, notifications,
  event ordering, long payloads, concurrent requests, reconnect, multi-client
  broadcast, and `jscontextId` selection/routing through simulated upstreams.
- SDK lifecycle idempotency, concurrent Start/Close, cancellation and timeout
  waits, pending-request wakeup, bounded subscriptions and slow-subscriber
  isolation, recording/replay, target operations, status/log/context events,
  native manifest/hash/download/cache errors, and external-module isolation.
- CLI argument/exit behavior, fixed listener addresses, startup ordering,
  address configuration selection, Agent embedding, native loader contracts,
  Windows shim build, release ZIP structure, and SHA-256 manifests.

## Remaining Real-Environment Validation

1. A live target must be refreshed after attachment and exercised through the
   full DevTools matrix: Runtime, Debugger, Page, DOM, Network, Console,
   Performance, exceptions, breakpoints/pause, call frames, context switching,
   long/concurrent requests, reconnect, and graceful shutdown.
2. The live run must confirm the upstream peer survives shutdown. Immediate
   rebinding of `127.0.0.1:9421` and `127.0.0.1:62000` already passed after the
   bounded no-upstream run.
3. Additional WMPF address versions and non-Windows native implementations need
   their platform-specific live checks; Windows amd64 Frida core `17.3.2` is the
   pinned first-platform artifact in this change.

## Reproduction

```powershell
powershell -ExecutionPolicy Bypass -File scripts/native-prepare.ps1 `
  -ExpectedArchiveSHA256 <release-sha256>
powershell -ExecutionPolicy Bypass -File scripts/build-windows.ps1
powershell -ExecutionPolicy Bypass -File scripts/coverage-gate.ps1
powershell -ExecutionPolicy Bypass -File scripts/smoke-windows.ps1
```

Before a production release, append the live command output, native manifest
hash, `target-survives-shutdown`, and `ports-released` result to this record.
