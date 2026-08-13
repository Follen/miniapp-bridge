param(
    [ValidateSet('All', 'Go', 'C')]
    [string]$Mode = 'All',
    [switch]$UseExistingNative
)

$ErrorActionPreference = 'Stop'

$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
Set-Location $repo

function Run([string]$name, [scriptblock]$command) {
    Write-Output "=== $name ==="
    & $command
    if ($LASTEXITCODE -ne 0) { throw "$name failed with exit $LASTEXITCODE" }
}

function Get-GitProvenance {
    $head = (& git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $head -notmatch '^[0-9a-f]{40}$') { throw 'could not record Git HEAD for Go coverage' }
    $status = @(& git status --porcelain --untracked-files=no)
    if ($LASTEXITCODE -ne 0) { throw 'could not record Git dirty state for Go coverage' }
    return [ordered]@{ head = $head; dirty = [bool]($status.Count -gt 0) }
}

function ConvertFrom-GoListJson {
    param([string[]]$Arguments)
    $lines = @(& go list -json @Arguments)
    if ($LASTEXITCODE -ne 0) { throw "go list failed for: $($Arguments -join ' ')" }
    $objects = @()
    $buffer = [Text.StringBuilder]::new()
    foreach ($line in $lines) {
        [void]$buffer.AppendLine($line)
        if ($line -eq '}') {
            $objects += ($buffer.ToString() | ConvertFrom-Json)
            [void]$buffer.Clear()
        }
    }
    if ($buffer.Length -ne 0 -or $objects.Count -eq 0) { throw "go list returned invalid JSON for: $($Arguments -join ' ')" }
    return $objects
}

