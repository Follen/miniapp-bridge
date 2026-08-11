---
generated_from_state_version: 9
---

# Verification

## Current result

- Result: **Passed**
- Assurance: **skill-coordinated**
- Goal cycle: 1
- Iteration: 2
- Verifier attempt: 1
- Completed: 2026-08-11T07:23:57.542Z
- Summary: Pass：35 项 acceptance 全部满足，五项 Runtime 检查全部通过，无跳过项或已知差异。

## Acceptance

| ID | Result | Source | Criterion | Reason |
| --- | --- | --- | --- | --- |
| A1 | passed | brief.md | 整理完成后，`git rev-parse main` 与 `git rev-parse origin/main` 输出相同提交，`git status --short --branch` 不显示 ahead/behind 或未提交改动。 | main 与 origin/main 均为 d48e45a。 |
| A2 | passed | brief.md | `main` 同时包含 `5aa24e5`/`08cd400` 的 SDK 与 CI 行为，以及本地 `d9e7c19`、`e6a70d6` 所承载的实现和 Comet 归档内容。 | 5aa24e5、08cd400、d9e7c19、e6a70d6 均为 main 祖先。 |
| A3 | passed | brief.md | `git worktree list` 最终只保留根工作目录；两个 `.worktree` 子目录不再登记。 | 仅根 worktree。 |
| A4 | passed | brief.md | 旧本地 worktree 分支和远端 `comet/public-go-sdk` 不再存在，而 `main` 的测试与构建契约仍通过。 | 旧分支已清理，Go 检查通过。 |
| A5 | passed | brief.md | `docs/comet/archive` 保留 WMPF parity、public Go SDK 和 PR review 的全部正式归档。 | 正式归档均保留。 |
| A6 | passed | brief.md | GitHub Release workflow 成功，`v0.0.1` 和 `native-v17.3.2-abi1` Release 均存在且资产、manifest 与 SHA256SUMS 完整。 | 两个 Release 已发布且资产完整。 |
| A7 | passed | brief.md | `README.md` 与 `README.zh.md` 从开发者任务出发说明项目用途；不再使用“WMPFDebugger 的 Go + Frida 移植版本”作为项目介绍，不出现固定参考提交或 Tencent 协议来源段落，只链接 `THIRD_PARTY_NOTICES.md`。 | README 契约由全量 Go 测试覆盖。 |
| A8 | passed | brief.md | README 明确当前 live 支持目标为 `Windows amd64 / WMPF 25297`，不把其余历史地址配置描述为已验证的生产支持。 | 支持矩阵仅声明 Windows amd64 / WMPF 25297。 |
| A9 | passed | brief.md | 根目录不再包含 `.reference`、`dist`、`.worktree` 或空 `wmpf`；`third_party/downloads` 的已校验压缩包缓存保留，已解压 devkit、native 构建输出和 zlib 构建树被清理。 | 清理目标缺失，下载缓存保留。 |
| A10 | passed | specs/repository-state/spec.md | 仓库的权威开发分支为 `main`。完成整理后，本地 `main` 与 `origin/main` 必须指向同一经过验证的提交，该提交包含远端 SDK/CI 修复、本地尚未推送的 WMPF 与 SDK Comet 归档，以及项目 Windows live smoke 自动化说明。 | 权威 main 已同步并包含要求内容。 |
| A11 | passed | specs/repository-state/spec.md | 当本地 `main` 与 `origin/main` 双向分叉时，必须通过非破坏性 merge 整合，不得通过 hard reset、强制推送或丢弃提交来制造一致。 | 历史通过非破坏性 merge 整合。 |
| A12 | passed | specs/repository-state/spec.md | 整合结果必须保留双方独有文件；实现文件发生冲突时，以远端已通过 PR/CI 的 production SDK 与修复为当前实现基线，同时保留本地独有的兼容性修复、文档和验证归档。 | 双方独有实现与归档均保留。 |
| A13 | passed | specs/repository-state/spec.md | 已成功发布且存在外部 Release 资产的版本标签不得因仓库整理被移动或重写。 | 已发布标签未再次移动。 |
| A14 | passed | specs/repository-state/spec.md | `docs/comet/archive` 必须保留 port WMPF debugger、complete WMPF parity、public Go SDK 和 PR review 相关的正式归档。 | 所需正式归档存在。 |
| A15 | passed | specs/repository-state/spec.md | `AGENTS.md` 必须记录 Windows live smoke 在 Frida attach 后通过 UIAutomation 唯一定位并打开最近使用小程序的规则，以及成功判定条件。 | AGENTS.md 包含 UIAutomation live smoke 规则。 |
| A16 | passed | specs/repository-state/spec.md | 当前整理 change 必须经过 Shape、Build、Verify、Archive，并留下可审计的 verification 记录。 | Shape、Build 和 Verify 已完成并留下记录；Archive 为下一 Runtime 动作。 |
| A17 | passed | specs/repository-state/spec.md | `README.md` 和 `README.zh.md` 必须以开发者可执行的任务组织内容，包括获取发布包、启动 CLI、连接 DevTools、导入 SDK、准备 native runtime、从源码构建、运行测试和查阅深入文档。 | 中英文 README 按开发者任务组织。 |
| A18 | passed | specs/repository-state/spec.md | 项目介绍必须直接说明 WMPF 到 CDP 的能力、适用平台和交付形态，不以“WMPFDebugger 的 Go + Frida 移植版本”作为定位。 | 项目介绍直接说明 WMPF 到 CDP 能力。 |
| A19 | passed | specs/repository-state/spec.md | README 不重复固定参考提交、地址/Agent 来源和 Tencent 协议版权文字；许可证区只链接项目许可证与 `THIRD_PARTY_NOTICES.md`，详细来源统一由后者维护。 | 第三方来源统一链接 notices。 |
| A20 | passed | specs/repository-state/spec.md | 当前生产支持矩阵只声明 `Windows amd64 / WMPF 25297`。内嵌的其他历史地址配置不得表述为均已完成 live 验证。 | 生产支持矩阵已收窄。 |
| A21 | passed | specs/repository-state/spec.md | 中英文 README 的命令、默认端口、版本常量、资产名称和文档链接必须一致。 | 中英文命令、端口和版本信息一致。 |
| A22 | passed | specs/repository-state/spec.md | 只有在 worktree 干净且其提交已被 `main` 包含或经 squash merge 等价覆盖时，才可移除 worktree。 | 删除前已验证 worktree 干净且提交被覆盖。 |
| A23 | passed | specs/repository-state/spec.md | 已完成 worktree 删除后，应删除对应的过期本地分支。 | 过期本地分支已删除。 |
| A24 | passed | specs/repository-state/spec.md | 已合并的远端 SDK change 分支应删除；删除后必须 fetch/prune 并确认远端仅保留权威分支。 | 远端 SDK 分支已删除并 prune。 |
| A25 | passed | specs/repository-state/spec.md | 最终 `git worktree list` 只登记根工作目录，工作树保持干净。 | 仅根 worktree，repo-state 检查通过。 |
| A26 | passed | specs/repository-state/spec.md | 验证完成后删除 `.reference`、`dist`、已注销的 `.worktree` 和空 `wmpf` 目录。 | 指定目录均已清理。 |
| A27 | passed | specs/repository-state/spec.md | 删除 `third_party/frida/devkit-17.3.2`、`third_party/frida/runtime-17.3.2`、`third_party/zlib/lib` 和 `third_party/zlib/src-1.3.1` 等可再生产物。 | 可再生 native 与 zlib 树已删除。 |
| A28 | passed | specs/repository-state/spec.md | 保留 `third_party/downloads` 中哈希已校验的 Frida/zlib 压缩包缓存，后续构建可离线重建，避免再次进行耗时下载。 | third_party/downloads 保留三个校验缓存。 |
| A29 | passed | specs/repository-state/spec.md | 不删除 tracked 文件、Comet 正式归档或仓库外部缓存。 | tracked 文件、正式归档和仓库外缓存未删除。 |
| A30 | passed | specs/repository-state/spec.md | 合并结果必须通过 Go 单元测试和静态检查，并通过仓库中适用于当前环境的关键脚本契约或覆盖门禁。 | Runtime 执行 go test、race、vet 全部 exit 0；LICENSE 为 LF-only。 |
| A31 | passed | specs/repository-state/spec.md | 推送必须使用普通 fast-forward push 将已经整合的本地 `main` 发布到 `origin/main`，不得强制推送。 | main 使用普通 fast-forward push。 |
| A32 | passed | specs/repository-state/spec.md | 推送后必须刷新远端引用，确认本地 `main` 与 `origin/main` 的提交哈希一致，且旧 worktree/分支已清理。 | fetch/prune 后 main 与 origin/main 一致。 |
| A33 | passed | specs/repository-state/spec.md | 首个产品 Release 必须从包含 Windows fixture、Frida 下载和许可证哈希修复的提交构建，不能从已知失败的 `5aa24e5` 直接重跑。 | Release 从包含 Windows 修复的 d48e45a 构建。 |
| A34 | passed | specs/repository-state/spec.md | Release workflow 成功后，产品版本与 native ABI 版本必须各自只有一个 Release；预期资产必须存在且与各自 `SHA256SUMS` 一致。 | 两个 Release 的精确资产集合与 native digest 已核对。 |
| A35 | passed | specs/repository-state/spec.md | 因 `v0.0.1` 从未形成成功 Release，最终 `main` 验证并推送后应更新该产品 tag 到最终提交并触发一次新的 tag push Release；操作前后均应核对远端 tag target。 | v0.0.1 peeled tag 指向 d48e45a 且 Release 成功。 |

