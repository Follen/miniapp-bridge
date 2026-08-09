param(
    [Parameter(Mandatory = $true)]
    [string]$Version,
    [string]$RepositoryRoot = '',
    [string]$InputDirectory = '',
    [string]$NativeDirectory = '',
    [string]$OutputDirectory = '',
    [ValidateSet('', 'DuringStage', 'AfterStage', 'AfterBackup', 'AfterPublish')]
    [string]$TestFailPoint = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Get-SHA256 {
    param([Parameter(Mandatory = $true)][string]$Path)
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

function Write-Utf8NoBom {
    param([Parameter(Mandatory = $true)][string]$Path, [Parameter(Mandatory = $true)][string[]]$Lines)
    [IO.File]::WriteAllText($Path, (($Lines -join "`n") + "`n"), [Text.UTF8Encoding]::new($false))
}

function Write-ReproducibleZip {
    param(
        [Parameter(Mandatory = $true)][string]$StageDirectory,
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string[]]$EntryNames
    )
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $fileStream = [IO.File]::Open($ArchivePath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    $archive = [IO.Compression.ZipArchive]::new($fileStream, [IO.Compression.ZipArchiveMode]::Create, $false)
    $fixedTime = [DateTimeOffset]::Parse('1980-01-01T00:00:00Z')
    try {
        foreach ($name in $EntryNames) {
            $source = Join-Path $StageDirectory $name
            if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
                throw "release entry missing: $name"
            }
            $entry = $archive.CreateEntry($name, [IO.Compression.CompressionLevel]::Optimal)
            $entry.LastWriteTime = $fixedTime
            $input = [IO.File]::OpenRead($source)
            $output = $entry.Open()
            try {
                $input.CopyTo($output)
            }
            finally {
                $output.Dispose()
                $input.Dispose()
            }
        }
    }
    finally {
        $archive.Dispose()
        $fileStream.Dispose()
    }
}

function Invoke-TestFailure {
    param([Parameter(Mandatory = $true)][string]$Point)
    if ($TestFailPoint -eq $Point) {
        throw "injected release packaging failure: $Point"
    }
}

function Assert-ChecksumFile {
    param(
        [Parameter(Mandatory = $true)][string]$Directory,
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][int]$ExpectedEntries
    )
    $lines = @(Get-Content -LiteralPath $Path | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($lines.Count -ne $ExpectedEntries) {
        throw "checksum entry count mismatch in $Path`: got $($lines.Count), want $ExpectedEntries"
    }
    foreach ($line in $lines) {
        if ($line -notmatch '^([0-9A-Fa-f]{64})  ([^\\/]+)$') {
            throw "invalid checksum line in $Path`: $line"
        }
        $expected = $Matches[1].ToUpperInvariant()
        $asset = Join-Path $Directory $Matches[2]
        if (-not (Test-Path -LiteralPath $asset -PathType Leaf)) {
            throw "checksum asset missing: $asset"
        }
        $actual = Get-SHA256 $asset
        if ($actual -ne $expected) {
            throw "checksum mismatch for $asset`: got $actual, want $expected"
        }
    }
}

$semver = '^v(0|1)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*)?$'
if ($Version -cnotmatch $semver) {
    throw "Version must be a Go-compatible semantic version tag for github.com/Follen/miniapp-bridge (v0 or v1, without build metadata), such as v0.0.1 or v0.0.1-rc.1: $Version"
}

$root = if ([string]::IsNullOrWhiteSpace($RepositoryRoot)) {
    (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
} else {
    [IO.Path]::GetFullPath($RepositoryRoot)
}
$input = if ([string]::IsNullOrWhiteSpace($InputDirectory)) { Join-Path $root 'dist' } else { [IO.Path]::GetFullPath($InputDirectory) }
$native = if ([string]::IsNullOrWhiteSpace($NativeDirectory)) { Join-Path $input 'native' } else { [IO.Path]::GetFullPath($NativeDirectory) }
$out = if ([string]::IsNullOrWhiteSpace($OutputDirectory)) { Join-Path $input 'release' } else { [IO.Path]::GetFullPath($OutputDirectory) }

$exe = Join-Path $input 'miniapp-bridge.exe'
$dll = Join-Path $input 'miniapp-frida.dll'
$manifestPath = Join-Path $input 'manifest.json'
$readme = Join-Path $root 'README.md'
$readmeZH = Join-Path $root 'README.zh.md'
$license = Join-Path $root 'LICENSE'
$notices = Join-Path $root 'THIRD_PARTY_NOTICES.md'
$zlibLicense = Join-Path $root 'third_party\zlib\src-1.3.1\LICENSE'

foreach ($required in @($exe, $dll, $manifestPath, $readme, $readmeZH, $license, $notices, $zlibLicense)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "required release file missing: $required"
    }
}

try {
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
}
catch {
    throw "manifest is not valid JSON: $manifestPath`: $($_.Exception.Message)"
}
foreach ($field in @('schema', 'nativeVersion', 'fridaCoreVersion', 'zlibVersion', 'abiVersion', 'os', 'arch', 'dll', 'size', 'sha256', 'requiredExports')) {
    if ($null -eq $manifest.PSObject.Properties[$field]) {
        throw "manifest missing required field: $field"
    }
}
if ($manifest.schema -ne 'miniapp-bridge.native-manifest.v1' -or
    $manifest.os -ne 'windows' -or $manifest.arch -ne 'amd64' -or
    $manifest.dll -ne 'miniapp-frida.dll') {
    throw "manifest platform contract mismatch: $manifestPath"
}
$dllInfo = Get-Item -LiteralPath $dll
$dllHash = Get-SHA256 $dll
if ([int64]$manifest.size -ne [int64]$dllInfo.Length -or
    -not [string]::Equals([string]$manifest.sha256, $dllHash, [StringComparison]::OrdinalIgnoreCase)) {
    throw "DLL does not match manifest: size=$($dllInfo.Length) sha256=$dllHash"
}

$nativeAssetName = "miniapp-frida-native-$($manifest.nativeVersion)-windows-amd64.zip"
$nativeArchive = Join-Path $native $nativeAssetName
$nativeSums = Join-Path $native 'SHA256SUMS'
foreach ($required in @($nativeArchive, $nativeSums)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "required native release file missing: $required"
    }
}
$nativeSumLines = @(Get-Content -LiteralPath $nativeSums | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
if ($nativeSumLines.Count -ne 1 -or $nativeSumLines[0] -notmatch '^([0-9A-Fa-f]{64})  ([^\\/]+)$' -or $Matches[2] -ne $nativeAssetName) {
    throw "native SHA256SUMS must contain exactly one entry for $nativeAssetName"
}
$expectedNativeHash = $Matches[1].ToUpperInvariant()
$actualNativeHash = Get-SHA256 $nativeArchive
if ($actualNativeHash -ne $expectedNativeHash) {
    throw "native archive SHA-256 mismatch: got $actualNativeHash, want $expectedNativeHash"
}

$outParent = Split-Path -Parent $out
$outLeaf = Split-Path -Leaf $out
if ([string]::IsNullOrWhiteSpace($outParent) -or [string]::IsNullOrWhiteSpace($outLeaf)) {
    throw "output directory must have a parent and leaf name: $out"
}
New-Item -ItemType Directory -Force -Path $outParent | Out-Null
if (Test-Path -LiteralPath $out -PathType Leaf) {
    throw "output directory is a file: $out"
}

$operationID = [Guid]::NewGuid().ToString('N')
$stage = Join-Path $outParent ".$outLeaf.staging-$operationID"
$backup = Join-Path $outParent ".$outLeaf.backup-$operationID"
$discard = Join-Path $outParent ".$outLeaf.discard-$operationID"
$productAssetName = "miniapp-bridge-$Version-windows-amd64.zip"
$stageProductContents = Join-Path $stage '.product-contents'
$stageProduct = Join-Path $stage $productAssetName
$stageNative = Join-Path $stage $nativeAssetName
$stageManifest = Join-Path $stage 'manifest.json'
$stageSums = Join-Path $stage 'SHA256SUMS'
$stageCompat = Join-Path $stage 'native-compat'
$stageCompatNative = Join-Path $stageCompat $nativeAssetName
$stageCompatSums = Join-Path $stageCompat 'SHA256SUMS'
$hadExisting = Test-Path -LiteralPath $out -PathType Container
$oldMoved = $false
$newPublished = $false

try {
    New-Item -ItemType Directory -Path $stage | Out-Null
    New-Item -ItemType Directory -Path $stageProductContents | Out-Null
    New-Item -ItemType Directory -Path $stageCompat | Out-Null
    $entries = [ordered]@{
        'LICENSE'                = $license
        'README.md'              = $readme
        'README.zh.md'           = $readmeZH
        'THIRD_PARTY_NOTICES.md' = $notices
        'ZLIB_LICENSE'           = $zlibLicense
        'manifest.json'          = $manifestPath
        'miniapp-bridge.exe'     = $exe
        'miniapp-frida.dll'      = $dll
    }
    foreach ($entry in $entries.GetEnumerator()) {
        Copy-Item -LiteralPath $entry.Value -Destination (Join-Path $stageProductContents $entry.Key)
    }
    Write-ReproducibleZip -StageDirectory $stageProductContents -ArchivePath $stageProduct -EntryNames @($entries.Keys)
    Invoke-TestFailure -Point 'DuringStage'
    Remove-Item -LiteralPath $stageProductContents -Recurse -Force
    Copy-Item -LiteralPath $nativeArchive -Destination $stageNative
    Copy-Item -LiteralPath $manifestPath -Destination $stageManifest -Force
    Copy-Item -LiteralPath $nativeArchive -Destination $stageCompatNative

    $productHash = Get-SHA256 $stageProduct
    $manifestHash = Get-SHA256 $stageManifest
    Write-Utf8NoBom -Path $stageSums -Lines @(
        "$productHash  $productAssetName",
        "$actualNativeHash  $nativeAssetName",
        "$manifestHash  manifest.json"
    )
    Write-Utf8NoBom -Path $stageCompatSums -Lines @(
        "$actualNativeHash  $nativeAssetName"
    )

    Assert-ChecksumFile -Directory $stage -Path $stageSums -ExpectedEntries 3
    Assert-ChecksumFile -Directory $stageCompat -Path $stageCompatSums -ExpectedEntries 1
    if ((Get-SHA256 $stageNative) -ne (Get-SHA256 $stageCompatNative)) {
        throw 'native compatibility asset differs from the primary native asset'
    }
    Invoke-TestFailure -Point 'AfterStage'

    if ($hadExisting) {
        Move-Item -LiteralPath $out -Destination $backup
        $oldMoved = $true
    }
    Invoke-TestFailure -Point 'AfterBackup'
    Move-Item -LiteralPath $stage -Destination $out
    $newPublished = $true
    Invoke-TestFailure -Point 'AfterPublish'

    if ($oldMoved) {
        Remove-Item -LiteralPath $backup -Recurse -Force
        $oldMoved = $false
    }

    $productAsset = Join-Path $out $productAssetName
    $publishedNative = Join-Path $out $nativeAssetName
    $publishedManifest = Join-Path $out 'manifest.json'
    $publishedSums = Join-Path $out 'SHA256SUMS'
    Write-Output "product_asset=$productAsset"
    Write-Output "product_sha256=$productHash"
    Write-Output "native_asset=$publishedNative"
    Write-Output "native_sha256=$actualNativeHash"
    Write-Output "manifest=$publishedManifest"
    Write-Output "sha256sums=$publishedSums"
}
catch {
    $failure = $_
    try {
        if ($newPublished -and (Test-Path -LiteralPath $out -PathType Container)) {
            Move-Item -LiteralPath $out -Destination $discard
            $newPublished = $false
        }
        if ($oldMoved -and (Test-Path -LiteralPath $backup -PathType Container)) {
            Move-Item -LiteralPath $backup -Destination $out
            $oldMoved = $false
        }
        if (Test-Path -LiteralPath $discard) {
            Remove-Item -LiteralPath $discard -Recurse -Force
        }
    }
    catch {
        throw "release packaging failed and rollback failed: $($failure.Exception.Message); rollback: $($_.Exception.Message)"
    }
    throw $failure
}
finally {
    if (Test-Path -LiteralPath $stage) {
        Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    }
    if ((Test-Path -LiteralPath $discard) -and
        ((Test-Path -LiteralPath $out -PathType Container) -or -not $hadExisting)) {
        Remove-Item -LiteralPath $discard -Recurse -Force -ErrorAction SilentlyContinue
    }
    if ((Test-Path -LiteralPath $backup) -and -not $oldMoved) {
        Remove-Item -LiteralPath $backup -Recurse -Force -ErrorAction SilentlyContinue
    }
}
