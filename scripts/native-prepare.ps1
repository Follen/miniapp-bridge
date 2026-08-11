param(
    [switch]$Offline,
    [ValidateRange(1, 600)][int]$LockTimeoutSeconds = 120,
    [ValidateRange(1, 10)][int]$DownloadAttempts = 3,
    [ValidateRange(1, 900)][int]$DownloadTimeoutSeconds = 300,
    [ValidateRange(0, 60)][int]$DownloadRetrySeconds = 5,
    [string]$Version = '17.3.2-abi1',
    [string]$CacheDirectory = '',
    [string]$DestinationDirectory = '',
    [string]$SourceURL = '',
    [string]$ExpectedArchiveSHA256 = '',
    [ValidateRange(2, 20)][int]$RetentionCount = 2,
    [switch]$Rollback,
    [string]$CanaryCommand = ''
)

$ErrorActionPreference = 'Stop'
$expectedArchiveSHA = ([string]$ExpectedArchiveSHA256).Trim().ToUpperInvariant()
if (-not $Rollback -and -not $expectedArchiveSHA) {
    throw 'ExpectedArchiveSHA256 is required for native preparation in online and offline modes'
}
if ($expectedArchiveSHA -and $expectedArchiveSHA -notmatch '^[0-9A-F]{64}$') {
    throw 'ExpectedArchiveSHA256 must contain exactly 64 hexadecimal characters'
}

$asset = "miniapp-frida-native-$Version-windows-amd64.zip"
$cache = if ($CacheDirectory) { [IO.Path]::GetFullPath($CacheDirectory) } else { Join-Path $env:LOCALAPPDATA "miniapp-bridge\native\$Version\windows-amd64" }
$destination = if ($DestinationDirectory) { [IO.Path]::GetFullPath($DestinationDirectory) } else { (Get-Location).Path }
$stateRoot = Join-Path $destination '.native-runtime'
$versionsRoot = Join-Path $stateRoot 'versions'
$destinationLockPath = Join-Path $stateRoot 'update.lock'
$journalPath = Join-Path $stateRoot 'transaction.json'
$currentPath = Join-Path $stateRoot 'current.json'
$rollbackPath = Join-Path $stateRoot 'rollback.json'
$url = if ($SourceURL) { $SourceURL } else { "https://github.com/Follen/miniapp-bridge/releases/download/native-v$Version/$asset" }
$archive = Join-Path $cache $asset
$lockPath = "$archive.lock"
$partial = "$archive.partial"
$stage = Join-Path $cache "$asset.extracting"
$dllName = 'miniapp-frida.dll'
$installed = Join-Path $destination $dllName
$installedManifest = Join-Path $destination 'manifest.json'
$temporaryDLL = "$installed.partial"
$temporaryManifest = "$installedManifest.partial"
$publishID = [guid]::NewGuid().ToString('N')
$backupDLL = "$installed.backup-$publishID"
$backupManifest = "$installedManifest.backup-$publishID"
$lock = $null
$destinationLock = $null
$timer = [Diagnostics.Stopwatch]::StartNew()

function Get-SHA256([string]$Path) {
    $stream = [IO.File]::OpenRead($Path)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha256.ComputeHash($stream))).Replace('-', '')
    }
    finally {
        $sha256.Dispose()
        $stream.Dispose()
    }
}

function Test-IntegerValue($Value) {
    return (($Value -is [byte]) -or ($Value -is [sbyte]) -or
        ($Value -is [int16]) -or ($Value -is [uint16]) -or
        ($Value -is [int32]) -or ($Value -is [uint32]) -or
        ($Value -is [int64]) -or ($Value -is [uint64]))
}

