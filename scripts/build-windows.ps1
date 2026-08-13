param(
    [switch]$CoverageExportMismatchFixture,
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
$env:CGO_ENABLED = '1'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
Set-Location $repo

function Assert-NativeExports {
    param(
        [string[]]$Required,
        [string[]]$Actual
    )
    $missing = @($Required | Where-Object { $_ -notin $Actual })
    $unexpected = @($Actual | Where-Object { $_ -notin $Required })
    if ($missing.Count -ne 0 -or $unexpected.Count -ne 0) {
        throw "native DLL export mismatch; missing=$($missing -join ','); unexpected=$($unexpected -join ',')"
    }
}

if ($CoverageExportMismatchFixture) {
    Assert-NativeExports -Required @('mb_fixture_required') -Actual @()
}
New-Item -ItemType Directory -Force -Path dist | Out-Null
& $PSScriptRoot\build-frida-shim.ps1
$runtime = Resolve-Path third_party/frida/runtime-17.3.2
$env:PATH = "$runtime;$env:PATH"
$env:MINIAPP_BRIDGE_NATIVE_PATH = (Join-Path $runtime 'miniapp-frida.dll')
if (-not $SkipTests) {
    go test -tags frida -race ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw "native tests failed with exit $LASTEXITCODE" }
}
Copy-Item third_party/frida/runtime-17.3.2/miniapp-frida.dll dist/miniapp-frida.dll -Force
& $PSScriptRoot\native-release.ps1 -RuntimeDirectory $runtime -OutputDirectory (Join-Path $repo 'dist\native') -ManifestOutputDirectory (Join-Path $repo 'dist')
if ($LASTEXITCODE -ne 0) { throw "native release packaging failed with exit $LASTEXITCODE" }
$manifest = Get-Content 'dist\manifest.json' -Raw | ConvertFrom-Json
$nativeAsset = Join-Path $repo "dist\native\miniapp-frida-native-$($manifest.nativeVersion)-windows-amd64.zip"
$archiveHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $nativeAsset).Hash.ToUpperInvariant()
$generatedTrust = Join-Path $repo 'internal\native\trust_native_generated.go'
$trustSource = @"
//go:build native_generated

package native

const (
`tNativeDLLSize = int64($($manifest.size))
`tNativeDLLSHA256 = "$([string]$manifest.sha256)"
`tNativeArchiveSHA256 = "$archiveHash"
)
"@
try {
    [IO.File]::WriteAllText($generatedTrust, $trustSource, [Text.UTF8Encoding]::new($false))
    go test -tags 'frida,native_generated' ./internal/native ./sdk -run '^$'
    if ($LASTEXITCODE -ne 0) { throw "generated trust-root compile check failed with exit $LASTEXITCODE" }
    go build -tags 'frida,native_generated' -trimpath -o dist/miniapp-bridge.exe ./cmd/miniapp-bridge
    if ($LASTEXITCODE -ne 0) { throw "native build failed with exit $LASTEXITCODE" }
}
finally {
    Remove-Item -LiteralPath $generatedTrust -Force -ErrorAction SilentlyContinue
}
$vsdev = 'C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\Common7\Tools\VsDevCmd.bat'
$dll = (Resolve-Path 'dist\miniapp-frida.dll').Path
$exportCommand = 'call "{0}" -arch=x64 -host_arch=x64 >nul && dumpbin /exports "{1}"' -f $vsdev, $dll
$exports = cmd.exe /d /s /c $exportCommand
if ($LASTEXITCODE -ne 0) { throw "dumpbin exports failed with exit $LASTEXITCODE" }
$actualExports = @($exports | ForEach-Object {
    if ($_ -match '^\s*\d+\s+[0-9A-Fa-f]+\s+[0-9A-Fa-f]+\s+(mb_[A-Za-z0-9_]+)(?:\s|$)') { $Matches[1] }
} | Sort-Object -Unique)
$missingExports = @($manifest.requiredExports | Where-Object { $_ -notin $actualExports })
$unexpectedExports = @($actualExports | Where-Object { $_ -notin $manifest.requiredExports })
Assert-NativeExports -Required @($manifest.requiredExports) -Actual $actualExports
$dependentCommand = 'call "{0}" -arch=x64 -host_arch=x64 >nul && dumpbin /dependents "{1}"' -f $vsdev, $dll
$dependents = cmd.exe /d /s /c $dependentCommand
if ($LASTEXITCODE -ne 0) { throw "dumpbin dependents failed with exit $LASTEXITCODE" }
if ($dependents -match '(?i)zlib1\.dll') { throw 'native DLL must not depend on zlib1.dll' }
Get-FileHash -Algorithm SHA256 dist/miniapp-bridge.exe
Get-FileHash -Algorithm SHA256 dist/miniapp-frida.dll
Get-FileHash -Algorithm SHA256 dist/manifest.json
