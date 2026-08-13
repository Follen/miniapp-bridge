# Acceptance evidence

本轮 Build 修复迭代已完成单 upstream、单 CDP controller 的生产加固实现与本地验证。以下结果均来自当前工作树和当前源码。

| Area | Evidence | Result |
| --- | --- | --- |
| Go unit | `go test ./... -count=1 -timeout 180s` | passed |
| Go race | `go test -race ./... -count=1 -timeout 240s` | passed |
| Go fuzz smoke | 13 CI fuzz targets, `-fuzztime=2s -parallel=1` | 13/13 passed |
| Go soak/chaos | app deterministic matrix `-count=20` | passed |
| Capture soak | capture rotation/replay/backpressure matrix `-count=10` | passed |
| Windows native build | `scripts/build-windows.ps1` | passed; DLL/archive/manifest/SHA256SUMS produced |
| Windows update soak | 4 native prepare rollback tests, `-count=3` | passed |
| C shim | `scripts/cshim-coverage.ps1` | 233/233 lines, 70/70 functions, 202/202 branch sites |
| PowerShell Build | Build shard | 919/919 commands, 100.00%, 6/6 tests |
| PowerShell Native | Native shard | 885/885 commands, 100.00%, 11/11 tests |
| PowerShell Smoke | Smoke shard | 687/687 commands, 100.00%, 1/1 test |
| PowerShell merged | strict 3-shard merge | 2491/2491 commands, 100.00%, 0 missed, 0 failed |
| Static/security | `go vet ./...`; `govulncheck ./...` | passed; no vulnerabilities |

## CI repair verification

- `go test -race ./internal/app -run TestCoverageGateUpgradeInstallOwnerRace -count=20` passed after serializing the WebSocket upgrade fixture lifecycle.
- `go test -race ./... -count=1 -timeout 240s` passed in 126.6 seconds.
- Windows tagged SDK tests passed at 100.0% statement coverage, including deterministic DOS 8.3-to-long-path identity expansion.
- The three GitHub Windows regressions (`ResolveAndLoaderErrors`, `BootstrapFailureAndSuccess`, and `BlocksReplacementDuringLoad`) passed for 20 consecutive runs.
- `scripts/build-windows.ps1` passed in 141.8 seconds and produced the DLL, archive, manifest, and SHA256SUMS.

## Independent review repair

- Upstream owner installation now follows the established `dispatchMu -> connMu` lock order before clearing CDP response fences.
- Network CDP requests without an active upstream or selected context receive a structured error and do not enter the pending request registry.
- Segmented capture serializes queue send/close with `queueMu`; the close/write race passed 100 race-detector repetitions.
- Proxy listeners track accepted connections and close them before waiting for handlers, so idle clients cannot block shutdown.
- Windows CI now runs the real PowerShell command coverage gate. Build, Native, and Smoke shards run in isolated PowerShell processes to prevent ledger state leakage.
- PowerShell results after isolation: Build 919/919, Native 885/885, Smoke 687/687; strict merge 2491/2491, 0 missed, 0 failed.
- The default three-shard orchestration passed end to end in 1036.3 seconds. JSON arrays cross the Windows process boundary through temporary files, run-level mutation is serialized, ledger filenames are process-unique, and breakpoint actions preserve native `$LASTEXITCODE`.

## Commands and results

关键报告由 CI job `windows-verification-logs-<run-id>-<attempt>` 和
`linux-test-logs-<run-id>` 作为 workflow artifacts 上传；本地 `ci-artifacts/`
目录是可复现的临时输出，不纳入源代码提交。报告名称包括：

- `powershell-coverage.log`（严格合并：`2491/2491`）
- `powershell/build/coverage.json`
- `powershell/native/coverage.json`
- `powershell/smoke/coverage.json`
- `c-shim-coverage.log`
- `windows-native-build.log`
- `linux-fuzz.log`

严格合并报告的 literal summary 为：`commands_analyzed=2491`, `commands_executed=2491`, `commands_missed=0`, `failed_tests=0`, `result=passed`。

## Skipped checks

- Authenticode trusted-timestamp signing remains dependent on protected release credentials and was not asserted locally.
- Real WeChat/WMPF live canary, attach/handshake/CDP round-trip and port-reuse matrix require the external Windows fixture and were not asserted locally.
- GitHub push, PR, required checks, independent PR review, merge and post-merge cleanup are delivery steps still pending this change's handoff.

## Spec consistency

- Runtime keeps one upstream owner and one CDP controller owner; no application-layer authentication or token path is introduced.
- Native update uses destination locking, immutable version directories, atomic current pointer, journal recovery, rollback and N-1 retention.
- Capture uses bounded asynchronous writing, segment rotation/retention, disk backpressure, CRC/commit validation and replay limits.
- C shim synchronous calls have a bounded cancellation watchdog and deterministic resource cleanup.

## Known limitations and risks

- Signing and live canary evidence are environment-dependent and must remain explicit risks until protected credentials and the Windows fixture are available.
- GitHub delivery evidence is not present until the branch is pushed, CI completes, an independent agent reviews the PR, and the PR is merged.

## Conclusion

All locally executable Go, C shim and PowerShell implementation gates pass at 100%, including race, fuzz, soak, native build/update and security checks. The remaining work is external signing/live compatibility evidence and the requested GitHub PR/review/merge/cleanup workflow.
