# miniapp-bridge

[English](README.md) | [简体中文](README.zh.md)

[![CI](https://github.com/Follen/miniapp-bridge/actions/workflows/ci.yml/badge.svg)](https://github.com/Follen/miniapp-bridge/actions/workflows/ci.yml)
[![Release](https://github.com/Follen/miniapp-bridge/actions/workflows/release.yml/badge.svg)](https://github.com/Follen/miniapp-bridge/actions/workflows/release.yml)

`miniapp-bridge` 是
[evi0s/WMPFDebugger](https://github.com/evi0s/WMPFDebugger) 的 Go + Frida
移植版本。它可以发现并附加 Windows WMPF 进程，将 WMPF 私有调试协议转换为
标准 Chrome DevTools Protocol（CDP），同时提供独立 CLI 和公开 Go SDK。

Go 主程序负责进程发现、Frida 生命周期、Protobuf 与 zlib、请求关联、上下文
路由、WebSocket 服务、录制回放、配置和日志。嵌入式 JavaScript Agent 只负责
Patch 目标运行时并转发原始消息。最终程序运行时不依赖 Node.js。

## 默认地址

零配置启动会保留参考项目的地址和启动顺序：

- WMPF 调试 WebSocket：`127.0.0.1:9421`
- CDP WebSocket 代理：`127.0.0.1:62000`
- DevTools 地址：
  `devtools://devtools/bundled/inspector.html?ws=127.0.0.1:62000`

程序会先启动两个监听器，再发现目标、执行 Frida attach 并加载 Agent。看到
attach 成功日志后，再打开或重新加载目标小程序，最后打开上面的 DevTools
地址。`OnLoadStart` Hook 只能观察 attach 之后发生的加载。

## 平台状态

当前版本唯一的生产级 native 目标是 Windows amd64，固定依赖为：

- Frida core `17.3.2`
- miniapp native ABI `1`（`17.3.2-abi1`）
- zlib `1.3.1`

仓库保留了平台抽象和可移植协议测试，但目前不宣称 macOS、Linux、Windows
arm64 或其他系统具备 native 目标发现、attach、打包或 live 等价能力。未启用
native tag 的构建仍保留 Go 代理和回放能力，但不会自动 attach 目标。

## CLI 快速开始

从源码构建 native 版本需要 Go `1.23` 或更高版本、Windows amd64、供 cgo
使用的 MinGW-w64 `gcc.exe` 和 `ar.exe`、包含 MSVC 的 Visual Studio 2022 C++
Build Tools，以及 PowerShell。首次无缓存构建会下载固定版本的 Frida devkit
和 zlib 源码，并校验两个压缩包。

```powershell
git clone https://github.com/Follen/miniapp-bridge.git
cd miniapp-bridge
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-windows.ps1
.\dist\miniapp-bridge.exe
```

看到 attach 日志后打开或重新加载小程序，再使用上面的 DevTools 地址连接。
CLI 参数如下：

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

`scripts\build-windows.ps1` 会构建 native shim、运行带 tag 的测试、构建
`miniapp-bridge.exe`，并把 DLL 和 manifest 放到 `dist` 中的 EXE 同目录。

## 公开 Go SDK

Module 路径是 `github.com/Follen/miniapp-bridge`，公开包路径是
`github.com/Follen/miniapp-bridge/sdk`。CLI 和 SDK 调用同一个 `sdk.Service`
实现。使用方不需要导入 `internal`，也不会接触 C 指针或持有 Frida handle。

```bash
go get github.com/Follen/miniapp-bridge/sdk@v0.0.1
```

```go
package main

import (
    "context"
    "time"

    "github.com/Follen/miniapp-bridge/sdk"
)

func run(ctx context.Context) error {
    service, err := sdk.New(sdk.Options{})
    if err != nil {
        return err
    }
    if err := service.Start(ctx); err != nil {
        return err
    }

    _, requestErr := service.Send(ctx, sdk.Request{
        Method: "Runtime.enable",
    })

    closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    closeErr := service.Close(closeCtx)
    if requestErr != nil {
        return requestErr
    }
    return closeErr
}
```

SDK 还提供状态、日志、CDP 与上下文订阅，结构化和原始 CDP 请求，目标发现与
attach/detach，`jscontextId` 选择，录制回放，native 准备，以及支持
`errors.Is`/`errors.As` 的结构化错误。详见 [docs/sdk.md](docs/sdk.md) 和
[examples/sdk](examples/sdk)。

公开 SDK 遵循 Go Module SemVer。当前版本最低要求 Go `1.23`。同一主版本内
兼容的 minor 和 patch 版本会保持已记录的 SDK API、结构化错误模型、请求与
事件顺序、默认地址和 native 版本常量稳定。

## Native runtime：准备、构建与部署

Go Module 只包含 Go 源码和最小 Windows loader 源码，不提交
`miniapp-frida.dll` 或生成的压缩包。DLL 通过独立 GitHub Release 资产发布，
必须与 `manifest.json` 一起放在最终 EXE 同目录。loader 不会搜索当前工作
目录、`PATH`、注册表或全局安装位置。

把已经发布的 runtime 准备到 EXE 目录：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\native-prepare.ps1 `
  -DestinationDirectory .\dist `
  -ExpectedArchiveSHA256 1597ADCC6B3B13B5BCBA910904046AB7D2E1E3D73AE16961C73E400373BDE87A
```

默认下载地址为：

```text
https://github.com/Follen/miniapp-bridge/releases/download/native-v17.3.2-abi1/miniapp-frida-native-17.3.2-abi1-windows-amd64.zip
```

准备过程使用独占锁、临时下载、SHA-256 校验、manifest 和 PE amd64 校验、
暂存解压与原子安装。默认缓存目录是
`%LOCALAPPDATA%\miniapp-bridge\native\17.3.2-abi1\windows-amd64`。使用相同的
预期压缩包哈希并传入 `-Offline`，可以强制只使用已校验缓存且不进行下载。

SDK 使用方启用 native tag 构建程序，并把准备好的文件部署到同一目录：

```powershell
go build -tags frida -o .\dist\my-app.exe .\cmd\my-app
# .\dist\my-app.exe
# .\dist\miniapp-frida.dll
# .\dist\manifest.json
```

需要本地构建 DLL 时运行 `scripts\build-windows.ps1`。脚本会固定并校验官方
Frida devkit 和 zlib `1.3.1`，使用 MSVC 构建不透明 C ABI shim，运行
`go test -tags frida -race ./...`，并在 `dist` 生成带 tag 的 CLI 包。

## 版本与发布

源码和 SDK 的首发版本统一为 `v0.0.1`。后续版本必须使用不带 build metadata
的 canonical Go Module v0/v1 SemVer tag；当前固定模块路径不接受 v2+ tag。
native runtime 独立版本化，因为 Go proxy 不应承载大型平台 DLL。当前固定
native tag 是 `native-v17.3.2-abi1`，资产名为：

```text
miniapp-frida-native-17.3.2-abi1-windows-amd64.zip
```

压缩包包含 `miniapp-frida.dll`、`manifest.json`、`LICENSE`、
`ZLIB_LICENSE`、`THIRD_PARTY_NOTICES.md` 和内部 `SHA256SUMS`。

GitHub Actions 包含两个 workflow：

- [CI](.github/workflows/ci.yml) 在 push、pull request 和手动触发时运行。
  Linux 负责可移植 unit、vet、race、Module、格式和仓库检查；Windows 负责固定
  native 构建和完整 `100.0%` 覆盖率门禁。官方 Action 全部固定到完整 commit
  hash，workflow 权限为只读。
- [Release](.github/workflows/release.yml) 接受已经存在的 canonical v0/v1 tag，
  或手动指定该 tag。只读 Windows job 会重新执行 Module、确定性测试、
  coverage/race/vet、native 构建、导出、依赖和哈希检查。只有最终发布 job 获得
  `contents: write`，该 job 不 checkout，也不执行仓库代码。共享固定 native tag
  的发布会串行执行，`queue: max` 最多保留 100 个 pending Release。

产品 Release 发布以下资产：

```text
miniapp-bridge-v0.0.1-windows-amd64.zip
miniapp-frida-native-17.3.2-abi1-windows-amd64.zip
manifest.json
SHA256SUMS
```

产品 ZIP 包含 EXE、匹配的 DLL 和 manifest、两份 README，以及所需许可证和
声明文件。workflow 还会创建或验证默认 SDK 下载地址所使用的不可变
`native-v17.3.2-abi1` 兼容 Release。native ZIP 必须匹配 SDK 固定的 SHA-256：
`1597ADCC6B3B13B5BCBA910904046AB7D2E1E3D73AE16961C73E400373BDE87A`。
发布 job 会创建或恢复 draft，逐字节核对全部资产，再次检查产品 tag commit，
然后才正式发布。已有产品 Release 不会被覆盖；已有 native Release 的资产必须
逐字节一致，而且不会被设为 Latest。SemVer 预发布 tag 会被标记为 GitHub
prerelease。

CI 通过后创建 annotated 产品 tag 并推送：

```bash
git tag -a v0.0.1 -m "miniapp-bridge v0.0.1"
git push origin v0.0.1
```

GitHub hosted CI 中没有真实 WMPF 目标进程，因此不把 hosted 结果描述成 live
验证。声称支持新的目标版本前，需要在交互式 Windows 环境运行下面的 live
matrix。

## WMPF 版本

从固定参考仓库恢复的 47 份地址配置全部嵌入程序并经过静态测试。运行时会按
发现的目标版本选择配置；需要明确覆盖时可以设置
`sdk.Options.AddressConfigDir`。

配置已嵌入并通过单元测试，不等于每个历史 WMPF 二进制都完成过 live 验证。
生产发布应为其声称支持的每个目标版本提供新的 live receipt。

## 测试与验证

运行可移植测试：

```powershell
go test ./...
```

运行确定性发布门禁：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\coverage-gate.ps1
```

该门禁要求声明范围内的 CLI/Frida、`internal`、公开 SDK、带 tag 的
internal+SDK 和 smoke runner 的 Go 语句覆盖率全部精确达到 `100.0%`，同时
运行 unit、race、tagged-race、vet、Protobuf differential/golden、损坏帧、
请求顺序、上下文、重连、录制回放、native loader 和外部 Module 测试。稳定
发布契约见 [docs/verification.md](docs/verification.md)，与当前 scope 绑定的
命令结果以 Comet verification report 和 receipts 为准。

Windows live matrix 独立于确定性覆盖率：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\smoke-windows.ps1 `
  -UpstreamWaitSeconds 300 -CDPMode all
```

看到 `action-required=open-or-reload-miniapp` 后，再打开或重新加载目标。通过的
结果会验证 WMPF upstream 所有权、有代表性的 Runtime、Debugger、Page、DOM、
Network、Console 和 Performance 行为、输入交互、重连、事件与请求语义、
Agent/session/device/runtime 的优雅释放、目标进程存活和端口立即重绑定。
一次 live 运行只验证当前安装的目标版本和环境，项目不会据此推断所有历史
版本都已完成 live 等价验证。

## 来源、许可证与声明

本项目基于 [evi0s/WMPFDebugger](https://github.com/evi0s/WMPFDebugger) 的固定
提交 `2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d`。地址配置和 Agent 行为保留
来源声明。对应参考仓库 `src/third-party` 的协议定义源自 WeChat DevTools，
继续保留 Tencent Holdings Ltd. 版权。

项目采用 [GPL-2.0-only](LICENSE) 许可证。分发 native 资产时还必须保留
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)、zlib 许可证和适用的 Frida
声明。
