# Outcome

记录用户在 Build 前将公开 Go SDK 实现迁移到 `.worktree/public-go-sdk` 的决定，并关闭这个已绑定当前目录、尚未实施产品代码的 Shape change。正式实现由新 worktree 中的独立 Native change 承接。

# Scope

- 保留本轮完整代码、native、测试和发布链审计结论。
- 记录当前 change 没有进入 Build、没有修改产品代码、没有建立 canonical capability。
- 在本 change 归档后，从当前 `main` 创建 `.worktree/public-go-sdk` 与 `comet/public-go-sdk`，在其中重新建立正式 SDK change。

# Non-goals

- 本 change 不实现 SDK、module path、动态 loader、发布资产或测试。
- 本 change 不改变 CLI、WMPF/CDP、Frida、zlib、构建或文档行为。
- 本 change 不创建 canonical SDK 规格；正式完整规格属于 worktree change。

# Acceptance examples

- `git diff` 中除 Native change 自身管理产物外没有产品文件变化。
- Native scope 明确记录 no-code 原因并通过 required check。
- 归档后当前目录无 active change，可以安全创建用户指定的新 worktree。

# Constraints and invariants

- 不复制 active change、不手改 `workspace.json`、Runtime 状态、hash、锁或事务。
- 不把当前目录绑定的 change 冒充为 worktree change。
- 正式 SDK 的所有已确认需求和决定必须原样写入新 worktree change。

# Decisions

- 用户选择 `Start(ctx)` 在 listener、attach、Agent load 全部就绪后立即返回，运行期取消触发有序关闭。
- 用户选择慢订阅者队列满时只断开该订阅，并返回 `ErrSlowSubscriber`。
- 用户明确要求实现位于 `.worktree/public-go-sdk`；change 分支使用 `comet/public-go-sdk`，目标分支为当前 `main`。
- 用户确认先正常收尾当前未实施 change，再在新 worktree 建立正式 change。

# Open questions

None.

# Verification expectations

- 运行 `git diff --name-only`，确认不存在本 change 引入的产品实现变化。
- 使用 Native no-code scope 和 required check 验证本 change 仅为迁移记录。
- Archive 完成后确认 `comet native status --json` 没有 active change。
