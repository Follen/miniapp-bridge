# Public Go SDK

## Module and package boundary

模块路径为 `github.com/Follen/miniapp-bridge`，稳定公开包为 `github.com/Follen/miniapp-bridge/sdk`。公开声明不得暴露 `internal` 类型、cgo 指针、GLib 对象、Frida handle 或 WebSocket 实现对象。`cmd/miniapp-bridge` 只负责参数、信号、输出和退出码，并与 SDK 调用同一核心实现。

## Construction and lifecycle

`New(Options)` 只校验选项和分配内存，不监听端口、加载 DLL、attach、注册全局信号或退出进程。零值选项保留 `127.0.0.1:9421`、`127.0.0.1:62000`、自动发现/attach、嵌入式地址配置和参考启动顺序。

`Start(ctx)` 并发安全；一个调用执行 listener、upstream/CDP server、目标发现、Frida attach 和 Agent load，其他调用等待同一结果。启动过程发布 `StateStarting`，完全就绪后发布 `StateRunning`。取消会回滚全部已取得资源并返回结构化 context 错误；运行期 lifetime 取消触发异步有序 Close。

`Close(ctx)` 在 Start 前、并发、重复和超时场景均安全。首个调用启动唯一关闭流程，各调用者只用自己的 context 等待。关闭过程先发布 `StateStopping`，然后拒绝请求、失败并清理两层 pending、关闭客户端 writer 和网络资源、停止 replay/recording、卸载 Agent、detach session、关闭 device、调用 native runtime shutdown，最后释放 DLL 并发布终态。runtime shutdown 必须在最后一个 loader 引用释放前真实执行且每个资源只释放一次。

## Public data, target status, and errors

SDK 提供版本化 DTO 和可 `errors.Is/As` 的 sentinel/结构化错误。`Status()` 返回副本和稳定排序的 context。默认自动 attach 与显式 attach 都必须在 `TargetStatus` 中保存真实 PID、名称、路径和版本；detach、失败和 Close 不得留下伪 attached 状态。日志、状态和事件订阅保持独立有界队列，慢订阅者只关闭自身并报告 `ErrSlowSubscriber`。

## CDP requests and correlation

结构化和原始 CDP 请求共享一个串行 dispatcher。首次 upstream 尚未连接、已断线或正在关闭时，需要响应的请求立即返回对应结构化错误，不注册 waiter。通知不进入 pending 表。请求取消、超时、发送失败、upstream 断线和 Close 必须同时移除 SDK pending 与内部 app correlator。

原始 JSON 使用保留数字文本的解码方式。字符串 ID 和合法 JSON 数字 ID 以类型与规范化字面值关联，不经过 `float64`；必须覆盖 `2^53` 边界和 `uint64` 最大值。重复 ID 被拒绝，未知响应不影响其他请求，错误响应与事件保持原顺序。

## WebSocket delivery

每个 WebSocket 客户端拥有独立有界 outbound queue 和单一 writer goroutine。协议 dispatcher 的广播只复制/入队，不执行 socket write；队列溢出只断开该客户端。每个健康客户端保持事件顺序，一个慢或失效客户端不能阻塞 native callback、协议路由、其他客户端或关闭流程。writer、queue、connection 和 Hub 的所有权及关闭顺序必须无双重关闭、泄漏或 goroutine 残留。

## CLI logging compatibility

Frida info、error 和 debug 先形成一条 SDK `LogEvent`。CLI 通过同一 logger 输出：info 到 stdout、error/Agent exception 到 stderr，debug 仅在 `--debug-frida` 开启时到 stdout。SDK 事件总线、CLI writer 和 native callback 之间不得重复发布同一事件。

## Native runtime and licenses

Windows amd64 通过最小动态 C loader 加载固定 Frida core `17.3.2`、ABI `1` 和 zlib `1.3.1`。指针和导出表留在 `internal/frida`，native callback 只复制并排队消息。loader 引用计数、runtime init/shutdown 和 DLL unload 串行化；测试 DLL 必须证明 shutdown export 的真实调用和卸载顺序。

源代码与 Release 资产保留项目 `GPL-2.0-only`、zlib 许可证、Frida 17.3.2 `COPYING`、其要求的 `COPYING.LIB` 和第三方声明。Frida 文件由固定上游内容和 SHA-256 锁定。native ZIP 与产品 ZIP 均包含这些正文及覆盖所有 payload 的 `SHA256SUMS`；缺失或内容漂移使打包失败。

## Release and CI integrity

tag push checkout 必须绑定触发事件的 `github.sha`，并断言所请求产品 tag 的 peeled commit 等于该 SHA；手动 workflow 才按输入 tag 解析。产品和 native compatibility tag 在 draft 创建、资产 reconcile、publish 前后都解析并与预期 commit 比较，移动即失败。发布 job 保持单点最小写权限，不 checkout 或执行仓库脚本，资产仍逐字节校验并支持 draft 恢复。

CI 和 Release 使用原生 Node 24 的官方 Actions 并固定完整 commit SHA。`concurrency.queue: max` 作为 GitHub 正式语法保留；actionlint 的 schema 滞后只允许精确忽略该字段，其他 workflow 错误必须失败。所有 tracked Go 文件必须通过仓库指定 Go 版本的 gofmt。

## Public repository hygiene and documentation

Git 不跟踪由 Comet 投影的开发机绝对路径 Hook 文件。公开文档链接必须指向稳定路径；Known Differences 如实列出 malformed frame、平台和 live 版本风险，验证报告不得声称不存在这些差异。历史 Comet 原始 runtime evidence、PID、用户名、安装路径和无效 receipt 不进入公开提交；保留的规格和验证摘要必须脱敏且足以审计。

## Compatibility and verification

修复不得改变监听地址、WMPF Protobuf 字段/wire type、压缩标志、CDP payload、请求/事件顺序、context 路由、Agent hook 职责、启动顺序或已确认错误语义。验证包括外部 Module、单元测试、race、100% 声明范围 coverage、协议 differential、模拟代理、慢客户端、取消/断线、native loader/download、Windows native build、可复现打包、workflow 合约和当前环境可执行的 live matrix。未执行的真实环境检查必须明确记录，不得写成通过。
