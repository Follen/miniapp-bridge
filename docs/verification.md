# Verification Contract

Module: `github.com/Follen/miniapp-bridge`

Reference: `evi0s/WMPFDebugger` commit
`2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d`

This file is the stable release verification contract. The current command exit
statuses, output summaries, acceptance mapping, skipped checks, and residual
risks are recorded in the Comet Native report at
[`comet/changes/public-go-sdk-worktree/verification.md`](comet/changes/public-go-sdk-worktree/verification.md).
Typed receipts under that change bind every result to the exact contract,
implementation scope, source revision, and worktree snapshot. Old receipts are
never evidence for a changed scope.

## Required Commands

Every release candidate must complete all of these checks with exit status `0`:

```powershell
go mod tidy
go test ./sdk -count=1
go test ./... -count=1
go test ./scripts -run '^TestExternalModuleImportsOnlySDK$' -count=1 -v

$env:MINIAPP_BRIDGE_NATIVE_PATH = (Resolve-Path '.\dist\miniapp-frida.dll').Path
go test -v -tags frida ./internal/... -count=1
go test -v -tags frida ./sdk -count=1

powershell -NoProfile -ExecutionPolicy Bypass -File scripts/coverage-gate.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-windows.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/native-release.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/native-prepare.ps1 -Offline `
  -ExpectedArchiveSHA256 <release-sha256>

git ls-files '*.dll' '*.zip' '*.lib' '*.a' '*.exe'
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/smoke-windows.ps1 `
  -UpstreamWaitSeconds 300 -CDPMode all
git diff --check
```

The `git ls-files` command must produce no output. The source Module publishes
Go and loader source only; native binaries are Release assets.

## Deterministic Gates

- All declared Go statement scopes must report exactly `100.0%`: CLI/Frida Go,
  `internal`, public `sdk`, tagged internal+SDK, and the smoke process runner.
- Unit, race, tagged race, and vet must pass.
- Differential fixtures cover 55 protobuf types, 131 fields, explicit-zero
  values, malformed frames, every reference Codex category, and command
  direction.
- Simulated proxy tests cover request IDs, responses/errors, notifications,
  event order, multiple clients and contexts, long/concurrent traffic,
  disconnect/reconnect, recording/replay, and corrupt-frame recovery.
- External-Module tests import only `github.com/Follen/miniapp-bridge/sdk` and
  exercise `New`, `Start`, and `Close` without repository-private libraries.
- Native tests cover PE architecture, hash, manifest, ABI, version, exports,
  dependency/load failures, download/cache/offline behavior, atomic install,
  and exact Agent -> session -> device -> runtime shutdown order.

## Live Acceptance

After the smoke prints `action-required=open-or-reload-miniapp`, open or reload
the target miniapp. A passing `CDPMode all` run must emit all of these facts:

- `listeners: 9421=true 62000=true`
- a Frida attach line and `upstream-peer-validated=true`
- `cdp-step=matrix passed=true`
- `cdp-step=link passed=true`
- `cdp-step=interaction passed=true`
- `cdp-coverage=full mode=all acceptance=true`
- a correlated new or reused renderer selection
- `child-exit-code=0`
- successful rebinds for ports `9421` and `62000`
- `target-survives-shutdown=true`
- Agent unload, session detach, device close, and native runtime release markers
- `smoke-success=true`

The matrix covers Runtime, Debugger, Page, DOM, Network, Console, Performance,
objects, exceptions, console events, script parsing, breakpoints, pause/resume,
call frames, contexts, long messages, concurrent requests, structured errors,
reconnect, mouse input, keyboard input, and graceful shutdown. Assertions match
events by payload, breakpoint, location, or request ID and do not impose CDP
response/event ordering that the protocol does not guarantee.

## Pinned Artifacts

The Windows amd64 runtime is Frida core `17.3.2`, native ABI `1`, and zlib
`1.3.1`. The pinned DLL SHA-256 is
`05CF2B66A6A031E813FEB1C0A895A1272A68770233C3F270272243F48D11E846`.
The reproducible release ZIP SHA-256 is checked by the current Native report;
the release script also writes `dist/native/SHA256SUMS`.

## Evidence Hygiene

Generated binaries, archives, coverage profiles, caches, `.comet/current-change.json`,
and stale receipts are excluded from source commits. Any code, test, public doc,
or protocol fixture change requires a new implementation scope and fresh
receipts. The pre-SDK historical record remains in
[`verification-current.md`](verification-current.md) and is not release
evidence for the public SDK.
