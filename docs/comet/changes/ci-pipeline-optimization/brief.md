# Outcome

将 GitHub Actions 从同一提交在分支 push、PR、main 与 tag 上重复执行完整门禁，改造成一次验证、一次可信产物生成、一次轻量晋级的生产级流水线。功能分支只在 PR 上验证；main 为提交生成可追溯、可复用的 Windows 发布候选；tag Release 晋级该提交已经验证的候选，不重新构建或重跑完整测试。

# Scope

- 收紧 CI 触发器：功能分支 push 不触发 CI，PR 与 main push 各自只触发一次预期流程，tag 不触发 CI。
- 增加变更分类，区分轻量 workflow/docs 变更与 Go、C、native、构建、打包等核心变更。
- 将现有 Windows 串行大 job 拆为可并行的 build、behavior、Go coverage、C coverage、PE/package 门禁；原生构建产物只生成一次并由后续 job 下载复用。
- 增加唯一稳定的 `ci-gate` 汇总 job，接受按分类合理 skipped 的 job，拒绝失败、取消或缺失的必需结果。
- main CI 将已通过门禁的发布候选以 commit SHA 为身份保存，包含发布所需二进制、manifest、native archive、许可证和可验证元数据。
- tag Release 校验 tag 与 main commit 的关系，下载精确 commit SHA 的可信候选，仅执行版本化打包、checksum、SBOM、provenance/attestation 和 GitHub Release 发布。
- 更新 workflow 合约测试、文档与 Branch Protection 所需的 check 名称说明。

# Non-goals

- 不改变 SDK、bridge、WMPF/CDP 协议或单客户端语义。
- 不增加鉴权、证书、PFX、Authenticode 签名或时间戳服务。
- 不降低 Go 100% statement coverage、C 100% line/function coverage或生产级 Windows 行为验收。
- 不在 tag Release 中重新运行测试、race、fuzz、coverage、native build、soak 或 PE 构建门禁。
- 不把历史 tag 或失败 Release 的资产作为新流水线的可信输入。

# Acceptance examples

- 向功能分支 push 但尚未创建 PR 时不产生 CI run；同一 commit 创建或更新 PR 后只产生一套 PR CI。
- docs/workflow-only PR 运行分类、actionlint、文档与 release/package/checksum fixture，核心 Windows/coverage job 合理 skip，`ci-gate` 成功。
- Go、C、native 或生产构建相关 PR 运行其完整门禁；Windows build 仅执行一次，其 artifact 被 behavior、coverage 与 PE/package job 复用。
- 任一应运行 job 失败或取消时 `ci-gate` 失败；被分类器明确判定无需运行的 job 为 skipped 时 `ci-gate` 仍可成功。
- main commit 门禁通过后存在按完整 commit SHA 命名且包含元数据的可信候选；门禁失败时不生成可晋级候选。
- tag 指向没有可信 main 候选、候选 SHA/manifest/hash 不匹配或 tag 不在 main 时 Release 失败关闭，不回退到重新构建。
- 合法 tag 晋级同一候选并发布完整产品/native 资产、LF `SHA256SUMS`、SBOM、provenance 与 attestation，且不执行完整 CI。

# Constraints and invariants

- GitHub Actions 使用固定完整 commit SHA 的官方 action，最小权限，发布写权限只存在于发布 job。
- 可信候选必须绑定完整 source commit SHA；下载后逐文件校验 hash、manifest、PE/import/export 和发布资产集合，不允许“最新成功 run”式模糊选择。
- PR 与 main 的同一 workflow 使用互不冲突的 concurrency identity；取消只作用于同一 ref/PR 的旧 run。
- main artifact 的保存期必须覆盖正常发版窗口；Release 对过期或缺失 artifact 明确失败关闭。
- PowerShell 以真实构建、产物一致性、失败路径、清理、幂等和恢复行为验收，不设逐命令数值覆盖率。
- 所有 linked worktree 位于 `.worktree/<change-name>`。

# Decisions

- 用户确认取消当前重复执行的 Release 与 tag CI，并单独实施 CI 优化 change。
- 功能分支只触发 `pull_request`，`push` 仅限 `main`，tag 不运行 CI。
- 轻量变更目标 3–5 分钟；完整 CI 通过 Windows 并行拆分和 artifact 复用目标 10–20 分钟；tag Release 目标 3–5 分钟。
- Branch Protection 只 required 一个稳定 `ci-gate` job。
- main 生成按 commit SHA 标识的可信 artifact；tag Release 晋级同一 artifact，不重新验证同一源代码。
- Go 与 C 的 100% 覆盖率要求保留；PowerShell 采用生产级行为覆盖。
- Release 不要求任何证书或二进制签名。

# Open questions

- 无。

# Verification expectations

- 使用 actionlint 与 workflow 合约测试验证触发、分类、job 依赖、权限、固定 action SHA、artifact producer/consumer 和 Release 禁止项。
- 运行全部 Go 测试、workflow/package/checksum fixture 与既有 coverage contract。
- 在 GitHub PR 上验证轻量分类与完整分类的 job/skipped/`ci-gate` 组合；完整变更必须保持 Go/C 100% 和 Windows 生产门禁全绿。
- 合并后验证 main 只运行一次并生成按 commit SHA 标识的可信候选。
- 使用新 tag 验证 Release 只晋级该候选、资产和 attestation 完整且不重跑完整 CI。
