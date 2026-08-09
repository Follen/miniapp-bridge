# miniapp-bridge

[English](README.md) | [简体中文](README.zh.md)

[![CI](https://github.com/Follen/miniapp-bridge/actions/workflows/ci.yml/badge.svg)](https://github.com/Follen/miniapp-bridge/actions/workflows/ci.yml)
[![Release](https://github.com/Follen/miniapp-bridge/actions/workflows/release.yml/badge.svg)](https://github.com/Follen/miniapp-bridge/actions/workflows/release.yml)

`miniapp-bridge` is a Go and Frida port of
[evi0s/WMPFDebugger](https://github.com/evi0s/WMPFDebugger). It discovers and
attaches to a Windows WMPF process, converts the private WMPF debug protocol to
standard Chrome DevTools Protocol (CDP), and exposes both a standalone CLI and
a public Go SDK.

The Go process owns process discovery, Frida lifecycle, Protobuf and zlib,
request correlation, context routing, WebSocket serving, capture/replay,
configuration, and logging. The embedded JavaScript Agent only patches the
target runtime and forwards raw messages. The final program does not require
Node.js.

## Endpoints

The zero-configuration service preserves the reference endpoints and startup
order:

- WMPF debug WebSocket: `127.0.0.1:9421`
- CDP WebSocket proxy: `127.0.0.1:62000`
- DevTools URL:
  `devtools://devtools/bundled/inspector.html?ws=127.0.0.1:62000`

Both listeners start before target discovery, Frida attach, and Agent load.
After the bridge reports a successful attach, open or reload the target
miniapp, then open the DevTools URL. The `OnLoadStart` hook can only observe
loads that occur after attachment.

## Platform status

Windows amd64 is the only native production target in the current release.
The native package is pinned to:

- Frida core `17.3.2`
- miniapp native ABI `1` (`17.3.2-abi1`)
- zlib `1.3.1`

The repository contains platform abstractions and portable protocol tests, but
it does not claim native target discovery, attach, packaging, or live parity on
macOS, Linux, Windows arm64, or other systems. Untagged builds retain the Go
proxy and replay surface without automatic native attach.

## CLI quick start

Requirements for a native source build are Go `1.23` or newer, Windows amd64,
MinGW-w64 `gcc.exe` and `ar.exe` for cgo, Visual Studio 2022 C++ Build Tools
with MSVC, and PowerShell. The first uncached build downloads the pinned Frida
devkit and zlib source and verifies both archives.

```powershell
git clone https://github.com/Follen/miniapp-bridge.git
cd miniapp-bridge
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-windows.ps1
.\dist\miniapp-bridge.exe
```

Then open or reload the miniapp after the attach message and connect DevTools
with the URL above. Available CLI options are:

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

`scripts\build-windows.ps1` builds the native shim, runs the tagged test suite,
builds `miniapp-bridge.exe`, and places the DLL and manifest beside the EXE in
`dist`.

## Public Go SDK

The module path is `github.com/Follen/miniapp-bridge`; the public package is
`github.com/Follen/miniapp-bridge/sdk`. The CLI uses the same `sdk.Service`
implementation. Consumers do not import `internal`, receive C pointers, or own
Frida handles.

```bash
go get github.com/Follen/miniapp-bridge/sdk@v0.0.1
```

```go
package main

import (
    "context"
    "time"

    "github.com/Follen/miniapp-bridge/sdk"
)

func run(ctx context.Context) error {
    service, err := sdk.New(sdk.Options{})
    if err != nil {
        return err
    }
    if err := service.Start(ctx); err != nil {
        return err
    }

    _, requestErr := service.Send(ctx, sdk.Request{
        Method: "Runtime.enable",
    })

    closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    closeErr := service.Close(closeCtx)
    if requestErr != nil {
        return requestErr
    }
    return closeErr
}
```

The SDK also exposes status, logs, CDP and context subscriptions; structured and
raw CDP requests; target discovery and attach/detach; `jscontextId` selection;
capture/replay; native preparation; and structured errors compatible with
`errors.Is` and `errors.As`. See [docs/sdk.md](docs/sdk.md) and
[examples/sdk](examples/sdk).

The public SDK follows Go Module SemVer. This release requires Go `1.23` or
newer. Compatible minor and patch releases keep the documented SDK API,
structured error model, request/event ordering, default endpoints, and native
version constants stable within the current major version.

## Native runtime: prepare, build, and deploy

The Go Module contains Go source and the minimal Windows loader source only.
`miniapp-frida.dll` and generated archives are not committed to the source
Module. The DLL is distributed separately as a GitHub Release asset and must be
placed beside the final EXE together with `manifest.json`. The loader does not
search the working directory, `PATH`, the registry, or a global installation.

Prepare a published runtime into an executable directory:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\native-prepare.ps1 `
  -DestinationDirectory .\dist `
  -ExpectedArchiveSHA256 E521ED5828176DE066474D1DE91C69B1FC9B17BC4E7ECFCBDB64B752309A2C2B
```

The default download is:

```text
https://github.com/Follen/miniapp-bridge/releases/download/native-v17.3.2-abi1/miniapp-frida-native-17.3.2-abi1-windows-amd64.zip
```

Preparation uses an exclusive lock, partial download, SHA-256 verification,
manifest and PE amd64 validation, staging extraction, and atomic installation.
The default cache is
`%LOCALAPPDATA%\miniapp-bridge\native\17.3.2-abi1\windows-amd64`. Pass
`-Offline` with the same expected archive hash to require an already verified
cache and prohibit downloading.

An SDK consumer builds its executable with the native tag and deploys the
prepared files beside it:

```powershell
go build -tags frida -o .\dist\my-app.exe .\cmd\my-app
# .\dist\my-app.exe
# .\dist\miniapp-frida.dll
# .\dist\manifest.json
```

To build the DLL locally instead, run `scripts\build-windows.ps1`. It pins and
verifies the official Frida devkit archive and zlib `1.3.1`, builds the opaque C
ABI shim with MSVC, runs `go test -tags frida -race ./...`, and produces the
tagged CLI package in `dist`.

## Versions and releases

The initial source and SDK release is `v0.0.1`. Later releases must use
canonical Go Module v0/v1 SemVer tags without build metadata; the fixed module
path does not accept v2+ tags. Native runtime assets are versioned independently
because a Go proxy must not carry large platform DLLs. The pinned native tag is
`native-v17.3.2-abi1`, and its asset name is:

```text
miniapp-frida-native-17.3.2-abi1-windows-amd64.zip
```

The native ZIP contains `miniapp-frida.dll`, `manifest.json`, `LICENSE`,
`FRIDA_COPYING`, `FRIDA_COPYING.LIB`, `ZLIB_LICENSE`,
`THIRD_PARTY_NOTICES.md`, and its internal `SHA256SUMS`.

GitHub Actions uses two workflows:

- [CI](.github/workflows/ci.yml) runs on pushes, pull requests, and manual
  dispatch. Linux runs the portable unit, vet, race, module, formatting, and
  repository checks. Windows runs the pinned native build and the complete
  `100.0%` coverage gate. Official Node 24 actions are pinned to full commit
  hashes and workflow permissions are read-only.
- [Release](.github/workflows/release.yml) runs for an existing canonical v0/v1
  tag or manual dispatch of that tag. A read-only Windows job repeats module,
  deterministic, coverage/race/vet, native build, export, dependency, and hash
  checks. Only the final publish job receives `contents: write`, and it never
  checks out or executes repository code. Runs sharing the pinned native tag
  are serialized, and `queue: max` retains up to 100 pending releases.

A product release publishes:

```text
miniapp-bridge-v0.0.1-windows-amd64.zip
miniapp-frida-native-17.3.2-abi1-windows-amd64.zip
manifest.json
SHA256SUMS
```

The product ZIP contains the EXE, matching DLL and manifest, both READMEs, and
the required license and notice files. The workflow also creates or verifies
the immutable `native-v17.3.2-abi1` compatibility release used by the default
SDK download URL. Its native ZIP must match the SDK-pinned SHA-256
`E521ED5828176DE066474D1DE91C69B1FC9B17BC4E7ECFCBDB64B752309A2C2B`.
The publisher creates or resumes a draft, reconciles every asset byte-for-byte,
rechecks both product and native tag commits before and after publication, and
only then publishes. An existing product
release is never overwritten; an existing native release must have
byte-identical assets and is never selected as Latest. SemVer prerelease tags
are marked as GitHub prereleases.

Create an annotated product tag and push it after CI passes:

```bash
git tag -a v0.0.1 -m "miniapp-bridge v0.0.1"
git push origin v0.0.1
```

Hosted CI does not claim a live WMPF result because it has no target process.
Run the live matrix described below on an interactive Windows machine before a
release that claims a new target version.

## WMPF versions

All 47 address configurations recovered from the fixed reference repository are
embedded and statically tested. At runtime the bridge selects the configuration
matching the discovered target version; `sdk.Options.AddressConfigDir` can
provide an explicit override.

Embedding and unit tests do not constitute live verification of every
historical WMPF binary. A production release should carry a fresh live receipt
for each target version it claims to support.

## Testing and verification

Run the portable suite:

```powershell
go test ./...
```

Run the deterministic release gate:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\coverage-gate.ps1
```

The gate requires exactly `100.0%` Go statement coverage for the declared
CLI/Frida, `internal`, public SDK, tagged internal+SDK, and smoke-runner scopes.
It also runs unit, race, tagged-race, vet, protobuf differential/golden,
corrupt-frame, request-ordering, context, reconnect, capture/replay, native
loader, and external-Module coverage. See [docs/verification.md](docs/verification.md)
for the stable release contract and the current Comet verification report for
scope-bound command receipts.

The live Windows matrix is separate from deterministic coverage:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\smoke-windows.ps1 `
  -UpstreamWaitSeconds 300 -CDPMode all
```

Wait for `action-required=open-or-reload-miniapp`, then open or reload the
target. A passing run checks WMPF upstream ownership, representative Runtime,
Debugger, Page, DOM, Network, Console and Performance behavior, interaction
input, reconnect, event/request semantics, graceful Agent/session/device/runtime
teardown, target survival, and immediate port rebinding. A live run validates
only the installed target version and environment; the project does not infer
historical-version parity from one receipt.

## Reference, license, and notices

This port is based on
[evi0s/WMPFDebugger](https://github.com/evi0s/WMPFDebugger) at fixed commit
`2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d`. Address configurations and Agent
behavior retain their source attribution. Protocol definitions corresponding to
the reference `src/third-party` files originated from WeChat DevTools and retain
the Tencent Holdings Ltd. copyright.

The project is licensed under [GPL-2.0-only](LICENSE). Distributed native assets
must also retain [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), the zlib
license, Frida's pinned [wxWindows license](licenses/frida-17.3.2/COPYING), and
the referenced [GNU Library GPL 2.0 text](licenses/frida-17.3.2/COPYING.LIB).
