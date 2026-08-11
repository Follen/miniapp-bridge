# Acceptance evidence

<!-- historical-acceptance-summary:start -->
The public archive keeps the historical acceptance IDs and statuses. Raw machine receipts, snapshots, and developer-specific runtime metadata are intentionally excluded; the command summaries below are the retained audit record.

| Acceptance ID | Historical status |
| --- | --- |
| `acceptance-121aa9ac52f8ca3ce5d57bd707277504f9c8ef4273aaee334400784f447408dd` | passed |
| `acceptance-1689ac69764ec97230e410f6319914ffc0c061ce304423668c7af31c1c278f86` | passed |
| `acceptance-2f73d3dc90c9d5d5becaa36fe1d0c63a8da5692bdcaaccc60fe3b66050784e48` | passed |
| `acceptance-3142a0a03549f74290c8b80dfdc548a2fda3240da1a4d5a5481c24db4f7d0e36` | passed |
| `acceptance-31b0f5a042f67734358e305516168adc805443c5cdd121c77ee021e10518f8e4` | passed |
| `acceptance-45351ee15618083d13cd6e5fadb5fdd42ab95395eff25d02ed65926120ff1bb7` | passed |
| `acceptance-4f2d44b847c8dd8c719932a45b79731d1ad2d472b85482a605cdd02aed8f657a` | passed |
| `acceptance-509d8a925b855bcbf774c57033562c26d5c9552eab56f290a92549f4d4ad9a7e` | passed |
| `acceptance-665bbd063f3891249a11ce692793045960619d1f326994d3008b6fd880ae9550` | passed |
| `acceptance-67a33bff9d58f064dcf2e04c4c60ac748b4f9dfb5fdf581b7622e78971c045a4` | passed |
| `acceptance-819bf1b23ee4330f5c192d362b105a80f81bc9cc58d7bdb0739d5ccba218b09d` | passed |
| `acceptance-a42d0190e373980cb49c9575720643007674f28de81471cb1efecb3373f2061a` | passed |
| `acceptance-ae8b3367addf0fe612cab807bbb7ae7a5781031f6de0799560acc63fe8ef0e97` | passed |
| `acceptance-d3d1601b956f004627354c9b305daa3ff654a7a1f6870e4f0840c7eaf259c029` | passed |
| `acceptance-e183d1270a3b79ef3b7f4f65a44395aa030b3aa3e7909e6f6c596c681ab1bc7d` | passed |
| `acceptance-e9cb527aa2f37b705f10729d3ba00a58f30d482d2c7a2e3a49e627dc567b5b03` | passed |
| `acceptance-faac62c8e51c470eb8344a3e2abf6ae6756d83764087db5ddb58d778f898da58` | passed |
| `acceptance-fb389f2bd226a79ae6f9115289d975196e338d7165b125d19ef1d4ca5fe88cb7` | passed |
| `acceptance-fb7226fbac2a9cb5dc41cf5513574b1ff014a8f7b17cff20c768eb1eb701b374` | passed |
<!-- historical-acceptance-summary:end -->

# Commands and results

