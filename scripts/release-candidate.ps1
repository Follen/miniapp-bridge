param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('Create', 'Verify', 'Rebind')]
    [string]$Mode,
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[0-9a-f]{40}$')]
    [string]$SourceCommit,
    [string]$RepositoryRoot = '',
    [string]$InputDirectory = '',
    [string]$OutputDirectory = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Get-SHA256([string]$Path) {
    return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Write-Utf8NoBom([string]$Path, [string]$Text) {
    [IO.File]::WriteAllText($Path, $Text, [Text.UTF8Encoding]::new($false))
}

function Get-ProductionInputs([string]$Root) {
    $paths = @(& git -C $Root ls-files)
    if ($LASTEXITCODE -ne 0) { throw 'could not enumerate tracked production inputs' }
    $selected = @($paths | Where-Object {
        $_ -in @('go.mod', 'go.sum', 'README.md', 'README.zh.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md') -or
        $_ -match '^licenses/' -or
        $_ -match '^(cmd|frida|internal|sdk)/.*\.go$' -and $_ -notmatch '_test\.go$' -or
        $_ -match '^native/.*\.(c|h|def)$' -or
        $_ -match '^scripts/(build-frida-shim|build-windows|build-zlib|ensure-frida-devkit|generate-address-configs|native-release|package-windows-release|promote-release|release-candidate)\.ps1$'
    } | Sort-Object -Unique)
    if ($selected.Count -eq 0) { throw 'production input manifest is empty' }
    $result = [ordered]@{}
    foreach ($relative in $selected) {
        $path = Join-Path $Root $relative
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "tracked production input is missing: $relative" }
        $result[$relative] = Get-SHA256 $path
    }
    return $result
}

function Write-Checksums([string]$Directory) {
    $names = @(Get-ChildItem -LiteralPath $Directory -File | Where-Object Name -ne 'SHA256SUMS' |
        Select-Object -ExpandProperty Name | Sort-Object)
    $lines = @($names | ForEach-Object { "$(Get-SHA256 (Join-Path $Directory $_))  $_" })
    Write-Utf8NoBom (Join-Path $Directory 'SHA256SUMS') (($lines -join "`n") + "`n")
}

$root = if ($RepositoryRoot) { [IO.Path]::GetFullPath($RepositoryRoot) } else { (Resolve-Path (Join-Path $PSScriptRoot '..')).Path }
$input = if ($InputDirectory) { [IO.Path]::GetFullPath($InputDirectory) } else { Join-Path $root 'dist' }
$output = if ($OutputDirectory) { [IO.Path]::GetFullPath($OutputDirectory) } else { Join-Path $root 'dist\candidate' }
$nativeName = 'miniapp-frida-native-17.3.2-abi1.1-windows-amd64.zip'
$payload = [ordered]@{
    'LICENSE' = Join-Path $root 'LICENSE'
    'README.md' = Join-Path $root 'README.md'
    'README.zh.md' = Join-Path $root 'README.zh.md'
    'THIRD_PARTY_NOTICES.md' = Join-Path $root 'THIRD_PARTY_NOTICES.md'
    'FRIDA_COPYING' = Join-Path $root 'licenses\frida-17.3.2\COPYING'
    'FRIDA_COPYING.LIB' = Join-Path $root 'licenses\frida-17.3.2\COPYING.LIB'
    'ZLIB_LICENSE' = Join-Path $root 'third_party\zlib\src-1.3.1\LICENSE'
    'miniapp-bridge.exe' = Join-Path $input 'miniapp-bridge.exe'
    'miniapp-frida.dll' = Join-Path $input 'miniapp-frida.dll'
    'manifest.json' = Join-Path $input 'manifest.json'
    'miniapp-bridge.cdx.json' = Join-Path $input 'miniapp-bridge.cdx.json'
    $nativeName = Join-Path $input "native\$nativeName"
}

