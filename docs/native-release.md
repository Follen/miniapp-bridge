# Native Release Assets

The Windows native runtime is distributed separately from the Go module. Build
the complete Windows output and native archive with:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-windows.ps1
```

To repackage an already built shim directly, run:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/native-release.ps1 `
  -RuntimeDirectory third_party/frida/runtime-17.3.2 `
  -OutputDirectory dist/native
```

The command emits `miniapp-frida-native-17.3.2-abi1.1-windows-amd64.zip` and
`SHA256SUMS`. The archive contains `miniapp-frida.dll`, a pinned
`manifest.json`, `LICENSE`, `FRIDA_COPYING`, `FRIDA_COPYING.LIB`,
`ZLIB_LICENSE`, `THIRD_PARTY_NOTICES.md`, and its internal `SHA256SUMS`. The
manifest records the DLL size and SHA-256 and all C
ABI exports required by the dynamic loader. Version probes cover ABI 1, native
runtime `17.3.2-abi1.1`, Frida Core `17.3.2`, and the pinned zlib contract `1.3.1`.
The shim's zlib probe is an ABI compatibility marker;
the actual compression code is compiled from the pinned stock zlib 1.3.1 source
into the DLL. The 18 required exports include bounded compress/decompress
operations and same-CRT byte release. The Go executable contains only the
dynamic loader and has no `zlib1.dll`, zlib header, or import-library
dependency. The Windows build checks the DLL export and dependent-module tables
before packaging.

## Product bundle

After `build-windows.ps1`, create the release bundle with the canonical Go
Module version `v0.0.5`:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass `
  -File scripts/package-windows-release.ps1 -Version v0.0.5
```

The command writes these files under `dist/release`:

```text
miniapp-bridge-v0.0.5-windows-amd64.zip
miniapp-frida-native-17.3.2-abi1.1-windows-amd64.zip
manifest.json
SHA256SUMS
```

The product ZIP contains the executable, matching DLL and manifest, both
READMEs, project GPL license, Frida wxWindows/LGPL texts, zlib license, and
third-party notices. Packaging rechecks the DLL against the manifest and the
native archive against its sidecar. It
builds and verifies the entire bundle in a sibling staging directory, then
publishes it with a rollback-capable directory rename.

## GitHub Actions release

`.github/workflows/release.yml` runs for pushed `v*` tags and manual dispatch of
an existing tag. It accepts canonical v0/v1 Go Module SemVer without build
metadata, binds tag-push checkout to the triggering commit, checks that the
checkout commit equals the tag commit, and reruns module verification,
deterministic tests, the 100% coverage/race/vet gate, native build, export and
dependency inspection, packaging, and every published checksum.

The product tag publishes the four assets above. The workflow also creates or
verifies `native-v17.3.2-abi1.1`, which is the compatibility Release used by the
default SDK and PowerShell download URL. The publisher creates or resumes a
draft, reconciles every expected asset byte-for-byte, rejects unexpected assets,
rechecks the remote tag commit, and only then publishes. Existing product
releases are not replaced. Existing native assets must be byte-identical to the
generated ones, and the native release is never selected as Latest.
The pinned native archive SHA-256 is:

```text
A63B7F121794DD9C6D51CAC9F47C6D0CE43EB61E753AC23075845630F6A76BFD
```

Only the final publisher job has `contents: write`; it consumes the verified
workflow artifact without checking out or executing repository code. Official
actions are pinned to full commit hashes. SemVer prerelease tags are published
as GitHub prereleases. All product releases sharing the pinned native tag are
serialized, and `queue: max` retains up to 100 pending runs.

After CI passes, create and push an annotated tag:

```bash
git tag -a v0.0.5 -m "miniapp-bridge v0.0.5"
git push origin v0.0.5
```

Hosted CI verifies deterministic and native packaging behavior but has no live
WMPF target. Run `scripts/smoke-windows.ps1 -CDPMode all` on an interactive
Windows environment before claiming a new live target version.
The ordinary `frida` test tag is hosted-CI safe; only `frida live` enables tests
that enumerate, attach to, or reload a real WMPF process.

## Runtime preparation

`scripts/native-prepare.ps1` and `sdk.PrepareNativeRuntime` consume the release
archive. They verify the archive hash before extraction, reject zip-slip paths,
validate manifest version/ABI/platform, and install through an atomic rename.
Use `-Offline` (PowerShell) or `NativePrepareOptions{Offline: true}` (Go) to
require a verified cache without network access. A missing cache, bad hash,
malformed manifest, wrong architecture, or missing DLL is returned as a
structured native error.
