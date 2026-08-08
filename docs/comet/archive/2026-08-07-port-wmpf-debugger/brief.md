# Outcome

交付一个名为 `miniapp-bridge` 的独立 Go 项目，在 Windows 首个平台上复刻参考仓库 WMPFDebugger（可验证参考提交 `2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d`）的全部可观察调试行为。程序不依赖 Node.js，Go 可执行文件嵌入 JavaScript Frida Agent，并提供兼容原项目的 WMPF 私有协议到标准 CDP 的桥接。

# Scope

- Go CLI、配置、日志、生命周期和平台抽象。
- Windows 进程发现：枚举 `WeChatAppEx.exe`，按父进程频次选择 WMPF 宿主，解析 `RadiumWMPF/<version>`。
- 通过 cgo 封装 `frida-core` 的 device、session、script 和 message callback；Agent 嵌入二进制并支持加载、消息转发、卸载、detach。
- 复原参考 Protobuf 的全部消息、字段号、wire type、嵌套关系；zlib 压缩标志、原始字节保留和损坏帧错误处理。
- 私有调试类别转换（含 `chromeDevtools`、`chromeDevtoolsResult`、`add/remove/connectJsContext`）以及 CDP 请求 ID 关联、事件顺序、广播和路由。
- `127.0.0.1:9421` WMPF 调试 WebSocket 服务与 `127.0.0.1:62000` CDP WebSocket 代理，支持多客户端、断线重连、录制和回放。
- 47 个参考版本地址配置迁移，版本选择逻辑和未知版本诊断。
- 构建/打包脚本、GPL-2.0-only 许可证、来源版权声明、golden fixtures、Windows smoke test、已知差异报告和实际验证记录。

# Non-goals

- 不改变参考项目默认网络地址、启动顺序或协议字段语义。
- 不在 JavaScript Agent 中实现协议路由、日志系统或业务状态管理。
- 不把 Frida 指针暴露给 Go 业务层；不引入 Node.js 运行时。
- 不承诺未提供真实 WMPF/WeChat/Frida DLL 的环境中完成端到端注入验证。

# Acceptance examples

1. 无参数启动后监听 `127.0.0.1:9421` 与 `127.0.0.1:62000`，输出 DevTools URL。
2. 发现目标父进程、解析版本、加载对应 Agent；未知版本和缺失 Agent 以可诊断错误退出或重试，不崩溃。
3. WMPF 二进制帧可解码、解压并转换为标准 CDP；CDP 请求可封装回 WMPF，响应按 ID 返回，事件按接收顺序广播给全部客户端。
4. `add/remove/connectJsContext` 更新、选择并路由当前 `jscontextId`；多客户端和断线重连保持一致状态。
5. 执行 `Runtime.enable`、`Debugger.enable`、`Runtime.evaluate` 的 golden fixture 对比参考输出、事件顺序和错误结果。
6. 优雅退出关闭 WebSocket、卸载 Agent、detach session、释放 Frida/GLib 资源，端口可立即重新绑定。

# Constraints and invariants

- Go 为主程序；cgo 只封装最小明确的 frida-core C API 边界。
- Agent JavaScript 由 `embed` 嵌入 Go 可执行文件，运行时不读取外部脚本且不依赖 Node.js。
- 保留未知 Protobuf 类别/字段的原始字节和诊断日志；损坏帧不得导致主进程崩溃。
- Frida/GLib 回调线程与 Go 生命周期同步，所有资源有明确所有权和释放路径。
- 参考提交用户给定值 `2b90b77fc6f13dd18480cd7d7dd9c052cc26c9d` 在远端不可解析；实际仓库存在并已审计的提交为 `2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d`，实现以后一提交为基线并在报告中标明。
- 许可证为 GPL-2.0-only；移植自参考/腾讯第三方代码的文件保留来源与版权信息。

# Decisions

- 工作区使用 Native `current` 隔离，change 名为 `port-wmpf-debugger`。
- 首个平台为 Windows；平台接口从第一版抽象出来。
- 默认端口固定为 9421（调试服务）和 62000（CDP 代理），CLI 允许显式覆盖且校验 1-65535。
- 先启动两个 WebSocket 服务，再发现/附加进程并加载 Agent；保持参考实现要求的启动和连接顺序。
- 以结构化 Protobuf 编解码和 zlib 实现协议，不使用字符串拼接替代。
- 用户已确认目标、范围、默认端口/启动顺序、Windows 首发、GPL-2.0-only、参考提交实际可解析哈希及验收标准。

# Open questions

- 无。

# Verification expectations

- Go 单元测试覆盖 Protobuf、压缩、ID 关联、jscontext 路由、多客户端广播、断线重连、损坏帧和版本配置选择。
- 参考与 Go 的 golden fixtures 对比解码对象、CDP payload、事件顺序和错误结果。
- Windows smoke test 验证监听、进程附加、Agent 加载、WMPF 上游、DevTools 连接和三条 CDP 命令。
- 记录每条命令的退出状态、关键输出、跳过项和真实环境限制。
