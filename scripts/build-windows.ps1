$ErrorActionPreference = 'Stop'
$env:CGO_ENABLED = '1'
New-Item -ItemType Directory -Force -Path dist | Out-Null
& $PSScriptRoot\build-zlib.ps1
& $PSScriptRoot\build-frida-shim.ps1
$runtime = Resolve-Path third_party/frida/runtime-17.3.2
$env:PATH = "$runtime;$env:PATH"
go test -tags frida ./...
if ($LASTEXITCODE -ne 0) { throw "native tests failed with exit $LASTEXITCODE" }
go build -tags frida -trimpath -o dist/miniapp-bridge.exe ./cmd/miniapp-bridge
if ($LASTEXITCODE -ne 0) { throw "native build failed with exit $LASTEXITCODE" }
Copy-Item third_party/frida/runtime-17.3.2/miniapp-frida.dll dist/miniapp-frida.dll -Force
Get-FileHash -Algorithm SHA256 dist/miniapp-bridge.exe
Get-FileHash -Algorithm SHA256 dist/miniapp-frida.dll
