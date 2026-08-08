param(
    [ValidateRange(1, 300)]
    [int]$UpstreamWaitSeconds = 60,

    [ValidateRange(1, 60)]
    [int]$ShutdownTimeoutSeconds = 20,

    # Keep the bridge and target WMPF session alive after protocol checks. This
    # is useful for interactive DevTools use; the default still exercises the
    # complete graceful-shutdown contract.
    [switch]$KeepBridgeRunning
)

$ErrorActionPreference = 'Stop'

function Get-TargetProcessSnapshot {
    $rows = @(Get-CimInstance Win32_Process -Filter "Name = 'WeChatAppEx.exe'" -ErrorAction Stop)
    $snapshot = foreach ($row in $rows) {
        $process = Get-Process -Id ([int]$row.ProcessId) -ErrorAction SilentlyContinue
        if (-not $process) { continue }
        try {
            $startTimeUtc = $process.StartTime.ToUniversalTime()
            $mainWindowHandle = [long]$process.MainWindowHandle
            $mainWindowTitle = [string]$process.MainWindowTitle
            $responding = [bool]$process.Responding
        } catch {
            continue
        }
        [pscustomobject]@{
            Id                = [int]$row.ProcessId
            ParentId          = [int]$row.ParentProcessId
            StartTimeUtc      = $startTimeUtc.ToString('o')
            StartTimeUtcTicks = $startTimeUtc.Ticks
            Identity          = "$($row.ProcessId)@$($startTimeUtc.Ticks)"
            CommandLine       = ([string]$row.CommandLine -replace '[\r\n]+', ' ')
            MainWindowHandle  = $mainWindowHandle
            MainWindowTitle   = ($mainWindowTitle -replace '[\r\n]+', ' ')
            Responding        = $responding
        }
    }
    return @($snapshot)
}

function Add-ObservedTargetSnapshot {
    param(
        [hashtable]$Observed,
        [object[]]$Snapshot
    )
    foreach ($item in $Snapshot) {
        $Observed[$item.Identity] = $item
    }
}

function Write-TargetProcessDetail {
    param(
        [string]$Phase,
        [object]$Process,
        [string]$Roles = ''
    )
    Write-Output "target-process-detail: phase=$Phase pid=$($Process.Id) ppid=$($Process.ParentId) start-utc=$($Process.StartTimeUtc) start-ticks=$($Process.StartTimeUtcTicks) window=$($Process.MainWindowHandle) responding=$($Process.Responding) roles=$Roles title=$($Process.MainWindowTitle) command-line=$($Process.CommandLine)"
}

function Test-IsRendererProcess {
    param([object]$Process)
    return $Process.CommandLine -match '(?:^|\s)--type=renderer(?:\s|$)'
}

function Test-IsAppletRendererProcess {
    param([object]$Process)
    return (Test-IsRendererProcess -Process $Process) -and
        $Process.CommandLine -match '(?:^|\s)--wmpf-render-type=4(?:\s|$)' -and
        $Process.CommandLine -match '(?:^|\s)--wmpf-appid=(?!preload-)[^\s]+'
}

function Test-IsAnyAppletRendererProcess {
    param([object]$Process)
    return (Test-IsRendererProcess -Process $Process) -and
        $Process.CommandLine -match '(?:^|\s)--wmpf-render-type=4(?:\s|$)' -and
        $Process.CommandLine -match '(?:^|\s)--wmpf-appid=[^\s]+'
}

$root = Resolve-Path '.'
$exe = (Resolve-Path 'dist/miniapp-bridge.exe').Path
$id = [guid]::NewGuid().ToString('N')
$stdout = Join-Path $env:TEMP "miniapp-bridge-$id.stdout.log"
$stderr = Join-Path $env:TEMP "miniapp-bridge-$id.stderr.log"
$stopFile = Join-Path $env:TEMP "miniapp-bridge-$id.stop"
$runnerExe = Join-Path $env:TEMP "miniapp-smoke-runner-$id.exe"

go build -o $runnerExe ./scripts/smoke-process-runner
if ($LASTEXITCODE -ne 0) { throw "smoke process runner build failed with exit $LASTEXITCODE" }

