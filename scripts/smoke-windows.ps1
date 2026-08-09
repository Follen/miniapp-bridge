param(
    [ValidateRange(1, 300)]
    [int]$UpstreamWaitSeconds = 60,

    [ValidateRange(1, 60)]
    [int]$ShutdownTimeoutSeconds = 20,

    # Formal acceptance runs every CDP layer. Other values are intended only
    # for diagnosis and are never accepted as a full smoke result.
    [ValidateSet('all', 'link', 'matrix', 'interaction')]
    [string]$CDPMode = 'all',

    # Keep the bridge and target WMPF session alive after protocol checks. This
    # is useful for interactive DevTools use; the default still exercises the
    # complete graceful-shutdown contract.
    [switch]$KeepBridgeRunning
)

$ErrorActionPreference = 'Stop'

function Normalize-TcpAddress {
    param([object]$Address)
    $value = [string]$Address
    if ([string]::IsNullOrWhiteSpace($value)) { return '' }
    try {
        $parsed = [System.Net.IPAddress]::Parse($value)
        if ($parsed.AddressFamily -eq [System.Net.Sockets.AddressFamily]::InterNetworkV6 -and $parsed.IsIPv4MappedToIPv6) {
            return $parsed.MapToIPv4().ToString()
        }
        return $parsed.ToString()
    } catch {
        return $value.ToLowerInvariant()
    }
}

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
$smokeChecksPassed = $false
$smokeFailure = $null
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
    $attachDeadline = [DateTime]::UtcNow.AddSeconds(30)
    $output = ''
    while ([DateTime]::UtcNow -lt $attachDeadline) {
        if ($runner.HasExited) {
            $failure = Get-Content $stderr -Raw -ErrorAction SilentlyContinue
            throw "miniapp-bridge exited before Frida attach: $failure"
        }
        $output = Get-Content $stdout -Raw -ErrorAction SilentlyContinue
        if ($output -match '(?m)\[frida\] attached pid=(\d+)') {
            $attachedTargetPid = [int]$Matches[1]
            break
        }
        Start-Sleep -Milliseconds 250
    }
    if (-not $attachedTargetPid) { throw "Frida attach was not confirmed within 30s: $output" }
    $attachLogOffset = $output.Length
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
    $upstream = @()
    $upstreamPeerConnections = @()
    $lastPeerPortCandidates = @()
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($runner.HasExited) {
            $failure = Get-Content $stderr -Raw -ErrorAction SilentlyContinue
            throw "miniapp-bridge exited while waiting for WMPF upstream: $failure"
        }
        $tcpConnections = @(Get-NetTCPConnection -State Established -ErrorAction SilentlyContinue)
        $upstream = @($tcpConnections | Where-Object { $_.OwningProcess -eq $childPid -and $_.LocalPort -eq 9421 })
        $lastPeerPortCandidates = @($tcpConnections | Where-Object {
            $candidate = $_
            @($upstream | Where-Object {
                $candidate.LocalPort -eq $_.RemotePort -and
                $candidate.RemotePort -eq $_.LocalPort
            }).Count -gt 0
        })
        $upstreamPeerConnections = @(foreach ($serverConnection in $upstream) {
            $tcpConnections | Where-Object {
                $_.OwningProcess -gt 0 -and
                $_.OwningProcess -ne $childPid -and
                (Normalize-TcpAddress $_.LocalAddress) -eq (Normalize-TcpAddress $serverConnection.RemoteAddress) -and
                $_.LocalPort -eq $serverConnection.RemotePort -and
                (Normalize-TcpAddress $_.RemoteAddress) -eq (Normalize-TcpAddress $serverConnection.LocalAddress) -and
                $_.RemotePort -eq $serverConnection.LocalPort
            }
        })
        if ($upstreamPeerConnections.Count -gt 0) { break }
        $currentTargetSnapshot = @(Get-TargetProcessSnapshot)
        Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $currentTargetSnapshot
        Start-Sleep -Milliseconds 100
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
    $upstreamPeerPids = @($upstreamPeerConnections | Select-Object -ExpandProperty OwningProcess -Unique | Where-Object { $_ -gt 0 })
    if ($upstreamPeerPids.Count -eq 0) {
        $serverDiagnostic = @($upstream | ForEach-Object { "$($_.LocalAddress):$($_.LocalPort)<-$($_.RemoteAddress):$($_.RemotePort) pid=$($_.OwningProcess)" }) -join ';'
        $candidateDiagnostic = @($lastPeerPortCandidates | ForEach-Object { "$($_.LocalAddress):$($_.LocalPort)->$($_.RemoteAddress):$($_.RemotePort) pid=$($_.OwningProcess)" }) -join ';'
        throw "WMPF upstream peer process could not be identified within ${UpstreamWaitSeconds}s; server=$serverDiagnostic; reverse-port-candidates=$candidateDiagnostic"
    }

    # Revalidate the exact connection and process identity after the initial
    # selection. TCP rows and process IDs can change between snapshots, so a
    # port-only match is insufficient for attaching shutdown-survival roles.
    $selectedServerConnection = $null
    $selectedPeerConnection = $null
    foreach ($serverConnection in @($upstream)) {
        $candidatePeer = @($upstreamPeerConnections | Where-Object {
            $_.OwningProcess -gt 0 -and
            (Normalize-TcpAddress $_.LocalAddress) -eq (Normalize-TcpAddress $serverConnection.RemoteAddress) -and
            $_.LocalPort -eq $serverConnection.RemotePort -and
            (Normalize-TcpAddress $_.RemoteAddress) -eq (Normalize-TcpAddress $serverConnection.LocalAddress) -and
            $_.RemotePort -eq $serverConnection.LocalPort
        } | Select-Object -First 1)
        if ($candidatePeer.Count -eq 1) {
            $selectedServerConnection = $serverConnection
            $selectedPeerConnection = $candidatePeer[0]
            break
        }
    }
    if (-not $selectedServerConnection -or -not $selectedPeerConnection) {
        throw 'WMPF upstream peer did not form a complete server/peer tuple'
    }
    $connectedTargetSnapshot = @(Get-TargetProcessSnapshot)
    Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $connectedTargetSnapshot
    $upstreamPeerTargets = @($connectedTargetSnapshot | Where-Object { $_.Id -eq $selectedPeerConnection.OwningProcess })
    if ($upstreamPeerTargets.Count -ne 1) {
        throw "WMPF upstream peer is not an identifiable WeChatAppEx process: peer=$($upstreamPeerPids -join ',')"
    }
    $peerIdentity = $upstreamPeerTargets[0].Identity
    $peerValidationDiagnostics = @()
    $peerValidated = $false
    for ($attempt = 0; $attempt -lt 10; $attempt++) {
        $tcpValidationConnections = @(Get-NetTCPConnection -State Established -ErrorAction SilentlyContinue)
        $connectedTargetSnapshot = @(Get-TargetProcessSnapshot)
        Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $connectedTargetSnapshot
        $currentServers = @($tcpValidationConnections | Where-Object {
            $_.OwningProcess -eq $childPid -and $_.LocalPort -eq 9421
        })
        $currentPeers = @(foreach ($serverConnection in $currentServers) {
            $tcpValidationConnections | Where-Object {
                $_.OwningProcess -gt 0 -and
                $_.OwningProcess -ne $childPid -and
                (Normalize-TcpAddress $_.LocalAddress) -eq (Normalize-TcpAddress $serverConnection.RemoteAddress) -and
                $_.LocalPort -eq $serverConnection.RemotePort -and
                (Normalize-TcpAddress $_.RemoteAddress) -eq (Normalize-TcpAddress $serverConnection.LocalAddress) -and
                $_.RemotePort -eq $serverConnection.LocalPort
            }
        })
        $serverMatch = @($currentServers | Where-Object {
            (Normalize-TcpAddress $_.LocalAddress) -eq (Normalize-TcpAddress $selectedServerConnection.LocalAddress) -and
            $_.LocalPort -eq $selectedServerConnection.LocalPort -and
            (Normalize-TcpAddress $_.RemoteAddress) -eq (Normalize-TcpAddress $selectedServerConnection.RemoteAddress) -and
            $_.RemotePort -eq $selectedServerConnection.RemotePort -and
            $_.OwningProcess -eq $childPid
        } | Select-Object -First 1)
        $peerMatch = @($currentPeers | Where-Object {
            (Normalize-TcpAddress $_.LocalAddress) -eq (Normalize-TcpAddress $selectedPeerConnection.LocalAddress) -and
            $_.LocalPort -eq $selectedPeerConnection.LocalPort -and
            (Normalize-TcpAddress $_.RemoteAddress) -eq (Normalize-TcpAddress $selectedPeerConnection.RemoteAddress) -and
            $_.RemotePort -eq $selectedPeerConnection.RemotePort -and
            $_.OwningProcess -eq $selectedPeerConnection.OwningProcess
        } | Select-Object -First 1)
        $peerTupleMatch = @($currentPeers | Where-Object {
            (Normalize-TcpAddress $_.LocalAddress) -eq (Normalize-TcpAddress $selectedPeerConnection.LocalAddress) -and
            $_.LocalPort -eq $selectedPeerConnection.LocalPort -and
            (Normalize-TcpAddress $_.RemoteAddress) -eq (Normalize-TcpAddress $selectedPeerConnection.RemoteAddress) -and
            $_.RemotePort -eq $selectedPeerConnection.RemotePort
        } | Select-Object -First 1)
        $peerProcess = @($connectedTargetSnapshot | Where-Object {
            $_.Id -eq $selectedPeerConnection.OwningProcess
        } | Select-Object -First 1)
        if ($serverMatch.Count -eq 1 -and $peerMatch.Count -eq 1 -and $peerProcess.Count -eq 1 -and
            $peerProcess[0].Identity -eq $peerIdentity) {
            $peerValidated = $true
            $upstream = @($serverMatch)
            $upstreamPeerConnections = @($peerMatch)
            $upstreamPeerPids = @($peerMatch | Select-Object -ExpandProperty OwningProcess -Unique)
            $upstreamPeerTargets = @($peerProcess)
            Write-Output "upstream-peer-validated=true attempt=$($attempt + 1) tuple=$((Normalize-TcpAddress $serverMatch[0].LocalAddress)):$($serverMatch[0].LocalPort)<->$((Normalize-TcpAddress $serverMatch[0].RemoteAddress)):$($serverMatch[0].RemotePort) pid=$($peerProcess[0].Id) identity=$($peerProcess[0].Identity)"
            break
        }

        $reason = @()
        if ($serverMatch.Count -ne 1) { $reason += 'server-four-tuple-missing' }
        if ($peerMatch.Count -ne 1) { $reason += 'peer-four-tuple-missing' }
        if ($peerTupleMatch.Count -eq 1 -and $peerTupleMatch[0].OwningProcess -ne $selectedPeerConnection.OwningProcess) { $reason += 'peer-owning-pid-changed' }
        if ($peerProcess.Count -ne 1) { $reason += 'peer-process-missing' }
        elseif ($peerIdentity -and $peerProcess[0].Identity -ne $peerIdentity) { $reason += 'peer-pid-start-time-changed' }
        $peerValidationDiagnostics += "attempt=$($attempt + 1):$($reason -join ',')"

        # If the selected tuple disappeared, select a fresh complete tuple
        # from this snapshot and bind its PID/start-time before retrying.
        if ($serverMatch.Count -ne 1 -or $peerMatch.Count -ne 1) {
            $freshServer = @($currentServers | Select-Object -First 1)
            $freshPeer = @()
            if ($freshServer.Count -eq 1) {
                $freshPeer = @($tcpValidationConnections | Where-Object {
                    $_.OwningProcess -gt 0 -and
                    $_.OwningProcess -ne $childPid -and
                    (Normalize-TcpAddress $_.LocalAddress) -eq (Normalize-TcpAddress $freshServer[0].RemoteAddress) -and
                    $_.LocalPort -eq $freshServer[0].RemotePort -and
                    (Normalize-TcpAddress $_.RemoteAddress) -eq (Normalize-TcpAddress $freshServer[0].LocalAddress) -and
                    $_.RemotePort -eq $freshServer[0].LocalPort
                } | Select-Object -First 1)
            }
            $freshProcess = @($connectedTargetSnapshot | Where-Object {
                $freshPeer.Count -eq 1 -and $_.Id -eq $freshPeer[0].OwningProcess
            } | Select-Object -First 1)
            if ($freshServer.Count -eq 1 -and $freshPeer.Count -eq 1 -and $freshProcess.Count -eq 1) {
                $selectedServerConnection = $freshServer[0]
                $selectedPeerConnection = $freshPeer[0]
                $peerIdentity = $freshProcess[0].Identity
                $upstreamPeerPids = @($selectedPeerConnection.OwningProcess)
                $peerValidationDiagnostics += "attempt=$($attempt + 1):reselected-peer=$($freshProcess[0].Identity)"
            }
        }
        Start-Sleep -Milliseconds 200
    }
    if (-not $peerValidated) {
        throw "WMPF upstream peer failed TOCTOU validation after 10 attempts: $($peerValidationDiagnostics -join ';')"
    }
    foreach ($item in $connectedTargetSnapshot) {
        $role = if ($item.Id -in $upstreamPeerPids) { 'upstream-peer' } else { '' }
        Write-TargetProcessDetail -Phase connected -Process $item -Roles $role
    }

    $agentOutput = Get-Content $stdout -Raw -ErrorAction SilentlyContinue
    $postAttachOutput = if ($agentOutput.Length -gt $attachLogOffset) { $agentOutput.Substring($attachLogOffset) } else { '' }
    $onLoadStartHit = $postAttachOutput -match 'AppletIndexContainer::OnLoadStart onEnter'
    if (-not $onLoadStartHit) {
        throw 'WMPF upstream connected without a post-attach AppletIndexContainer::OnLoadStart onEnter event'
    }
    Write-Output 'agent-on-load-start=true'

    $runLink = $CDPMode -eq 'all' -or $CDPMode -eq 'link'
    $runMatrix = $CDPMode -eq 'all' -or $CDPMode -eq 'matrix'
    $runInteraction = $CDPMode -eq 'all' -or $CDPMode -eq 'interaction'
    if ($runMatrix) {
        go run scripts/smoke-client.go --url ws://127.0.0.1:62000 --mode matrix
        if ($LASTEXITCODE -ne 0) { throw "live CDP matrix failed with exit $LASTEXITCODE" }
        Write-Output 'cdp-step=matrix passed=true domains=Runtime,Debugger,Page,DOM,Network,Console,Performance'
    }
    if ($runLink) {
        go run scripts/smoke-client.go --url ws://127.0.0.1:62000 --mode link
        if ($LASTEXITCODE -ne 0) { throw "CDP link smoke failed with exit $LASTEXITCODE" }
        Write-Output 'cdp-step=link passed=true'
    }
    if ($runInteraction) {
        go run scripts/smoke-client.go --url ws://127.0.0.1:62000 --mode interaction
        if ($LASTEXITCODE -ne 0) { throw "CDP interaction smoke failed with exit $LASTEXITCODE" }
        Write-Output 'cdp-step=interaction passed=true input=mouse,keyboard'
    }
    if ($CDPMode -ne 'all') {
        Write-Output "cdp-coverage=partial mode=$CDPMode acceptance=false"
    } else {
        Write-Output 'cdp-coverage=full mode=all acceptance=true'
    }

    $preShutdownTargetSnapshot = @(Get-TargetProcessSnapshot)
    Add-ObservedTargetSnapshot -Observed $observedTargetSnapshot -Snapshot $preShutdownTargetSnapshot
    $newWindowTargets = @($preShutdownTargetSnapshot | Where-Object {
        $_.Identity -notin $initialTargetIdentities -and $_.MainWindowHandle -ne 0
    })
    $newRendererTargets = @($preShutdownTargetSnapshot | Where-Object {
        $_.ParentId -eq $attachedTargetPid -and
        $_.Identity -notin $initialTargetIdentities -and
        (Test-IsRendererProcess -Process $_)
    } | Sort-Object StartTimeUtcTicks -Descending)
    $newAppletRendererTargets = @($newRendererTargets |
        Where-Object {
            $_.ParentId -eq $attachedTargetPid -and
            $_.Identity -notin $initialTargetIdentities -and
            (Test-IsAppletRendererProcess -Process $_)
        } |
        Sort-Object StartTimeUtcTicks -Descending |
        Select-Object -First 1)
    $fallbackAppletRenderer = $false
    $reusedAppletRenderer = $false
    if ($newAppletRendererTargets.Count -lt 1) {
        $newAppletRendererTargets = @($newRendererTargets |
            Where-Object {
                $_.ParentId -eq $attachedTargetPid -and
                $_.Identity -notin $initialTargetIdentities -and
                (Test-IsAnyAppletRendererProcess -Process $_)
            } |
            Sort-Object StartTimeUtcTicks -Descending |
            Select-Object -First 1)
        if ($newAppletRendererTargets.Count -gt 0) {
            $fallbackAppletRenderer = $true
            $fallbackPids = @($newAppletRendererTargets | Select-Object -ExpandProperty Id -Unique)
            Write-Output "fallback-applet-renderer=true diagnostic=non-preload applet renderer absent; tracking new type=4 renderer with preload-or-other appid pids=$($fallbackPids -join ',')"
        }
    }
    if ($newAppletRendererTargets.Count -gt 0) {
        Write-Output "renderer-selection=new pids=$(@($newAppletRendererTargets | Select-Object -ExpandProperty Id) -join ',')"
    } else {
        $reusedAppletRendererTargets = @($preShutdownTargetSnapshot |
            Where-Object { $_.ParentId -eq $attachedTargetPid -and $_.Identity -in $initialTargetIdentities -and (Test-IsAppletRendererProcess -Process $_) } |
            Sort-Object StartTimeUtcTicks -Descending)
        if ($reusedAppletRendererTargets.Count -lt 1) {
            $reusedAppletRendererTargets = @($preShutdownTargetSnapshot |
                Where-Object { $_.ParentId -eq $attachedTargetPid -and $_.Identity -in $initialTargetIdentities -and (Test-IsAnyAppletRendererProcess -Process $_) } |
                Sort-Object StartTimeUtcTicks -Descending)
            if ($reusedAppletRendererTargets.Count -gt 0) {
                $fallbackAppletRenderer = $true
            }
        }
        if ($reusedAppletRendererTargets.Count -lt 1) {
            throw 'no type=4 applet renderer correlated with the post-attach load'
        }

        $newAppletRendererTargets = @($reusedAppletRendererTargets | Select-Object -First 1)
        $reusedAppletRenderer = $true
        $reusedIdentity = $newAppletRendererTargets[0].Identity
        Write-Output "reused-applet-renderer=true pid=$($newAppletRendererTargets[0].Id) identity=$reusedIdentity evidence=onload-start,upstream,cdp-matrix,cdp-link"
        Write-Output "renderer-selection=reused pid=$($newAppletRendererTargets[0].Id) identity=$reusedIdentity"
    }
    $selectedAppletRendererTargets = @($newAppletRendererTargets)
    $selectedAppletRendererIdentities = @($newAppletRendererTargets | Select-Object -ExpandProperty Identity)
    $attachedHostTargets = @($initialTargetSnapshot | Where-Object { $_.Id -eq $attachedTargetPid })
    if ($attachedHostTargets.Count -ne 1) {
        throw "attached Frida host PID $attachedTargetPid was not present with an exact initial PID@StartTime identity"
    }
    $attachedHostTarget = $attachedHostTargets[0]
    $trackedTargetRoles[$attachedHostTarget.Identity] = 'attached-host'
    $trackedTargetMetadata[$attachedHostTarget.Identity] = $attachedHostTarget
    foreach ($item in @($upstreamPeerTargets)) {
        $trackedTargetRoles[$item.Identity] = 'upstream-peer'
        $trackedTargetMetadata[$item.Identity] = $item
    }
    foreach ($item in @($selectedAppletRendererTargets)) {
        $role = if (Test-IsAnyAppletRendererProcess -Process $item) { 'renderer,applet-renderer' } else { 'renderer' }
        if ($fallbackAppletRenderer -and $item.Identity -in $selectedAppletRendererIdentities -and -not (Test-IsAppletRendererProcess -Process $item)) {
            $role += ',fallback-applet-renderer'
        }
        if ($reusedAppletRenderer -and $item.Identity -in $selectedAppletRendererIdentities) {
            $role += ',reused-applet-renderer'
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

    $smokeChecksPassed = $true
    if ($KeepBridgeRunning) {
        Write-Output "bridge-kept-running=true pid=$childPid"
        Write-Output "bridge-stop-file=$stopFile"
        Write-Output "bridge-stop-command=New-Item -ItemType File -LiteralPath '$stopFile' -Force"
        Write-Output "devtools-url=devtools://devtools/bundled/inspector.html?ws=127.0.0.1:62000"
        return
    }
} catch {
    $smokeFailure = $_
    throw
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
    $runnerExited = $false
    if ($runner) {
        $liveRunner = Get-Process -Id $runner.Id -ErrorAction SilentlyContinue
        if (-not $liveRunner) {
            # CTRL_BREAK can leave the Start-Process handle stale even after
            # the runner has exited. A missing PID is an independent exit fact.
            $runnerExited = $true
        } elseif ($runner.HasExited) {
            $runner.Refresh()
            $runnerExitCode = $runner.ExitCode
            $runnerExited = $true
        }
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
    $listeners = @()
    do {
        $listeners = @(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
            Where-Object { $_.LocalPort -eq 9421 -or $_.LocalPort -eq 62000 }
        )
        if (-not $listeners) { break }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $portsDeadline)
    if ($listeners) { $shutdownFailure = "ports were not released: $($listeners.LocalPort -join ',')" }

    # A vanished LISTEN row alone is insufficient: prove both fixed endpoints
    # can be rebound, in order, and release each temporary listener immediately.
    $rebindFailures = @()
    foreach ($port in @(9421, 62000)) {
        $probe = $null
        try {
            $ip = [System.Net.IPAddress]::Parse('127.0.0.1')
            $probe = [System.Net.Sockets.TcpListener]::new($ip, [int]$port)
            $probe.Start()
            Write-Output "port-rebind: port=$port success=true address=127.0.0.1"
        } catch {
            $rebindFailures += "${port}:$($_.Exception.Message)"
            Write-Output "port-rebind: port=$port success=false error=$($_.Exception.Message)"
        } finally {
            if ($probe) { $probe.Stop() }
        }
    }
    if ($rebindFailures.Count -gt 0) {
        $shutdownFailure = "ports could not be rebound: $($rebindFailures -join ';')"
    }

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
    if (-not $listeners -and $rebindFailures.Count -eq 0) { Write-Output 'ports-released=true' }

    # The current production logger does not emit per-native-resource close
    # lines. A zero graceful child exit is therefore the observable teardown
    # boundary: SDK.Close runs script unload -> session detach -> device close
    # -> native runtime release before the process returns. Keep this explicit
    # so a future logger can replace the proxy with four concrete markers.
    $teardownNames = @('agent-unload', 'session-detach', 'device-close', 'native-runtime-release')
    $explicitTeardown = @($teardownNames | Where-Object {
        $finalOutput -match "(?m)^teardown-$($_)=true\s*$" -or
        $finalError -match "(?m)^teardown-$($_)=true\s*$"
    })
    $stopMarker = $finalOutput -match '(?m)^stop-requested=true\s*$'
    $childExitMarker = $finalOutput -match '(?m)^child-exit-code=0\s*$'
    Write-Output "teardown-evidence: forced-fallback=$forcedFallback runner-exited=$runnerExited runner-exit-code=$runnerExitCode stop-marker=$stopMarker child-exit-marker=$childExitMarker"
    if ($explicitTeardown.Count -eq $teardownNames.Count) {
        Write-Output 'teardown-markers=agent-unload,session-detach,device-close,native-runtime-release source=bridge-log'
    } elseif (-not $forcedFallback -and $runnerExited -and ($runnerExitCode -eq 0 -or $null -eq $runnerExitCode) -and $stopMarker -and $childExitMarker) {
        Write-Output 'teardown-markers=agent-unload,session-detach,device-close,native-runtime-release source=runner-child-exit-proxy dependency=sdk-native-close-order'
    } else {
        $shutdownFailure = 'native teardown markers were not observed and graceful runner exit was not proven'
    }

    Remove-Item -LiteralPath $stopFile -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $runnerExe -Force -ErrorAction SilentlyContinue
    if ($shutdownFailure -and $null -eq $smokeFailure) { throw $shutdownFailure }
    if ($null -eq $smokeFailure -and -not $smokeChecksPassed) { throw 'smoke checks did not complete' }
    if ($null -eq $smokeFailure -and $smokeChecksPassed) { Write-Output 'smoke-success=true' }
    }
}
