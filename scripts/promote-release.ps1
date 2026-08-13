param(
    [Parameter(Mandatory = $true)]
    [string]$ReleaseTag,
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[0-9a-f]{40}$')]
    [string]$SourceCommit,
    [Parameter(Mandatory = $true)]
    [string]$CandidateDirectory,
    [string]$RepositoryRoot = '',
    [string]$MainRunID = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$root = if ($RepositoryRoot) { [IO.Path]::GetFullPath($RepositoryRoot) } else { (Resolve-Path (Join-Path $PSScriptRoot '..')).Path }
Set-Location $root
$candidate = [IO.Path]::GetFullPath($CandidateDirectory)
& (Join-Path $PSScriptRoot 'release-candidate.ps1') -Mode Verify -SourceCommit $SourceCommit -RepositoryRoot $root -OutputDirectory $candidate
if ($LASTEXITCODE -ne 0) { throw 'release candidate verification failed' }

$native = 'miniapp-frida-native-17.3.2-abi1.1-windows-amd64.zip'
New-Item -ItemType Directory -Force (Join-Path $root 'dist\native'), (Join-Path $root 'third_party\zlib\src-1.3.1') | Out-Null
foreach ($name in @('miniapp-bridge.exe', 'miniapp-frida.dll', 'manifest.json')) {
    Copy-Item -LiteralPath (Join-Path $candidate $name) -Destination (Join-Path $root "dist\$name") -Force
}
Copy-Item -LiteralPath (Join-Path $candidate $native) -Destination (Join-Path $root "dist\native\$native") -Force
Copy-Item -LiteralPath (Join-Path $candidate 'ZLIB_LICENSE') -Destination (Join-Path $root 'third_party\zlib\src-1.3.1\LICENSE') -Force
$nativeHash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $root "dist\native\$native")).Hash.ToLowerInvariant()
[IO.File]::WriteAllText((Join-Path $root 'dist\native\SHA256SUMS'), "$nativeHash  $native$([char]10)", [Text.UTF8Encoding]::new($false))

& (Join-Path $PSScriptRoot 'package-windows-release.ps1') -Version $ReleaseTag -RepositoryRoot $root
if ($LASTEXITCODE -ne 0) { throw 'release packaging failed' }
$bundle = (Resolve-Path (Join-Path $root 'dist\release')).Path
$manifest = Get-Content -LiteralPath (Join-Path $bundle 'manifest.json') -Raw | ConvertFrom-Json
if ($manifest.nativeVersion -cne '17.3.2-abi1.1') { throw 'candidate native version does not match the release contract' }

$sbom = Join-Path $bundle 'miniapp-bridge.cdx.json'
Copy-Item -LiteralPath (Join-Path $candidate 'miniapp-bridge.cdx.json') -Destination $sbom -Force
$bom = Get-Content -LiteralPath $sbom -Raw | ConvertFrom-Json
if ($bom.bomFormat -cne 'CycloneDX' -or $bom.specVersion -cne '1.6') { throw 'candidate SBOM format is invalid' }
$bom.metadata.component | Add-Member -NotePropertyName version -NotePropertyValue $ReleaseTag -Force
$properties = @([ordered]@{ name = 'miniapp-bridge:source-revision'; value = $SourceCommit })
if ($MainRunID) { $properties += [ordered]@{ name = 'miniapp-bridge:main-ci-run'; value = $MainRunID } }
$bom.metadata | Add-Member -NotePropertyName properties -NotePropertyValue $properties -Force
[IO.File]::WriteAllText($sbom, (($bom | ConvertTo-Json -Depth 100) + [char]10), [Text.UTF8Encoding]::new($false))

$licenseFiles = [ordered]@{
    'LICENSE' = 'LICENSE'
    'THIRD_PARTY_NOTICES.md' = 'THIRD_PARTY_NOTICES.md'
    'FRIDA_COPYING' = 'FRIDA_COPYING'
    'FRIDA_COPYING.LIB' = 'FRIDA_COPYING.LIB'
    'ZLIB_LICENSE' = 'ZLIB_LICENSE'
}
foreach ($entry in $licenseFiles.GetEnumerator()) {
    Copy-Item -LiteralPath (Join-Path $candidate $entry.Value) -Destination (Join-Path $bundle $entry.Key) -Force
}

$subjects = @('manifest.json', "miniapp-bridge-$ReleaseTag-windows-amd64.zip", $native, 'miniapp-bridge.cdx.json') | ForEach-Object {
    [ordered]@{
        name = $_
        digest = [ordered]@{ sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $bundle $_)).Hash.ToLowerInvariant() }
    }
}
$provenance = [ordered]@{
    _type = 'https://in-toto.io/Statement/v1'
    subject = @($subjects)
    predicateType = 'https://slsa.dev/provenance/v1'
    predicate = [ordered]@{
        buildDefinition = [ordered]@{
            buildType = 'https://github.com/Follen/miniapp-bridge/.github/workflows/release.yml@v2'
            externalParameters = [ordered]@{ releaseTag = $ReleaseTag }
            internalParameters = [ordered]@{ promotedMainRun = $MainRunID }
            resolvedDependencies = @([ordered]@{
                uri = "git+https://github.com/$env:GITHUB_REPOSITORY@$SourceCommit"
                digest = [ordered]@{ gitCommit = $SourceCommit }
            })
        }
        runDetails = [ordered]@{
            builder = [ordered]@{ id = "$env:GITHUB_SERVER_URL/$env:GITHUB_REPOSITORY/actions/workflows/release.yml@$SourceCommit" }
            metadata = [ordered]@{ reproducible = $true }
            byproducts = @()
        }
    }
}
[IO.File]::WriteAllText((Join-Path $bundle 'provenance.intoto.json'), (($provenance | ConvertTo-Json -Depth 100) + [char]10), [Text.UTF8Encoding]::new($false))

$assets = @("miniapp-bridge-$ReleaseTag-windows-amd64.zip", $native, 'manifest.json', 'miniapp-bridge.cdx.json', 'provenance.intoto.json') + @($licenseFiles.Keys)
$lines = @($assets | Sort-Object | ForEach-Object {
    "$((Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $bundle $_)).Hash.ToLowerInvariant())  $_"
})
[IO.File]::WriteAllText((Join-Path $bundle 'SHA256SUMS'), (($lines -join [char]10) + [char]10), [Text.UTF8Encoding]::new($false))
$attestationSums = Join-Path $env:RUNNER_TEMP 'release-attestation-SHA256SUMS'
[IO.File]::WriteAllText($attestationSums, (($lines -join ([char]13 + [char]10)) + [char]13 + [char]10), [Text.UTF8Encoding]::new($false))
foreach ($line in Get-Content -LiteralPath (Join-Path $bundle 'SHA256SUMS')) {
    if ($line -cnotmatch '^([0-9a-f]{64})  ([^\\/]+)$') { throw "invalid release checksum line: $line" }
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $bundle $Matches[2])).Hash.ToLowerInvariant() -cne $Matches[1]) {
        throw "release checksum mismatch: $($Matches[2])"
    }
}
Write-Output "release_bundle=$bundle"
Write-Output "release_source_commit=$SourceCommit"
Write-Output "release_main_run_id=$MainRunID"