$runnerArgs = @(
    "-exe=`"$exe`"",
    "-workdir=`"$($root.Path)`"",
    "-stop-file=`"$stopFile`"",
    "-stop-timeout=${ShutdownTimeoutSeconds}s",
    '--',
    '--debug-frida'
)
$runner = Start-Process -FilePath $runnerExe -ArgumentList $runnerArgs -WorkingDirectory $root -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru -WindowStyle Hidden
$childPid = $null
$upstreamPeerPids = @()
$upstreamPeerTargets = @()
$initialTargetSnapshot = @()
$observedTargetSnapshot = @{}
$trackedTargetRoles = @{}
$trackedTargetMetadata = @{}
try {
    $pidDeadline = [DateTime]::UtcNow.AddSeconds(10)
    while ([DateTime]::UtcNow -lt $pidDeadline) {
        if ($runner.HasExited) {
            $failure = Get-Content $stderr -Raw -ErrorAction SilentlyContinue
            throw "smoke process runner exited before reporting child PID: $failure"
        }
        $output = Get-Content $stdout -Raw -ErrorAction SilentlyContinue
        if ($output -match '(?m)^child-pid=(\d+)\s*$') {
            $childPid = [int]$Matches[1]
            break
        }
        Start-Sleep -Milliseconds 100
    }
    if (-not $childPid) { throw 'smoke process runner did not report child PID within 10s' }

    Start-Sleep -Seconds 3
    if ($runner.HasExited) {
        $failure = Get-Content $stderr -Raw -ErrorAction SilentlyContinue
        throw "miniapp-bridge exited before smoke checks: $failure"
    }
    $ownedPorts = Get-NetTCPConnection -State Listen -OwningProcess $childPid -ErrorAction SilentlyContinue | Select-Object -ExpandProperty LocalPort
    if (9421 -notin $ownedPorts -or 62000 -notin $ownedPorts) { throw "listeners are not owned by pid ${childPid}: $($ownedPorts -join ',')" }
    $output = Get-Content $stdout -Raw -ErrorAction SilentlyContinue
    if ($output -notmatch '\[frida\] attached pid=') { throw "Frida attach was not confirmed: $output" }
    $initialTargetSnapshot = @(Get-TargetProcessSnapshot)
    if ($initialTargetSnapshot.Count -eq 0) { throw 'target-process: not-present' }
    Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $initialTargetSnapshot
    $initialTargetPids = @($initialTargetSnapshot | Select-Object -ExpandProperty Id)
    $initialTargetIdentities = @($initialTargetSnapshot | Select-Object -ExpandProperty Identity)
    Write-Output "listeners: 9421=true 62000=true owner=$childPid"
    Write-Output (($output -split "`r?`n") | Where-Object { $_ -match '\[frida\] attached pid=' } | Select-Object -First 1)
    Write-Output "target-process: found count=$($initialTargetSnapshot.Count)"
    foreach ($item in $initialTargetSnapshot) { Write-TargetProcessDetail -Phase initial -Process $item }
    Write-Output 'action-required=open-or-reload-miniapp'

    $deadline = [DateTime]::UtcNow.AddSeconds($UpstreamWaitSeconds)
    $upstream = $null
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($runner.HasExited) {
            $failure = Get-Content $stderr -Raw -ErrorAction SilentlyContinue
            throw "miniapp-bridge exited while waiting for WMPF upstream: $failure"
        }
        $upstream = Get-NetTCPConnection -State Established -OwningProcess $childPid -ErrorAction SilentlyContinue | Where-Object LocalPort -eq 9421
        if ($upstream) { break }
        $currentTargetSnapshot = @(Get-TargetProcessSnapshot)
        Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $currentTargetSnapshot
        Start-Sleep -Milliseconds 500
    }
    if (-not $upstream) {
        $output = Get-Content $stdout -Raw -ErrorAction SilentlyContinue
        $currentTargetSnapshot = @(Get-TargetProcessSnapshot)
        Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $currentTargetSnapshot
        $currentIdentities = @($currentTargetSnapshot | Select-Object -ExpandProperty Identity)
        $newPids = @($observedTargetSnapshot.Values | Where-Object { $_.Identity -notin $initialTargetIdentities } | Select-Object -ExpandProperty Id -Unique)
        $exitedPids = @($observedTargetSnapshot.Values | Where-Object { $_.Identity -notin $currentIdentities } | Select-Object -ExpandProperty Id -Unique)
        throw "WMPF upstream connection on 9421 was not established by pid ${childPid} within ${UpstreamWaitSeconds}s; observed-new-target-pids=$($newPids -join ','); exited-target-pids=$($exitedPids -join ','); agent-log=$output"
    }
    $upstreamPeerConnections = foreach ($serverConnection in @($upstream)) {
        Get-NetTCPConnection -State Established -ErrorAction SilentlyContinue |
            Where-Object {
                $_.OwningProcess -gt 0 -and
                $_.OwningProcess -ne $childPid -and
                $_.LocalPort -eq $serverConnection.RemotePort -and
                $_.RemotePort -eq $serverConnection.LocalPort
            }
    }
    $upstreamPeerPids = @($upstreamPeerConnections | Select-Object -ExpandProperty OwningProcess -Unique | Where-Object { $_ -gt 0 })
    if ($upstreamPeerPids.Count -eq 0) { throw 'WMPF upstream peer process could not be identified' }

    $connectedTargetSnapshot = @()
    $upstreamPeerTargets = @()
    for ($attempt = 0; $attempt -lt 10; $attempt++) {
        $connectedTargetSnapshot = @(Get-TargetProcessSnapshot)
        Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $connectedTargetSnapshot
        $upstreamPeerTargets = @($connectedTargetSnapshot | Where-Object { $_.Id -in $upstreamPeerPids })
        if ($upstreamPeerTargets.Count -eq $upstreamPeerPids.Count) { break }
        Start-Sleep -Milliseconds 200
    }
    if ($upstreamPeerTargets.Count -ne $upstreamPeerPids.Count) {
        throw "WMPF upstream peer is not an identifiable WeChatAppEx process: peer=$($upstreamPeerPids -join ',')"
    }
    foreach ($item in $connectedTargetSnapshot) {
        $role = if ($item.Id -in $upstreamPeerPids) { 'upstream-peer' } else { '' }
        Write-TargetProcessDetail -Phase connected -Process $item -Roles $role
    }

    go run scripts/smoke-client.go --url ws://127.0.0.1:62000 --mode matrix
    if ($LASTEXITCODE -ne 0) { throw "live CDP matrix failed with exit $LASTEXITCODE" }
    go run scripts/smoke-client.go --url ws://127.0.0.1:62000 --mode link
    if ($LASTEXITCODE -ne 0) { throw "CDP link smoke failed with exit $LASTEXITCODE" }

    $preShutdownTargetSnapshot = @(Get-TargetProcessSnapshot)
    Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $preShutdownTargetSnapshot
    $newWindowTargets = @($preShutdownTargetSnapshot | Where-Object {
        $_.Identity -notin $initialTargetIdentities -and $_.MainWindowHandle -ne 0
    })
    $newRendererTargets = @($preShutdownTargetSnapshot | Where-Object {
        $_.Identity -notin $initialTargetIdentities -and (Test-IsRendererProcess -Process $_)
    })
    $newAppletRendererTargets = @($newRendererTargets | Where-Object { Test-IsAppletRendererProcess -Process $_ })
    if ($newRendererTargets.Count -eq 0) {
        throw 'no new renderer remained after the CDP checks; opening or reloading the target did not produce a trackable renderer'
    }
    $fallbackAppletRenderer = $false
    if ($newAppletRendererTargets.Count -eq 0) {
        $newAppletRendererTargets = @($newRendererTargets | Where-Object { Test-IsAnyAppletRendererProcess -Process $_ })
        if ($newAppletRendererTargets.Count -gt 0) {
            $fallbackAppletRenderer = $true
            $fallbackPids = @($newAppletRendererTargets | Select-Object -ExpandProperty Id -Unique)
            Write-Output "fallback-applet-renderer=true diagnostic=non-preload applet renderer absent; tracking new type=4 renderer with preload-or-other appid pids=$($fallbackPids -join ',')"
        }
    }
    if ($newAppletRendererTargets.Count -eq 0) {
        throw 'no new type=4 applet renderer remained after the CDP checks'
    }
    foreach ($item in @($upstreamPeerTargets)) {
        $trackedTargetRoles[$item.Identity] = 'upstream-peer'
        $trackedTargetMetadata[$item.Identity] = $item
    }
    foreach ($item in $newRendererTargets) {
        $role = if (Test-IsAnyAppletRendererProcess -Process $item) { 'renderer,applet-renderer' } else { 'renderer' }
        if ($fallbackAppletRenderer -and (Test-IsAnyAppletRendererProcess -Process $item) -and -not (Test-IsAppletRendererProcess -Process $item)) {
            $role += ',fallback-applet-renderer'
        }
        if ($trackedTargetRoles.ContainsKey($item.Identity)) {
            $trackedTargetRoles[$item.Identity] += ",$role"
        } else {
            $trackedTargetRoles[$item.Identity] = $role
        }
        $trackedTargetMetadata[$item.Identity] = $item
    }
    foreach ($item in $newWindowTargets) {
        if ($trackedTargetRoles.ContainsKey($item.Identity)) {
            $trackedTargetRoles[$item.Identity] += ',window-owner'
        } else {
            $trackedTargetRoles[$item.Identity] = 'window-owner'
        }
        $trackedTargetMetadata[$item.Identity] = $item
    }
    foreach ($item in $preShutdownTargetSnapshot) {
        $role = if ($trackedTargetRoles.ContainsKey($item.Identity)) { $trackedTargetRoles[$item.Identity] } else { '' }
        Write-TargetProcessDetail -Phase pre-shutdown -Process $item -Roles $role
    }
    if ($trackedTargetRoles.Count -eq 0) { throw 'no connection or window-owned target process was available for shutdown survival verification' }

    if ($KeepBridgeRunning) {
        Write-Output "bridge-kept-running=true pid=$childPid"
        Write-Output "bridge-stop-file=$stopFile"
        Write-Output "bridge-stop-command=New-Item -ItemType File -LiteralPath '$stopFile' -Force"
        Write-Output "devtools-url=devtools://devtools/bundled/inspector.html?ws=127.0.0.1:62000"
        return
    }
} finally {
    if ($KeepBridgeRunning) {
        # The child is intentionally left alive for interactive inspection.
        # Do not delete its executable or stop file; the printed command above
        # remains valid until the caller requests graceful shutdown.
        Write-Output "bridge-session-preserved=true pid=$childPid stop-file=$stopFile"
    } else {
    $shutdownFailure = $null
    $forcedFallback = $false
    if ($runner -and -not $runner.HasExited) {
        New-Item -ItemType File -Path $stopFile -Force | Out-Null
        if (-not $runner.WaitForExit(($ShutdownTimeoutSeconds + 5) * 1000)) {
            $forcedFallback = $true
            if ($childPid) { Stop-Process -Id $childPid -Force -ErrorAction SilentlyContinue }
            Stop-Process -Id $runner.Id -Force -ErrorAction SilentlyContinue
            $shutdownFailure = "graceful shutdown exceeded ${ShutdownTimeoutSeconds}s; force termination fallback was used"
        }
    }
    if ($runner) {
        Wait-Process -Id $runner.Id -ErrorAction SilentlyContinue
        $runner.Refresh()
    }

    $finalOutput = Get-Content $stdout -Raw -ErrorAction SilentlyContinue
    $finalError = Get-Content $stderr -Raw -ErrorAction SilentlyContinue
    $runnerExitCode = $null
    if ($runner -and $runner.HasExited) {
        $runner.Refresh()
        $runnerExitCode = $runner.ExitCode
    }
    if (-not $forcedFallback -and $null -ne $runnerExitCode -and $runnerExitCode -ne 0) {
        $shutdownFailure = "smoke process runner exited with $runnerExitCode`: $finalError"
    }
    if (-not $forcedFallback -and $finalOutput -notmatch '(?m)^stop-requested=true\s*$') {
        $shutdownFailure = 'smoke process runner did not confirm the graceful stop request'
    }
    if (-not $forcedFallback -and $finalOutput -notmatch '(?m)^child-exit-code=0\s*$') {
        $shutdownFailure = "miniapp-bridge did not report a zero graceful exit: $finalOutput"
    }

    $portsDeadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        $listeners = Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
            Where-Object { $_.LocalPort -eq 9421 -or $_.LocalPort -eq 62000 }
        if (-not $listeners) { break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $portsDeadline)
    if ($listeners) { $shutdownFailure = "ports were not released: $($listeners.LocalPort -join ',')" }

    if ($trackedTargetRoles.Count -gt 0) {
        # Catch delayed teardown caused by closing the bridge, not just processes alive at WaitForExit.
        Start-Sleep -Seconds 5
        $finalTargetSnapshot = @(Get-TargetProcessSnapshot)
        Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $finalTargetSnapshot
        $finalTargetsByIdentity = @{}
        foreach ($item in $finalTargetSnapshot) { $finalTargetsByIdentity[$item.Identity] = $item }
        $missingTrackedTargets = @($trackedTargetRoles.Keys | Where-Object { -not $finalTargetsByIdentity.ContainsKey($_) })
        $unresponsiveWindowTargets = @($trackedTargetRoles.Keys | Where-Object {
            $trackedTargetRoles[$_] -match 'window-owner' -and
            $finalTargetsByIdentity.ContainsKey($_) -and
            -not $finalTargetsByIdentity[$_].Responding
        })
        foreach ($identity in $trackedTargetRoles.Keys) {
            $item = if ($finalTargetsByIdentity.ContainsKey($identity)) { $finalTargetsByIdentity[$identity] } else { $trackedTargetMetadata[$identity] }
            Write-TargetProcessDetail -Phase post-shutdown -Process $item -Roles $trackedTargetRoles[$identity]
        }
        if ($missingTrackedTargets.Count -gt 0) {
            $missing = @($missingTrackedTargets | ForEach-Object { "$($trackedTargetMetadata[$_].Id):$($trackedTargetRoles[$_])" })
            $shutdownFailure = "tracked target process exited during bridge shutdown: $($missing -join ',')"
        } elseif ($unresponsiveWindowTargets.Count -gt 0) {
            $unresponsive = @($unresponsiveWindowTargets | ForEach-Object { "$($trackedTargetMetadata[$_].Id):$($trackedTargetRoles[$_])" })
            $shutdownFailure = "tracked target window stopped responding after bridge shutdown: $($unresponsive -join ',')"
        } else {
            $surviving = @($trackedTargetRoles.Keys | ForEach-Object { "$($finalTargetsByIdentity[$_].Id):$($trackedTargetRoles[$_])" })
            Write-Output "target-survives-shutdown=true tracked=$($surviving -join ',') settle-seconds=5"
        }
    }
    if ($initialTargetSnapshot.Count -gt 0) {
        if (-not $finalTargetSnapshot) {
            $finalTargetSnapshot = @(Get-TargetProcessSnapshot)
            Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $finalTargetSnapshot
        }
        $finalTargetIdentities = @($finalTargetSnapshot | Select-Object -ExpandProperty Identity)
        $newTargetProcesses = @($observedTargetSnapshot.Values | Where-Object { $_.Identity -notin $initialTargetIdentities })
        $survivingNewTargetPids = @($newTargetProcesses | Where-Object { $_.Identity -in $finalTargetIdentities } | Select-Object -ExpandProperty Id -Unique)
        $transientExitedTargetPids = @($newTargetProcesses | Where-Object {
            $_.Identity -notin $finalTargetIdentities -and -not $trackedTargetRoles.ContainsKey($_.Identity)
        } | Select-Object -ExpandProperty Id -Unique)
        $newTargetPids = @($newTargetProcesses | Select-Object -ExpandProperty Id -Unique)
        Write-Output "target-process-delta: new=$($newTargetPids -join ',') surviving-new=$($survivingNewTargetPids -join ',') transient-exited=$($transientExitedTargetPids -join ',')"
    }
    if (-not $listeners) { Write-Output 'ports-released=true' }

    Remove-Item -LiteralPath $stopFile -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $runnerExe -Force -ErrorAction SilentlyContinue
    if ($shutdownFailure) { throw $shutdownFailure }
    }
}
