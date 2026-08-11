$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$ensure = Join-Path $PSScriptRoot 'ensure-frida-devkit.ps1'
Write-Host 'Frida bootstrap: begin'
& $ensure
Write-Host 'Frida bootstrap: complete'
Write-Host 'zlib bootstrap: begin'
& (Join-Path $PSScriptRoot 'build-zlib.ps1')
Write-Host 'zlib bootstrap: complete'
$devkit = Join-Path $root 'third_party\frida\devkit-17.3.2'
$runtime = Join-Path $root 'third_party\frida\runtime-17.3.2'
$shim = Join-Path $root 'internal\frida\shim'
$zlibSource = Join-Path $root 'third_party\zlib\src-1.3.1'
$zlibObjectDirectory = Join-Path ([IO.Path]::GetTempPath()) ("miniapp-bridge-zlib-1.3.1-" + [Guid]::NewGuid().ToString('N'))
$legacyZlibObjectDirectory = Join-Path $runtime 'zlib-1.3.1-obj'
$vsdev = 'C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\Common7\Tools\VsDevCmd.bat'

foreach ($path in @((Join-Path $devkit 'frida-core.h'), (Join-Path $devkit 'frida-core.lib'), (Join-Path $zlibSource 'zlib.h'), $vsdev)) {
    if (-not (Test-Path $path)) { throw "required Frida/MSVC file missing: $path" }
}
New-Item -ItemType Directory -Force -Path $runtime | Out-Null
if (Test-Path -LiteralPath $legacyZlibObjectDirectory) { Remove-Item -LiteralPath $legacyZlibObjectDirectory -Recurse -Force }
New-Item -ItemType Directory -Force -Path $zlibObjectDirectory | Out-Null

try {
    Write-Host 'MSVC zlib objects: begin'
    $zlibNames = @('adler32', 'compress', 'crc32', 'deflate', 'gzclose', 'gzlib', 'gzread', 'gzwrite', 'infback', 'inffast', 'inflate', 'inftrees', 'trees', 'uncompr', 'zutil')
    $zlibSources = @($zlibNames | ForEach-Object { '"' + (Join-Path $zlibSource "$_.c") + '"' }) -join ' '
    $zlibObjectOutput = $zlibObjectDirectory.Replace('\', '/') + '/'
    $zlibCompile = 'call "{0}" -arch=x64 -host_arch=x64 >nul && cl /nologo /c /MT /O2 /utf-8 /I"{1}" /Fo"{2}" {3}' -f $vsdev, $zlibSource, $zlibObjectOutput, $zlibSources
    cmd.exe /d /s /c $zlibCompile
    if ($LASTEXITCODE -ne 0) { throw "MSVC zlib build failed with exit $LASTEXITCODE" }
    Write-Host 'MSVC zlib objects: complete'
    $zlibObjects = @($zlibNames | ForEach-Object {
        $object = Join-Path $zlibObjectDirectory "$_.obj"
        if (-not (Test-Path -LiteralPath $object)) { throw "MSVC zlib build did not produce $object" }
        '"' + $object + '"'
    }) -join ' '

    $command = 'call "{0}" -arch=x64 -host_arch=x64 >nul && cl /nologo /LD /MT /O2 /utf-8 /Fo"{8}" /I"{1}" /I"{2}" "{3}" {9} "{4}" setupapi.lib /link /Brepro /DEF:"{5}" /OUT:"{6}" /IMPLIB:"{7}"' -f `
        $vsdev, $devkit, $shim, (Join-Path $shim 'miniapp_frida.c'), (Join-Path $devkit 'frida-core.lib'), `
        (Join-Path $shim 'miniapp_frida.def'), (Join-Path $runtime 'miniapp-frida.dll'), (Join-Path $runtime 'miniapp-frida.lib'), (Join-Path $runtime 'miniapp_frida.obj'), $zlibObjects
    cmd.exe /d /s /c $command
    if ($LASTEXITCODE -ne 0) { throw "MSVC shim build failed with exit $LASTEXITCODE" }
    Write-Host 'MSVC Frida shim: complete'

    # Go loads this DLL through LoadLibraryExW/GetProcAddress. Do not generate or
    # require a Go import library; the MSVC .lib above is only the shim's internal
    # link artifact. The release packager records this hash in manifest.json.
    $dll = Join-Path $runtime 'miniapp-frida.dll'
    Write-Output 'native_version=17.3.2-abi1'
    Write-Output 'frida_core_version=17.3.2'
    Write-Output 'abi_version=1'
    Write-Output "dll_sha256=$((Get-FileHash -Algorithm SHA256 $dll).Hash)"
}
finally {
    if (Test-Path -LiteralPath $zlibObjectDirectory) { Remove-Item -LiteralPath $zlibObjectDirectory -Recurse -Force }
}