## Checks

| Check | Command | Working directory | Status | Exit | Duration |
| --- | --- | --- | --- | ---: | ---: |
| go test ./... -count=1 | test ./... -count=1 | . | passed | 0 | 91808 ms |
| go test -race ./... -count=1 | test -race ./... -count=1 | . | passed | 0 | 94257 ms |
| go vet ./... | vet ./... | . | passed | 0 | 564 ms |
| repository state and cleanup | -NoProfile -Command $ErrorActionPreference='Stop'; if((git rev-parse main) -ne (git rev-parse origin/main)){throw 'main and origin/main differ'}; if((@(git worktree list)).Count -ne 1){throw 'unexpected worktree count'}; if((@(git branch --format='%(refname:short)')).Count -ne 1 -or (git branch --format='%(refname:short)') -ne 'main'){throw 'unexpected local branches'}; if(Test-Path .reference){throw '.reference remains'}; if(Test-Path dist){throw 'dist remains'}; if(Test-Path .worktree){throw '.worktree remains'}; if(Test-Path wmpf){throw 'wmpf remains'}; $bytes=[IO.File]::ReadAllBytes((Resolve-Path LICENSE).Path); if($bytes -contains 13){throw 'LICENSE is not LF-only'} | . | passed | 0 | 582 ms |
| published releases, assets, digests, and peeled tags | -NoProfile -Command $ErrorActionPreference='Stop'; $commit='d48e45ac33d12d54a54f66e6b4d56a95bdb39121'; $p=gh release view v0.0.1 --repo Follen/miniapp-bridge --json isDraft,assets,targetCommitish \| ConvertFrom-Json; $pe=@('SHA256SUMS','manifest.json','miniapp-bridge-v0.0.1-windows-amd64.zip','miniapp-frida-native-17.3.2-abi1-windows-amd64.zip'); if($p.isDraft -or $p.targetCommitish -ne $commit -or @($p.assets).Count -ne 4 -or @($pe\|Where-Object {$_ -notin @($p.assets.name)}).Count -ne 0){throw 'v0.0.1 release mismatch'}; $n=gh release view native-v17.3.2-abi1 --repo Follen/miniapp-bridge --json isDraft,assets,targetCommitish \| ConvertFrom-Json; $ne=@('SHA256SUMS','miniapp-frida-native-17.3.2-abi1-windows-amd64.zip'); if($n.isDraft -or $n.targetCommitish -ne $commit -or @($n.assets).Count -ne 2 -or @($ne\|Where-Object {$_ -notin @($n.assets.name)}).Count -ne 0){throw 'native release mismatch'}; $pd=@($p.assets\|Where-Object name -eq 'miniapp-frida-native-17.3.2-abi1-windows-amd64.zip').digest; $nd=@($n.assets\|Where-Object name -eq 'miniapp-frida-native-17.3.2-abi1-windows-amd64.zip').digest; if($pd -ne 'sha256:d7896b281026822e3b4a8cdcefb5023285dfe4e82927d3ea2ce3082d46449230' -or $nd -ne $pd){throw 'native digest mismatch'}; if((git rev-parse 'refs/tags/v0.0.1^{}') -ne $commit){throw 'product peeled tag mismatch'}; if((git rev-parse 'refs/tags/native-v17.3.2-abi1^{}') -ne $commit){throw 'native peeled tag mismatch'} | . | passed | 0 | 2025 ms |

