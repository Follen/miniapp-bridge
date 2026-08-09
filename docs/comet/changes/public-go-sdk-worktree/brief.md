# Outcome

发布 `github.com/Follen/miniapp-bridge/sdk`，让其他 Go 项目在不接触 `internal`、cgo 指针或 native handle 的前提下复用当前 WMPF/CDP 调试桥的全部外部行为。CLI 和 SDK 使用同一套 Service 核心；实现位于当前 Git worktree 的 `comet/public-go-sdk` 分支。

# Scope

- 将模块路径改为 `github.com/Follen/miniapp-bridge`，同步全部 Go import 并保持 CLI 可执行文件行为。
- 新增线程安全、可版本化的 `sdk` 包：构造、启动、关闭、状态、日志/状态/CDP 订阅、结构化及原始 CDP 请求、请求关联、jscontext 查询/选择/路由、目标发现/attach/detach、录制/回放和连接状态。
- 将生命周期、native ownership、网络代理、协议路由和 capture 资源统一放入 SDK Service；CLI 只负责参数、系统信号、输出和退出码。
- 保持 127.0.0.1:9421 调试服务、127.0.0.1:62000 CDP WebSocket、监听后 attach 的启动顺序、WMPF 私有 Protobuf/zlib/CDP 转换、ID/事件/错误语义和全部地址配置。
- 把 Frida 与 zlib 的仓库私有静态链接替换为最小 Windows 动态 loader；提供版本/ABI/export/hash 检查、native prepare/download/cache/offline、ZIP、manifest、许可证和 SHA256SUMS 脚本。
- 增加外部 Module、生命周期/并发/慢订阅、协议 differential、模拟代理、fake DLL、native 下载、Windows build/live matrix 和 coverage/race/vet 验证。
- 更新 README、SDK 文档、行为矩阵、已知差异、第三方声明和使用示例。

# Non-goals

- 不改变参考提交 `2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d` 的网络接口、协议字段、端口、启动/关闭顺序或 CDP 可观察语义。
- 不在 Agent 中实现协议路由、日志或业务状态；Agent 只负责 hook、patch、捕获和原始消息转发。
- 不要求 SDK 使用方安装 Node.js、Frida CLI、系统 PATH 中的 DLL 或访问 `internal` 包。
- 非 Windows 平台保留清晰抽象和可测试 fallback，但本 change 的 native 交付目标为 Windows amd64。

# Acceptance examples

- 干净外部 Go Module 通过 `replace github.com/Follen/miniapp-bridge => <repo>` 只导入 `sdk`，可 `go test`、`go build` 并运行 New/Start/Close，不需要仓库私有 `.a/.lib`。
- `Start` 在 listener、目标 attach、Agent load 完成后返回；启动取消完整回滚；并发/重复 Start 和 Close 不泄漏、不卡死，Close 超时后可再次等待最终结果。
- 每个日志、状态、CDP 订阅使用独立有界队列；慢订阅者被断开并返回 `ErrSlowSubscriber`，不会阻塞 native callback、协议路由或关闭。
- 结构化/原始 CDP 请求保持请求 ID、响应、通知、事件顺序、错误和多 `jscontextId` 路由；断线会终结 pending waiter 并可重连。
- fake DLL 覆盖成功、缺失、架构、hash、版本、ABI、导出和依赖错误；下载 server 覆盖缓存、并发、哈希失败、离线和原子安装。
- 现有 differential/golden、模拟代理、Windows build、live CDP matrix、race/vet 和声明范围内 100% Go 语句覆盖率全部通过。

# Constraints and invariants

- 公开 API 不泄漏 `internal` 类型、C 指针、GLib 对象或 native handle；所有错误通过返回值和 `errors.Is/As` 传播。
- Service 资源所有权唯一且关闭顺序固定：拒绝新操作 -> 客户端/监听器 -> replay/recording -> Agent script -> session -> device -> native runtime -> 关闭订阅。
- `Start(ctx)` 成功后立即返回；该 context 的后续取消异步触发有序关闭。启动阶段取消必须同步完成回滚并返回 context 错误。
- 订阅 publisher 永不执行用户代码或阻塞等待；订阅 Close 幂等，channel 关闭顺序可观察且无发送后关闭竞态。
- WMPF outer envelope、字段号、wire type、压缩标志、zlib 1.3.1 行为、未知消息原始字节和损坏帧恢复必须保持。
- 默认 native 版本固定为 Frida core 17.3.2、ABI 1；配置选择支持参考仓库的全部 WMPF 地址文件，默认地址配置嵌入二进制并可显式覆盖。
- DLL 只从 EXE 同目录或显式绝对路径加载；不依赖 CWD、PATH、注册表或全局安装。发布资产不提交到源码仓库。

# Decisions

- 用户选择在 `.worktree/public-go-sdk` 独立实现，分支名为 `comet/public-go-sdk`；本 change 在该 worktree 内创建。
- `Start(ctx)` 在完整启动就绪后返回；运行期 context 取消启动异步关闭。
- 慢订阅者有界队列满时只断开该订阅，并令 `Err()` 返回 `ErrSlowSubscriber`。
- 结构化 SDK 请求使用 Service 私有单调 ID；Raw 请求保留调用方 ID 但拒绝同一 route 的重复 pending ID。
- 默认关闭等待由调用者 context 控制，首次超时不取消真实关闭；后续 Close 可继续等待并返回保存的最终结果。
- 版本地址配置和现有协议实现作为兼容基线，不通过字符串拼接替代 Protobuf 编解码。

# Open questions

None. 已确认的行为由本 brief 和完整规格固定；未实现或未在真实 WMPF 设备上执行的内容必须在 Verify 报告中列为风险，而不能默认为通过。

# Verification expectations

- 运行 `go test ./...`、`go test -race ./...`、`go vet ./...`、既有 coverage gate、Frida tagged tests、WMPF differential/golden 和模拟 CDP matrix。
- 建立临时外部 Module，验证只依赖 `sdk`；执行生命周期、并发、context、订阅溢出、请求关联、上下文路由、录制回放、断线重连和损坏帧测试。
- 在 fake native DLL 和本地 HTTP server 上执行全部 loader/download 错误矩阵；验证 PE 架构、manifest、ABI、导出、ZIP slip、hash、offline 和原子缓存。
- 执行 Windows native build，确认 EXE 不再静态导入 `miniapp-frida.dll`，并运行真实 WMPF live matrix（Runtime/Debugger/Page/DOM/Network/Console/Performance、异常、断点、上下文切换、重连和优雅退出）。
- 记录每条命令的退出状态、关键输出、覆盖率范围和仍需真实环境验证的差异；归档前不得把未运行检查写为通过。
