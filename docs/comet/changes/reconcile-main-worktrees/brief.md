# Outcome

将本地与远端仓库整理为单一、可追溯且干净的最新 `main`：保留本地尚未推送的 WMPF 完整性归档和 Go SDK Comet 归档，合入远端 Go SDK 与 CI 修复，归档 `AGENTS.md` 的 Windows live smoke 自动化规则，修复失败的首个 GitHub Release，并清理已完成的 worktree 与过期分支。

# Scope

- 获取并核对 `origin/main`、`origin/comet/public-go-sdk` 与全部本地分支、worktree 的提交关系。
- 在当前根工作目录的 `main` 上合并 `origin/main`，保留双方已确认的实现、测试、文档和 Comet 归档。
- 将当前 `AGENTS.md` 的 Windows live smoke 自动打开小程序规则纳入版本控制。
- 运行与合并风险相匹配的 Go、脚本契约和 Git 状态验证。
- 将整理后的 `main` 推送到 `origin/main`，确认本地与远端指向同一提交。
- 删除已完成且干净的 `.worktree/pr1-review-fixes`、`.worktree/public-go-sdk`，并删除其不再需要的本地分支。
- 在确认 SDK 分支内容已经由已合并 PR 覆盖后，删除远端 `comet/public-go-sdk` 分支并 prune 远端引用。
- 定位 GitHub Release 失败原因，在最终 `main` 上恢复 `v0.0.1` 发布并核对产品与 native 资产。
- 面向 CLI 与 Go SDK 使用方重新编写中英文 README，提供安装、快速开始、配置、SDK、构建、测试、发布资产和故障定位入口。
- 清理根目录中的旧 worktree、参考仓库副本、构建输出、空目录及可从本地缓存重建的 native 中间产物。
- 完成本 change 的 Verify 与 Archive，使整理过程本身可审计。

# Non-goals

- 不新增产品功能或改变 miniapp-bridge 的协议、端口、启动顺序和运行时行为。
- 不改写任何已经成功发布并被外部 Release 资产引用的版本。
- 不删除仍包含未合并唯一提交或未提交文件的分支、worktree。
- 不清理仓库外部的缓存、SDK、微信或系统环境。
- 不在 README 重复第三方来源、固定参考提交或第三方版权明细；这些信息统一由 `THIRD_PARTY_NOTICES.md` 承载。

# Acceptance examples

- 整理完成后，`git rev-parse main` 与 `git rev-parse origin/main` 输出相同提交，`git status --short --branch` 不显示 ahead/behind 或未提交改动。
- `main` 同时包含 `5aa24e5`/`08cd400` 的 SDK 与 CI 行为，以及本地 `d9e7c19`、`e6a70d6` 所承载的实现和 Comet 归档内容。
- `git worktree list` 最终只保留根工作目录；两个 `.worktree` 子目录不再登记。
- 旧本地 worktree 分支和远端 `comet/public-go-sdk` 不再存在，而 `main` 的测试与构建契约仍通过。
- `docs/comet/archive` 保留 WMPF parity、public Go SDK 和 PR review 的全部正式归档。
- GitHub Release workflow 成功，`v0.0.1` 和 `native-v17.3.2-abi1` Release 均存在且资产、manifest 与 SHA256SUMS 完整。
- `README.md` 与 `README.zh.md` 从开发者任务出发说明项目用途；不再使用“WMPFDebugger 的 Go + Frida 移植版本”作为项目介绍，不出现固定参考提交或 Tencent 协议来源段落，只链接 `THIRD_PARTY_NOTICES.md`。
- README 明确当前 live 支持目标为 `Windows amd64 / WMPF 25297`，不把其余历史地址配置描述为已验证的生产支持。
- 根目录不再包含 `.reference`、`dist`、`.worktree` 或空 `wmpf`；`third_party/downloads` 的已校验压缩包缓存保留，已解压 devkit、native 构建输出和 zlib 构建树被清理。

# Constraints and invariants

- 不使用 hard reset 或 checkout 丢弃用户改动。
- 合并冲突按语义保留双方已确认契约；远端 production SDK/CI 修复优先作为当前实现，本地独有归档与 smoke 规则必须保留。
- 删除 worktree 前必须再次确认其工作树干净，且对应提交已被 `main` 覆盖或已由 squash merge 等价替代。
- 推送前必须完成本地验证；推送后再次 fetch 并核对本地/远端提交一致。
- Release 必须从包含 `08cd400` 修复的最终提交构建；不得再次从已知会失败的旧 tag 提交直接重跑。
- 删除根目录产物前先完成本地验证和 Release 所需构建；只删除 ignored 且可证明可再生的目录。
- Comet Runtime 管理文件只通过公开 CLI 更新。

# Decisions

- 使用 `current` 隔离，直接整理根目录的 `main`，不新建额外 worktree。
- 使用普通 Git merge 保留本地与远端两侧历史，不 rebase 或强制推送。
- `AGENTS.md` 的 Windows live smoke 自动化说明属于本次需归档的有效项目改动。
- 旧分支只在证明其内容已合并或等价覆盖后删除。
- Release `31386600761` 的直接根因是 `v0.0.1` 指向旧提交 `5aa24e5`：该提交的 Windows 测试依赖不可移植的 tar/xz 行为、缺失 zlib fixture 目录，并使用错误的 Frida 许可证哈希；修复位于后续 `08cd400`。
- 用户已确认在最终 `main` 验证通过后更新尚未成功发布的 `v0.0.1` tag，并重新触发 Release。
- README 使用产品能力说明开篇，不以移植来源作为项目定位；第三方与来源信息只保留统一的 `THIRD_PARTY_NOTICES.md` 链接。
- README 的生产支持矩阵只宣称 Windows amd64 上的 WMPF 25297；其他内嵌地址配置作为兼容数据存在，不宣称逐版本 live 验证。
- 根目录清理保留 `third_party/downloads` 已校验下载缓存，以避免再次发生耗时网络下载；其余 ignored 构建/解压产物在验证后删除。

# Open questions

无。

# Verification expectations

- 执行 `go test ./... -count=1` 与 `go vet ./...`。
- 执行仓库现有的脚本契约、SDK 外部模块或覆盖门禁中适用于当前 Windows 环境的验证。
- 核对关键文件、Comet 归档目录、提交祖先关系、worktree/branch 列表和远端 ref。
- 监控 Release workflow 到终态，检查两个 Release 的 tag、target、资产名、数量和 SHA-256。
- 运行 README/Release 契约测试并人工核对中英文信息架构、命令可执行性、链接和支持矩阵。
- 清理后统计根目录与 ignored 目录，确认只保留源码、正式文档和明确保留的下载缓存。
- 记录每条验证命令的退出状态与关键输出；任何跳过项均在 verification 中说明。
