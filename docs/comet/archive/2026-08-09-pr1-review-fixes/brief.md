# Outcome

修复 PR #1 在独立三组审查中确认的全部可操作问题，使公开 Go SDK、独立 CLI、Windows native 生命周期、WebSocket 广播、GitHub CI/Release、许可证和公开文档达到可合并状态，并保持现有 WMPF/CDP 外部行为不变。

# Scope

- 调整 Windows native 关闭顺序，确保最后一个 DLL 引用释放前实际调用 runtime shutdown，并用可观测测试 DLL 验证调用。
- 将 CDP WebSocket 广播改为每客户端有界队列和独立 writer；慢客户端只断开自身，不阻塞协议路由、其他客户端或关闭。
- 恢复 CLI 对 Frida error 和 `--debug-frida` 消息的兼容输出，同时保证 SDK 日志事件只发布一次。
- 使用不丢精度的 JSON 数字解码和 ID 规范化，覆盖 `2^53`、`MaxUint64`、字符串、通知、重复和未知请求 ID。
- 自动 attach 后保存并公开真实 target PID、名称、路径和版本；发布完整 starting/stopping/terminal 状态变化。
- 在请求取消、超时、断线和 Close 时同步清理 SDK pending 与内部 correlator；首次 upstream 尚未建立时请求立即返回结构化 `ErrNoUpstream`。
- 修复 Release tag 的事件 SHA 绑定、产品/native tag 发布前后复核和竞态测试。
- 修复 Go 格式门禁，更新官方 Actions 到原生 Node 24 版本并继续固定完整 commit SHA。
- 固定并打包 Frida 17.3.2 的 `COPYING` 与 `COPYING.LIB`，纳入 checksum、manifest/清单、双语文档和契约测试。
- 移除公开 Git 跟踪中的机器绝对路径 Hook 投影；修复 Known Differences 链接、验证结论矛盾，并精简或脱敏已归档 Comet 原始运行证据。
- 为每项修复增加确定性单元、race、打包和 workflow 合约测试，并回归现有 differential、模拟代理、100% coverage 和 Windows native build。

# Non-goals

- 不改变 `127.0.0.1:9421`、`127.0.0.1:62000`、启动顺序、WMPF 字段、压缩标志、CDP 事件顺序或错误传播语义。
- 不新增非 Windows native 发布目标，不改变 Frida core `17.3.2`、ABI `1` 或 zlib `1.3.1`。
- 不创建或移动 `v0.0.1` tag，不实际发布 GitHub Release，不声称未安装历史 WMPF 版本已完成 live 验证。
- 不修改根 worktree 中与本 change 无关的未跟踪归档和事务文件。

# Acceptance examples

- 关闭 Service 时，测试 DLL 记录顺序为 script unload、session detach、device close、runtime shutdown、DLL unload，且每步恰好一次。
- 一个永不读取的 WebSocket 客户端达到队列上限后被单独断开；健康客户端仍按原顺序收到事件，广播调用和 Close 不等待慢 socket deadline。
- Frida error 写入 CLI stderr；只有启用 `--debug-frida` 时 debug 消息写入 stdout；SDK 订阅各收到一条对应事件。
- 请求 ID `9007199254740993` 与 `18446744073709551615` 能准确关联相同 JSON 数字响应，取消后两层 pending 数量均为零。
- 默认自动 attach 的 `Status().Target` 包含真实发现目标，首次 upstream 前的结构化和原始请求立即返回 `ErrNoUpstream`。
- tag push 始终 checkout `github.sha`；tag 在 checkout、打包、draft reconcile 或 publish 前后移动都会失败且不会错误发布另一个 commit。
- native/product ZIP 同时包含项目许可证、zlib 许可证、Frida `COPYING`、Frida `COPYING.LIB`、third-party notices 和内部校验清单。
- 干净 checkout 通过 gofmt、单元测试、race、100% coverage、workflow contract/actionlint、Windows native build 和外部 Module 导入测试。
- tracked 文件不包含开发者绝对 worktree/Tencent 路径，公开 Markdown 链接有效，Known Differences 与 Verify 结论一致。

# Constraints and invariants

- Go 使用方不接触 `internal`、C 指针或 native handle；CLI 与 SDK 继续调用同一核心实现。
- 慢消费者处理必须有明确的所有权、关闭顺序和有界内存，不能从 native callback 同步执行网络写入或订阅者代码。
- Release publisher 保持最小 `contents: write` 权限，不 checkout 或执行不可信仓库脚本；所有第三方 Actions 固定完整 SHA。
- 许可证正文来自固定 Frida 17.3.2 上游内容并由 SHA-256/测试锁定，不以链接或项目 GPL 文本替代。
- 所有改动在 `comet/pr1-review-fixes` worktree 完成，目标分支固定为 `comet/public-go-sdk`。

# Decisions

- 用户在收到完整 PR 审计报告后明确要求使用 Comet 修复，范围解释为全部已确认 findings 和两个直接影响生产 SDK 的 open question。
- 首次 upstream 未连接时不排队等待，立即返回 `ErrNoUpstream`，避免无界挂起。
- `StateStarting` 与 `StateStopping` 作为公开状态必须发布，终态仍保持现有错误语义。
- `concurrency.queue: max` 是 GitHub 当前正式支持的语法，保留该行为；仅继续对 actionlint 滞后 schema 做精确忽略。
- 不手工删除原 PR worktree 的失效 Comet transaction；使用隔离 worktree 完成本次 change。

# Open questions

无。上一轮审计报告构成共享理解摘要，用户随后明确回复“修”。

# Verification expectations

- 对涉及包运行聚焦单元测试和 race 测试，再运行 `go test ./...`、`go vet ./...`、全仓 race 与 100% coverage gate。
- 运行 Windows frida tagged tests、native loader 测试 DLL矩阵、native/product reproducible packaging、offline prepare 和 Windows native build。
- 运行 GitHub CI/Release 合约测试、actionlint、内嵌 Bash 语法和 tag 竞态测试。
- 运行外部临时 Module、协议 differential、模拟端到端代理及现有 live/smoke 可在当前环境执行的部分；环境依赖项如实记录。
- 验证 Git diff 清洁、公开路径脱敏、Markdown 链接和许可证资产清单。
