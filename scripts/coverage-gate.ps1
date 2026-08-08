$ErrorActionPreference = 'Stop'

$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
Set-Location $repo

function Run([string]$name, [scriptblock]$command) {
    Write-Output "=== $name ==="
    & $command
    if ($LASTEXITCODE -ne 0) { throw "$name failed with exit $LASTEXITCODE" }
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

$matrix = Get-Content docs/behavior-matrix.md -Raw
$rows = $matrix -split "`n" | Where-Object { $_ -match '^\|' -and $_ -notmatch '^\|---' } | Select-Object -Skip 1
$implemented = ([char]0x5df2) + ([char]0x5b9e) + ([char]0x73b0)
if ($rows.Count -ne 13 -or ($rows | Where-Object { $_ -notmatch $implemented }).Count -ne 0) {
    throw 'behavior matrix is incomplete'
}

Run 'unit' { go test ./... -count=1 -timeout 90s }
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
} finally {
    Remove-Item -LiteralPath $runnerCoverage -Force -ErrorAction SilentlyContinue
}
$taggedCoverage = Join-Path $env:TEMP "miniapp-bridge-coverage-frida-$([guid]::NewGuid().ToString('N')).out"
$oldPath = $env:PATH
try {
    $dist = Join-Path $repo 'dist'
    if (Test-Path $dist) { $env:PATH = (Resolve-Path $dist).Path + ';' + $env:PATH }
    Run 'tagged statement coverage' { go test -tags frida ./internal/... -count=1 -timeout 180s "-coverprofile=$taggedCoverage" }
    $taggedReport = & go tool cover "-func=$taggedCoverage"
    if ($LASTEXITCODE -ne 0) { throw "tagged coverage report failed with exit $LASTEXITCODE" }
    $taggedReport | Write-Output
    $taggedTotal = $taggedReport | Select-Object -Last 1
    if ($taggedTotal -notmatch '^total:\s+\(statements\)\s+100\.0%$') {
        throw "tagged Go statement coverage must be 100.0%; got: $taggedTotal"
    }
} finally {
    $env:PATH = $oldPath
    Remove-Item -LiteralPath $taggedCoverage -Force -ErrorAction SilentlyContinue
}
Run 'race' { go test -race ./... -count=1 -timeout 180s }
Run 'vet' { go vet ./... }
Write-Output 'coverage_gate=100% reference behaviors; internal_go_statements=100.0%; smoke_runner_go_statements=100.0%; unit/race/vet=passed'
