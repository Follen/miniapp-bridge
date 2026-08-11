# Repository State

## 目标状态

仓库的权威开发分支为 `main`。完成整理后，本地 `main` 与 `origin/main` 必须指向同一经过验证的提交，该提交包含远端 SDK/CI 修复、本地尚未推送的 WMPF 与 SDK Comet 归档，以及项目 Windows live smoke 自动化说明。

## 历史整合

- 当本地 `main` 与 `origin/main` 双向分叉时，必须通过非破坏性 merge 整合，不得通过 hard reset、强制推送或丢弃提交来制造一致。
- 整合结果必须保留双方独有文件；实现文件发生冲突时，以远端已通过 PR/CI 的 production SDK 与修复为当前实现基线，同时保留本地独有的兼容性修复、文档和验证归档。
- 已成功发布且存在外部 Release 资产的版本标签不得因仓库整理被移动或重写。

## 归档完整性

- `docs/comet/archive` 必须保留 port WMPF debugger、complete WMPF parity、public Go SDK 和 PR review 相关的正式归档。
- `AGENTS.md` 必须记录 Windows live smoke 在 Frida attach 后通过 UIAutomation 唯一定位并打开最近使用小程序的规则，以及成功判定条件。
- 当前整理 change 必须经过 Shape、Build、Verify、Archive，并留下可审计的 verification 记录。

## 开发者文档

- `README.md` 和 `README.zh.md` 必须以开发者可执行的任务组织内容，包括获取发布包、启动 CLI、连接 DevTools、导入 SDK、准备 native runtime、从源码构建、运行测试和查阅深入文档。
- 项目介绍必须直接说明 WMPF 到 CDP 的能力、适用平台和交付形态，不以“WMPFDebugger 的 Go + Frida 移植版本”作为定位。
- README 不重复固定参考提交、地址/Agent 来源和 Tencent 协议版权文字；许可证区只链接项目许可证与 `THIRD_PARTY_NOTICES.md`，详细来源统一由后者维护。
- 当前生产支持矩阵只声明 `Windows amd64 / WMPF 25297`。内嵌的其他历史地址配置不得表述为均已完成 live 验证。
- 中英文 README 的命令、默认端口、版本常量、资产名称和文档链接必须一致。

## Worktree 与分支生命周期

- 只有在 worktree 干净且其提交已被 `main` 包含或经 squash merge 等价覆盖时，才可移除 worktree。
- 已完成 worktree 删除后，应删除对应的过期本地分支。
- 已合并的远端 SDK change 分支应删除；删除后必须 fetch/prune 并确认远端仅保留权威分支。
- 最终 `git worktree list` 只登记根工作目录，工作树保持干净。

## 本地目录清理

- 验证完成后删除 `.reference`、`dist`、已注销的 `.worktree` 和空 `wmpf` 目录。
- 删除 `third_party/frida/devkit-17.3.2`、`third_party/frida/runtime-17.3.2`、`third_party/zlib/lib` 和 `third_party/zlib/src-1.3.1` 等可再生产物。
- 保留 `third_party/downloads` 中哈希已校验的 Frida/zlib 压缩包缓存，后续构建可离线重建，避免再次进行耗时下载。
- 不删除 tracked 文件、Comet 正式归档或仓库外部缓存。

## 验证与发布

- 合并结果必须通过 Go 单元测试和静态检查，并通过仓库中适用于当前环境的关键脚本契约或覆盖门禁。
- 推送必须使用普通 fast-forward push 将已经整合的本地 `main` 发布到 `origin/main`，不得强制推送。
- 推送后必须刷新远端引用，确认本地 `main` 与 `origin/main` 的提交哈希一致，且旧 worktree/分支已清理。
- 首个产品 Release 必须从包含 Windows fixture、Frida 下载和许可证哈希修复的提交构建，不能从已知失败的 `5aa24e5` 直接重跑。
- Release workflow 成功后，产品版本与 native ABI 版本必须各自只有一个 Release；预期资产必须存在且与各自 `SHA256SUMS` 一致。
- 因 `v0.0.1` 从未形成成功 Release，最终 `main` 验证并推送后应更新该产品 tag 到最终提交并触发一次新的 tag push Release；操作前后均应核对远端 tag target。
