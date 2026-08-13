param(
    [string]$RuntimeDirectory = '',
    [string]$OutputDirectory = '',
    [string]$ManifestOutputDirectory = '',
    [ValidateSet('17.3.2-abi1.1')][string]$Version = '17.3.2-abi1.1',
    [string]$LicenseFile = '',
    [string]$FridaCopyingFile = '',
    [string]$FridaLibraryLicenseFile = '',
    [string]$ZlibLicenseFile = '',
    [string]$ThirdPartyNoticesFile = '',
    [string]$DumpbinPath = '',
    [ValidateRange(1, 600)][int]$LockTimeoutSeconds = 120
)

$ErrorActionPreference = 'Stop'

function Write-Utf8NoBom {
    param([string]$Path, [string[]]$Lines)
    $text = ($Lines -join "`n") + "`n"
    [IO.File]::WriteAllText($Path, $text, [Text.UTF8Encoding]::new($false))
}

function Get-SHA256 {
    param([string]$Path)
    $stream = [IO.File]::OpenRead($Path)
    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($hasher.ComputeHash($stream))).Replace('-', '')
    }
    finally {
        $hasher.Dispose()
        $stream.Dispose()
    }
}

function Write-ReproducibleZip {
    param(
        [string]$StageDirectory,
        [string]$ArchivePath,
        [string[]]$EntryNames
    )
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $fileStream = [IO.File]::Open($ArchivePath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    $archive = [IO.Compression.ZipArchive]::new($fileStream, [IO.Compression.ZipArchiveMode]::Create, $false)
    $fixedTime = [DateTimeOffset]::Parse('1980-01-01T00:00:00Z')
    try {
        foreach ($name in $EntryNames) {
            $source = Join-Path $StageDirectory $name
            if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw "release entry missing: $name" }
            $entry = $archive.CreateEntry($name, [IO.Compression.CompressionLevel]::Optimal)
            $entry.LastWriteTime = $fixedTime
            $input = [IO.File]::OpenRead($source)
            $output = $entry.Open()
            try { $input.CopyTo($output) }
            finally { $output.Dispose(); $input.Dispose() }
        }
    }
    finally {
        $archive.Dispose()
        $fileStream.Dispose()
    }
}

function Resolve-Dumpbin {
    if ($DumpbinPath) {
        $candidate = [IO.Path]::GetFullPath($DumpbinPath)
        if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) { throw "dumpbin missing: $candidate" }
        return $candidate
    }
    $command = Get-Command dumpbin.exe -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    $roots = @(
        'C:\Program Files\Microsoft Visual Studio\2022',
        'C:\Program Files (x86)\Microsoft Visual Studio\2022'
    )
    $candidates = @($roots | ForEach-Object {
        Get-ChildItem -LiteralPath $_ -Filter dumpbin.exe -Recurse -ErrorAction SilentlyContinue |
            Where-Object { $_.FullName -match '\\bin\\Hostx64\\x64\\dumpbin\.exe$' }
    } | Sort-Object FullName -Descending)
    if ($candidates.Count -eq 0) { throw 'dumpbin.exe for Hostx64/x64 was not found' }
    return $candidates[0].FullName
}

$requiredExports = @(
    'mb_abi_version', 'mb_native_version', 'mb_frida_core_version', 'mb_zlib_version',
    'mb_zlib_compress', 'mb_zlib_decompress', 'mb_bytes_free',
    'mb_device_open', 'mb_device_enumerate', 'mb_processes_free', 'mb_device_attach',
    'mb_device_close', 'mb_runtime_shutdown', 'mb_session_load_script', 'mb_session_detach',
    'mb_script_post', 'mb_script_unload', 'mb_error_free'
)

