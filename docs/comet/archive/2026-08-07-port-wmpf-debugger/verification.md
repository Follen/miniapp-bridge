# Acceptance evidence

The acceptance IDs and statuses are recorded from the Native status page at Verify time. Local evidence covers protocol, zlib, CDP correlation, context routing, listener behavior, capture, configuration selection, Agent embedding, and build output. Native Frida injection and live WMPF upstream behavior remain skipped because this workspace has no frida-core SDK/DLL contract bound to the target runtime.

<!-- historical-acceptance-summary:start -->
The public archive keeps the historical acceptance IDs and statuses. Raw machine receipts, snapshots, and developer-specific runtime metadata are intentionally excluded; the command summaries below are the retained audit record.

| Acceptance ID | Historical status |
| --- | --- |
| `acceptance-1ca4a0d9b5c337b582460cf486daf7b71d0280e846de492c6a625e10586480fe` | passed |
| `acceptance-3c71936f8859f1c6008462a03c5e7e104741204217e592d37d67a9997545d173` | passed |
| `acceptance-734d1318f3d82ae0f7387dc3b202ccda27bd326559814eea80c81bca18ac5197` | passed |
| `acceptance-ba06c2d11157eebc7d500b12a8b91db4775102f44ae190fb9a8bf248b01d5738` | passed |
| `acceptance-bc21b3837d32d52c0b53f0be2f07e063e792114696eb893043203df1e4d35f7d` | passed |
| `acceptance-cdf4662e7792451e9f98fe80bdba71183d6b8cddb0efbd50d34bf85997744c23` | passed |
<!-- historical-acceptance-summary:end -->

# Commands and results

- `go test ./...` → exit `0`; all packages passed, including WMPF golden/corrupt-frame tests, CDP correlation, context registry, proxy hub, version selection, Frida bootstrap and WebSocket bridge/rebind.
- `go vet ./...` → exit `0`; no diagnostics.
- `powershell -ExecutionPolicy Bypass -File .\scripts\build-windows.ps1` → exit `0`; tests passed and `dist/miniapp-bridge.exe` built. SHA-256: `74731D38A0F004CAAE64593A137CBAF9074E205DDF3624C9A82D8517160F4251`.
- `powershell -ExecutionPolicy Bypass -File .\scripts\smoke-windows.ps1` → exit `0`; output `listeners: 9421=true 62000=true` and `target-process: found count=9`.
- `Get-ChildItem configs\addresses\addresses.*.json` → `47` files; all parsed by `ConvertFrom-Json` during Frida subtask validation.
- SHA-256 comparison of `frida/hook.js` against the audited reference → both `3278DB6CCE182619D87A19756C83F1081F237859FF214560DF1B1758E67C53D5`.
- `internal/app.TestBridgeAndRebind` → exit `0`; CDP text frame became WMPF binary, WMPF result became CDP text, shutdown allowed immediate port rebind.

# Skipped checks

- Concrete `frida-core 17.3.2` cgo attach/script callbacks and DLL loading: skipped; SDK headers/import library/DLL are not present in the workspace. Interface boundary and mock lifecycle are tested.
- Live WMPF upstream connection, DevTools `Runtime.enable`, `Debugger.enable`, and `Runtime.evaluate` against a real target: skipped; requires a matching Windows target runtime and permissions.
- Full inner-message Go generated bindings: partial; `proto/wmpf_remote_debug.proto` contains all 55 recovered message declarations and field numbers, while runtime structured decoding currently focuses on ChromeDevtools/CustomMessage and preserves other payload bytes.

# Spec consistency

The implementation preserves default ports, startup ordering, outer envelope field numbers/wire types, zlib flag semantics, CDP text/binary frame directions, context registry operations, exact version selection, and reverse-order shutdown. The behavior matrix marks fully implemented and partial rows explicitly.

# Known limitations and risks

- Native Frida integration is build-tagged and requires the pinned SDK layout described in README.
- Process metadata from `tasklist` lacks parent/path fields; the Frida metadata interface is the intended production discovery source.
- The recovered `.proto` is generated from the fixed reference commit; nested/repeated runtime codecs beyond the tested categories still need generated Go bindings for complete protocol parity.

# Conclusion

Local build, unit tests, protocol fixtures, WebSocket bridge, capture primitives, version configuration, and Windows listener smoke checks passed. Verify should record the native-injection and live-target acceptance items as skipped with the reasons above rather than claiming end-to-end parity.