function Invoke-VerifiedDownload {
    param(
        [string]$URL,
        [string]$Destination,
        [string]$ExpectedSHA256,
        [string]$DisplayName
    )

    $webRequest = Get-Command Invoke-WebRequest
    $lastError = $null
    for ($attempt = 1; $attempt -le $DownloadAttempts; $attempt++) {
        Remove-Item -LiteralPath $Destination -Force -ErrorAction SilentlyContinue
        $parameters = @{
            UseBasicParsing = $true
            Uri             = $URL
            OutFile         = $Destination
            ErrorAction     = 'Stop'
        }
        if ($webRequest.Parameters.ContainsKey('ConnectionTimeoutSeconds')) {
            $parameters.ConnectionTimeoutSeconds = [Math]::Min(30, $DownloadTimeoutSeconds)
            $parameters.OperationTimeoutSeconds = $DownloadTimeoutSeconds
        } elseif ($webRequest.Parameters.ContainsKey('TimeoutSec')) {
            $parameters.TimeoutSec = $DownloadTimeoutSeconds
        }

        Write-Host "Downloading $DisplayName attempt=$attempt/$DownloadAttempts"
        try {
            Invoke-WebRequest @parameters
            $downloadHash = Get-SHA256 $Destination
            if ($downloadHash -cne $ExpectedSHA256) {
                throw "native archive SHA-256 mismatch: expected $ExpectedSHA256, got $downloadHash"
            }
            return
        } catch {
            $lastError = $_
            Remove-Item -LiteralPath $Destination -Force -ErrorAction SilentlyContinue
            if ($attempt -lt $DownloadAttempts) {
                Write-Warning "native archive download attempt $attempt failed: $($_.Exception.Message)"
                if ($DownloadRetrySeconds -gt 0) {
                    Start-Sleep -Seconds $DownloadRetrySeconds
                }
            }
        }
    }
    throw "native archive download failed after $DownloadAttempts attempts: $($lastError.Exception.Message)"
}

function Invoke-TestPublishFailure([string]$Step) {
    if ([Environment]::GetEnvironmentVariable('MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_PUBLISH_FAILURE') -ceq $Step) {
        throw "injected native publish failure: $Step"
    }
}

