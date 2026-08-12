param(
    [string]$ReportPath,
    [string]$GCCPath,
    [string]$GCovPath
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$source = Join-Path $repo 'internal\frida\shim\miniapp_frida.c'
$header = Join-Path $repo 'internal\frida\shim\miniapp_frida.h'
$fixtureRoot = Join-Path $PSScriptRoot 'cshim-coverage-fixtures'
$driver = Join-Path $fixtureRoot 'cshim-coverage-driver.c'
$stubs = Join-Path $fixtureRoot 'cshim-coverage-stubs.c'
$stubHeader = Join-Path $fixtureRoot 'cshim-coverage-stubs.h'
$fridaHeader = Join-Path $fixtureRoot 'cshim-coverage-frida-core.h'

foreach ($path in @($source, $header, $driver, $stubs, $stubHeader, $fridaHeader)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "C shim coverage input is missing: $path"
    }
}

$gccCommand = if ([string]::IsNullOrWhiteSpace($GCCPath)) {
    $command = Get-Command gcc.exe -ErrorAction SilentlyContinue
    if (-not $command) { $command = Get-Command gcc -ErrorAction SilentlyContinue }
    $command
} else {
    [pscustomobject]@{ Source = [IO.Path]::GetFullPath($GCCPath) }
}
$gcovCommand = if ([string]::IsNullOrWhiteSpace($GCovPath)) {
    $command = Get-Command gcov.exe -ErrorAction SilentlyContinue
    if (-not $command) { $command = Get-Command gcov -ErrorAction SilentlyContinue }
    $command
} else {
    [pscustomobject]@{ Source = [IO.Path]::GetFullPath($GCovPath) }
}
if (-not $gccCommand) { throw 'C shim coverage requires gcc/gcc.exe with gcov instrumentation support' }
if (-not $gcovCommand) { throw 'C shim coverage requires gcov/gcov.exe; coverage is not inferred or synthesized' }

$gcc = $gccCommand.Source
$gcov = $gcovCommand.Source
$gccVersion = (& $gcc -dumpfullversion).Trim()
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($gccVersion)) {
    throw "gcc version probe failed with exit $LASTEXITCODE"
}
$gcovVersionOutput = @(& $gcov --version)
$gcovVersionText = if ($gcovVersionOutput.Count -gt 0) { $gcovVersionOutput[0] } else { '' }
if ($LASTEXITCODE -ne 0 -or $gcovVersionText -notmatch '(?<version>[0-9]+(?:\.[0-9]+)+)') {
    throw "gcov version probe failed with exit $LASTEXITCODE"
}
$gcovVersion = $Matches.version
if (($gccVersion -split '\.')[0] -ne ($gcovVersion -split '\.')[0]) {
    throw "gcc/gcov major versions must match: gcc=$gccVersion gcov=$gcovVersion"
}

if ([string]::IsNullOrWhiteSpace($ReportPath)) {
    $ReportPath = Join-Path $repo 'ci-artifacts\c-shim-coverage.log'
} else {
    $ReportPath = [IO.Path]::GetFullPath($ReportPath)
}
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $ReportPath) | Out-Null

