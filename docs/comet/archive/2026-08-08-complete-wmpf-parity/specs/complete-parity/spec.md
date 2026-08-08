# 完整复刻规格

## Frida

使用 `frida-core 17.3.2` 的真实 C API。实现 device enumerate（名称、PID、PPID、路径）、attach、create/load script、message callback、unload、detach 和 GLib/Frida 释放。回调不得把 C 指针泄漏到 Go；Go channel/锁保证线程安全和关闭顺序。Windows DLL/headers/import library 的版本和装载路径有构建检查。

## 进程与启动

先绑定两个 WebSocket 监听器，再枚举 `WeChatAppEx.exe`，按参考父 PID 频次选择宿主，从宿主路径解析 WMPF 版本，精确读取 `addresses.<version>.json`，attach 并加载嵌入 Agent。缺失进程、路径、版本或脚本返回与参考一致的可诊断错误。

## Protobuf/Codex

运行时实现参考 `WARemoteDebugProtobuf.js` 的全部 55 类型和所有字段：DataFormat、Base/Comm、登录、房间、心跳、同步、SetupContext、CallInterface、EvaluateJavascript、Ping/Pong、Dom、Network、ChromeDevtools、JsContext、CustomMessage。保留字段号、wire type、嵌套/重复关系和未知字段。zlib 标志、originalSize、损坏帧和未知类别的错误/原始数据语义与参考一致。产品运行时必须支持参考地址配置覆盖的全部 WMPF 版本，并逐版本验证地址加载、Agent Hook 和协议行为。

## CDP/context

CDP 文本帧包装为 WMPF `chromeDevtools`，响应 `chromeDevtoolsResult` 原样恢复为文本；请求 ID 精确关联，事件按接收顺序广播。`addJsContext`、`removeJsContext`、`connectJsContext` 更新选择状态并路由请求；重连恢复和错误传播与参考一致。

验证分为三层：协议 differential 对参考实现和 Go 输入同一组 WMPF/CDP 帧，比较 payload、字段、错误和事件顺序；模拟端到端代理测试使用 fake upstream 与多个 WebSocket client 覆盖 Runtime、Debugger、Page、DOM、Network、Console、Performance 等 CDP domain 的透明代理，以及多 `jscontextId`、错误响应、通知、重复/未知 ID、长消息、并发、顺序、损坏帧与断线重连；Windows live CDP matrix 复现真实 DevTools 初始化序列，验证代表性 domain 命令、异常、断点、暂停/继续、调用栈、脚本解析、上下文切换和断线恢复。

## Test gate

每条 acceptance 绑定真实命令和 receipt。Go production 语句覆盖率必须由标准 coverage profile 证明为 100.0%，不得排除生产文件或伪造覆盖。基础 `Runtime.enable`、`Debugger.enable`、`Runtime.evaluate` 只称为链路 smoke，不作为全部 CDP 功能证明。没有真实 native/Windows live CDP matrix 结果时 Verify 结果必须为 fail 并回 Build。只有全部 differential、模拟端到端、unit、race、native build、链路 smoke 和 live CDP matrix acceptance 通过才可 Archive。
