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

`powershell -ExecutionPolicy Bypass -File scripts\coverage-gate.ps1` runs the deterministic reference-behavior gate and requires real Go statement coverage of both `internal/...` and the Windows smoke process runner to be exactly 100.0%. It checks all 55 protobuf types, 131 field fixtures, explicit-zero and corrupt frames, every Codex category/command direction, then unit, race, and vet. Windows native checks use `scripts\build-windows.ps1`; live parity additionally requires the target to open the WMPF upstream socket on `127.0.0.1:9421`.

## Frida packaging

The native integration is pinned to `frida-core 17.3.2`, matching the reference dependency line. The official `frida-core-devkit-17.3.2-windows-x86_64.tar.xz` archive is stored under `third_party/downloads` (SHA-256 `8AF15423D6E534626F91A67FAA0582E42C67A07A95A190F4C622695105549C72`) and extracted under `third_party/frida/devkit-17.3.2`.

`scripts/build-frida-shim.ps1` uses MSVC to encapsulate the official static library in `miniapp-frida.dll`. cgo links only the shim's small opaque C ABI; Frida pointers never enter Go business packages. `scripts/build-windows.ps1` builds the shim, runs `go test -tags frida ./...`, builds the tagged executable, and copies the DLL beside it in `dist`.

Windows compression is pinned to official zlib 1.3.1. The committed source archive is `third_party/zlib/zlib-1.3.1.tar.gz` (SHA-256 `9A93B2B7DFDAC77CEBA5A558A580E74667DD6FEDE4585B91EEFB60F03B72DF23`). `scripts/build-zlib.ps1` verifies the archive, restores the ignored extraction cache when needed, checks the header version, and builds a static `libz.a`; this reproduces Node zlib's observable deflate bytes without a `zlib1.dll` runtime dependency.

The final executable does not use Node.js. Node and the reference repository's locked development dependencies are only used to regenerate differential fixtures:

```powershell
corepack yarn install --frozen-lockfile --ignore-scripts --cwd .reference\WMPFDebugger-2b90b77
node scripts\generate-reference-fixtures.js
node scripts\generate-reference-codex-fixtures.js
go test ./internal/wmpf -run TestReferenceGeneratedGoldenMessages
go test ./internal/wmpf -run TestReferenceCodex
```

## Startup order

Start `miniapp-bridge`, then open a target miniapp after the Agent reports that it attached, then open DevTools. The `OnLoadStart` hook only observes miniapps loaded after attachment. The bridge starts both listeners before process discovery and Agent attachment. Shutdown closes WebSockets, unloads the script, detaches the session, and releases the native runtime in reverse order.

The strict Windows validation requires a live target and a WMPF connection owned by the launched bridge. It first runs the live CDP matrix so the first `Runtime.enable` captures initialization events, then runs a link smoke with `Runtime.enable`, `Debugger.enable`, and `Runtime.evaluate` to verify a subsequent client connection. The matrix covers representative DevTools initialization, Runtime, Debugger, Page, DOM, Network, Console, Performance, objects, exceptions, console events, scripts, pause/resume with call frames, long messages, concurrent requests, structured errors, execution contexts, and reconnect:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\smoke-windows.ps1
```

The live script reports `action-required=open-or-reload-miniapp` after Frida is attached. It launches the bridge in a new Windows process group and requests shutdown with `CTRL_BREAK_EVENT`, so Agent unload, Frida detach, listener shutdown, zero process exit, target-process survival, and immediate port rebinding are verified. Force termination is used only as a timed cleanup fallback and makes the smoke fail.

## Reference and copyright

Ported from [evi0s/WMPFDebugger](https://github.com/evi0s/WMPFDebugger), audited at commit `2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d`. Address configurations and Agent behavior retain their source attribution. Protocol code corresponding to the reference `src/third-party` originated from WeChat DevTools and remains copyright Tencent Holdings Ltd. See `THIRD_PARTY_NOTICES.md` for bundled dependency notices.

This project is licensed under GPL-2.0-only. See `LICENSE`.
