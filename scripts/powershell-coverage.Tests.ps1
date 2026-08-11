Set-StrictMode -Version Latest

Describe 'PowerShell coverage contract' {
    It 'executes the coverage runner with an explicit test path' {
        $runner = Join-Path $PSScriptRoot 'powershell-coverage.ps1'
        Test-Path -LiteralPath $runner -PathType Leaf | Should Be $true
    }

    It 'keeps all production scripts in the default scope' {
        $scripts = @(Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.ps1' -File |
            Where-Object { $_.Name -notlike '*.Tests.ps1' -and $_.Name -ne 'powershell-coverage.ps1' } |
            Select-Object -ExpandProperty Name)
        $scripts.Count | Should BeGreaterThan 5
        ($scripts -contains 'coverage-gate.ps1') | Should Be $true
        ($scripts -contains 'cshim-coverage.ps1') | Should Be $true
    }

    It 'keeps the child coverage hook outside the production scope' {
        $hook = Join-Path $PSScriptRoot 'test-support\powershell-coverage-hook.ps1'
        (Test-Path -LiteralPath $hook -PathType Leaf) | Should Be $true
        $topLevel = @(Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.ps1' -File |
            Select-Object -ExpandProperty FullName)
        ($topLevel -contains (Resolve-Path -LiteralPath $hook).Path) | Should Be $false
    }

    It 'does not prepend a coverage parameter ahead of production positional parameters' {
        $runner = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'powershell-coverage.ps1') -Raw
        $runner | Should Match 'Append the hidden optional parameter'
        $runner | Should Match '\$paramOffsets\.Close'
        $runner | Should Not Match 'Insert\(\$openOffset \+ 1'
        $runner | Should Match 'CommandBaseAst'
        $runner | Should Match 'Breakpoints use coordinates from the instrumented file'
    }

    It 'passes immutable child paths so reduced environments still record coverage' {
        $runner = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'powershell-coverage.ps1') -Raw
        $runner | Should Match '-CoverageDirectory'
        $runner | Should Match '-ScopeFile'
        $runner | Should Match '-and \$_\.FullName -ne \$PSCommandPath'
    }

    It 'uses Pester-compatible breakpoints for child process coverage' {
        $hook = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'test-support\powershell-coverage-hook.ps1') -Raw
        $hook | Should Match 'Set-PSBreakpoint'
        $hook | Should Match 'CommandBaseAst'
        $hook | Should Match "kind = 'hit'"
    }

    It 'executes deterministic build and generation orchestrators' {
        if ($env:MINIAPP_BRIDGE_PS_COVERAGE_RUN_GO -ne '1') {
            return
        }
        if ($env:MINIAPP_BRIDGE_PS_COVERAGE_SKIP_ORCHESTRATORS -eq '1') {
            return
        }
        $repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
        $suffix = [guid]::NewGuid().ToString('N')
        $generated = Join-Path $env:TEMP "miniapp-addresses-$suffix.go"
        $cReport = Join-Path $env:TEMP "miniapp-cshim-coverage-$suffix.json"
        try {
            & (Join-Path $PSScriptRoot 'generate-address-configs.ps1') -Output $generated
            if ($LASTEXITCODE -ne 0) { throw "address generation failed with exit $LASTEXITCODE" }
            & (Join-Path $PSScriptRoot 'cshim-coverage.ps1') -ReportPath $cReport
            if ($LASTEXITCODE -ne 0) { throw "C shim coverage failed with exit $LASTEXITCODE" }
            & (Join-Path $PSScriptRoot 'build-frida-shim.ps1')
            if ($LASTEXITCODE -ne 0) { throw "Frida shim build failed with exit $LASTEXITCODE" }
            & (Join-Path $PSScriptRoot 'coverage-gate.ps1')
            if ($LASTEXITCODE -ne 0) { throw "coverage gate failed with exit $LASTEXITCODE" }
            & (Join-Path $PSScriptRoot 'build-windows.ps1')
            if ($LASTEXITCODE -ne 0) { throw "Windows build failed with exit $LASTEXITCODE" }
        } finally {
            Remove-Item -LiteralPath $generated -Force -ErrorAction SilentlyContinue
            Remove-Item -LiteralPath $cReport -Force -ErrorAction SilentlyContinue
        }
    }

    It 'runs the scripts integration package when child coverage is enabled' {
        if ($env:MINIAPP_BRIDGE_PS_COVERAGE_RUN_GO -ne '1') {
            return
        }
        if ($env:MINIAPP_BRIDGE_PS_COVERAGE_SKIP_ORCHESTRATORS -eq '1') {
            return
        }
        $output = @(& go test ./scripts -count=1 -timeout 240s 2>&1)
        $exitCode = $LASTEXITCODE
        if ($exitCode -ne 0) {
            throw "go test ./scripts failed with exit $exitCode`n$($output -join [Environment]::NewLine)"
        }
    }
}

