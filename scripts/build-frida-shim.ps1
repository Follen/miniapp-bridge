$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$devkit = Join-Path $root 'third_party\frida\devkit-17.3.2'
$runtime = Join-Path $root 'third_party\frida\runtime-17.3.2'
$shim = Join-Path $root 'internal\frida\shim'
$vsdev = 'C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\Common7\Tools\VsDevCmd.bat'

foreach ($path in @((Join-Path $devkit 'frida-core.h'), (Join-Path $devkit 'frida-core.lib'), $vsdev)) {
    if (-not (Test-Path $path)) { throw "required Frida/MSVC file missing: $path" }
}
New-Item -ItemType Directory -Force -Path $runtime | Out-Null

$command = 'call "{0}" -arch=x64 -host_arch=x64 >nul && cl /nologo /LD /MT /O2 /utf-8 /Fo"{8}" /I"{1}" /I"{2}" "{3}" "{4}" setupapi.lib /link /Brepro /DEF:"{5}" /OUT:"{6}" /IMPLIB:"{7}"' -f `
    $vsdev, $devkit, $shim, (Join-Path $shim 'miniapp_frida.c'), (Join-Path $devkit 'frida-core.lib'), `
    (Join-Path $shim 'miniapp_frida.def'), (Join-Path $runtime 'miniapp-frida.dll'), (Join-Path $runtime 'miniapp-frida.lib'), (Join-Path $runtime 'miniapp_frida.obj')
cmd.exe /d /s /c $command
if ($LASTEXITCODE -ne 0) { throw "MSVC shim build failed with exit $LASTEXITCODE" }

& dlltool -d (Join-Path $shim 'miniapp_frida.def') -l (Join-Path $runtime 'libminiapp-frida.a')
if ($LASTEXITCODE -ne 0) { throw "dlltool import library build failed with exit $LASTEXITCODE" }

Get-FileHash -Algorithm SHA256 (Join-Path $runtime 'miniapp-frida.dll')