function Get-GoSourceManifest {
    $scopes = @(
        @('./cmd/...', './frida'),
        @('./internal/...'),
        @('./scripts/smoke-process-runner'),
        @('./sdk'),
        @('-tags', 'frida', './internal/...', './sdk')
    )
    $sources = @{}
    $repoRoot = ([IO.Path]::GetFullPath($repo)).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    foreach ($scope in $scopes) {
        foreach ($package in @(ConvertFrom-GoListJson -Arguments $scope)) {
            foreach ($name in @($package.GoFiles) + @($package.CgoFiles)) {
                if ([string]::IsNullOrWhiteSpace([string]$name) -or $name.EndsWith('_test.go', [StringComparison]::OrdinalIgnoreCase)) { continue }
                $full = [IO.Path]::GetFullPath((Join-Path ([string]$package.Dir) ([string]$name)))
                if (-not $full.StartsWith($repoRoot, [StringComparison]::OrdinalIgnoreCase)) { throw "Go coverage source is outside repository: $full" }
                $relative = $full.Substring($repoRoot.Length).Replace('\', '/')
                if ($sources.ContainsKey($relative)) {
                    if ($sources[$relative] -ne $full) { throw "Go coverage source has duplicate canonical path: $relative" }
                } else {
                    $sources[$relative] = $full
                }
            }
        }
    }
    if ($sources.Count -eq 0) { throw 'Go coverage source manifest is empty' }
    return @($sources.Keys | Sort-Object | ForEach-Object {
            $item = Get-Item -LiteralPath $sources[$_]
            [ordered]@{
                path = $_
                bytes = [int64]$item.Length
                sha256 = (Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
            }
        })
}

function Write-GoCoverageReport {
    param(
        [object[]]$Profiles,
        [object]$GitProvenance,
        [object[]]$SourceManifest
    )
    $reportPath = Join-Path $repo 'ci-artifacts\go-coverage.log'
    $report = [ordered]@{
        schema = 'miniapp_bridge.coverage.v1'
        language = 'Go'
        tool = 'go test coverprofile + go tool cover'
        git_head = $GitProvenance.head
        git_dirty = $GitProvenance.dirty
        profile_count = [int]$Profiles.Count
        profiles = @($Profiles)
        source_count = [int]$SourceManifest.Count
        source_manifest = $SourceManifest
        result = 'passed'
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $reportPath) | Out-Null
    [IO.File]::WriteAllText($reportPath, ($report | ConvertTo-Json -Depth 8 -Compress), [Text.UTF8Encoding]::new($false))

    $verified = Get-Content -LiteralPath $reportPath -Raw | ConvertFrom-Json
    if ($verified.schema -ne 'miniapp_bridge.coverage.v1' -or $verified.language -ne 'Go' -or $verified.result -ne 'passed') { throw 'Go coverage report identity validation failed' }
    $currentGit = Get-GitProvenance
    if ($verified.git_head -ne $GitProvenance.head -or [bool]$verified.git_dirty -ne [bool]$GitProvenance.dirty -or $currentGit.head -ne $GitProvenance.head -or $currentGit.dirty -ne $GitProvenance.dirty) { throw 'Go coverage Git provenance changed during coverage execution' }
    if ([int]$verified.profile_count -ne 5 -or @($verified.profiles).Count -ne 5 -or @($verified.profiles | Where-Object { [double]$_.statement_percent -ne 100.0 }).Count -ne 0) { throw 'Go coverage report profile validation failed' }
    $expectedProfiles = @('internal', 'public_cli_frida', 'sdk', 'smoke_process_runner', 'tagged_internal_sdk')
    $actualProfiles = @($verified.profiles | ForEach-Object { [string]$_.name } | Sort-Object -Unique)
    if ($actualProfiles.Count -ne $expectedProfiles.Count -or (Compare-Object $expectedProfiles $actualProfiles).Count -ne 0) { throw 'Go coverage report profile names are incomplete or duplicated' }
    if ([int]$verified.source_count -ne @($verified.source_manifest).Count -or [int]$verified.source_count -eq 0) { throw 'Go coverage report source manifest validation failed' }
    $seen = @{}
    foreach ($source in @($verified.source_manifest)) {
        $relative = ([string]$source.path).Replace('\', '/')
        if ($relative -ne [string]$source.path -or $relative.EndsWith('_test.go', [StringComparison]::OrdinalIgnoreCase) -or $seen.ContainsKey($relative)) { throw "Go coverage report has invalid source path: $relative" }
        $seen[$relative] = $true
        $full = [IO.Path]::GetFullPath((Join-Path $repo $relative))
        $repoRoot = ([IO.Path]::GetFullPath($repo)).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
        if (-not $full.StartsWith($repoRoot, [StringComparison]::OrdinalIgnoreCase)) { throw "Go coverage report source is outside repository: $relative" }
        $item = Get-Item -LiteralPath $full
        $hash = (Get-FileHash -LiteralPath $full -Algorithm SHA256).Hash.ToLowerInvariant()
        if ([int64]$source.bytes -ne [int64]$item.Length -or [string]$source.sha256 -ne $hash) { throw "Go coverage source changed after report generation: $relative" }
    }
    Get-Content -LiteralPath $reportPath | Write-Output
}

if ($Mode -eq 'C') {
    Run 'C shim native line and branch-site coverage' {
        & (Join-Path $PSScriptRoot 'cshim-coverage.ps1') -ReportPath (Join-Path $repo 'ci-artifacts\c-shim-coverage.log')
        if ($LASTEXITCODE -ne 0) { throw "C shim coverage failed with exit $LASTEXITCODE" }
    }
    Write-Output 'coverage_gate=c_shim_lines=100.0%; c_shim_branch_sites=100.0%'
    exit 0
}

$messages = Get-Content testdata/golden/reference_messages.json -Raw | ConvertFrom-Json
if ($messages.fixtures.Count -ne 55) { throw "expected 55 protobuf fixtures, got $($messages.fixtures.Count)" }
$fieldCount = ($messages.fixtures | ForEach-Object { $_.fields.Count } | Measure-Object -Sum).Sum
if ($fieldCount -ne 131) { throw "expected 131 field fixtures, got $fieldCount" }
if (($messages.fixtures | Where-Object { $_.explicit_zero }).Count -ne 55) { throw 'explicit-zero coverage is incomplete' }
if ($messages.corrupt_inputs.Count -ne 6) { throw 'corrupt protobuf coverage is incomplete' }

$codex = Get-Content testdata/golden/reference_codex.json -Raw | ConvertFrom-Json
if ($codex.debug_wrap.Count -ne 7 -or $codex.developer_outgoing.Count -ne 11 -or $codex.client_response_frames.Count -ne 10 -or $codex.incoming_frames.Count -ne 26) {
    throw 'Codex category/command fixture coverage is incomplete'
}

$matrix = Get-Content docs/behavior-matrix.md -Encoding UTF8 -Raw
$rows = @($matrix -split "`n" | Where-Object { $_ -match '^\|' -and $_ -notmatch '^\|---' } | Select-Object -Skip 1)
$implemented = -join @([char]0x5df2, [char]0x5b9e, [char]0x73b0)
if ($rows.Count -ne 13 -or @($rows | Where-Object { $_ -notmatch $implemented }).Count -ne 0) {
    throw 'behavior matrix is incomplete'
}

$coverageGitProvenance = Get-GitProvenance
$coverageSourceManifest = @(Get-GoSourceManifest)
$coverageProfiles = @()
Run 'unit' { go test ./... -count=1 -timeout 600s }
$publicCoverage = Join-Path $env:TEMP "miniapp-bridge-public-coverage-$([guid]::NewGuid().ToString('N')).out"
try {
    Run 'public CLI and Frida statement coverage' { go test ./cmd/... ./frida -count=1 -timeout 90s "-coverprofile=$publicCoverage" }
    $publicReport = & go tool cover "-func=$publicCoverage"
    if ($LASTEXITCODE -ne 0) { throw "public coverage report failed with exit $LASTEXITCODE" }
    $publicReport | Write-Output
    $publicTotal = $publicReport | Select-Object -Last 1
    if ($publicTotal -notmatch '^total:\s+\(statements\)\s+100\.0%$') {
        throw "public CLI and Frida Go statement coverage must be 100.0%; got: $publicTotal"
    }
    $coverageProfiles += [ordered]@{ name = 'public_cli_frida'; statement_percent = 100.0 }
} finally {
    Remove-Item -LiteralPath $publicCoverage -Force -ErrorAction SilentlyContinue
}
$coverage = Join-Path $env:TEMP "miniapp-bridge-coverage-$([guid]::NewGuid().ToString('N')).out"
try {
    Run 'statement coverage' { go test ./internal/... -count=1 -timeout 90s "-coverprofile=$coverage" }
    $coverReport = & go tool cover "-func=$coverage"
    if ($LASTEXITCODE -ne 0) { throw "coverage report failed with exit $LASTEXITCODE" }
    $coverReport | Write-Output
    $total = $coverReport | Select-Object -Last 1
    if ($total -notmatch '^total:\s+\(statements\)\s+100\.0%$') {
        throw "Go statement coverage must be 100.0%; got: $total"
    }
    $coverageProfiles += [ordered]@{ name = 'internal'; statement_percent = 100.0 }
} finally {
    Remove-Item -LiteralPath $coverage -Force -ErrorAction SilentlyContinue
}
$runnerCoverage = Join-Path $env:TEMP "miniapp-bridge-runner-coverage-$([guid]::NewGuid().ToString('N')).out"
try {
    Run 'smoke runner statement coverage' { go test ./scripts/smoke-process-runner -count=1 -timeout 90s "-coverprofile=$runnerCoverage" }
    $runnerReport = & go tool cover "-func=$runnerCoverage"
    if ($LASTEXITCODE -ne 0) { throw "smoke runner coverage report failed with exit $LASTEXITCODE" }
    $runnerReport | Write-Output
    $runnerTotal = $runnerReport | Select-Object -Last 1
    if ($runnerTotal -notmatch '^total:\s+\(statements\)\s+100\.0%$') {
        throw "smoke runner Go statement coverage must be 100.0%; got: $runnerTotal"
    }
    $coverageProfiles += [ordered]@{ name = 'smoke_process_runner'; statement_percent = 100.0 }
} finally {
    Remove-Item -LiteralPath $runnerCoverage -Force -ErrorAction SilentlyContinue
}
$sdkCoverage = Join-Path $env:TEMP "miniapp-bridge-sdk-coverage-$([guid]::NewGuid().ToString('N')).out"
try {
    Run 'sdk statement coverage' { go test ./sdk -count=1 -timeout 90s "-coverprofile=$sdkCoverage" }
    $sdkReport = & go tool cover "-func=$sdkCoverage"
    if ($LASTEXITCODE -ne 0) { throw "sdk coverage report failed with exit $LASTEXITCODE" }
    $sdkReport | Write-Output
    $sdkTotal = $sdkReport | Select-Object -Last 1
    if ($sdkTotal -notmatch '^total:\s+\(statements\)\s+100\.0%$') {
        throw "SDK Go statement coverage must be 100.0%; got: $sdkTotal"
    }
    $coverageProfiles += [ordered]@{ name = 'sdk'; statement_percent = 100.0 }
} finally {
    Remove-Item -LiteralPath $sdkCoverage -Force -ErrorAction SilentlyContinue
}
$taggedCoverage = Join-Path $env:TEMP "miniapp-bridge-coverage-frida-$([guid]::NewGuid().ToString('N')).out"
$oldPath = $env:PATH
$oldNativePath = $env:MINIAPP_BRIDGE_NATIVE_PATH
try {
    if (-not $UseExistingNative) {
        Run 'native shim build' { & $PSScriptRoot\build-frida-shim.ps1 }
    }
    $runtime = (Resolve-Path (Join-Path $repo 'third_party\frida\runtime-17.3.2')).Path
    $env:PATH = $runtime + ';' + $env:PATH
    $env:MINIAPP_BRIDGE_NATIVE_PATH = Join-Path $runtime 'miniapp-frida.dll'
    Run 'tagged internal and SDK statement coverage' { go test -tags frida ./internal/... ./sdk -count=1 -timeout 180s "-coverprofile=$taggedCoverage" }
    $taggedReport = & go tool cover "-func=$taggedCoverage"
    if ($LASTEXITCODE -ne 0) { throw "tagged coverage report failed with exit $LASTEXITCODE" }
    $taggedReport | Write-Output
    $taggedTotal = $taggedReport | Select-Object -Last 1
    if ($taggedTotal -notmatch '^total:\s+\(statements\)\s+100\.0%$') {
        throw "tagged Go statement coverage must be 100.0%; got: $taggedTotal"
    }
    $coverageProfiles += [ordered]@{ name = 'tagged_internal_sdk'; statement_percent = 100.0 }
    Run 'tagged race' { go test -tags frida -race -p 1 ./... -count=1 -timeout 900s }
} finally {
    $env:PATH = $oldPath
    $env:MINIAPP_BRIDGE_NATIVE_PATH = $oldNativePath
    Remove-Item -LiteralPath $taggedCoverage -Force -ErrorAction SilentlyContinue
}
Run 'race' { go test -race ./... -count=1 -timeout 420s }
Run 'vet' { go vet ./... }
Write-GoCoverageReport -Profiles $coverageProfiles -GitProvenance $coverageGitProvenance -SourceManifest $coverageSourceManifest
if ($Mode -eq 'All') {
    Run 'C shim native line and branch-site coverage' {
        & (Join-Path $PSScriptRoot 'cshim-coverage.ps1') -ReportPath (Join-Path $repo 'ci-artifacts\c-shim-coverage.log')
        if ($LASTEXITCODE -ne 0) { throw "C shim coverage failed with exit $LASTEXITCODE" }
    }
    Write-Output 'coverage_gate=100% reference behaviors; cli_frida_go_statements=100.0%; internal_go_statements=100.0%; sdk_go_statements=100.0%; tagged_internal_sdk_go_statements=100.0%; smoke_runner_go_statements=100.0%; c_shim_lines=100.0%; c_shim_branch_sites=100.0%; unit/race/tagged-race/vet=passed'
} else {
    Write-Output 'coverage_gate=Go reference behaviors; cli_frida_go_statements=100.0%; internal_go_statements=100.0%; sdk_go_statements=100.0%; tagged_internal_sdk_go_statements=100.0%; smoke_runner_go_statements=100.0%; unit/race/tagged-race/vet=passed'
}
