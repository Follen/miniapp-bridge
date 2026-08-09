# WMPFDebugger -> miniapp-bridge 行为矩阵

| 参考源码入口 | 输入 | 输出 | 状态变化 | 网络消息 | 错误行为 | Go 对应实现 | 验证方式 | 完成状态 |
|---|---|---|---|---|---|---|---|---|
| `src/cli.ts` | CLI 参数/帮助 | 端口与 debug/录制/回放选项 | 配置初始化 | 无 | JS Number 兼容端口校验；非法参数退出 2 | `internal/config`, `cmd/miniapp-bridge` | config tests + CLI exit smoke | 已实现/已验证 |
| `src/index.ts:debug_server` | 裸 `DebugMessage` 二进制帧 | CDP payload/上下文事件 | context、request 状态更新 | `chromeDevtoolsResult` -> CDP 文本 | 损坏帧记录并继续 | `internal/app`, `internal/wmpf` | 参考 differential：同帧 payload、字段、错误、事件顺序 | 已实现/已验证 |
| `src/index.ts:proxy_server` | CDP 文本 | `chromeDevtools` protobuf | 单调 seq、参考 0–100 op ID、pending request | 多 WMPF client 广播 | 断线移除客户端并记录日志 | `internal/app`, `internal/cdp`, `internal/proxy` | fake upstream + 多 client：domain、重连、ID、通知、长消息、并发、损坏帧、顺序 | 已实现/待本轮完整复验 |
| `src/index.ts:frida_server` | FULL scope process metadata | attach/load callback | device/session/script ownership | Agent message -> Go logger | 进程/路径/版本/config/attach 错误可诊断 | `internal/frida`, `internal/process`, `internal/version` | native enumerate/lifecycle/reattach | 已实现/已验证（WMPF 25297） |
| `frida/hook.js:patchOnLoadStart` | rcx/rdx 与 scene 指针 | debug flag=1、scene=1101 | 目标调试条件改变 | `send()` 日志 | 仅参考允许的 scene | embedded `frida/hook.js` | live Agent callback | 已实现/Agent load 已验证，OnLoadStart 待本轮 miniapp upstream |
| `frida/hook.js:patchCDPFilter` | filter 结构 | 值 6 改 0 | CDP filter patch | `send()` 日志 | null pointer 返回 | embedded `frida/hook.js` | live hook logs | 已实现/Agent load 已验证，filter callback 待本轮 miniapp upstream |
| `WARemoteDebugProtobuf.js` 全 55 类型 | protobuf bytes/struct | typed + generic encode/decode | typed/generic 均保留未知 wire bytes | 外层/调试/category payload | 截断、fixed32/fixed64/bytes/varint 损坏返回 error | `messages.go`, `generic.go`, `registry.go` | 55 类型、131 字段、55 explicit-zero、6 corrupt golden | 已实现/已验证 |
| `RemoteDebugCodex.wrap/unwrapDebugMessageData` | 19 category、snake_case、zlib flags | 结构化 category；未知 category 外部 `{}` 且 raw 可诊断 | originalSize fallback、压缩统计 | 18-export DLL ABI 提供 stock zlib 1.3.1 exact deflate/inflate；Go Module 无 `zlib1.dll` import | 损坏/超限 zlib 返回 error | `protocol.go`, `zlib_windows_cgo.go`, `internal/frida` loader | 7 debug、11 developer、10 client、26 incoming differential；外部 tagged PE import 审计 | 已实现/已验证 |
| `RemoteDebugCodex` DataFormat/cmd | 1000-1006、2000-2006、3001-3003 | request/response direction-aware typed payload | client response 显式零字段 | DataFormat protobuf | outgoing 未知 cmd 报错；incoming 未知 cmd 返回 `{}` | `transport.go` | 13 mapping + 7 developer + 7 client response + 12 incoming differential | 已实现/已验证 |
| `add/remove/connectJsContext` | context 消息 | CDP 路由 context ID | 注册/选择/删除 | 出站 `jscontext_id` | connect 未知 ID 自动建立 | `internal/context`, `internal/app` | app context integration | 已实现/已验证 |
| capture/replay 扩展 | 裸 DebugMessage 帧 | 长度前缀 capture 文件/按序回放 | recorder 锁与 flush | 回放重新进入 decode pipeline | 截断或 >64 MiB 声明长度返回 error | `internal/capture`, `internal/app` | 文件 roundtrip、并发、corrupt tests | 已实现/已验证 |
| 启动顺序 | listeners -> enumerate -> config -> attach -> load | 服务与 Agent ready | 资源按逆序释放 | 9421/62000 | 任一步失败退出并关闭 listener | CLI + native backend | native lifecycle; strict smoke | 已实现/已验证 |
| Windows live | attach 后新加载 miniapp + DevTools 初始化 | 链路 smoke、独立 CDP matrix、优雅退出结果 | request/event/context/reconnect flow；进程组 CTRL_BREAK 触发 Agent unload/detach | 9421 established、62000 CDP；退出后端口可立即重绑 | 非本 PID 监听、无 upstream、domain/error/事件超时、非零退出、强杀 fallback、目标 peer 退出均失败 | `scripts/smoke-windows.ps1`, `smoke-client.go`, `smoke-process-runner` | link：Runtime/Debugger/evaluate；matrix：7 domain、对象、异常、console、脚本、暂停/继续、调用栈、长消息、并发、错误、上下文、重连；当前 attach/关闭/重绑已通过，upstream 未建立 | 已实现/待打开或刷新 miniapp 后完成 live CDP matrix |

## Public SDK mapping

The public package is a façade over the same `internal/app.App` used by the
CLI. The list below maps the stable SDK surface to the behavior rows above;
it does not claim that native or live Windows evidence is available on every
machine.

- `sdk.New`, `Service.Start`, `Service.Close` map to CLI startup and reverse
  shutdown. Lifecycle tests cover idempotent and concurrent calls; native
  lifetime remains environment-dependent.
- `Service.Send`, `SendRaw`, `Notify`, and `SubscribeCDP` map to CDP proxy,
  request correlation, and event broadcast. Fake-upstream tests cover response,
  error, notification, unknown-ID, ordering, long-frame, and reconnect cases.
- `Contexts`, `SelectContext`, and `SubscribeContexts` map to
  `add/remove/connectJsContext`; registry tests cover listing, selection, route,
  and removal.
- `StartRecording`, `StopRecording`, and `Replay` map to capture/replay; file
  round-trip, truncation, and cancellation tests cover the deterministic format.
- `Discover`, `Attach`, and `Detach` map to Frida process/session lifecycle;
  tagged native tests require the prepared Windows DLL.
- `PrepareNativeRuntime` and `CheckNativeRuntime` map to native packaging and
  loader checks; release/live verification requires native artifacts.
