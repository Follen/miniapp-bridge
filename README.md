# miniapp-bridge

[English](README.md) | [简体中文](README.zh.md)

[![CI](https://github.com/Follen/miniapp-bridge/actions/workflows/ci.yml/badge.svg)](https://github.com/Follen/miniapp-bridge/actions/workflows/ci.yml)
[![Release](https://github.com/Follen/miniapp-bridge/actions/workflows/release.yml/badge.svg)](https://github.com/Follen/miniapp-bridge/actions/workflows/release.yml)

`miniapp-bridge` exposes a local Chrome DevTools Protocol bridge for Windows
WMPF mini programs. Connect Chromium DevTools over WebSocket, or embed the
public Go SDK to send CDP commands, select JavaScript contexts, subscribe to
events, and record or replay protocol traffic.

The Windows executable discovers WMPF, attaches the embedded Frida Agent, and
owns the complete bridge lifecycle. Node.js is not required at runtime.

## Supported environment

| Component | Supported version |
| --- | --- |
| Operating system | Windows amd64 |
| WMPF | **25297** |
| Frida Core | 17.3.2 |
| Native ABI | 1 (`17.3.2-abi1.1`) |
| Go SDK and source builds | Go 1.23 or newer |

WMPF 25297 is the production and live-verified target for this release.
Address data for other historical builds is compatibility data and is not a
claim of live support.

## Quick start

1. Download `miniapp-bridge-v0.0.8-windows-amd64.zip` from
   [GitHub Releases](https://github.com/Follen/miniapp-bridge/releases/tag/v0.0.8).
2. Extract the archive. Keep `miniapp-bridge.exe`, `miniapp-frida.dll`, and
   `manifest.json` in the same directory.
3. Start the bridge:

   ```powershell
   .\miniapp-bridge.exe
   ```

4. Wait for the Frida attached log, then open or reload the target mini
   program. Loading it before attachment will not trigger the required hook.
5. Open Chromium DevTools with:

   ```text
   devtools://devtools/bundled/inspector.html?ws=127.0.0.1:62000
   ```

Stop the process with `Ctrl+C`. Shutdown closes clients and listeners, unloads
the Agent, detaches the Frida session, and releases the native runtime.

## Endpoints and CLI

The default listeners bind only to loopback:

| Address | Purpose |
| --- | --- |
| `127.0.0.1:9421` | WMPF upstream debug WebSocket |
| `127.0.0.1:62000` | CDP WebSocket for DevTools and other clients |

```text
Usage: miniapp-bridge [options]

Options:
  --debug-port <port>  Remote debug server port (default: 9421)
  --cdp-port <port>    CDP proxy server port (default: 62000)
  --debug-main         Output main process debug messages
  --debug-frida        Output Frida client messages
  --record <file>      Record incoming raw debug frames
  --replay <file>      Replay recorded raw debug frames
  -h, --help           Show this help message
```

Use `--record` to save raw upstream frames. Use `--replay` to feed a capture
back through the same protocol pipeline without attaching a live target.

## Go SDK

The module path is `github.com/Follen/miniapp-bridge`; applications import the
public package `github.com/Follen/miniapp-bridge/sdk`. The full usage guide is
[`SDK.md`](SDK.md) (中文版：[`SDK.zh.md`](SDK.zh.md)):

```powershell
go get github.com/Follen/miniapp-bridge/sdk@v0.0.8
```

```go
package bridge

import (
    "context"
    "time"

    "github.com/Follen/miniapp-bridge/sdk"
)

func Run(ctx context.Context) error {
    service, err := sdk.New(sdk.Options{})
    if err != nil {
        return err
    }
    if err := service.Start(ctx); err != nil {
        return err
    }

    <-ctx.Done()

    closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    return service.Close(closeCtx)
}
```

`Service` also provides structured and raw CDP requests, request correlation,
log/status/CDP/context subscriptions, context selection, target attach and
detach, recording, replay, and structured errors compatible with
`errors.Is`/`errors.As`. Wait for an upstream connection and an execution
context before sending context-bound CDP requests.

See [Public Go SDK](docs/sdk.md) and the [SDK example](examples/sdk) for the
complete API and lifecycle contract.

## Native runtime

The Go module contains source and the Windows loader, but not
`miniapp-frida.dll`. Release assets publish the native runtime separately as
`miniapp-frida-native-17.3.2-abi1.1-windows-amd64.zip` under the compatibility
tag `native-v17.3.2-abi1.1`.

SDK applications that attach WMPF must:

1. Build with `CGO_ENABLED=1` and `-tags frida`.
2. Deploy `miniapp-frida.dll` and its matching `manifest.json` beside the final
   executable.
3. Use `sdk.CheckNativeRuntime` or `sdk.PrepareNativeRuntime` when runtime
   validation or managed caching is required.

The loader does not search `PATH` or a global installation. Missing files,
wrong architecture, ABI mismatch, bad manifests, missing exports, and hash
errors are returned as structured SDK errors. See
[Native release assets](docs/native-release.md) for offline preparation,
cache, packaging, and release details.

## Build from source

Native Windows builds require:

- Go 1.23 or newer
- Windows amd64 and PowerShell
- MinGW-w64 `gcc.exe` and `ar.exe` for cgo
- Visual Studio 2022 C++ Build Tools with MSVC

```powershell
git clone https://github.com/Follen/miniapp-bridge.git
cd miniapp-bridge
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-windows.ps1
```

The script verifies pinned Frida and zlib inputs, builds the native shim, runs
the native test suite, and writes the executable, DLL, and manifest to `dist`.
Verified download caches can be reused for offline rebuilds.

## Testing and troubleshooting

Portable tests and the deterministic Windows gate are separate from the live
WMPF matrix:

```powershell
go test ./... -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\coverage-gate.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\smoke-windows.ps1 -CDPMode all
```

Common checks:

- **No target attached:** confirm the running WMPF build is 25297. Start the
  bridge first, then open or reload the mini program after attachment.
- **DevTools cannot connect:** check that port 62000 is listening and is not
  occupied by another process.
- **No upstream:** check that WMPF connected to port 9421 after the mini program
  was loaded.
- **Native load error:** keep the matching DLL and manifest beside the EXE;
  inspect the returned structured error for architecture, version, ABI, export,
  or hash details.

See [verification](docs/verification.md), the
[behavior matrix](docs/behavior-matrix.md), and
[known differences](KNOWN-DIFFERENCES.md) for deeper validation details.

## License

miniapp-bridge is licensed under [GPL-2.0-only](LICENSE).

Third-party licenses and notices are collected in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
