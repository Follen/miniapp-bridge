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
| PowerShell Build | Build shard | 917/917 commands, 100.00%, 6/6 tests |
| PowerShell Native | Native shard | 885/885 commands, 100.00%, 11/11 tests |
| PowerShell Smoke | Smoke shard | 687/687 commands, 100.00%, 1/1 test |
| PowerShell merged | strict 3-shard merge | 2489/2489 commands, 100.00%, 0 missed, 0 failed |
| Static/security | `go vet ./...`; `govulncheck ./...` | passed; no vulnerabilities |

## Commands and results

关键报告：

- `ci-artifacts/powershell-coverage.log`
- `ci-artifacts/powershell/build/coverage.json`
- `ci-artifacts/powershell/native/coverage.json`
- `ci-artifacts/powershell/smoke/coverage.json`
- `ci-artifacts/c-shim-coverage.log`
- `ci-artifacts/windows-native-build.log`
- `ci-artifacts/linux-fuzz.log`

严格合并报告的 literal summary 为：`commands_analyzed=2489`, `commands_executed=2489`, `commands_missed=0`, `failed_tests=0`, `result=passed`。

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
