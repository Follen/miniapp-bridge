# miniapp-bridge 完整目标规格

## 入口与生命周期

程序解析 `--debug-port`、`--cdp-port`、`--debug-main`、`--debug-frida` 和 `-h/--help`。端口默认为 9421/62000，非法值返回带参数名的错误。启动顺序为创建调试 WebSocket、创建 CDP WebSocket、枚举目标进程、选择 WMPF 父进程、读取版本地址、attach、创建并加载嵌入 Agent。关闭时按逆序关闭客户端、卸载脚本、detach session 并释放 Frida/GLib 资源。

## 进程与版本

枚举名称为 `WeChatAppEx.exe` 的进程，使用 `ppid` 统计选择出现次数最多的父进程；不存在时报告 `[frida] WeChatAppEx.exe process not found`。从宿主路径提取数字版本，无法解析时报告 `[frida] error in find wmpf version`；缺少 `addresses.<version>.json` 时报告版本号。版本配置包含 `Version`、`LoadStartHookOffset`、`CDPFilterHookOffset`、`SceneOffsets`。

## Agent

Agent 通过 `embed` 资源加载，选择 `flue.dll`（版本 >=13331）或 `WeChatAppEx.exe` 主模块。`OnLoadStart` hook 将低字节强制为 1，并按 `SceneOffsets` 读取场景；命中已知场景集合时写入 1101。CDP filter hook 在 leave 阶段将输入结构偏移 8 的值 6 改为 0。Agent 的日志与异常作为 Frida 消息交给 Go 日志层。

## WMPF 协议

外层消息为 `mmbizwxadevremote.WARemoteDebug_DebugMessage`，字段号和 wire type 与参考生成代码一致；内层覆盖 DataFormat、BaseReq/Resp、CommReq/Resp、登录/房间/心跳/同步消息及全部调试消息类型。类别转换保持 snake_case 与 camelCase 的映射。`CompressAlgo.Zlib` 标志触发 zlib deflate/inflate；保留 `originalSize`，未知类别或损坏帧返回诊断错误和原始字节，不让主循环崩溃。

## CDP 与上下文

来自 WMPF 的 `chromeDevtoolsResult` payload 原样发送给全部已连接 CDP 客户端；来自 CDP 的文本帧包装为 `chromeDevtools`，生成 `seq` 单调递增、`op_id` 与请求关联，默认 `jscontext_id` 为空。`addJsContext` 保存上下文，`removeJsContext` 删除，`connectJsContext` 选择当前上下文；请求按当前上下文路由，事件按接收顺序广播。连接断开后状态可重连恢复，错误按原始错误语义传播。

## 服务与工具

调试服务监听 `127.0.0.1:9421`，CDP 代理监听 `127.0.0.1:62000`，输出 `devtools://devtools/bundled/inspector.html?ws=127.0.0.1:62000`。支持原始帧录制/回放、配置选择、结构化日志、构建/打包和 Windows smoke test。