$work = Join-Path ([IO.Path]::GetTempPath()) ("miniapp-bridge-cshim-coverage-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null

function Invoke-NativeChecked([string]$Name, [string]$Command, [string[]]$Arguments) {
    $output = @(& $Command @Arguments 2>&1)
    $exitCode = $LASTEXITCODE
    $output | Write-Output
    if ($exitCode -ne 0) { throw "$Name failed with exit $exitCode" }
    return $output
}

function Get-GitProvenance {
    $headOutput = @(& git -C $repo rev-parse --verify HEAD 2>&1)
    if ($LASTEXITCODE -ne 0 -or $headOutput.Count -ne 1 -or $headOutput[0] -notmatch '^[0-9a-fA-F]{40}$') {
        throw "git HEAD probe failed: $($headOutput -join [Environment]::NewLine)"
    }
    $statusOutput = @(& git -C $repo status --porcelain --untracked-files=no 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "git dirty-state probe failed: $($statusOutput -join [Environment]::NewLine)"
    }
    return [ordered]@{
        head = ([string]$headOutput[0]).ToLowerInvariant()
        dirty = [bool]($statusOutput.Count -gt 0)
    }
}

function Get-SourceManifestEntry([string]$RelativePath) {
    if ([string]::IsNullOrWhiteSpace($RelativePath) -or
        [IO.Path]::IsPathRooted($RelativePath) -or
        $RelativePath.Contains('\') -or
        $RelativePath.Split('/') -contains '..' -or
        $RelativePath.Split('/') -contains '.') {
        throw "source manifest path is not canonical: $RelativePath"
    }
    $fullPath = [IO.Path]::GetFullPath((Join-Path $repo $RelativePath))
    $repoPrefix = $repo.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    if (-not $fullPath.StartsWith($repoPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "source manifest path escapes repository: $RelativePath"
    }
    $canonicalPath = [IO.Path]::GetRelativePath($repo, $fullPath).Replace('\', '/')
    if ($canonicalPath -cne $RelativePath -or -not (Test-Path -LiteralPath $fullPath -PathType Leaf)) {
        throw "source manifest path is missing or not canonical: $RelativePath"
    }
    return [ordered]@{
        path = $canonicalPath
        bytes = [int64](Get-Item -LiteralPath $fullPath).Length
        sha256 = (Get-FileHash -LiteralPath $fullPath -Algorithm SHA256).Hash.ToUpperInvariant()
    }
}

function Assert-SourceManifest([object[]]$Manifest) {
    if ($null -eq $Manifest -or $Manifest.Count -eq 0) {
        throw 'source manifest must not be empty'
    }
    $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($entry in $Manifest) {
        if (-not $seen.Add([string]$entry.path)) {
            throw "source manifest contains duplicate path: $($entry.path)"
        }
        $current = Get-SourceManifestEntry ([string]$entry.path)
        if ([int64]$entry.bytes -ne $current.bytes -or
            [string]$entry.sha256 -cne $current.sha256) {
            throw "source manifest no longer matches current file: $($entry.path)"
        }
    }
}

try {
    $coverageSourceDirectory = Join-Path $work 'internal\frida\shim'
    New-Item -ItemType Directory -Force -Path $coverageSourceDirectory | Out-Null
    Copy-Item -LiteralPath $source -Destination (Join-Path $coverageSourceDirectory 'miniapp_frida.c')
    Copy-Item -LiteralPath $header -Destination (Join-Path $coverageSourceDirectory 'miniapp_frida.h')
    Copy-Item -LiteralPath $fridaHeader -Destination (Join-Path $work 'frida-core.h')

    $includeWork = $work.Replace('\', '/')
    $common = @('-O0', '-g', '-fprofile-arcs', '-ftest-coverage', "-I$includeWork", "-I$fixtureRoot")
    Invoke-NativeChecked 'instrumented C shim compile' $gcc @(
        $common +
        @('-Iinternal/frida/shim', '-Dmalloc=mb_cov_malloc', '-Dcalloc=mb_cov_calloc', '-Dfree=mb_cov_free',
          '-c', 'internal/frida/shim/miniapp_frida.c', '-o', (Join-Path $work 'miniapp_frida.o'))
    ) | Out-Null
    Invoke-NativeChecked 'C shim coverage stub compile' $gcc @(
        $common + @('-c', $stubs, '-o', (Join-Path $work 'stubs.o'))
    ) | Out-Null
    Invoke-NativeChecked 'C shim coverage driver compile' $gcc @(
        $common + @('-Iinternal/frida/shim', '-c', $driver, '-o', (Join-Path $work 'driver.o'))
    ) | Out-Null
    Invoke-NativeChecked 'C shim coverage driver link' $gcc @(
        '-fprofile-arcs', '-ftest-coverage',
        (Join-Path $work 'miniapp_frida.o'),
        (Join-Path $work 'stubs.o'),
        (Join-Path $work 'driver.o'),
        '-o', (Join-Path $work 'cshim-coverage.exe')
    ) | Out-Null
    Invoke-NativeChecked 'C shim coverage driver' (Join-Path $work 'cshim-coverage.exe') @() | Out-Null

    Push-Location $work
    try {
        $gcovOutput = Invoke-NativeChecked 'gcov report' $gcov @(
            '-b', '-c', '-f', '-o', '.', 'internal/frida/shim/miniapp_frida.c'
        )
    } finally {
        Pop-Location
    }
    $lineSummary = @($gcovOutput | Where-Object { $_ -match '^Lines executed:(?<percent>[0-9]+(?:\.[0-9]+)?)% of (?<total>[0-9]+)$' }) | Select-Object -Last 1
    $branchSummary = @($gcovOutput | Where-Object { $_ -match '^Branches executed:(?<percent>[0-9]+(?:\.[0-9]+)?)% of (?<total>[0-9]+)$' }) | Select-Object -Last 1
    if (-not $lineSummary) { throw 'gcov did not emit a numeric line coverage summary for miniapp_frida.c' }
    if (-not $branchSummary) { throw 'gcov did not emit a numeric branch-site coverage summary for miniapp_frida.c' }
    $null = $lineSummary -match '^Lines executed:(?<percent>[0-9]+(?:\.[0-9]+)?)% of (?<total>[0-9]+)$'
    $linePercent = [decimal]$Matches.percent
    $lineTotal = [int]$Matches.total
    $null = $branchSummary -match '^Branches executed:(?<percent>[0-9]+(?:\.[0-9]+)?)% of (?<total>[0-9]+)$'
    $branchSitePercent = [decimal]$Matches.percent
    $branchTotal = [int]$Matches.total
    $functionTotal = 0
    $functionMissed = 0
    $inFunction = $false
    foreach ($line in $gcovOutput) {
        if ($line -match '^\s*Function\s+''[^'']+''') {
            $functionTotal++
            $inFunction = $true
            continue
        }
        if ($inFunction -and $line -match '^Lines executed:(?<percent>[0-9]+(?:\.[0-9]+)?)% of (?<total>[0-9]+)$') {
            if ([decimal]$Matches.percent -ne 100.00) { $functionMissed++ }
            $inFunction = $false
        }
    }
    if ($functionTotal -le 0 -or $inFunction) { throw 'gcov did not emit complete per-function coverage summaries for miniapp_frida.c' }
    $functionPercent = if ($functionMissed -eq 0) { [decimal]100.00 } else { [decimal]((($functionTotal - $functionMissed) * 100.0) / $functionTotal) }
    if ($lineTotal -le 0 -or $linePercent -ne 100.00) {
        throw "C shim line coverage must be 100.00%; got $linePercent% of $lineTotal lines"
    }
    if ($branchTotal -le 0 -or $branchSitePercent -ne 100.00) {
        throw "C shim branch sites must all execute; got $branchSitePercent% of $branchTotal sites"
    }
    if ($functionTotal -le 0 -or $functionPercent -ne 100.00) {
        throw "C shim function coverage must be 100.00%; got $functionPercent% of $functionTotal functions (missed=$functionMissed)"
    }

    $gitProvenance = Get-GitProvenance
    $sourceManifest = @(
        Get-SourceManifestEntry 'internal/frida/shim/miniapp_frida.c'
        Get-SourceManifestEntry 'internal/frida/shim/miniapp_frida.h'
    )
    Assert-SourceManifest $sourceManifest
    $report = [ordered]@{
        schema = 'miniapp_bridge.coverage.v1'
        language = 'C'
        source = 'internal/frida/shim/miniapp_frida.c'
        tool = 'gcov'
        tool_version = $gcovVersion
        git_head = $gitProvenance.head
        git_dirty = $gitProvenance.dirty
        source_manifest = $sourceManifest
        line_percent = [double]$linePercent
        line_total = $lineTotal
        branch_site_percent = [double]$branchSitePercent
        branch_site_total = $branchTotal
        function_percent = [double]$functionPercent
        function_total = $functionTotal
        threshold_percent = 100.0
        result = 'passed'
    }
    $json = $report | ConvertTo-Json -Compress
    [IO.File]::WriteAllText($ReportPath, $json, [Text.UTF8Encoding]::new($false))
    $persistedReport = Get-Content -LiteralPath $ReportPath -Raw | ConvertFrom-Json
    if ($persistedReport.schema -cne 'miniapp_bridge.coverage.v1' -or
        $persistedReport.language -cne 'C' -or
        $persistedReport.git_head -cne $gitProvenance.head -or
        [bool]$persistedReport.git_dirty -ne $gitProvenance.dirty -or
        [double]$persistedReport.line_percent -ne 100.0 -or
        [double]$persistedReport.branch_site_percent -ne 100.0 -or
        [double]$persistedReport.function_percent -ne 100.0 -or
        [double]$persistedReport.threshold_percent -ne 100.0 -or
        $persistedReport.result -cne 'passed') {
        throw 'persisted C shim coverage report failed provenance or coverage validation'
    }
    Assert-SourceManifest @($persistedReport.source_manifest)
    Get-Content -LiteralPath $ReportPath | Write-Output
    Write-Output "c_shim_line_coverage=100.00%; c_shim_function_coverage=100.00%; c_shim_branch_sites_executed=100.00%; report=$ReportPath"
} finally {
    if (Test-Path -LiteralPath $work) {
        Remove-Item -LiteralPath $work -Recurse -Force
    }
}