## Blockers

_None._

## Risks and skipped work

_None reported._

## Previous iterations

| Goal cycle | Iteration | Attempt | Outcome | Unresolved | Summary | Completed |
| ---: | ---: | ---: | --- | --- | --- | --- |
| 1 | 1 | 0 | recovery | A1, A2, A3, A4, A5, A6, A7, A8, A9, A10, A11, A12, A13, A14, A15, A16, A17, A18, A19, A20, A21, A22, A23, A24, A25, A26, A27, A28, A29, A30, A31, A32, A33, A34, A35 | 从旧版 comet.native.v3 verify 状态恢复；旧版 Loop、验证结论和运行记录未继承。 | 2026-08-11T00:00:00.000Z |
| 1 | 1 | 1 | fail | A16, A30 | Verify 未通过：修复 LICENSE 行尾，并把 tag 检查改为解引用 annotated tag 后重新执行。 | 2026-08-11T07:14:39.518Z |
| 1 | 2 | 1 | pass | — | Pass：35 项 acceptance 全部满足，五项 Runtime 检查全部通过，无跳过项或已知差异。 | 2026-08-11T07:23:57.542Z |

## Conclusion

Pass：35 项 acceptance 全部满足，五项 Runtime 检查全部通过，无跳过项或已知差异。
