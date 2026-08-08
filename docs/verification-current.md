# 当前验证记录

参考仓库：`evi0s/WMPFDebugger`

固定提交：`2b90b77fc6f13dd18480cd07d7dd9c052cc26c9d`

验证日期：2026-08-08（Windows amd64）

## 自动化验证

| 命令 | 退出状态 | 关键结果 |
|---|---:|---|
| `node scripts/generate-reference-fixtures.js` | 0 | 55 protobuf 类型、131 字段、55 explicit-zero、6 corrupt fixtures |
| `node scripts/generate-reference-codex-fixtures.js` | 0 | 7 debug、11 developer、10 client response、26 incoming fixtures |
| `powershell -ExecutionPolicy Bypass -File scripts/coverage-gate.ps1` | 0 | reference gate、默认 internal 100.0%、tagged Frida 100.0%、smoke runner 100.0%、unit/race/vet 全通过 |
| `go test ./... -count=1` | 0 | 全包 unit/integration/audit，包括 fake upstream CDP matrix |
| `go test -race ./... -count=1` | 0 | 全包 race 通过 |
| `go test -tags frida ./... -count=1` | 0 | 真实 Frida enumerate/attach/Agent load/unload/detach/reattach 通过 |
| `go test -race -tags frida ./... -count=1` | 0 | tagged native + race 通过 |
| `go vet ./...` | 0 | 无诊断 |
| `go test ./internal/app -run TestSimulatedEndToEndCDPMatrix -count=1 -v` | 0 | fake upstream + 双 client；7 domain、上下文切换、错误/通知/未知 ID、256 KiB、损坏帧、重连、严格 seq |
| `go test ./scripts -run TestLinkAndMatrixClients -count=1 -v` | 0 | link-smoke 与 live-cdp-matrix 客户端状态机 fake server 全通过 |
| `powershell -ExecutionPolicy Bypass -File scripts/build-windows.ps1` | 0 | zlib 1.3.1、Frida shim、tagged tests、EXE/DLL 打包通过 |

## 覆盖率证据

默认构建：`go test ./internal/... -coverprofile=<temporary>` → `total: (statements) 100.0%`。

Frida tagged 构建：`go test -tags frida ./internal/... -coverprofile=<temporary>` → `total: (statements) 100.0%`；`internal/frida` native Windows 分支与 callback 入口均无 uncovered block。

Windows smoke runner：`go test ./scripts/smoke-process-runner -coverprofile=<temporary>` → `total: (statements) 100.0%`；真实子进程组集成测试确认 `GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT)` 在 bridge 中映射为 `os.Interrupt`。

## 分层 CDP 验证

- 协议 differential：参考 JavaScript 与 Go 对同一 WMPF/CDP 帧比较字段、payload、错误和事件顺序。
- 模拟端到端：`TestSimulatedEndToEndCDPMatrix` 覆盖 Runtime、Debugger、Page、DOM、Network、Console、Performance，多 `jscontextId`、广播、请求关联、长消息、并发、损坏帧和重连。
- Windows live：`scripts/smoke-windows.ps1` 先运行 `matrix` 捕获首次初始化事件，再运行 `link` 验证后续客户端连接。link 只证明监听、attach、upstream、三条基础命令和退出；matrix 还验证对象、异常、console、脚本解析、暂停/继续、调用栈、长消息、并发、结构化错误、执行上下文和重连。

## Windows live 状态

最终构建在 WMPF 25297 上完成真实 live 验证。Agent 命中 `OnLoadStart`，将 scene `1256` 改为 `1101`，并命中 CDP filter 的 `6 → 0` patch；9421 upstream 建立后返回：

```text
live-cdp-matrix: domains=Runtime,Debugger,Page,DOM,Network,Console,Performance init=10 objects=true exceptions=true console=true scripts=true pause-resume=true callframes=true long-bytes=491520 concurrent=16 error-response=true contexts=true reconnect=true events=371
link-smoke: Runtime.enable=true Debugger.enable=true Runtime.evaluate=true events=1
target-survives-shutdown=true peer=66620
ports-released=true
```

smoke runner 使用新 Windows 进程组和 `CTRL_BREAK_EVENT`，bridge 日志为 `stop-requested=true`、`child-exit-status=0`、`child-exit-code=0`，stderr 为空；未使用强杀 fallback。上游 peer PID 66620 在退出后仍存活；本轮打开 miniapp 后新 renderer PID 55016 在 bridge 退出数分钟后的延时检查中仍存活且 `Responding=True`，旧 preload PID 61248 被替换。该进程级证据排除了原先的打开后立即闪退。

## 构建产物

- `dist/miniapp-bridge.exe`: `68A6367C4C8EED44CAFF7AF9E2DDC8910F3F6E8EDE1274C45E993AAAE9BAC480`
- `dist/miniapp-frida.dll`: `BE79569BC2C764F72A37888A06635F7E6312C5155838D35F64C49BFAFBE42B90`
- `third_party/zlib/lib/windows-x86_64/libz.a`: `9DE45B674DA1FC9F11D3E1833CFC6FA98AE27468D5F0E222556E65FC9B950D2A`
- zlib 1.3.1 source archive: `9A93B2B7DFDAC77CEBA5A558A580E74667DD6FEDE4585B91EEFB60F03B72DF23`

## 当前结论

Go 默认、tagged 与 smoke runner 语句覆盖率均严格达到 100%，协议 differential、模拟端到端 CDP matrix、真实 Windows 扩展 live CDP matrix、优雅退出和目标 renderer 延时存活检查均通过；可以提交本轮 Verify。