- `go test ./sdk -run 'Test(NewDefaultsAndAllocationOnlyContract|SDKAndCoreDoNotExitOrRegisterSignals)$' -count=1`: exit 0; zero-value defaults, allocation-only construction, no listener/native/record side effects, and no SDK/core process exit or global signal registration passed.
- `go test ./sdk -count=1`: exit 0; lifecycle concurrency, cancellation, request correlation, context routing, subscriptions, recording/replay, and cross-layer shutdown ordering passed.
- `go test ./... -count=1`: exit 0; every repository package passed, including WMPF golden/differential fixtures, simulated proxy behavior, CLI/config, Agent, native fixtures, and smoke-client semantic fixtures.
- `go test ./scripts -run '^TestExternalModuleImportsOnlySDK$' -count=1`: exit 0; a temporary external Go Module imported only `github.com/Follen/miniapp-bridge/sdk` and built/exercised the public lifecycle.
- `go test ./internal/native ./scripts -count=1`: exit 0; fake DLL, loader errors, local download server, cache, concurrency, hash failure, offline, ZIP validation, and atomic install tests passed.
- Correctly quoted PowerShell wrapper with `MINIAPP_BRIDGE_NATIVE_PATH`, then `go test -tags frida ./internal/... ./sdk -count=1`: exit 0; all tagged internal and SDK tests passed.
- Tagged verbose native acceptance tests: exit 0; `TestWindowsLoaderFakeDLLAcceptance` and `TestPlatformNativeShutdownOrderIsExactAndIdempotent` both explicitly reported `PASS`.
- `scripts/coverage-gate.ps1`: exit 0; CLI/Frida, internal, SDK, tagged internal+SDK, and smoke runner each reported statement coverage `100.0%`; unit, race, tagged race, and vet all passed.
- `scripts/build-windows.ps1`: exit 0; native shim, tagged race, Windows EXE, export/dependency checks, manifest, release asset, and SHA-256 output passed.
- `scripts/native-release.ps1` with isolated output directories: exit 0; `miniapp-frida-native-17.3.2-abi1-windows-amd64.zip`, manifest, licenses, notices, and `SHA256SUMS` were generated; archive SHA-256 was `1597ADCC6B3B13B5BCBA910904046AB7D2E1E3D73AE16961C73E400373BDE87A`.
- `scripts/native-prepare.ps1 -Offline` against a fresh cache populated from that ZIP: exit 0; installed DLL matched manifest SHA-256 `05CF2B66A6A031E813FEB1C0A895A1272A68770233C3F270272243F48D11E846`.
- `git ls-files '*.dll' '*.zip' '*.lib' '*.a' '*.exe'`: exit 0 and no matches; source Module tracks no native or executable asset.
- `go mod tidy` with before/after `go.mod` and `go.sum` hashes: exit 0 and unchanged.
- `git diff --check`: exit 0; only Windows line-ending advisory output, no whitespace errors.
- `scripts/smoke-windows.ps1 -UpstreamWaitSeconds 300 -CDPMode all`: live child run completed with `smoke-success=true`. It validated listeners, Frida attach to WMPF 25297, upstream peer ownership, Runtime/Debugger/Page/DOM/Network/Console/Performance, a 491520-byte payload, 16 concurrent requests, error propagation, 45 correlation assertions, contexts, reconnect, mouse/keyboard interaction, target survival, immediate port rebinding, and Agent/session/device/runtime teardown. The exact log SHA-256 is `88B70A00FA658340CD27C3BBB0A8F83AAD0AC10BB61C0962D07505334D9462C2`; machine marker verification passed.

# Skipped checks

No required acceptance check was skipped. Two invalid intermediate runs were excluded before the historical acceptance status was recorded: one hid tagged-test failures behind a broken PowerShell wrapper, and one contained a mistyped live-log hash. The command summaries above describe the corrected successful runs.

# Spec consistency

The module path, public SDK boundary, CLI adapter, lifecycle ownership, subscription behavior, CDP correlation/routing, WMPF codecs, native loader, release preparation, documentation, and fixed external interfaces match the confirmed brief and complete target specification. The recorded implementation scope was `df341855cc5112bec61124ba65d0dea9de41b8fb117d694c52ea051b2d7a3ba1`, revision 22, contract `4613a8aa3f31c57ff4ecd5747e9281aedd3281f2473412ada3fceb06a7ca1274`, and snapshot `4554b0b88e00f905484fa513ef4550a53db02456c95306c529458be5110881e9`.

# Known limitations and risks

- The live run exercised the installed WMPF 25297 runtime on Windows amd64. Historical address versions are preserved and covered by configuration selection, fixtures, and deterministic tests, but were not each installed and live-tested in this run.
- Windows amd64 is the native production target for this change. Other operating systems retain explicit platform fallbacks and abstractions but have no native Frida release asset in this delivery.
- The live trace reported `agent-on-load-start=false` with `post-attach-upstream-without-onloadstart`; the script continued because the owned upstream connection was validated and the complete CDP matrix passed. This is retained as a diagnostic rather than claimed as an observed OnLoadStart callback.
- Release ZIP generation, hashes, offline installation, PE architecture, ABI, exports, and dependencies were verified locally. Uploading the generated ZIP and `SHA256SUMS` to a GitHub Release remains a release-operation responsibility, not a source-tree behavior.

# Conclusion

Verify passed for the recorded revision. All 19 acceptance items were marked passed after the required deterministic, race, vet, coverage, Windows native, packaging, offline, and live checks completed. No acceptance blocker remained within the confirmed Windows amd64 scope; stable implementation differences and environment limits remain documented in `KNOWN-DIFFERENCES.md`.
