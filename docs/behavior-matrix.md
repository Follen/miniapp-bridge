# WMPFDebugger -> miniapp-bridge 行为矩阵

| 参考源码入口 | 输入 | 输出 | 状态变化 | 网络消息 | 错误行为 | Go 对应实现 | 验证方式 | 完成状态 |
|---|---|---|---|---|---|---|---|---|
| `src/cli.ts` | CLI 参数/帮助 | 端口与 debug/录制/回放选项 | 配置初始化 | 无 | JS Number 兼容端口校验；非法参数退出 2 | `internal/config`, `cmd/miniapp-bridge` | config tests + CLI exit smoke | 已实现/已验证 |
| `src/index.ts:debug_server` | 裸 `DebugMessage` 二进制帧 | CDP payload/上下文事件 | context、request 状态更新 | `chromeDevtoolsResult` -> CDP 文本 | 损坏帧记录并继续 | `internal/app`, `internal/wmpf` | 参考 differential：同帧 payload、字段、错误、事件顺序 | 已实现/已验证 |
| `src/index.ts:proxy_server` | CDP 文本 | `chromeDevtools` protobuf | 单调 seq、参考 0–100 op ID、pending request | 发送到唯一 active WMPF upstream | 无 upstream 时，需要响应的请求立即返回结构化错误；第二 owner 被拒绝 | `internal/app`, `internal/cdp`, `internal/proxy` | fake upstream + 单 owner：domain、重连、ID、通知、长消息、并发、损坏帧、顺序 | 已实现/已验证 |
| `src/index.ts:frida_server` | FULL scope process metadata | attach/load callback | device/session/script ownership | Agent message -> Go logger | 进程/路径/版本/config/attach 错误可诊断；显式 reattach 在卸载旧会话前重新枚举并绑定进程身份 | `internal/frida`, `internal/process`, `internal/version`, `sdk` | native enumerate/lifecycle/reattach/identity rejection | 已实现/已验证（WMPF 25297） |
| `frida/hook.js:patchOnLoadStart` | rcx/rdx 与 scene 指针 | debug flag=1、scene=1101 | 目标调试条件改变 | `send()` 日志 | 仅参考允许的 scene | embedded `frida/hook.js` | Agent audit + live callback receipt | 已实现；当前实机结果见 Native verification report |
| `frida/hook.js:patchCDPFilter` | filter 结构 | 值 6 改 0 | CDP filter patch | `send()` 日志 | null pointer 返回 | embedded `frida/hook.js` | Agent audit + live callback receipt | 已实现；当前实机结果见 Native verification report |
| `WARemoteDebugProtobuf.js` 全 55 类型 | protobuf bytes/struct | typed + generic encode/decode | typed/generic 均保留未知 wire bytes | 外层/调试/category payload | 截断、fixed32/fixed64/bytes/varint 损坏返回 error | `messages.go`, `generic.go`, `registry.go` | 55 类型、131 字段、55 explicit-zero、6 corrupt golden | 已实现/已验证 |
| `RemoteDebugCodex.wrap/unwrapDebugMessageData` | 19 category、snake_case、zlib flags | 结构化 category；未知 category 外部 `{}` 且 raw 可诊断 | originalSize fallback、压缩统计 | 18-export DLL ABI 提供 stock zlib 1.3.1 exact deflate/inflate；Go Module 无 `zlib1.dll` import | 损坏/超限 zlib 返回 error | `protocol.go`, `zlib_windows_cgo.go`, `internal/frida` loader | 7 debug、11 developer、10 client、26 incoming differential；外部 tagged PE import 审计 | 已实现/已验证 |
| `RemoteDebugCodex` DataFormat/cmd | 1000-1006、2000-2006、3001-3003 | request/response direction-aware typed payload | client response 显式零字段 | DataFormat protobuf | outgoing 未知 cmd 报错；incoming 未知 cmd 返回 `{}` | `transport.go` | 13 mapping + 7 developer + 7 client response + 12 incoming differential | 已实现/已验证 |
| WMPF 私有 context 消息 + CDP Runtime context 事件 | `add/remove/connectJsContext`、`Runtime.executionContextCreated/Destroyed/ContextsCleared` | CDP 路由 context ID；upstream 连接建立时自动发送一次 `Runtime.enable`（重连后重新发送） | 注册/选择/删除/清空；数字 ID 原样转十进制字符串 | 有选中项时路由其 `jscontext_id`，首次发现前发送空值；自动 enable 走空 context bootstrap 路由 | connect 未知 ID 自动建立；stale generation 丢弃；断线按稳定顺序发布完整 removal；自动 enable 走独立 correlator scope，其响应被桥吞掉、绝不错配同 ID 客户端请求 | `internal/context`, `internal/app`, `sdk` | 私有路径 integration + SDK WebSocket bootstrap/destroy + App clear/disconnect observer + upstream connect/reconnect 自动 enable 帧断言 | 已实现/已验证 |
| capture/replay 扩展 | 裸 DebugMessage 帧 | 长度前缀 capture 文件/按序回放 | recorder 锁与 flush | 回放重新进入 decode pipeline | 截断或 >64 MiB 声明长度返回 error | `internal/capture`, `internal/app` | 文件 roundtrip、并发、corrupt tests | 已实现/已验证 |
| 启动顺序 | listeners -> enumerate -> config -> attach -> load | 服务与 Agent ready | 资源按逆序释放 | 9421/62000 | 任一步失败退出并关闭 listener | CLI + native backend | native lifecycle; strict smoke | 已实现/已验证 |
| Windows live | attach 后打开或重载 miniapp + DevTools 初始化 | 链路 smoke、独立 CDP matrix、输入交互和优雅退出结果 | request/event/context/reconnect flow；进程组 CTRL_BREAK 触发 Agent unload/detach | 9421 established、62000 CDP；退出后端口立即重绑 | 非本 PID 监听、无 upstream、domain/error/事件超时、非零退出、强杀 fallback、目标 peer 退出均失败 | `scripts/smoke-windows.ps1`, `smoke-client.go`, `smoke-process-runner` | Runtime/Debugger/Page/DOM/Network/Console/Performance、对象、异常、console、脚本、断点、暂停/继续、调用栈、长消息、并发、错误、上下文、重连、鼠标、键盘、退出和重绑 | 已实现；当前实机结果见 Native verification report |

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
  Numeric request IDs are correlated without `float64`, including legal JSON
  exponents outside IEEE-754 range; event params, response results, and error
  data expose preserved `json.Number` values.
- `Contexts`, `SelectContext`, and `SubscribeContexts` map to both
  `add/remove/connectJsContext` and standard CDP Runtime context lifecycle
  events; registry tests cover listing, selection, bootstrap routing, removal,
  clearing, complete target metadata, and deterministic disconnect removals.
  The bridge sends one automatic `Runtime.enable` (empty `jscontext_id`
  bootstrap route) whenever an upstream transport connects, including after a
  reconnect, so `Contexts()` fills in without a manual client enable; the
  request is registered in a private correlator scope and its response is
  swallowed by the bridge, so a client request that reuses the same id can
  never be satisfied by the bootstrap response. Later client enables remain
  idempotent.
- `StartRecording`, `StopRecording`, and `Replay` map to capture/replay; file
  round-trip, truncation, and cancellation tests cover the deterministic format.
- `Discover`, `Attach`, and `Detach` map to Frida process/session lifecycle;
  Windows discovery tests cover CIM PID/parent/path/version metadata and direct
  `Target` mapping, while tagged native attach tests require the prepared DLL.
- `PrepareNativeRuntime` and `CheckNativeRuntime` map to native packaging and
  loader checks; release/live verification requires native artifacts.
