# Outcome

将当前 `miniapp-bridge` 从部分兼容实现推进到参考 WMPFDebugger 固定可验证提交 `2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d` 的完整可观察行为复刻。所有参考行为必须有 Go/Agent 实现和可重复测试；任何 skipped、partial 或 mock-only acceptance 都不得提交 Verify pass。

# Scope

- 真实 `frida-core 17.3.2` Windows cgo 集成：device 枚举、进程元数据、attach、session/script、消息回调、线程与释放。
- Go CLI 启动顺序、自动发现 `WeChatAppEx.exe` 父进程、解析 `RadiumWMPF/<version>`、精确版本地址选择、Agent 加载/卸载。
- 从参考生成 JavaScript 恢复全部 55 个 Protobuf 消息的 Go 运行时编解码，包括嵌套、重复字段、wire type、未知字段、zlib 和损坏帧。
- 完整 `RemoteDebugCodex` 类别转换和错误语义，覆盖所有调试类别、snake_case/camelCase、原始字节诊断。
- CDP 请求 ID、响应/事件顺序、广播、`jscontextId` add/remove/connect/route、断线重连和错误传播。
- `127.0.0.1:9421`、`127.0.0.1:62000` WebSocket 服务、录制回放、CLI、配置、日志和优雅退出。
- 参考 differential runner：同一输入帧同时送入参考 JS 与 Go，比较 protobuf、payload、事件序列、错误。
- Windows live 验证分为链路 smoke 与 CDP matrix：链路 smoke 只证明真实目标附加、Agent 加载、WMPF upstream、三条基础命令和退出重绑定；CDP matrix 复现真实 DevTools 初始化序列，并覆盖 Runtime、Debugger、Page、DOM、Network、Console、Performance 的代表性命令、异常、断点、上下文切换和断线恢复。
- 模拟端到端代理矩阵：fake upstream 加多个 WebSocket client，覆盖全部 CDP domain 的透明 payload、请求关联、事件广播、错误响应、通知、重复/未知 ID、长消息、并发、顺序、损坏帧和重连。

# Non-goals

- 不降低验收标准，不以 mock 或静态文件替代真实 frida-core/Windows 端到端证据。
- 不改变参考端口、启动顺序、字段号、类别名称、错误和连接语义。

# Acceptance examples

1. 无 Frida SDK、DLL 或真实目标时，native acceptance 明确失败；Verify 不得通过。
2. 有匹配 Windows SDK/DLL/目标时，程序自动 attach 并收到 Agent message；退出释放后端口立即重绑。
3. 55 个消息类型的 encode/decode golden 与参考逐字段一致；未知字段保留，损坏帧返回错误且进程继续。
4. 全部 RemoteDebugCodex 类别 differential fixtures 通过，包含 zlib、上下文、CDP、错误和事件顺序。
5. 多 CDP 客户端、断线重连和上下文切换测试通过，Runtime.evaluate 响应 ID 精确对应。
6. Windows 链路 smoke 不作为完整 CDP 功能证明；只有扩展 live CDP matrix 的 Runtime、Debugger、Page、DOM、Network、Console、Performance 代表性命令及异常、断点、上下文、重连结果全部通过，才可声明真实 CDP 功能验收通过。

# Constraints and invariants

- Verify 只接受全部 acceptance `passed`；任何 `skipped`/`failed`/`partial` 直接回 Build。
- native 绑定默认构建必须可在 Windows SDK 环境编译；测试使用真实 DLL 或明确的可替换 test double，不能把 mock 标作真实通过。
- 归档前必须提供命令退出状态、输入、输出和可重放 fixture。

# Decisions

- 使用 Native `current`，change 名为 `complete-wmpf-parity`。
- 测试先行：每个缺口先增加失败测试/fixture，再实现，再运行完整回归。
- 用户要求“完整复刻”，已确认上一 change 的 partial/skip 结论不再接受。
- 用户已确认“完全测试盯死”；所有 acceptance 必须有真实可重放证据，任一 skipped/partial/mock-only 自动回 Build。
- 用户确认产品恢复为支持参考项目覆盖的全部 WMPF 版本；每个地址配置版本都必须可加载、可诊断并纳入版本矩阵验证。
- 生产等价定义为参考提交全部可观察行为、目标实际暴露 CDP 方法的透明转发和原始错误保真；不虚构目标未暴露的 Chromium 能力。
- 验证证据严格分层为协议 differential、模拟端到端代理测试、Windows live 验证；不得把基础三命令链路 smoke 描述成“全部 CDP 功能验证”。

# Open questions

- 无。

# Verification expectations

- `go test ./internal/... -coverprofile=coverage.out` 与 `go tool cover -func=coverage.out` 的总语句覆盖率必须为 100.0%；不得通过排除生产文件、删除防御分支或伪造 coverage profile 达成。
- differential runner 比较参考 JS 与 Go 的字节/对象/payload/事件顺序/错误结果。
- fake upstream + 多 WebSocket client 的模拟端到端测试覆盖 CDP domain、ID、通知、长消息、并发、重连、损坏帧和事件顺序。
- `go test -tags frida ./...` 在真实 SDK 环境通过；Windows 链路 smoke 与扩展 live CDP matrix 分别通过并分别记录。
- Verify evidence 不允许 manual receipt 替代未执行的真实检查。
