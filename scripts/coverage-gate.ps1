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

$matrix = Get-Content docs/behavior-matrix.md -Encoding UTF8 -Raw
$rows = $matrix -split "`n" | Where-Object { $_ -match '^\|' -and $_ -notmatch '^\|---' } | Select-Object -Skip 1
$implemented = -join @([char]0x5df2, [char]0x5b9e, [char]0x73b0)
if ($rows.Count -ne 13 -or ($rows | Where-Object { $_ -notmatch $implemented }).Count -ne 0) {
    throw 'behavior matrix is incomplete'
}

Run 'unit' { go test ./... -count=1 -timeout 180s }
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
} finally {
    Remove-Item -LiteralPath $sdkCoverage -Force -ErrorAction SilentlyContinue
}
$taggedCoverage = Join-Path $env:TEMP "miniapp-bridge-coverage-frida-$([guid]::NewGuid().ToString('N')).out"
$oldPath = $env:PATH
$oldNativePath = $env:MINIAPP_BRIDGE_NATIVE_PATH
try {
    Run 'native shim build' { & $PSScriptRoot\build-frida-shim.ps1 }
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
    Run 'tagged race' { go test -tags frida -race ./... -count=1 -timeout 240s }
} finally {
    $env:PATH = $oldPath
    $env:MINIAPP_BRIDGE_NATIVE_PATH = $oldNativePath
    Remove-Item -LiteralPath $taggedCoverage -Force -ErrorAction SilentlyContinue
}
Run 'race' { go test -race ./... -count=1 -timeout 180s }
Run 'vet' { go vet ./... }
Write-Output 'coverage_gate=100% reference behaviors; cli_frida_go_statements=100.0%; internal_go_statements=100.0%; sdk_go_statements=100.0%; tagged_internal_sdk_go_statements=100.0%; smoke_runner_go_statements=100.0%; unit/race/tagged-race/vet=passed'
