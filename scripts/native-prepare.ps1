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
    [string]$ExpectedArchiveSHA256 = ''
)

$ErrorActionPreference = 'Stop'
$expectedArchiveSHA = ([string]$ExpectedArchiveSHA256).Trim().ToUpperInvariant()
if (-not $expectedArchiveSHA) {
    throw 'ExpectedArchiveSHA256 is required for native preparation in online and offline modes'
}
if ($expectedArchiveSHA -notmatch '^[0-9A-F]{64}$') {
    throw 'ExpectedArchiveSHA256 must contain exactly 64 hexadecimal characters'
}

$asset = "miniapp-frida-native-$Version-windows-amd64.zip"
$cache = if ($CacheDirectory) { [IO.Path]::GetFullPath($CacheDirectory) } else { Join-Path $env:LOCALAPPDATA "miniapp-bridge\native\$Version\windows-amd64" }
$destination = if ($DestinationDirectory) { [IO.Path]::GetFullPath($DestinationDirectory) } else { (Get-Location).Path }
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
$cleanupPublishBackups = $false
$lock = $null
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

New-Item -ItemType Directory -Force -Path $cache,$destination | Out-Null
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

    Remove-Item -LiteralPath $temporaryDLL,$temporaryManifest -Force -ErrorAction SilentlyContinue
    Copy-Item -LiteralPath $manifestPath -Destination $temporaryManifest -Force
    Copy-Item -LiteralPath $dllPath -Destination $temporaryDLL -Force
    if ((Get-SHA256 $temporaryDLL) -cne $dllHash) {
        throw 'native destination partial DLL SHA-256 mismatch'
    }

    # The DLL is the readiness marker. Back up the previous pair, publish the
    # manifest, and publish the verified DLL last. Rollback follows the same
    # readiness ordering, restoring the previous DLL only after its manifest.
    if (Test-Path -LiteralPath $installedManifest -PathType Container) {
        throw 'native destination manifest path is a directory'
    }
    if (Test-Path -LiteralPath $installed -PathType Container) {
        throw 'native destination DLL path is a directory'
    }

    $hadInstalledDLL = Test-Path -LiteralPath $installed -PathType Leaf
    $hadInstalledManifest = Test-Path -LiteralPath $installedManifest -PathType Leaf
    $dllBackedUp = $false
    $manifestBackedUp = $false
    try {
        if ($hadInstalledDLL) {
            Move-Item -LiteralPath $installed -Destination $backupDLL -Force
            $dllBackedUp = $true
        }
        Invoke-TestPublishFailure 'after-dll-backup'

        if ($hadInstalledManifest) {
            Move-Item -LiteralPath $installedManifest -Destination $backupManifest -Force
            $manifestBackedUp = $true
        }
        Invoke-TestPublishFailure 'after-manifest-backup'

        Move-Item -LiteralPath $temporaryManifest -Destination $installedManifest -Force
        Invoke-TestPublishFailure 'after-manifest-publish'
        Move-Item -LiteralPath $temporaryDLL -Destination $installed -Force
        Invoke-TestPublishFailure 'after-dll-publish'
        $cleanupPublishBackups = $true
    }
    catch {
        $publishFailure = $_
        $rollbackErrors = @()

        if ($dllBackedUp -or -not $hadInstalledDLL) {
            try {
                if (Test-Path -LiteralPath $installed) {
                    Remove-Item -LiteralPath $installed -Force -ErrorAction Stop
                }
            }
            catch {
                $rollbackErrors += $_.Exception.Message
            }
        }
        if ($manifestBackedUp -or -not $hadInstalledManifest) {
            try {
                if (Test-Path -LiteralPath $installedManifest) {
                    Remove-Item -LiteralPath $installedManifest -Force -ErrorAction Stop
                }
            }
            catch {
                $rollbackErrors += $_.Exception.Message
            }
        }
        if ($manifestBackedUp) {
            try {
                Move-Item -LiteralPath $backupManifest -Destination $installedManifest -Force
                $manifestBackedUp = $false
            }
            catch {
                $rollbackErrors += $_.Exception.Message
            }
        }
        if ($dllBackedUp) {
            try {
                Move-Item -LiteralPath $backupDLL -Destination $installed -Force
                $dllBackedUp = $false
            }
            catch {
                $rollbackErrors += $_.Exception.Message
            }
        }

        if ($rollbackErrors.Count -gt 0) {
            throw "native publish failed: $($publishFailure.Exception.Message); rollback failed: $($rollbackErrors -join '; ')"
        }
        $cleanupPublishBackups = $true
        throw $publishFailure
    }

    Write-Output "native_version=$Version"
    Write-Output "native_dll=$installed"
    Write-Output "native_manifest=$installedManifest"
    Write-Output "native_dll_sha256=$dllHash"
}
finally {
    if ($null -ne $lock) {
        Remove-Item -LiteralPath $partial,$temporaryDLL,$temporaryManifest -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $stage) {
            Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
        }
        if ($cleanupPublishBackups) {
            Remove-Item -LiteralPath $backupDLL,$backupManifest -Force -ErrorAction SilentlyContinue
        }
        $lock.Dispose()
        Remove-Item -LiteralPath $lockPath -Force -ErrorAction SilentlyContinue
    }
}