Describe 'PowerShell Windows smoke coverage fixture' {
    It 'executes the Windows smoke success state machine with deterministic process fixtures' {
        if ($env:MINIAPP_BRIDGE_PS_COVERAGE_RUN_GO -ne '1') {
            return
        }

        $start = [DateTime]::Parse('2026-01-01T00:00:00Z').ToUniversalTime()
        $hostProcess = [pscustomobject]@{
            StartTime = $start
            MainWindowHandle = [long]1
            MainWindowTitle = 'WeChat'
            Responding = $true
        }
        $peerProcessFixture = [pscustomobject]@{
            StartTime = $start.AddSeconds(1)
            MainWindowHandle = [long]2
            MainWindowTitle = 'Miniapp'
            Responding = $true
        }
        $rendererProcess = [pscustomobject]@{
            StartTime = $start.AddSeconds(2)
            MainWindowHandle = [long]3
            MainWindowTitle = 'Renderer'
            Responding = $true
        }
        $freshPeerProcess = [pscustomobject]@{
            StartTime = $start.AddSeconds(3)
            MainWindowHandle = [long]4
            MainWindowTitle = 'Fresh peer'
            Responding = $true
        }
        $replacementPeerProcess = [pscustomobject]@{
            StartTime = $start.AddSeconds(30)
            MainWindowHandle = [long]2
            MainWindowTitle = 'Replacement peer'
            Responding = $true
        }
        $windowProcess = [pscustomobject]@{
            StartTime = $start.AddSeconds(4)
            MainWindowHandle = [long]5
            MainWindowTitle = 'New window'
            Responding = $true
        }
        $backgroundProcess = [pscustomobject]@{
            StartTime = $start.AddSeconds(5)
            MainWindowHandle = [long]0
            MainWindowTitle = ''
            Responding = $true
        }
        $runner = [pscustomobject]@{
            Id = 999
            HasExited = $false
            ExitCode = 0
        }
        $runner | Add-Member ScriptMethod WaitForExit {
            param([int]$Milliseconds)
            if ($global:miniappBridgeSmokeWaitSuccess -eq $false) {
                return $false
            }
            $this.HasExited = $true
            return $true
        }
        $runner | Add-Member ScriptMethod Refresh { return }

        $global:miniappBridgeSmokeStdout = @'
child-pid=500
[frida] attached pid=100
AppletIndexContainer::OnLoadStart onEnter
miniapp client connected
stop-requested=true
child-exit-code=0
teardown-agent-unload=true
teardown-session-detach=true
teardown-device-close=true
teardown-native-runtime-release=true
'@
        $server = [pscustomobject]@{
            OwningProcess = 500
            LocalAddress = '127.0.0.1'
            LocalPort = 9421
            RemoteAddress = '127.0.0.1'
            RemotePort = 40000
        }
        $peer = [pscustomobject]@{
            OwningProcess = 200
            LocalAddress = '127.0.0.1'
            LocalPort = 40000
            RemoteAddress = '127.0.0.1'
            RemotePort = 9421
        }
        $freshServerFixture = [pscustomobject]@{
            OwningProcess = 500
            LocalAddress = '127.0.0.1'
            LocalPort = 9421
            RemoteAddress = '127.0.0.1'
            RemotePort = 40001
        }
        $freshPeerFixture = [pscustomobject]@{
            OwningProcess = 202
            LocalAddress = '127.0.0.1'
            LocalPort = 40001
            RemoteAddress = '127.0.0.1'
            RemotePort = 9421
        }
        $pidChangedPeerFixture = [pscustomobject]@{
            OwningProcess = 202
            LocalAddress = '127.0.0.1'
            LocalPort = 40000
            RemoteAddress = '127.0.0.1'
            RemotePort = 9421
        }
        $unstablePeerFixture = [pscustomobject]@{
            LocalAddress = '127.0.0.1'
            LocalPort = 40000
            RemoteAddress = '127.0.0.1'
            RemotePort = 9421
        }
        $unstablePeerFixture | Add-Member ScriptProperty OwningProcess {
            $global:miniappBridgeSmokeUnstableOwnerReads++
            if ($global:miniappBridgeSmokeUnstableOwnerReads -le 4) { return 200 }
            return 0
        }
        $zeroPidCandidate = [pscustomobject]@{
            OwningProcess = 0
            LocalAddress = '127.0.0.1'
            LocalPort = 40000
            RemoteAddress = '127.0.0.1'
            RemotePort = 9421
        }

        function global:go {
            $requestedMode = [string]($args | Select-Object -Last 1)
            if ($global:miniappBridgeSmokeGoFailure -eq 'build' -or
                $global:miniappBridgeSmokeGoFailure -eq $requestedMode) {
                $global:LASTEXITCODE = 1
                return
            }
            $global:LASTEXITCODE = 0
        }
        Mock Start-Process { return $runner }
        Mock Start-Sleep {
            param($Seconds, $Milliseconds)
            if ($global:miniappBridgeSmokePhase -eq 'exit-after-pid' -and $Seconds -eq 3) {
                $runner.HasExited = $true
            }
        }
        Mock Get-Content {
            param($Path, $LiteralPath)
            $requestedPath = if ($null -ne $LiteralPath) { [string]$LiteralPath } else { [string]$Path }
            if ($requestedPath -like '*scope.json') {
                return [IO.File]::ReadAllText($requestedPath)
            }
            if ($requestedPath -like '*.stderr.log') { return '' }
            $global:miniappBridgeSmokeContentCall++
            if ($global:miniappBridgeSmokeContentMode -eq 'no-child') {
                return ($global:miniappBridgeSmokeStdout -replace '(?m)^child-pid=.*\r?\n?', '')
            }
            if ($global:miniappBridgeSmokeContentMode -eq 'no-attach') {
                return ($global:miniappBridgeSmokeStdout -replace '(?m)^\[frida\] attached.*\r?\n?', '')
            }
            if ($global:miniappBridgeSmokeContentMode -eq 'dynamic-onload' -and
                $global:miniappBridgeSmokeContentCall -ge 3) {
                return $global:miniappBridgeSmokeStdout + "`nAppletIndexContainer::OnLoadStart onEnter`n"
            }
            return $global:miniappBridgeSmokeStdout
        }
        $global:miniappBridgeSmokeSnapshotCall = 0
        $global:miniappBridgeSmokeRendererMode = 'new'
        $global:miniappBridgeSmokeNetworkMode = 'complete'
        $global:miniappBridgeSmokeNetworkCall = 0
        $global:miniappBridgeSmokeGoFailure = ''
        $global:miniappBridgeSmokeWaitSuccess = $true
        $global:miniappBridgeSmokeContentMode = ''
        $global:miniappBridgeSmokeContentCall = 0
        $global:miniappBridgeSmokePhase = ''
        $global:miniappBridgeSmokeListenerMode = 'complete'
        $global:miniappBridgeSmokeCimMode = 'complete'
        $global:miniappBridgeSmokeRunnerVisible = $false
        $global:miniappBridgeSmokeUnstableOwnerReads = 0
        $global:miniappBridgeSmokeTeardownListenMode = 'empty'
        $global:miniappBridgeSmokeTeardownListenCall = 0
        $global:miniappBridgeSmokeFinalMode = 'stable'
        $global:miniappBridgeSmokeFinalSnapshotCall = 0
        Mock Get-CimInstance {
            $global:miniappBridgeSmokeSnapshotCall++
            if ($global:miniappBridgeSmokeCimMode -eq 'empty') { return @() }
            if ($runner.HasExited -and $global:miniappBridgeSmokeFinalMode -eq 'empty-once') {
                $global:miniappBridgeSmokeFinalSnapshotCall++
                if ($global:miniappBridgeSmokeFinalSnapshotCall -eq 1) { return @() }
            }
            $rows = @(
                [pscustomobject]@{ ProcessId = 100; ParentProcessId = 1; CommandLine = '--host' },
                [pscustomobject]@{
                    ProcessId = 200
                    ParentProcessId = 100
                    CommandLine = $(if ($global:miniappBridgeSmokeCimMode -eq 'peer-renderer') {
                        '--type=renderer --wmpf-render-type=4 --wmpf-appid=fixture'
                    } else {
                        '--upstream-peer'
                    })
                }
            )
            $includeRenderer = $global:miniappBridgeSmokeRendererMode -ne 'absent' -and
                ($global:miniappBridgeSmokeRendererMode -in @('reused', 'reused-fallback') -or
                    $global:miniappBridgeSmokeSnapshotCall -ge 4)
            if ($includeRenderer) {
                $appid = if ($global:miniappBridgeSmokeRendererMode -eq 'fallback') { 'preload-fixture' } else { 'fixture' }
                if ($global:miniappBridgeSmokeRendererMode -eq 'reused-fallback') { $appid = 'preload-fixture' }
                $rendererParent = if ($global:miniappBridgeSmokeRendererMode -eq 'missing-host') { 300 } else { 100 }
                $rows += [pscustomobject]@{ ProcessId = 201; ParentProcessId = $rendererParent; CommandLine = " --type=renderer --wmpf-render-type=4 --wmpf-appid=$appid " }
            }
            if ($global:miniappBridgeSmokeNetworkMode -in @('reselection', 'pid-change')) {
                $rows += [pscustomobject]@{ ProcessId = 202; ParentProcessId = 100; CommandLine = '--fresh-upstream-peer' }
            }
            if ($global:miniappBridgeSmokeCimMode -eq 'window-and-background') {
                $rows += [pscustomobject]@{ ProcessId = 204; ParentProcessId = 100; CommandLine = '--background' }
                if ($global:miniappBridgeSmokeSnapshotCall -ge 4) {
                    $rows += [pscustomobject]@{ ProcessId = 203; ParentProcessId = 100; CommandLine = '--utility' }
                }
            }
            if ($global:miniappBridgeSmokePhase -eq 'exit-upstream' -and
                $global:miniappBridgeSmokeSnapshotCall -eq 1) {
                $runner.HasExited = $true
            }
            return $rows
        }
        Mock Get-Process {
            param($Id)
            if ($Id -eq $runner.Id -and $global:miniappBridgeSmokeRunnerVisible) { return $runner }
            if ($Id -eq 100) { return $hostProcess }
            if ($Id -eq 200) {
                if ($global:miniappBridgeSmokeNetworkMode -eq 'process-missing' -and
                    $global:miniappBridgeSmokeSnapshotCall -ge 3) {
                    return $null
                }
                if ($global:miniappBridgeSmokeNetworkMode -eq 'identity-change' -and
                    $global:miniappBridgeSmokeSnapshotCall -ge 3) {
                    return $replacementPeerProcess
                }
                return $peerProcessFixture
            }
            if ($Id -eq 201) {
                if ($runner.HasExited -and $global:miniappBridgeSmokeFinalMode -eq 'unresponsive') {
                    return [pscustomobject]@{
                        StartTime = $rendererProcess.StartTime
                        MainWindowHandle = $rendererProcess.MainWindowHandle
                        MainWindowTitle = $rendererProcess.MainWindowTitle
                        Responding = $false
                    }
                }
                return $rendererProcess
            }
            if ($Id -eq 202) { return $freshPeerProcess }
            if ($Id -eq 203) { return $windowProcess }
            if ($Id -eq 204) { return $backgroundProcess }
            return $null
        }
        Mock Get-NetTCPConnection {
            param($State, $OwningProcess)
            if ($State -eq 'Established') {
                $global:miniappBridgeSmokeNetworkCall++
                if ($global:miniappBridgeSmokeNetworkMode -eq 'empty') { return @() }
                if ($global:miniappBridgeSmokeNetworkMode -eq 'server-only') { return @($server) }
                if ($global:miniappBridgeSmokeNetworkMode -eq 'server-candidate') { return @($server, $zeroPidCandidate) }
                if ($global:miniappBridgeSmokeNetworkMode -eq 'unknown-peer') {
                    $unknown = $peer.PSObject.Copy()
                    $unknown.OwningProcess = 998
                    return @($server, $unknown)
                }
                if ($global:miniappBridgeSmokeNetworkMode -eq 'incomplete-tuple') {
                    return @($server, $unstablePeerFixture)
                }
                if ($global:miniappBridgeSmokeNetworkMode -eq 'pid-change' -and
                    $global:miniappBridgeSmokeNetworkCall -gt 1) {
                    return @($server, $pidChangedPeerFixture)
                }
                if ($global:miniappBridgeSmokeNetworkMode -eq 'reselection' -and
                    $global:miniappBridgeSmokeNetworkCall -gt 1) { return @($freshServerFixture, $freshPeerFixture) }
                if ($global:miniappBridgeSmokeNetworkMode -eq 'validation-empty' -and
                    $global:miniappBridgeSmokeNetworkCall -gt 1) { return @() }
                return @($server, $peer)
            }
            if ($PSBoundParameters.ContainsKey('OwningProcess')) {
                if ($global:miniappBridgeSmokePhase -eq 'exit-before-attach') {
                    $runner.HasExited = $true
                }
                if ($global:miniappBridgeSmokeListenerMode -eq 'missing') {
                    return @([pscustomobject]@{ LocalPort = 9421 })
                }
                return @(
                    [pscustomobject]@{ LocalPort = 9421 },
                    [pscustomobject]@{ LocalPort = 62000 }
                )
            }
            if ($State -eq 'Listen') {
                $global:miniappBridgeSmokeTeardownListenCall++
                if ($global:miniappBridgeSmokeTeardownListenMode -eq 'release-on-second' -and
                    $global:miniappBridgeSmokeTeardownListenCall -eq 1) {
                    return @([pscustomobject]@{ LocalPort = 9421 })
                }
                if ($global:miniappBridgeSmokeTeardownListenMode -eq 'persistent') {
                    return @([pscustomobject]@{ LocalPort = 9421 })
                }
            }
            return @()
        }
        Mock New-Item { return [pscustomobject]@{} }
        Mock Wait-Process { }
        Mock Stop-Process { }
        Mock Remove-Item { }

        try {
            $global:miniappBridgeSmokeGoFailure = 'build'
            $buildFailure = $null
            try {
                . (Join-Path $PSScriptRoot 'smoke-windows.ps1') -KeepBridgeRunning | Out-Null
            } catch {
                $buildFailure = $_
            }
            $buildFailure.Exception.Message | Should Match 'smoke process runner build failed'
            (Normalize-TcpAddress '') | Should Be ''
            (Normalize-TcpAddress '::ffff:127.0.0.1') | Should Be '127.0.0.1'
            (Normalize-TcpAddress 'NOT AN IP') | Should Be 'not an ip'
            $global:miniappBridgeSmokeGoFailure = ''

            $runner.HasExited = $true
            $earlyRunnerFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -KeepBridgeRunning | Out-Null
            } catch { $earlyRunnerFailure = $_ }
            $earlyRunnerFailure.Exception.Message | Should Match 'exited before reporting child PID'

            $runner.HasExited = $false
            $global:miniappBridgeSmokePhase = 'exit-after-pid'
            $afterPidFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -KeepBridgeRunning | Out-Null
            } catch { $afterPidFailure = $_ }
            $afterPidFailure.Exception.Message | Should Match 'exited before smoke checks'

            $runner.HasExited = $false
            $global:miniappBridgeSmokePhase = ''

            $global:miniappBridgeSmokeContentMode = 'no-child'
            $noPidFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -KeepBridgeRunning | Out-Null
            } catch { $noPidFailure = $_ }
            $noPidFailure.Exception.Message | Should Match 'did not report child PID within 10s'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeContentMode = 'no-attach'
            $attachedTargetPid = $null
            $attachTimeoutFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -KeepBridgeRunning | Out-Null
            } catch { $attachTimeoutFailure = $_ }
            $attachTimeoutFailure.Exception.Message | Should Match 'Frida attach was not confirmed within 30s'
            $global:miniappBridgeSmokeContentMode = ''
            $global:miniappBridgeSmokeListenerMode = 'missing'
            $listenerFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -KeepBridgeRunning | Out-Null
            } catch { $listenerFailure = $_ }
            $listenerFailure.Exception.Message | Should Match 'listeners are not owned'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeListenerMode = 'complete'
            $global:miniappBridgeSmokePhase = 'exit-before-attach'
            $attachFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -KeepBridgeRunning | Out-Null
            } catch { $attachFailure = $_ }
            $attachFailure.Exception.Message | Should Match 'exited before Frida attach'

            $runner.HasExited = $false
            $global:miniappBridgeSmokePhase = ''
            $global:miniappBridgeSmokeCimMode = 'empty'
            $targetFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -KeepBridgeRunning | Out-Null
            } catch { $targetFailure = $_ }
            $targetFailure.Exception.Message | Should Match 'target-process: not-present'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeCimMode = 'complete'
            $global:miniappBridgeSmokePhase = 'exit-upstream'
            $global:miniappBridgeSmokeSnapshotCall = 0
            $upstreamExitFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -KeepBridgeRunning | Out-Null
            } catch { $upstreamExitFailure = $_ }
            $upstreamExitFailure.Exception.Message | Should Match 'exited while waiting for WMPF upstream'

            $runner.HasExited = $false
            $global:miniappBridgeSmokePhase = ''

            $finalTargetSnapshot = @()
            $output = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode all -KeepBridgeRunning)
            ($output -match '^bridge-session-preserved=true').Count | Should Be 1
            ($output -contains 'cdp-coverage=full mode=all acceptance=true') | Should Be $true
            ($output -match '^upstream-peer-validated=true').Count | Should Be 1

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeRendererMode = 'fallback'
            $withOnLoadStdout = $global:miniappBridgeSmokeStdout
            $global:miniappBridgeSmokeStdout = $global:miniappBridgeSmokeStdout -replace "AppletIndexContainer::OnLoadStart onEnter`r?`n", ''
            $fallbackOutput = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning)
            ($fallbackOutput -match '^fallback-applet-renderer=true').Count | Should Be 1
            ($fallbackOutput -contains 'cdp-coverage=partial mode=link acceptance=false') | Should Be $true
            ($fallbackOutput -match '^agent-on-load-start=false').Count | Should Be 1
            $global:miniappBridgeSmokeStdout = $withOnLoadStdout

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeRendererMode = 'reused'
            $reusedOutput = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode interaction -KeepBridgeRunning)
            ($reusedOutput -match '^renderer-selection=reused').Count | Should Be 1
            ($reusedOutput -contains 'cdp-coverage=partial mode=interaction acceptance=false') | Should Be $true

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeRendererMode = 'reused-fallback'
            $reusedFallbackOutput = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning)
            ($reusedFallbackOutput -match '^renderer-selection=reused').Count | Should Be 1

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeRendererMode = 'new'
            $global:miniappBridgeSmokeContentMode = 'dynamic-onload'
            $global:miniappBridgeSmokeContentCall = 0
            $onLoadOutput = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning)
            ($onLoadOutput -contains 'agent-on-load-start=true') | Should Be $true
            $global:miniappBridgeSmokeContentMode = ''

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeNetworkMode = 'incomplete-tuple'
            $incompleteTupleFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning | Out-Null
            } catch { $incompleteTupleFailure = $_ }
            $incompleteTupleFailure.Exception.Message | Should Match 'complete server/peer tuple'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeNetworkMode = 'pid-change'
            $global:miniappBridgeSmokeRendererMode = 'new'
            $pidChangeOutput = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning)
            ($pidChangeOutput -match '^upstream-peer-validated=true attempt=2').Count | Should Be 1

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeNetworkMode = 'identity-change'
            $identityChangeFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning | Out-Null
            } catch { $identityChangeFailure = $_ }
            $identityChangeFailure.Exception.Message | Should Match 'TOCTOU validation'
            $identityChangeFailure.Exception.Message | Should Match 'peer-pid-start-time-changed'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeNetworkMode = 'process-missing'
            $processMissingFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning | Out-Null
            } catch { $processMissingFailure = $_ }
            $processMissingFailure.Exception.Message | Should Match 'TOCTOU validation'
            $global:miniappBridgeSmokeNetworkMode = 'complete'
            $global:miniappBridgeSmokeNetworkMode = 'complete'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeRendererMode = 'reused'
            $global:miniappBridgeSmokeContentMode = 'dynamic-onload'
            $global:miniappBridgeSmokeContentCall = 0
            $reusedOnLoadOutput = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode all -KeepBridgeRunning)
            ($reusedOnLoadOutput -match 'evidence=onload-start,upstream,cdp-matrix,cdp-link').Count | Should Be 1
            $global:miniappBridgeSmokeContentMode = ''

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeRendererMode = 'reused'
            $global:miniappBridgeSmokeCimMode = 'peer-renderer'
            $oldRendererStart = $rendererProcess.StartTime
            $rendererProcess.StartTime = $start.AddMilliseconds(500)
            try {
                $peerRendererOutput = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning)
            } finally {
                $rendererProcess.StartTime = $oldRendererStart
            }
            ($peerRendererOutput -match 'roles=.*upstream-peer.*renderer').Count | Should BeGreaterThan 0
            $global:miniappBridgeSmokeCimMode = 'complete'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeRendererMode = 'new'
            $global:miniappBridgeSmokeCimMode = 'window-and-background'
            $windowOutput = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning)
            ($windowOutput -match 'pid=203 .*roles=window-owner').Count | Should Be 1
            ($windowOutput -match 'roles= title=').Count | Should BeGreaterThan 0
            $global:miniappBridgeSmokeCimMode = 'complete'

            $plainRendererBreakpoint = Set-PSBreakpoint -Script ((Resolve-Path (Join-Path $PSScriptRoot 'smoke-windows.ps1')).Path) -Line 463 -Action {
                $targets = Get-Variable -Name newAppletRendererTargets -Scope 1 -ValueOnly -ErrorAction SilentlyContinue
                if ($targets -and $targets.Count -gt 0) { $targets[0].CommandLine = '--type=renderer' }
            }
            try {
                $runner.HasExited = $false
                $global:miniappBridgeSmokeSnapshotCall = 0
                $global:miniappBridgeSmokeNetworkCall = 0
                $global:miniappBridgeSmokeRendererMode = 'new'
                $plainRendererOutput = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning)
                ($plainRendererOutput -match 'roles=renderer').Count | Should BeGreaterThan 0
            } finally {
                Remove-PSBreakpoint -Id $plainRendererBreakpoint.Id -ErrorAction SilentlyContinue
            }

            $zeroRolesBreakpoint = Set-PSBreakpoint -Script ((Resolve-Path (Join-Path $PSScriptRoot 'smoke-windows.ps1')).Path) -Line 489 -Action {
                $trackedTargetRoles.Clear()
            }
            try {
                $runner.HasExited = $false
                $global:miniappBridgeSmokeSnapshotCall = 0
                $global:miniappBridgeSmokeNetworkCall = 0
                $global:miniappBridgeSmokeRendererMode = 'new'
                $zeroRolesFailure = $null
                try {
                    & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning | Out-Null
                } catch { $zeroRolesFailure = $_ }
                $zeroRolesFailure.Exception.Message | Should Match 'no connection or window-owned target process'
            } finally {
                Remove-PSBreakpoint -Id $zeroRolesBreakpoint.Id -ErrorAction SilentlyContinue
            }

            $originalStdout = $global:miniappBridgeSmokeStdout
            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeRendererMode = 'missing-host'
            $global:miniappBridgeSmokeStdout = $global:miniappBridgeSmokeStdout -replace '\[frida\] attached pid=100', '[frida] attached pid=300'
            $missingHost = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning | Out-Null
            } catch { $missingHost = $_ }
            $missingHost.Exception.Message | Should Match 'attached Frida host PID 300 was not present'
            $global:miniappBridgeSmokeStdout = $originalStdout

            foreach ($peerFailureCase in @(
                    [pscustomobject]@{ Mode = 'server-candidate'; Expected = 'peer process could not be identified' },
                    [pscustomobject]@{ Mode = 'unknown-peer'; Expected = 'not an identifiable WeChatAppEx process' }
                )) {
                $runner.HasExited = $false
                $global:miniappBridgeSmokeSnapshotCall = 0
                $global:miniappBridgeSmokeNetworkCall = 0
                $global:miniappBridgeSmokeRendererMode = 'new'
                $global:miniappBridgeSmokeNetworkMode = $peerFailureCase.Mode
                $peerFailure = $null
                try {
                    & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning | Out-Null
                } catch { $peerFailure = $_ }
                $peerFailure.Exception.Message | Should Match $peerFailureCase.Expected
            }

            foreach ($cdpFailureMode in 'matrix', 'link', 'interaction') {
                $runner.HasExited = $false
                $global:miniappBridgeSmokeSnapshotCall = 0
                $global:miniappBridgeSmokeNetworkCall = 0
                $global:miniappBridgeSmokeRendererMode = 'new'
                $global:miniappBridgeSmokeNetworkMode = 'complete'
                $global:miniappBridgeSmokeGoFailure = $cdpFailureMode
                $cdpFailure = $null
                try {
                    & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode $cdpFailureMode -KeepBridgeRunning | Out-Null
                } catch {
                    $cdpFailure = $_
                }
                $cdpFailure.Exception.Message | Should Match 'CDP|cdp'
            }
            $global:miniappBridgeSmokeGoFailure = ''

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeRendererMode = 'absent'
            $missingRenderer = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning | Out-Null
            } catch {
                $missingRenderer = $_
            }
            $missingRenderer.Exception.Message | Should Match 'no type=4 applet renderer'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeRendererMode = 'new'
            $global:miniappBridgeSmokeNetworkMode = 'validation-empty'
            $toctouFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning | Out-Null
            } catch {
                $toctouFailure = $_
            }
            $toctouFailure.Exception.Message | Should Match 'TOCTOU validation'

            foreach ($failureCase in @(
                    [pscustomobject]@{ Mode = 'empty'; Expected = 'WMPF upstream connection on 9421 was not established' },
                    [pscustomobject]@{ Mode = 'server-only'; Expected = 'WMPF upstream peer process could not be identified' }
                )) {
                $runner.HasExited = $false
                $global:miniappBridgeSmokeSnapshotCall = 0
                $global:miniappBridgeSmokeRendererMode = 'new'
                $global:miniappBridgeSmokeNetworkMode = $failureCase.Mode
                $connectionFailure = $null
                try {
                    & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link -KeepBridgeRunning | Out-Null
                } catch {
                    $connectionFailure = $_
                }
                $connectionFailure.Exception.Message | Should Match $failureCase.Expected
            }

            $completeStdout = $global:miniappBridgeSmokeStdout

            $runner.HasExited = $false
            $runner.ExitCode = 7
            $global:miniappBridgeSmokeRunnerVisible = $true
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeRendererMode = 'new'
            $global:miniappBridgeSmokeNetworkMode = 'complete'
            $global:miniappBridgeSmokeFinalMode = 'stable'
            $runnerExitFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link | Out-Null
            } catch { $runnerExitFailure = $_ }
            $runnerExitFailure.Exception.Message | Should Match 'smoke process runner exited with 7'

            $runner.HasExited = $false
            $runner.ExitCode = 0
            $global:miniappBridgeSmokeRunnerVisible = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeStdout = $completeStdout -replace '(?m)^(stop-requested|child-exit-code|teardown-[^=]+)=.*\r?\n?', ''
            $missingMarkersFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link | Out-Null
            } catch { $missingMarkersFailure = $_ }
            $missingMarkersFailure.Exception.Message | Should Match 'native teardown markers were not observed'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeStdout = $completeStdout -replace '(?m)^teardown-[^=]+=.*\r?\n?', ''
            $global:miniappBridgeSmokeTeardownListenMode = 'release-on-second'
            $global:miniappBridgeSmokeTeardownListenCall = 0
            $proxyOutput = @(& (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link)
            ($proxyOutput -contains 'teardown-markers=agent-unload,session-detach,device-close,native-runtime-release source=runner-child-exit-proxy dependency=sdk-native-close-order') | Should Be $true
            ($proxyOutput -match '^target-survives-shutdown=true').Count | Should Be 1
            ($proxyOutput -contains 'smoke-success=true') | Should Be $true

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeStdout = $completeStdout
            $global:miniappBridgeSmokeTeardownListenMode = 'empty'
            $global:miniappBridgeSmokeFinalMode = 'unresponsive'
            $unresponsiveFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link | Out-Null
            } catch { $unresponsiveFailure = $_ }
            $unresponsiveFailure.Exception.Message | Should Match 'tracked target window stopped responding'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeFinalMode = 'empty-once'
            $global:miniappBridgeSmokeFinalSnapshotCall = 0
            $emptyFinalFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link | Out-Null
            } catch { $emptyFinalFailure = $_ }
            $emptyFinalFailure.Exception.Message | Should Match 'tracked target process exited during bridge shutdown'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeFinalMode = 'stable'
            $global:miniappBridgeSmokeTeardownListenMode = 'persistent'
            $global:miniappBridgeSmokeTeardownListenCall = 0
            $persistentListenerFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link | Out-Null
            } catch { $persistentListenerFailure = $_ }
            $persistentListenerFailure.Exception.Message | Should Match 'ports were not released'

            $global:miniappBridgeSmokeTeardownListenMode = 'empty'
            $occupiedPort = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Parse('127.0.0.1'), 9421)
            $occupiedPort.Server.ExclusiveAddressUse = $true
            try {
                $occupiedPort.Start()
                $runner.HasExited = $false
                $global:miniappBridgeSmokeSnapshotCall = 0
                $global:miniappBridgeSmokeNetworkCall = 0
                $rebindFailure = $null
                try {
                    & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link | Out-Null
                } catch { $rebindFailure = $_ }
                $rebindFailure.Exception.Message | Should Match 'ports could not be rebound'
            } finally {
                $occupiedPort.Stop()
            }

            $smokeChecksBreakpoint = Set-PSBreakpoint -Script ((Resolve-Path (Join-Path $PSScriptRoot 'smoke-windows.ps1')).Path) -Line 651 -Action {
                Set-Variable -Name smokeChecksPassed -Value $false -Scope 1
            }
            try {
                $runner.HasExited = $false
                $global:miniappBridgeSmokeSnapshotCall = 0
                $global:miniappBridgeSmokeNetworkCall = 0
                $smokeChecksFailure = $null
                try {
                    & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link | Out-Null
                } catch { $smokeChecksFailure = $_ }
                $smokeChecksFailure.Exception.Message | Should Match 'smoke checks did not complete'
            } finally {
                Remove-PSBreakpoint -Id $smokeChecksBreakpoint.Id -ErrorAction SilentlyContinue
            }

            $global:miniappBridgeSmokeStdout = $completeStdout
            $global:miniappBridgeSmokeFinalMode = 'stable'
            $global:miniappBridgeSmokeTeardownListenMode = 'empty'

            $runner.HasExited = $false
            $global:miniappBridgeSmokeSnapshotCall = 0
            $global:miniappBridgeSmokeNetworkCall = 0
            $global:miniappBridgeSmokeWaitSuccess = $false
            $forcedFailure = $null
            try {
                & (Join-Path $PSScriptRoot 'smoke-windows.ps1') -UpstreamWaitSeconds 1 -ShutdownTimeoutSeconds 1 -CDPMode link | Out-Null
            } catch {
                $forcedFailure = $_
            }
            $forcedFailure | Should Not Be $null
        } finally {
            Remove-Item Function:\global:go -Force -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeSnapshotCall -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeRendererMode -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeNetworkMode -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeNetworkCall -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeGoFailure -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeWaitSuccess -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokePhase -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeListenerMode -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeCimMode -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeStdout -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeRunnerVisible -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeUnstableOwnerReads -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeTeardownListenMode -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeTeardownListenCall -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeFinalMode -Scope Global -ErrorAction SilentlyContinue
            Remove-Variable miniappBridgeSmokeFinalSnapshotCall -Scope Global -ErrorAction SilentlyContinue
        }
    }
}
