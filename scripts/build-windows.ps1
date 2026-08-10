$ErrorActionPreference = 'Stop'
$env:CGO_ENABLED = '1'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
Set-Location $repo
New-Item -ItemType Directory -Force -Path dist | Out-Null
& $PSScriptRoot\build-frida-shim.ps1
$runtime = Resolve-Path third_party/frida/runtime-17.3.2
$env:PATH = "$runtime;$env:PATH"
$env:MINIAPP_BRIDGE_NATIVE_PATH = (Join-Path $runtime 'miniapp-frida.dll')
go test -tags frida -race ./... -count=1
if ($LASTEXITCODE -ne 0) { throw "native tests failed with exit $LASTEXITCODE" }
go build -tags frida -trimpath -o dist/miniapp-bridge.exe ./cmd/miniapp-bridge
if ($LASTEXITCODE -ne 0) { throw "native build failed with exit $LASTEXITCODE" }
Copy-Item third_party/frida/runtime-17.3.2/miniapp-frida.dll dist/miniapp-frida.dll -Force
& $PSScriptRoot\native-release.ps1 -RuntimeDirectory $runtime -OutputDirectory (Join-Path $repo 'dist\native') -ManifestOutputDirectory (Join-Path $repo 'dist')
if ($LASTEXITCODE -ne 0) { throw "native release packaging failed with exit $LASTEXITCODE" }
$vsdev = 'C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\Common7\Tools\VsDevCmd.bat'
$dll = (Resolve-Path 'dist\miniapp-frida.dll').Path
$exportCommand = 'call "{0}" -arch=x64 -host_arch=x64 >nul && dumpbin /exports "{1}"' -f $vsdev, $dll
$exports = cmd.exe /d /s /c $exportCommand
if ($LASTEXITCODE -ne 0) { throw "dumpbin exports failed with exit $LASTEXITCODE" }
$manifest = Get-Content 'dist\manifest.json' -Raw | ConvertFrom-Json
$actualExports = @($exports | ForEach-Object {
    if ($_ -match '^\s*\d+\s+[0-9A-Fa-f]+\s+[0-9A-Fa-f]+\s+(mb_[A-Za-z0-9_]+)(?:\s|$)') { $Matches[1] }
} | Sort-Object -Unique)
$missingExports = @($manifest.requiredExports | Where-Object { $_ -notin $actualExports })
$unexpectedExports = @($actualExports | Where-Object { $_ -notin $manifest.requiredExports })
if ($missingExports.Count -ne 0 -or $unexpectedExports.Count -ne 0) {
    throw "native DLL export mismatch; missing=$($missingExports -join ','); unexpected=$($unexpectedExports -join ',')"
}
$dependentCommand = 'call "{0}" -arch=x64 -host_arch=x64 >nul && dumpbin /dependents "{1}"' -f $vsdev, $dll
$dependents = cmd.exe /d /s /c $dependentCommand
if ($LASTEXITCODE -ne 0) { throw "dumpbin dependents failed with exit $LASTEXITCODE" }
if ($dependents -match '(?i)zlib1\.dll') { throw 'native DLL must not depend on zlib1.dll' }
Get-FileHash -Algorithm SHA256 dist/miniapp-bridge.exe
Get-FileHash -Algorithm SHA256 dist/miniapp-frida.dll
Get-FileHash -Algorithm SHA256 dist/manifest.json
