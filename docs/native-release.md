# Native Release Assets

The Windows native runtime is distributed separately from the Go module. Build
the shim first with `scripts/build-frida-shim.ps1`, then package it with:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/native-release.ps1 `
  -RuntimeDirectory third_party/frida/runtime-17.3.2 `
  -OutputDirectory dist/native
```

The command emits `miniapp-frida-native-17.3.2-abi1-windows-amd64.zip` and
`SHA256SUMS`. The archive contains the DLL, a pinned `manifest.json`, `LICENSE`,
`ZLIB_LICENSE`, and `THIRD_PARTY_NOTICES.md`. The manifest records the DLL size and SHA-256 and
all C ABI exports required by the dynamic loader. Version probes cover ABI 1,
native runtime `17.3.2-abi1`, Frida Core `17.3.2`, and the pinned zlib contract
`1.3.1`. The shim's zlib probe is an ABI compatibility marker; the actual
compression code is compiled from the pinned stock zlib 1.3.1 source into the
DLL. The 18 required exports include bounded compress/decompress operations and
same-CRT byte release. The Go executable contains only the dynamic loader and
has no `zlib1.dll`, zlib header, or import-library dependency. The Windows build
checks the DLL export and dependent-module tables before packaging.

`scripts/native-prepare.ps1` and `sdk.PrepareNativeRuntime` consume the release
archive. They verify the archive hash before extraction, reject zip-slip paths,
validate manifest version/ABI/platform, and install through an atomic rename.
Use `-Offline` (PowerShell) or an empty `SourceURL` with an existing cache
(Go) to run without network access. A missing cache, bad hash, malformed
manifest, wrong architecture, or missing DLL is returned as a structured native
error.