function Write-AtomicJSON {
    param([string]$Path, [object]$Value)
    $temporary = "$Path.$([guid]::NewGuid().ToString('N')).partial"
    try {
        [IO.File]::WriteAllText($temporary, ($Value | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
        Move-Item -LiteralPath $temporary -Destination $Path -Force
    }
    finally {
        Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue
    }
}

function Read-Pointer([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $null }
    try { return Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { throw "invalid native transaction metadata: $Path`: $($_.Exception.Message)" }
}

function Publish-Version {
    param([string]$VersionDirectory)
    $sourceDLL = Join-Path $VersionDirectory $dllName
    $sourceManifest = Join-Path $VersionDirectory 'manifest.json'
    if (-not (Test-Path -LiteralPath $sourceDLL -PathType Leaf) -or
        -not (Test-Path -LiteralPath $sourceManifest -PathType Leaf)) {
        throw "native version directory is incomplete: $VersionDirectory"
    }
    Copy-Item -LiteralPath $sourceManifest -Destination $temporaryManifest -Force
    Copy-Item -LiteralPath $sourceDLL -Destination $temporaryDLL -Force
    if (Test-Path -LiteralPath $installed) { Remove-Item -LiteralPath $installed -Force }
    if (Test-Path -LiteralPath $installedManifest) { Remove-Item -LiteralPath $installedManifest -Force }
    Move-Item -LiteralPath $temporaryManifest -Destination $installedManifest -Force
    Move-Item -LiteralPath $temporaryDLL -Destination $installed -Force
}

function Recover-NativeTransaction {
    $journal = Read-Pointer $journalPath
    if ($null -eq $journal) { return }
    if ($journal.phase -in @('prepare', 'verify')) {
        if ($journal.stage -and (Test-Path -LiteralPath $journal.stage)) {
            Remove-Item -LiteralPath $journal.stage -Recurse -Force
        }
    }
    elseif ($journal.phase -eq 'publish') {
        if ($journal.previousVersionDirectory -and (Test-Path -LiteralPath $journal.previousVersionDirectory -PathType Container)) {
            Publish-Version $journal.previousVersionDirectory
            Write-AtomicJSON -Path $currentPath -Value ([ordered]@{ versionDirectory = $journal.previousVersionDirectory })
        }
        else {
            Remove-Item -LiteralPath $installed,$installedManifest,$currentPath -Force -ErrorAction SilentlyContinue
        }
    }
    Remove-Item -LiteralPath $journalPath -Force
}

function Invoke-Rollback {
    $record = Read-Pointer $rollbackPath
    $current = Read-Pointer $currentPath
    if ($record -and $current -and $record.to -ceq $current.versionDirectory) {
        Write-Output "native_rollback=noop"
        return
    }
    $candidates = @(Get-ChildItem -LiteralPath $versionsRoot -Directory -ErrorAction SilentlyContinue |
        Where-Object { $null -eq $current -or $_.FullName -cne $current.versionDirectory } |
        Sort-Object LastWriteTimeUtc -Descending)
    if ($candidates.Count -eq 0) { throw 'native rollback has no retained previous version' }
    $target = $candidates[0].FullName
    Publish-Version $target
    Write-AtomicJSON -Path $currentPath -Value ([ordered]@{ versionDirectory = $target })
    Write-AtomicJSON -Path $rollbackPath -Value ([ordered]@{ from = if ($current) { $current.versionDirectory } else { '' }; to = $target })
    Write-Output "native_rollback=$target"
}

New-Item -ItemType Directory -Force -Path $cache,$destination,$stateRoot,$versionsRoot | Out-Null
try {
    while ($null -eq $lock) {
        try {
            $lock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        }
        catch [IO.IOException] {
            if ($timer.Elapsed.TotalSeconds -ge $LockTimeoutSeconds) {
                throw "native cache lock timeout: $lockPath"
            }
            Start-Sleep -Milliseconds 100
        }
    }

    $destinationTimer = [Diagnostics.Stopwatch]::StartNew()
    while ($null -eq $destinationLock) {
        try {
            $destinationLock = [IO.File]::Open($destinationLockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        }
        catch [IO.IOException] {
            if ($destinationTimer.Elapsed.TotalSeconds -ge $LockTimeoutSeconds) {
                throw "native destination lock timeout: $destinationLockPath"
            }
            Start-Sleep -Milliseconds 100
        }
    }
    Recover-NativeTransaction
    if ($Rollback) {
        Invoke-Rollback
        return
    }

    if (-not (Test-Path -LiteralPath $currentPath -PathType Leaf) -and
        (Test-Path -LiteralPath $installed -PathType Leaf) -and
        (Test-Path -LiteralPath $installedManifest -PathType Leaf)) {
        $legacyHash = Get-SHA256 $installed
        $legacyDirectory = Join-Path $versionsRoot "legacy-$($legacyHash.Substring(0, 16))"
        if (-not (Test-Path -LiteralPath $legacyDirectory -PathType Container)) {
            New-Item -ItemType Directory -Path $legacyDirectory | Out-Null
            Copy-Item -LiteralPath $installed -Destination (Join-Path $legacyDirectory $dllName)
            Copy-Item -LiteralPath $installedManifest -Destination (Join-Path $legacyDirectory 'manifest.json')
        }
        Write-AtomicJSON -Path $currentPath -Value ([ordered]@{ versionDirectory = $legacyDirectory; nativeVersion = 'legacy'; sha256 = $legacyHash })
    }

    if (-not (Test-Path -LiteralPath $archive -PathType Leaf)) {
        if ($Offline) {
            throw "native runtime cache unavailable in offline mode: $archive"
        }
        Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
        try {
            Invoke-VerifiedDownload -URL $url -Destination $partial -ExpectedSHA256 $expectedArchiveSHA -DisplayName $asset
            Move-Item -LiteralPath $partial -Destination $archive -Force
        }
        finally {
            Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
        }
    }

    # Verify every cache state immediately before opening the archive.
    $archiveHash = Get-SHA256 $archive
    if ($archiveHash -cne $expectedArchiveSHA) {
        throw "native archive SHA-256 mismatch: expected $expectedArchiveSHA, got $archiveHash"
    }

    if (Test-Path -LiteralPath $stage) {
        Remove-Item -LiteralPath $stage -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $stage | Out-Null

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($archive)
    try {
        $entryNames = @()
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace('\', '/')
            if ($name.StartsWith('/') -or $name -match '^[A-Za-z]:' -or $name -match '(^|/)\.\.?(/|$)') {
                throw "native archive path traversal: $name"
            }
            if (-not $name.EndsWith('/')) {
                $entryNames += $name
            }
        }
        if (@($entryNames | Where-Object { $_ -ceq 'manifest.json' }).Count -ne 1 -or
            @($entryNames | Where-Object { $_ -ceq $dllName }).Count -ne 1) {
            throw 'native archive must contain exactly one root manifest.json and miniapp-frida.dll'
        }
    }
    finally {
        $zip.Dispose()
    }

    Expand-Archive -LiteralPath $archive -DestinationPath $stage -Force
    $manifestPath = Join-Path $stage 'manifest.json'
    $dllPath = Join-Path $stage $dllName
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $dllPath -PathType Leaf)) {
        throw 'native archive missing manifest.json or miniapp-frida.dll'
    }

    try {
        $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
    }
    catch {
        throw "native manifest is not valid JSON: $($_.Exception.Message)"
    }
    if ($null -eq $manifest -or $manifest -is [array]) {
        throw 'native manifest must be a JSON object'
    }

    $requiredFields = @('schema', 'nativeVersion', 'fridaCoreVersion', 'zlibVersion',
        'abiVersion', 'os', 'arch', 'dll', 'size', 'sha256', 'requiredExports')
    $propertyNames = @($manifest.PSObject.Properties | ForEach-Object { $_.Name })
    foreach ($field in $requiredFields) {
        if (-not ($propertyNames -ccontains $field)) {
            throw "native manifest missing required field: $field"
        }
    }

    $expectedStrings = [ordered]@{
        schema           = 'miniapp-bridge.native-manifest.v1'
        nativeVersion    = $Version
        fridaCoreVersion = '17.3.2'
        zlibVersion      = '1.3.1'
        os               = 'windows'
        arch             = 'amd64'
        dll              = $dllName
    }
    foreach ($field in $expectedStrings.Keys) {
        $value = $manifest.PSObject.Properties[$field].Value
        if (-not ($value -is [string]) -or $value -cne $expectedStrings[$field]) {
            throw "native manifest field mismatch: $field"
        }
    }
    if (-not (Test-IntegerValue $manifest.abiVersion) -or [int64]$manifest.abiVersion -ne 1) {
        throw 'native manifest field mismatch: abiVersion'
    }

    $dllInfo = Get-Item -LiteralPath $dllPath
    if ($dllInfo.Length -le 0 -or -not (Test-IntegerValue $manifest.size) -or
        [int64]$manifest.size -ne [int64]$dllInfo.Length) {
        throw 'native manifest field mismatch: size'
    }
    $dllHash = Get-SHA256 $dllPath
    if (-not ($manifest.sha256 -is [string]) -or $manifest.sha256 -notmatch '^[0-9A-Fa-f]{64}$' -or
        $manifest.sha256.ToUpperInvariant() -cne $dllHash) {
        throw "native DLL SHA-256 mismatch: got $dllHash"
    }

    $requiredExports = @(
        'mb_abi_version', 'mb_native_version', 'mb_frida_core_version', 'mb_zlib_version',
        'mb_zlib_compress', 'mb_zlib_decompress', 'mb_bytes_free', 'mb_device_open',
        'mb_device_enumerate', 'mb_processes_free', 'mb_device_attach', 'mb_device_close',
        'mb_runtime_shutdown', 'mb_session_load_script', 'mb_session_detach', 'mb_script_post',
        'mb_script_unload', 'mb_error_free'
    )
    $actualExports = @($manifest.requiredExports)
    if (@($actualExports | Where-Object { -not ($_ -is [string]) }).Count -ne 0 -or
        $actualExports.Count -ne $requiredExports.Count -or
        @($requiredExports | Where-Object { -not ($actualExports -ccontains $_) }).Count -ne 0) {
        throw 'native manifest required export set mismatch'
    }

    $versionDirectory = Join-Path $versionsRoot "$Version-$($dllHash.Substring(0, 16))"
    $versionStage = "$versionDirectory.staging-$publishID"
    Write-AtomicJSON -Path $journalPath -Value ([ordered]@{ phase = 'prepare'; stage = $versionStage; previousVersionDirectory = '' })
    if (-not (Test-Path -LiteralPath $versionDirectory -PathType Container)) {
        New-Item -ItemType Directory -Path $versionStage | Out-Null
        Copy-Item -LiteralPath $manifestPath -Destination (Join-Path $versionStage 'manifest.json')
        Copy-Item -LiteralPath $dllPath -Destination (Join-Path $versionStage $dllName)
        Write-AtomicJSON -Path $journalPath -Value ([ordered]@{ phase = 'verify'; stage = $versionStage; previousVersionDirectory = '' })
        if ((Get-SHA256 (Join-Path $versionStage $dllName)) -cne $dllHash) {
            throw 'native version directory DLL SHA-256 mismatch'
        }
        Move-Item -LiteralPath $versionStage -Destination $versionDirectory
    }

    if (Test-Path -LiteralPath $installedManifest -PathType Container) {
        throw 'native destination manifest path is a directory'
    }
    if (Test-Path -LiteralPath $installed -PathType Container) {
        throw 'native destination DLL path is a directory'
    }

    $previous = Read-Pointer $currentPath
    $previousDirectory = if ($previous) { [string]$previous.versionDirectory } else { '' }
    Write-AtomicJSON -Path $journalPath -Value ([ordered]@{ phase = 'publish'; stage = $versionStage; previousVersionDirectory = $previousDirectory })
    try {
        Invoke-TestPublishFailure 'after-dll-backup'
        Invoke-TestPublishFailure 'after-manifest-backup'
        Publish-Version $versionDirectory
        Invoke-TestPublishFailure 'after-manifest-publish'
        Invoke-TestPublishFailure 'after-dll-publish'
        Write-AtomicJSON -Path $currentPath -Value ([ordered]@{ versionDirectory = $versionDirectory; nativeVersion = $Version; sha256 = $dllHash })
        Remove-Item -LiteralPath $rollbackPath -Force -ErrorAction SilentlyContinue
        if ($CanaryCommand) {
            $global:LASTEXITCODE = 0
            & ([scriptblock]::Create($CanaryCommand))
            if ($LASTEXITCODE -ne 0) { throw "native canary failed with exit code $LASTEXITCODE" }
        }
    }
    catch {
        $publishFailure = $_
        if ($previousDirectory -and (Test-Path -LiteralPath $previousDirectory -PathType Container)) {
            Publish-Version $previousDirectory
            Write-AtomicJSON -Path $currentPath -Value ([ordered]@{ versionDirectory = $previousDirectory })
        } else {
            Remove-Item -LiteralPath $installed,$installedManifest,$currentPath -Force -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $journalPath -Force -ErrorAction SilentlyContinue
        throw $publishFailure
    }

    Write-AtomicJSON -Path $journalPath -Value ([ordered]@{ phase = 'cleanup'; stage = ''; previousVersionDirectory = $previousDirectory })
    $orderedDirectories = @(
        Get-Item -LiteralPath $versionDirectory
        if ($previousDirectory -and $previousDirectory -cne $versionDirectory -and (Test-Path -LiteralPath $previousDirectory)) {
            Get-Item -LiteralPath $previousDirectory
        }
        Get-ChildItem -LiteralPath $versionsRoot -Directory | Sort-Object LastWriteTimeUtc -Descending
    )
    $keep = @($orderedDirectories | Select-Object -ExpandProperty FullName -Unique | Select-Object -First $RetentionCount)
    foreach ($directory in @(Get-ChildItem -LiteralPath $versionsRoot -Directory)) {
        if ($keep -contains $directory.FullName) { continue }
        Remove-Item -LiteralPath $directory.FullName -Recurse -Force
    }
    Remove-Item -LiteralPath $journalPath -Force

    Write-Output "native_version=$Version"
    Write-Output "native_dll=$installed"
    Write-Output "native_manifest=$installedManifest"
    Write-Output "native_dll_sha256=$dllHash"
}
finally {
    if ($null -ne $destinationLock) {
        $destinationLock.Dispose()
        Remove-Item -LiteralPath $destinationLockPath -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $lock) {
        Remove-Item -LiteralPath $partial,$temporaryDLL,$temporaryManifest -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $stage) {
            Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
        }
        $lock.Dispose()
        Remove-Item -LiteralPath $lockPath -Force -ErrorAction SilentlyContinue
    }
}