$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$runtime = if ($RuntimeDirectory) { [IO.Path]::GetFullPath($RuntimeDirectory) } else { Join-Path $root 'third_party\frida\runtime-17.3.2' }
$out = if ($OutputDirectory) { [IO.Path]::GetFullPath($OutputDirectory) } else { Join-Path $root 'dist\native' }
$license = if ($LicenseFile) { [IO.Path]::GetFullPath($LicenseFile) } else { Join-Path $root 'LICENSE' }
$fridaCopying = if ($FridaCopyingFile) { [IO.Path]::GetFullPath($FridaCopyingFile) } else { Join-Path $root 'licenses\frida-17.3.2\COPYING' }
$fridaLibraryLicense = if ($FridaLibraryLicenseFile) { [IO.Path]::GetFullPath($FridaLibraryLicenseFile) } else { Join-Path $root 'licenses\frida-17.3.2\COPYING.LIB' }
$zlibLicense = if ($ZlibLicenseFile) { [IO.Path]::GetFullPath($ZlibLicenseFile) } else { Join-Path $root 'third_party\zlib\src-1.3.1\LICENSE' }
$notices = if ($ThirdPartyNoticesFile) { [IO.Path]::GetFullPath($ThirdPartyNoticesFile) } else { Join-Path $root 'THIRD_PARTY_NOTICES.md' }
$dllName = 'miniapp-frida.dll'
$dll = Join-Path $runtime $dllName
foreach ($required in @($dll, $license, $fridaCopying, $fridaLibraryLicense, $zlibLicense, $notices)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) { throw "required release file missing: $required" }
}

$dumpbin = Resolve-Dumpbin
$headers = @(& $dumpbin /nologo /headers $dll 2>&1)
if ($LASTEXITCODE -ne 0) { throw "dumpbin /headers failed with exit $LASTEXITCODE`: $($headers -join ' ')" }
if (-not ($headers -match '^\s*8664 machine \(x64\)\s*$')) { throw 'native DLL is not a Windows amd64 PE image' }
$exportOutput = @(& $dumpbin /nologo /exports $dll 2>&1)
if ($LASTEXITCODE -ne 0) { throw "dumpbin /exports failed with exit $LASTEXITCODE`: $($exportOutput -join ' ')" }
$actualExports = @($exportOutput | ForEach-Object {
    if ($_ -match '^\s*\d+\s+[0-9A-Fa-f]+\s+[0-9A-Fa-f]+\s+(mb_[A-Za-z0-9_]+)(?:\s|$)') { $Matches[1] }
} | Sort-Object -Unique)
$missingExports = @($requiredExports | Where-Object { $_ -notin $actualExports })
$unexpectedExports = @($actualExports | Where-Object { $_ -notin $requiredExports })
if ($missingExports.Count -ne 0 -or $unexpectedExports.Count -ne 0) {
    throw "native DLL export mismatch; missing=$($missingExports -join ','); unexpected=$($unexpectedExports -join ',')"
}

