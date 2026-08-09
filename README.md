# miniapp-bridge

`miniapp-bridge` is a Go port of WMPFDebugger. It exposes the same default endpoints:

- WMPF debug WebSocket: `127.0.0.1:9421`
- CDP WebSocket: `127.0.0.1:62000`
- DevTools: `devtools://devtools/bundled/inspector.html?ws=127.0.0.1:62000`

The Go process owns discovery, Frida lifecycle, protobuf/zlib handling, CDP routing, contexts, capture, configuration, and logs. The embedded JavaScript Agent only patches the runtime and forwards messages. Node.js is not required at runtime.

## Build and test

```powershell
go test ./...
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1
```

Run `miniapp-bridge.exe --help` for all CLI options.

`powershell -ExecutionPolicy Bypass -File scripts\coverage-gate.ps1` runs the deterministic reference-behavior gate and requires real Go statement coverage of `internal/...`, the public `sdk`, and the Windows smoke process runner to be exactly 100.0%. It checks all 55 protobuf types, 131 field fixtures, explicit-zero and corrupt frames, every Codex category/command direction, then unit, race, tagged-native race, and vet. Windows native checks use `scripts\build-windows.ps1`; live parity additionally requires the target to open the WMPF upstream socket on `127.0.0.1:9421`.

## Frida packaging

The native integration is pinned to `frida-core 17.3.2`, matching the reference dependency line. `scripts/ensure-frida-devkit.ps1` downloads the official `frida-core-devkit-17.3.2-windows-x86_64.tar.xz` archive when needed, verifies its SHA-256 (`8AF15423D6E534626F91A67FAA0582E42C67A07A95A190F4C622695105549C72`), and extracts it under `third_party/frida/devkit-17.3.2`. The download and extracted SDK are ignored build caches; an exclusive cache lock serializes concurrent builds, and extracted header/library hashes are checked before every native build. The first uncached native build needs access to the pinned GitHub release. A verified extracted SDK remains usable offline even if the download archive has been cleaned; `-Offline` makes a missing or invalid cache fail without network access.

`scripts/build-frida-shim.ps1` uses MSVC to encapsulate the official static library in `miniapp-frida.dll`. Windows cgo compiles only a small `LoadLibraryExW`/`GetProcAddress` loader; it does not link a Frida import library, and Frida pointers never enter Go business packages. `scripts/build-windows.ps1` builds the shim, runs `go test -tags frida ./...`, builds the tagged executable, and copies the DLL and manifest beside it in `dist`.

Windows compression is pinned to official zlib 1.3.1 from
`https://zlib.net/fossils/zlib-1.3.1.tar.gz` (SHA-256
`9A93B2B7DFDAC77CEBA5A558A580E74667DD6FEDE4585B91EEFB60F03B72DF23`).
`scripts/build-zlib.ps1` verifies the archive, restores the ignored download
and extraction caches (`third_party/downloads/cache/` and
`third_party/zlib/src-1.3.1/`), and checks the header version. The shim build
compiles that stock source with MSVC and embeds it in `miniapp-frida.dll`; the
Go executable and external SDK modules do not link `libz.a` or `zlib1.dll`.
This reproduces the reference deflate bytes while keeping the source Module
self-contained. The first uncached shim build requires the pinned archive;
verified caches support repeatable offline builds.

The final executable does not use Node.js. Node and the reference repository's locked development dependencies are only used to regenerate differential fixtures:

```powershell
corepack yarn install --frozen-lockfile --ignore-scripts --cwd .reference\WMPFDebugger-2b90b77
node scripts\generate-reference-fixtures.js
node scripts\generate-reference-codex-fixtures.js
go test ./internal/wmpf -run TestReferenceGeneratedGoldenMessages
go test ./internal/wmpf -run TestReferenceCodex
```

## Public Go SDK

Other Go projects can import the stable package directly:

```go
import "github.com/Follen/miniapp-bridge/sdk"

service, err := sdk.New(sdk.Options{})
if err != nil { return err }
if err := service.Start(ctx); err != nil { return err }
defer service.Close(context.Background())
```

The CLI uses the same Service. See [`docs/sdk.md`](docs/sdk.md) and
[`examples/sdk`](examples/sdk) for CDP requests, subscriptions, contexts,
capture/replay, structured errors, and native preparation. The module path is
`github.com/Follen/miniapp-bridge`; consumers never import `internal` or link a
Frida import library. Windows native builds use the SDK's zero-configuration
target discovery/attach/Agent load path. The 47 WMPF address configurations are
compiled into the binary; SDK callers can set `Options.AddressConfigDir` only
when an explicit configuration override is required.

Consumer applications enable the Windows native backend with `go build -tags frida`.
This requires a Windows cgo compiler, but no Frida or zlib development
files. Deploy `miniapp-frida.dll` and `manifest.json` from the matching Release
asset beside the final executable; the loader never searches CWD, PATH, the
registry, or a global installation.

The external-module boundary is exercised with:

```powershell
go test ./scripts -run TestExternalModuleImportsOnlySDK -count=1 -v
```

Current verification evidence, including the strict coverage result and native
runtime prerequisites, is tracked in [`docs/verification.md`](docs/verification.md).

To prepare the pinned Windows runtime into the executable directory, run:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\native-prepare.ps1 `
  -ExpectedArchiveSHA256 <release-sha256>
```

Use `-Offline` to require an already verified cache. Release assets are
`miniapp-frida-native-17.3.2-abi1-windows-amd64.zip` with a manifest, licenses,
third-party notices, and `SHA256SUMS`.

## Startup order

Start `miniapp-bridge`, then open a target miniapp after the Agent reports that it attached, then open DevTools. The `OnLoadStart` hook only observes miniapps loaded after attachment. The bridge starts both listeners before process discovery and Agent attachment. Shutdown closes WebSockets, unloads the script, detaches the session, and releases the native runtime in reverse order.

The strict Windows validation requires a live target and a WMPF connection owned by the launched bridge. It first runs the live CDP matrix so the first `Runtime.enable` captures initialization events, then runs a link smoke with `Runtime.enable`, `Debugger.enable`, and `Runtime.evaluate` to verify a subsequent client connection. The matrix covers representative DevTools initialization, Runtime, Debugger, Page, DOM, Network, Console, Performance, objects, exceptions, console events, scripts, pause/resume with call frames, long messages, concurrent requests, structured errors, execution contexts, and reconnect:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\smoke-windows.ps1
```

The live script reports `action-required=open-or-reload-miniapp` after Frida is attached. It launches the bridge in a new Windows process group and requests shutdown with `CTRL_BREAK_EVENT`. A successful run verifies Agent unload, Frida detach, listener shutdown, zero process exit, target-process survival, and immediate port rebinding. Force termination is used only as a timed cleanup fallback and makes the smoke fail.

## Reference and copyright

Ported from [evi0s/WMPFDebugger](https://github.com/evi0s/WMPFDebugger), audited at commit `2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d`. Address configurations and Agent behavior retain their source attribution. Protocol code corresponding to the reference `src/third-party` originated from WeChat DevTools and remains copyright Tencent Holdings Ltd. See `THIRD_PARTY_NOTICES.md` for bundled dependency notices.

This project is licensed under GPL-2.0-only. See `LICENSE`.