if ($Mode -eq 'Create') {
    foreach ($entry in $payload.GetEnumerator()) {
        if (-not (Test-Path -LiteralPath $entry.Value -PathType Leaf)) { throw "candidate input is missing: $($entry.Value)" }
    }
    $parent = Split-Path -Parent $output
    $leaf = Split-Path -Leaf $output
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    $stage = Join-Path $parent ".$leaf.staging-$([Guid]::NewGuid().ToString('N'))"
    try {
        New-Item -ItemType Directory -Path $stage | Out-Null
        foreach ($entry in $payload.GetEnumerator()) {
            Copy-Item -LiteralPath $entry.Value -Destination (Join-Path $stage $entry.Key)
        }
        $files = @($payload.Keys | Sort-Object)
        $hashes = [ordered]@{}
        foreach ($name in $files) { $hashes[$name] = Get-SHA256 (Join-Path $stage $name) }
        $metadata = [ordered]@{
            schema = 'miniapp-bridge.release-candidate.v1'
            sourceCommit = $SourceCommit
            buildCommit = $SourceCommit
            nativeVersion = '17.3.2-abi1.1'
            os = 'windows'
            arch = 'amd64'
            files = $hashes
            productionInputs = Get-ProductionInputs $root
        }
        Write-Utf8NoBom (Join-Path $stage 'candidate.json') (($metadata | ConvertTo-Json -Depth 8) + "`n")
        Write-Checksums $stage
        if (Test-Path -LiteralPath $output) { Remove-Item -LiteralPath $output -Recurse -Force }
        Move-Item -LiteralPath $stage -Destination $output
    }
    finally {
        Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    }
}

$metadataPath = Join-Path $output 'candidate.json'
$sumsPath = Join-Path $output 'SHA256SUMS'
foreach ($required in @($metadataPath, $sumsPath)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) { throw "candidate metadata is missing: $required" }
}
$metadata = Get-Content -LiteralPath $metadataPath -Raw | ConvertFrom-Json
if ($Mode -eq 'Rebind') {
    $currentInputs = Get-ProductionInputs $root
    $candidateInputs = [ordered]@{}
    foreach ($property in $metadata.productionInputs.PSObject.Properties) { $candidateInputs[$property.Name] = [string]$property.Value }
    if ($currentInputs.Count -ne $candidateInputs.Count) { throw 'candidate production input count differs from the target source tree' }
    foreach ($name in $currentInputs.Keys) {
        if (-not $candidateInputs.Contains($name) -or $candidateInputs[$name] -cne $currentInputs[$name]) {
            throw "candidate production input differs from the target source tree: $name"
        }
    }
    $metadata.sourceCommit = $SourceCommit
    Write-Utf8NoBom $metadataPath (($metadata | ConvertTo-Json -Depth 8) + "`n")
    Write-Checksums $output
    $metadata = Get-Content -LiteralPath $metadataPath -Raw | ConvertFrom-Json
}
if ($metadata.schema -cne 'miniapp-bridge.release-candidate.v1' -or
    $metadata.sourceCommit -cne $SourceCommit -or $metadata.buildCommit -cnotmatch '^[0-9a-f]{40}$' -or
    $metadata.nativeVersion -cne '17.3.2-abi1.1' -or
    $metadata.os -cne 'windows' -or $metadata.arch -cne 'amd64') {
    throw 'candidate identity does not match the requested source commit and platform'
}
$currentInputs = Get-ProductionInputs $root
$candidateInputs = [ordered]@{}
foreach ($property in $metadata.productionInputs.PSObject.Properties) { $candidateInputs[$property.Name] = [string]$property.Value }
if ($currentInputs.Count -ne $candidateInputs.Count) { throw 'candidate production input count differs from the requested source tree' }
foreach ($name in $currentInputs.Keys) {
    if (-not $candidateInputs.Contains($name) -or $candidateInputs[$name] -cne $currentInputs[$name]) {
        throw "candidate production input differs from the requested source tree: $name"
    }
}
$expectedNames = @($payload.Keys | Sort-Object)
$actualNames = @($metadata.files.PSObject.Properties.Name | Sort-Object)
if (Compare-Object $expectedNames $actualNames) { throw 'candidate payload set does not match the contract' }
foreach ($name in $expectedNames) {
    $path = Join-Path $output $name
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "candidate payload is missing: $name" }
    if ((Get-SHA256 $path) -cne [string]$metadata.files.$name) { throw "candidate payload hash mismatch: $name" }
}
$sumLines = @(Get-Content -LiteralPath $sumsPath)
if ($sumLines.Count -ne ($expectedNames.Count + 1)) { throw 'candidate checksum entry count mismatch' }
foreach ($line in $sumLines) {
    if ($line -cnotmatch '^([0-9a-f]{64})  ([^\\/]+)$') { throw "invalid candidate checksum line: $line" }
    $path = Join-Path $output $Matches[2]
    if (-not (Test-Path -LiteralPath $path -PathType Leaf) -or (Get-SHA256 $path) -cne $Matches[1]) {
        throw "candidate checksum mismatch: $($Matches[2])"
    }
}
Write-Output "candidate_source_commit=$SourceCommit"
Write-Output "candidate_directory=$output"