New-Item -ItemType Directory -Force -Path $out | Out-Null
$lockPath = Join-Path $out '.native-release.lock'
$releaseLock = $null
$lockTimer = [Diagnostics.Stopwatch]::StartNew()
while ($null -eq $releaseLock) {
    try { $releaseLock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None) }
    catch [IO.IOException] {
        if ($lockTimer.Elapsed.TotalSeconds -ge $LockTimeoutSeconds) { throw "native release output lock timeout: $lockPath" }
        Start-Sleep -Milliseconds 100
    }
}
$stage = Join-Path $out ".native-release-$([guid]::NewGuid().ToString('N'))"
$asset = "miniapp-frida-native-$Version-windows-amd64.zip"
$archive = Join-Path $out $asset
$sums = Join-Path $out 'SHA256SUMS'
$manifestOutput = if ($ManifestOutputDirectory) { Join-Path ([IO.Path]::GetFullPath($ManifestOutputDirectory)) 'manifest.json' } else { '' }
$archiveTemp = Join-Path $out ".$asset.$([guid]::NewGuid().ToString('N')).partial.zip"
$sumsTemp = Join-Path $out ".SHA256SUMS.$([guid]::NewGuid().ToString('N')).partial"
$manifestTemp = if ($manifestOutput) { "$manifestOutput.$([guid]::NewGuid().ToString('N')).partial" } else { '' }
try {
    New-Item -ItemType Directory -Force -Path $stage | Out-Null
    Copy-Item -LiteralPath $dll -Destination (Join-Path $stage $dllName)
    Copy-Item -LiteralPath $license -Destination (Join-Path $stage 'LICENSE')
    Copy-Item -LiteralPath $fridaCopying -Destination (Join-Path $stage 'FRIDA_COPYING')
    Copy-Item -LiteralPath $fridaLibraryLicense -Destination (Join-Path $stage 'FRIDA_COPYING.LIB')
    Copy-Item -LiteralPath $zlibLicense -Destination (Join-Path $stage 'ZLIB_LICENSE')
    Copy-Item -LiteralPath $notices -Destination (Join-Path $stage 'THIRD_PARTY_NOTICES.md')
    $dllInfo = Get-Item -LiteralPath (Join-Path $stage $dllName)
    $dllHash = Get-SHA256 $dllInfo.FullName
    $manifest = [ordered]@{
        schema = 'miniapp-bridge.native-manifest.v1'
        nativeVersion = $Version
        fridaCoreVersion = '17.3.2'
        zlibVersion = '1.3.1'
        abiVersion = 1
        os = 'windows'
        arch = 'amd64'
        dll = $dllName
        size = [int64]$dllInfo.Length
        sha256 = $dllHash
        requiredExports = $requiredExports
    }
    $manifestJSON = $manifest | ConvertTo-Json -Depth 4
    Write-Utf8NoBom -Path (Join-Path $stage 'manifest.json') -Lines $manifestJSON
    $payloadNames = @(
        $dllName, 'manifest.json', 'LICENSE', 'FRIDA_COPYING', 'FRIDA_COPYING.LIB',
        'ZLIB_LICENSE', 'THIRD_PARTY_NOTICES.md'
    )
    $payloadSums = @($payloadNames | ForEach-Object {
        $hash = Get-SHA256 (Join-Path $stage $_)
        "$hash  $_"
    })
    Write-Utf8NoBom -Path (Join-Path $stage 'SHA256SUMS') -Lines $payloadSums

    Write-ReproducibleZip -StageDirectory $stage -ArchivePath $archiveTemp -EntryNames @(
        $dllName, 'manifest.json', 'LICENSE', 'FRIDA_COPYING', 'FRIDA_COPYING.LIB',
        'ZLIB_LICENSE', 'THIRD_PARTY_NOTICES.md', 'SHA256SUMS'
    )
    Move-Item -LiteralPath $archiveTemp -Destination $archive -Force
    $archiveHash = Get-SHA256 $archive
    if ($manifestOutput) {
        $manifestDir = Split-Path -Parent $manifestOutput
        New-Item -ItemType Directory -Force -Path $manifestDir | Out-Null
        Write-Utf8NoBom -Path $manifestTemp -Lines $manifestJSON
        Move-Item -LiteralPath $manifestTemp -Destination $manifestOutput -Force
    }
    Write-Utf8NoBom -Path $sumsTemp -Lines @("$archiveHash  $asset")
    Move-Item -LiteralPath $sumsTemp -Destination $sums -Force
    Write-Output "asset=$archive"
    Write-Output 'manifest=manifest.json (inside archive)'
    Write-Output "dll_size=$($dllInfo.Length)"
    Write-Output "dll_sha256=$dllHash"
    Write-Output "archive_sha256=$archiveHash"
    Write-Output "sha256sums=$sums"
}
finally {
    foreach ($partial in @($archiveTemp, $sumsTemp, $manifestTemp)) {
        if ($partial) { Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue }
    }
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue }
    if ($null -ne $releaseLock) {
        $releaseLock.Dispose()
        Remove-Item -LiteralPath $lockPath -Force -ErrorAction SilentlyContinue
    }
}
