# miniapp-bridge SDK 使用文档

本文档是公共 Go SDK（`github.com/Follen/miniapp-bridge/sdk`）的实用使用指南；
完整契约见 [`docs/sdk.md`](docs/sdk.md)。

- 平台：Windows amd64（**WMPF 25297** 为 live 验证目标；其他历史地址数据仅作兼容）
- 最新版本：**v0.0.8**
- 最低 Go 版本：`go.mod` 的 `go` 指令（Go 1.23 或更新）
- 端口：upstream（小程序）`9421`，CDP WebSocket `62000`

## 1. 安装

```powershell
go get github.com/Follen/miniapp-bridge/sdk@v0.0.8
```

使用本地 checkout 调试：

```go
replace github.com/Follen/miniapp-bridge => D:/path/to/miniapp-bridge
```

运行时加载 `miniapp-frida.dll`（Frida core 17.3.2，native ABI 1.1）。将 DLL
及其 `manifest.json` 放在可执行文件旁边，或用 `Options.NativePath` 指向绝对路径。
DLL 发布在 `native-v17.3.2-abi1.1` GitHub Release 中，可用
`sdk.PrepareNativeRuntime` / `sdk.CheckNativeRuntime` 准备与校验。

## 2. 最小服务

```go
svc, err := sdk.New(sdk.Options{})
if err != nil { return err }
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
if err := svc.Start(ctx); err != nil { return err }
defer svc.Close(context.Background())
```

`Start` 先打开两个 listener，然后发现 WMPF 目标、attach Frida、加载内置 Agent。
零选项使用默认端口与默认 native starter。

## 3. 端到端示例（自举，无需手动 enable）

小程序连接建立后，bridge 会自动通过空 `jscontext_id` bootstrap 路由发送
`Runtime.enable`（每次重连后也会重新发送）。**不需要**手动开启 Runtime 域，
也不需要另开 WebSocket：

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Follen/miniapp-bridge/sdk"
)

