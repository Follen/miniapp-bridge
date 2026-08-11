<comet-ambient-resume>
<!-- Managed by Comet. Edits inside this block may be replaced by comet init/update. -->
<!-- Contract: comet.resume_probe.v2 -->

## Comet Ambient Resume

在这个仓库中，开始处理需要改动或调查的任务前，如果可能存在活跃 Comet workflow，把当前用户请求传入只读探针：`comet resume-probe . --stdin --json`。

- 如果用户通过宿主明确调用任意 Comet Skill（例如 `@comet`、`/comet`、`@comet-native` 或 `/comet-hotfix`），显式调用优先于本恢复协议；不要运行 resume probe，直接进入被调用的 Skill。
- 只信任返回的 `workflow`、`skill` 和 `entrySource`；它们只由项目配置或无配置兼容回退决定。不得扫描或切换另一套 workflow。
- 如果 probe 返回 `auto_resume`，简短说明选中的 active change，并进入 `nextCommand` 指向的永久入口。不要把状态命令当作恢复入口直接推进。
- 如果 probe 返回 `ask_user`，只问一个简短问题并等待用户回复。
- 如果当前请求未明确调用 Comet Skill，且 probe 返回 `out_of_scope` 或 `none`，不要进入 Comet workflow。
- 如果配置或状态无效且没有 `nextCommand`，停止并报告原因；不要猜测另一个 workflow。
- 不能只因为存在 active change 就把无关任务挂到该 change。Native 的未提交改动由 Native 入口检查，不由探针自动归因。
</comet-ambient-resume>

## Windows Live Smoke：自动打开小程序

执行 `scripts/smoke-windows.ps1` 时，Agent 应自行触发小程序加载。仅在 UIAutomation 无法唯一定位目标时再请用户操作。

1. 先启动 smoke，等待日志同时出现 `[frida] attached` 和 `action-required=open-or-reload-miniapp`。必须在 attach 之后打开或重新加载小程序，否则 Hook 不会捕获本次启动。
2. `weixin:` / `xweixin:` 只能启动微信；没有明确 appid 时，不用它们猜测小程序地址。
3. 使用 PowerShell 的 `.NET UIAutomation`，动态选择 `WeChatAppEx` 中 `MainWindowHandle -ne 0` 且标题为 `微信` 的唯一窗口。禁止硬编码 PID 和屏幕坐标。
4. 在窗口树中按完整可见名称查找“最近使用”的小程序，例如 `微信指数`。沿 RawView 父节点向上查找首个支持 `InvokePattern` 的卡片，并且只调用一次 `Invoke()`。
5. 若只出现 `--wmpf-appid=preload-*`，正常关闭小程序窗口后，在 bridge 仍已 attach 的状态下重新调用最近使用卡片。不要终止微信或 WMPF 宿主进程。
6. 只有同时观察到非 preload 的 applet renderer、9421 `Established` 连接以及 bridge 日志 `miniapp client connected`，才算打开成功；随后继续完整 CDP matrix。

可复用的卡片调用命令：

```powershell
$miniappName = '微信指数'

Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes

$hosts = @(Get-Process -Name WeChatAppEx -ErrorAction Stop |
    Where-Object { $_.MainWindowHandle -ne 0 -and $_.MainWindowTitle -eq '微信' })
if ($hosts.Count -ne 1) {
    throw "expected one WMPF window, found $($hosts.Count)"
}

$root = [System.Windows.Automation.AutomationElement]::FromHandle(
    $hosts[0].MainWindowHandle
)
$all = $root.FindAll(
    [System.Windows.Automation.TreeScope]::Descendants,
    [System.Windows.Automation.Condition]::TrueCondition
)
$matches = @($all | Where-Object { $_.Current.Name -eq $miniappName })
if ($matches.Count -ne 1) {
    throw "expected one miniapp named '$miniappName', found $($matches.Count)"
}

$walker = [System.Windows.Automation.TreeWalker]::RawViewWalker
$element = $matches[0]
$invoked = $false
for ($level = 0; $level -lt 8 -and $null -ne $element; $level++) {
    $pattern = $null
    if ($element.TryGetCurrentPattern(
        [System.Windows.Automation.InvokePattern]::Pattern,
        [ref]$pattern
    )) {
        ([System.Windows.Automation.InvokePattern]$pattern).Invoke()
        $invoked = $true
        break
    }
    $element = $walker.GetParent($element)
}
if (-not $invoked) {
    throw "miniapp card '$miniappName' has no InvokePattern"
}
```

调用后用以下只读检查确认结果：

```powershell
Get-CimInstance Win32_Process -Filter "Name='WeChatAppEx.exe'" |
    Where-Object { $_.CommandLine -match '--wmpf-appid=(?!preload-)' } |
    Select-Object ProcessId, ParentProcessId, CommandLine

Get-NetTCPConnection -LocalPort 9421 -State Established |
    Select-Object LocalAddress, LocalPort, RemoteAddress, RemotePort, OwningProcess
```
