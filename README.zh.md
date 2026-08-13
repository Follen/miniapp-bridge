# miniapp-bridge

[English](README.md) | [简体中文](README.zh.md)

[![CI](https://github.com/Follen/miniapp-bridge/actions/workflows/ci.yml/badge.svg)](https://github.com/Follen/miniapp-bridge/actions/workflows/ci.yml)
[![Release](https://github.com/Follen/miniapp-bridge/actions/workflows/release.yml/badge.svg)](https://github.com/Follen/miniapp-bridge/actions/workflows/release.yml)

`miniapp-bridge` 是面向 Windows WMPF 小程序的本地 Chrome DevTools Protocol
调试桥。开发者可以通过 WebSocket 连接 Chromium DevTools，也可以在 Go 项目中
直接使用公开 SDK，发送 CDP 命令、选择 JavaScript 上下文、订阅事件以及录制或
回放协议流量。

Windows 可执行程序会自动发现 WMPF、加载内嵌 Frida Agent，并管理完整连接
生命周期。程序运行时不依赖 Node.js。

## 支持环境

| 组件 | 支持版本 |
| --- | --- |
| 操作系统 | Windows amd64 |
| WMPF | **25297** |
| Frida Core | 17.3.2 |
| Native ABI | 1（`17.3.2-abi1.1`） |
| Go SDK 与源码构建 | Go 1.23 或更高版本 |

本版本的生产支持和 live 验证目标是 WMPF 25297。仓库中的其他历史地址数据仅用于
兼容，不代表这些版本均已完成 live 支持验证。

## 快速开始

1. 从 [GitHub Releases](https://github.com/Follen/miniapp-bridge/releases/tag/v0.0.5)
   下载 `miniapp-bridge-v0.0.5-windows-amd64.zip`。
2. 解压后保持 `miniapp-bridge.exe`、`miniapp-frida.dll` 和 `manifest.json`
   位于同一目录。
3. 启动 bridge：

   ```powershell
   .\miniapp-bridge.exe
   ```

4. 等待日志显示 Frida attach 成功，再打开或重新加载目标小程序。attach 之前的
   加载不会触发所需 Hook。
5. 使用以下地址打开 Chromium DevTools：

   ```text
   devtools://devtools/bundled/inspector.html?ws=127.0.0.1:62000
   ```

使用 `Ctrl+C` 停止程序。关闭过程会依次关闭客户端和监听器、卸载 Agent、detach
Frida session，并释放 native runtime。

## 默认端点与 CLI

默认监听仅绑定本机回环地址：

| 地址 | 用途 |
| --- | --- |
| `127.0.0.1:9421` | WMPF upstream 调试 WebSocket |
| `127.0.0.1:62000` | DevTools 和其他客户端使用的 CDP WebSocket |

```text
Usage: miniapp-bridge [options]

Options:
  --debug-port <port>  Remote debug server port (default: 9421)
  --cdp-port <port>    CDP proxy server port (default: 62000)
  --debug-main         Output main process debug messages
  --debug-frida        Output Frida client messages
  --record <file>      Record incoming raw debug frames
  --replay <file>      Replay recorded raw debug frames
  -h, --help           Show this help message
```

`--record` 用于保存 upstream 原始帧；`--replay` 会让录制文件重新经过同一套协议
处理流程，无需连接真实目标。

## Go SDK

Module 路径为 `github.com/Follen/miniapp-bridge`，业务项目只需导入公开包
`github.com/Follen/miniapp-bridge/sdk`：

```powershell
go get github.com/Follen/miniapp-bridge/sdk@v0.0.5
```

```go
package bridge

import (
    "context"
    "time"

    "github.com/Follen/miniapp-bridge/sdk"
)

func Run(ctx context.Context) error {
    service, err := sdk.New(sdk.Options{})
    if err != nil {
        return err
    }
    if err := service.Start(ctx); err != nil {
        return err
    }

    <-ctx.Done()

    closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    return service.Close(closeCtx)
}
```

`Service` 还提供结构化与原始 CDP 请求、请求关联、日志/状态/CDP/上下文订阅、
上下文选择、目标 attach/detach、录制回放，以及支持 `errors.Is`/`errors.As` 的
结构化错误。发送依赖执行上下文的 CDP 请求前，应先等待 upstream 建立并发现可用
上下文。

完整 API 和生命周期契约见 [公开 Go SDK](docs/sdk.md) 与
[SDK 示例](examples/sdk)。

## Native runtime

Go Module 包含源码和 Windows loader，但不包含 `miniapp-frida.dll`。Native
runtime 以 `miniapp-frida-native-17.3.2-abi1.1-windows-amd64.zip` 资产单独发布，
兼容 tag 为 `native-v17.3.2-abi1.1`。

需要 attach WMPF 的 SDK 程序必须：

1. 使用 `CGO_ENABLED=1` 和 `-tags frida` 构建。
2. 将 `miniapp-frida.dll` 和匹配的 `manifest.json` 放在最终 EXE 同目录。
3. 需要校验或托管缓存时使用 `sdk.CheckNativeRuntime` 或
   `sdk.PrepareNativeRuntime`。

Loader 不搜索 `PATH` 或全局安装位置。文件缺失、架构错误、ABI 不匹配、manifest
错误、导出缺失和哈希错误都会作为结构化 SDK 错误返回。离线准备、缓存、打包和
发布细节见 [Native 发布资产](docs/native-release.md)。

## 从源码构建

Windows native 构建需要：

- Go 1.23 或更高版本
- Windows amd64 与 PowerShell
- 供 cgo 使用的 MinGW-w64 `gcc.exe` 和 `ar.exe`
- 包含 MSVC 的 Visual Studio 2022 C++ Build Tools

```powershell
git clone https://github.com/Follen/miniapp-bridge.git
cd miniapp-bridge
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-windows.ps1
```

脚本会校验固定版本的 Frida 和 zlib 输入、构建 native shim、运行 native 测试，
并将可执行程序、DLL 和 manifest 写入 `dist`。已校验的下载缓存可以用于离线
重建。

## 测试与排错

可移植测试、Windows 确定性门禁与真实 WMPF live matrix 相互独立：

```powershell
go test ./... -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\coverage-gate.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\smoke-windows.ps1 -CDPMode all
```

常见检查：

- **没有附加目标：**确认当前运行的是 WMPF 25297；先启动 bridge，attach 成功后
  再打开或重新加载小程序。
- **DevTools 连接失败：**确认 62000 端口正在监听，并且没有被其他进程占用。
- **没有 upstream：**确认小程序加载后 WMPF 已连接到 9421 端口。
- **Native 加载错误：**确认 DLL 和 manifest 与 EXE 同目录且版本匹配；根据
  结构化错误检查架构、版本、ABI、导出或哈希问题。

更深入的验证信息见 [验证说明](docs/verification.md)、
[行为矩阵](docs/behavior-matrix.md) 与
[已知差异](KNOWN-DIFFERENCES.md)。

## 许可证

miniapp-bridge 使用 [GPL-2.0-only](LICENSE) 许可证。

第三方许可证和声明统一收录于
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