func main() {
	svc, err := sdk.New(sdk.Options{
		DebugPort:  9421,
		CDPPort:    62000,
		NativePath: "D:/path/to/miniapp-frida.dll", // 绝对路径；省略则默认发现
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "new:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(1)
	}
	defer svc.Close(context.Background())

	// 等待小程序连上 debug 端口（Frida attach 之后打开或重载小程序）。
	// bridge 会自动发送 Runtime.enable，因此无需手动 enable。
	upDeadline := time.Now().Add(4 * time.Minute)
	for !svc.Status().Connections.Upstream {
		if time.Now().After(upDeadline) {
			fmt.Fprintln(os.Stderr, "no miniapp connected")
			os.Exit(1)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 轮询直到自动 enable 产生 execution context。
	ctxDeadline := time.Now().Add(3 * time.Minute)
	var selected string
	for {
		contexts := svc.Contexts()
		if len(contexts) > 0 {
			fmt.Printf("contexts=%v\n", contexts)
			selected = contexts[0].ID
			break
		}
		if time.Now().After(ctxDeadline) {
			fmt.Fprintln(os.Stderr, "contexts never appeared")
			os.Exit(1)
		}
		time.Sleep(200 * time.Millisecond)
	}

	if err := svc.SelectContext(selected); err != nil {
		fmt.Fprintln(os.Stderr, "select:", err)
		os.Exit(1)
	}
	response, err := svc.Send(ctx, sdk.Request{
		Method: "Runtime.evaluate",
		Params: map[string]any{"expression": "1+1", "returnByValue": true},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "evaluate:", err)
		os.Exit(1)
	}
	fmt.Printf("result=%v error=%v\n", response.Result, response.Error)
}
```

2026-08-14 实机验证（WMPF 25297 / 微信 146.84.28 / 腾讯文档）：小程序连接后，
bridge 自动发送 `Runtime.enable`（id=1，空 `jscontext_id`），小程序回发
`Runtime.executionContextCreated`，`Contexts()` 自动填充，
`Runtime.evaluate("1+1")` 返回 `value: 2`——全程无需手动 enable。

## 4. 请求

```go
// 结构化请求：SDK 自动生成进程唯一整数 id。
resp, err := svc.Send(ctx, sdk.Request{
	ID:     1, // 可选；缺省为进程唯一整数
	Method: "Runtime.evaluate",
	Params: map[string]any{"expression": "1 + 1", "returnByValue": true},
})

// 原始 JSON 请求：调用方 id 原样保留。
resp, err = svc.SendRaw(ctx, []byte(`{"id":7,"method":"Runtime.evaluate","params":{"expression":"1+1","returnByValue":true}}`))

// 显式指定 context 路由。
resp, err = svc.Send(ctx, sdk.Request{
	Method: "Runtime.evaluate",
	Params: map[string]any{"expression": "1 + 1", "returnByValue": true},
	Route:  sdk.Route{JSContextID: "2"},
})

// 通知（无 id、无响应）。
err = svc.Notify(sdk.Request{Method: "Runtime.runIfWaitingForDebugger"})
```

ID 约定：

- WMPF 小程序调试端点**拒绝非整数 CDP id**（`-32600 Message must have integer
  'id' property`），因此结构化发送的默认 id 是进程唯一整数。`SendRaw` 仍保留
  调用方传入的字符串或 JSON 数字 id，但字符串 id 会被真实 WMPF 小程序拒绝。
- 空 `Route` 在发送时快照当前选中 context；无选中时走空 `jscontext_id`
  bootstrap 路由（自动 enable 即走此路由）。未知显式 context 返回
  `ErrUnknownContext`。
- 数字 id 按规范化十进制文本关联（不经 `float64`），支持超过 `2^53`、
  `uint64` 最大值、小数与超大指数。
- 重复的 pending id 返回 `ErrDuplicateRequestID`。

## 5. 自动 bootstrap 细节

- 每个 upstream 连接发送一次 `Runtime.enable`（空 `jscontext_id`，走正常出站通道）。
- 重连会安装新的 upstream generation 并触发一次新的 enable（id 递增）。
- 自动请求注册在**独立 correlator scope**：其响应由 bridge 解析并**吞掉**
  （不会广播给 CDP 客户端或 SDK），因此绝不会满足/混淆复用了相同 id 的客户端请求。
- 之后代码里显式发送 `Runtime.enable` 是幂等的。

## 6. 事件订阅

```go
cdp := svc.SubscribeCDP(sdk.SubscriptionOptions{Buffer: 128})
defer cdp.Close()
go func() {
	for event := range cdp.Channel() {
		fmt.Printf("cdp method=%s payload=%s\n", event.Method, event.Payload)
		if event.Response != nil {
			fmt.Printf("response id=%v result=%v error=%+v\n", event.Response.ID, event.Response.Result, event.Response.Error)
		}
	}
}()

contexts := svc.SubscribeContexts()
defer contexts.Close()
go func() {
	for event := range contexts.Channel() {
		fmt.Printf("context %s id=%s target=%s\n", event.Kind, event.Context.ID, event.Context.Target)
	}
}()
```

`SubscribeLogs`、`SubscribeStatus` 用法相同。每个订阅使用独立的有限队列；
队列满只断开该订阅，`sub.Err()` 返回 `sdk.ErrSlowSubscriber`。

## 7. Context 与录制回放

```go
contexts := svc.Contexts()
if len(contexts) != 0 { _ = svc.SelectContext(contexts[0].ID) }
if err := svc.StartRecording("capture.bin"); err != nil { return err }
defer svc.StopRecording()
if err := svc.Replay(ctx, "capture.bin"); err != nil { return err }
```

Context 列表是确定性的；CDP context id 原样保留为十进制字符串（`"2"`、`"6"`、`"7"`…）。
upstream 断线时注册表清空，并按同一确定顺序逐个发布 removal 事件。

## 8. 原生目标管理

```go
targets, err := svc.Discover(ctx)
if err != nil { return err }
if len(targets) > 0 {
	if err := svc.Attach(ctx, targets[0].PID); err != nil { return err }
	defer svc.Detach(context.Background())
}
```

`Discover` 读取一次 CIM 快照（PID、父 PID、名称、路径、WMPF 版本）。
`Attach` 会重新枚举并校验精确的进程身份，然后才卸载上一个会话。
bridge 通常在 `Start` 时自动 attach；这些调用用于显式重新定向目标。

## 9. 错误处理

```go
resp, err := svc.Send(ctx, sdk.Request{Method: "Runtime.evaluate"})
if errors.Is(err, sdk.ErrNoUpstream) {
	// 没有小程序连接
} else if errors.Is(err, sdk.ErrTimeout) {
	// 请求超时
} else if errors.Is(err, sdk.ErrClosed) {
	// 服务已关闭
}
if errors.Is(err, sdk.ErrNoContext) {
	// 没有选中 JavaScript context（bootstrap 尚未产生）
}
var structured *sdk.Error
if errors.As(err, &structured) {
	fmt.Printf("op=%s component=%s cause=%v\n", structured.Op, structured.Component, structured.Err)
}
```

哨兵错误用 `errors.Is`（`ErrClosed`、`ErrNoUpstream`、`ErrNoContext`、
`ErrUnknownContext`、`ErrDuplicateRequestID`、`ErrTimeout`、
`ErrUpstreamDisconnected`、`ErrSlowSubscriber`、native 错误等）；
细节用 `errors.As` 取 `*sdk.Error` / `*sdk.NativeRuntimeError`。

## 10. 注意事项

- Windows 消费端用 `CGO_ENABLED=1 go build -tags frida` 构建；不带 tag 的构建
  保留 Go 代理/回放能力，但不会自动发现或 attach 原生目标。
- 模块不暴露 internal 包、cgo 指针、Frida handle 或 WebSocket 对象；只 import
  `github.com/Follen/miniapp-bridge/sdk`。
- SDK 保持参考 bridge 语义：端口、启动顺序、WMPF protobuf/zlib 帧、CDP 转换、
  请求/事件顺序、context 路由、Agent 嵌入、逆序关闭。

完整契约见 [`docs/sdk.md`](docs/sdk.md)，验证命令见
[`docs/verification.md`](docs/verification.md)。
