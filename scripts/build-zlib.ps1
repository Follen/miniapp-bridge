$ErrorActionPreference = 'Stop'

$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$archive = Join-Path $repo 'third_party\zlib\zlib-1.3.1.tar.gz'
$source = Join-Path $repo 'third_party\zlib\src-1.3.1'
$output = Join-Path $repo 'third_party\zlib\lib\windows-x86_64'
$expectedArchiveHash = '9A93B2B7DFDAC77CEBA5A558A580E74667DD6FEDE4585B91EEFB60F03B72DF23'

if (!(Test-Path -LiteralPath $archive)) {
    throw "missing pinned zlib archive: $archive"
}
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash -ne $expectedArchiveHash) {
    throw "zlib 1.3.1 archive hash mismatch"
}
if (!(Test-Path -LiteralPath (Join-Path $source 'zlib.h'))) {
    New-Item -ItemType Directory -Force -Path $source | Out-Null
    & tar.exe -xzf $archive -C $source --strip-components 1
    if ($LASTEXITCODE -ne 0) {
        throw "zlib 1.3.1 extraction failed with exit $LASTEXITCODE"
    }
}
if (!(Test-Path -LiteralPath (Join-Path $source 'zlib.h'))) {
    throw "zlib 1.3.1 extraction did not produce zlib.h: $source"
}
if (!(Select-String -LiteralPath (Join-Path $source 'zlib.h') -SimpleMatch '#define ZLIB_VERSION "1.3.1"')) {
    throw 'zlib header version is not 1.3.1'
}

$gcc = (Get-Command gcc.exe -ErrorAction Stop).Source
$ar = (Get-Command ar.exe -ErrorAction Stop).Source
$build = Join-Path $output 'obj'
New-Item -ItemType Directory -Force -Path $build | Out-Null
Get-ChildItem -LiteralPath $build -Filter '*.o' -File -ErrorAction SilentlyContinue | Remove-Item -Force

$sources = @(
    'adler32.c', 'crc32.c', 'deflate.c', 'infback.c', 'inffast.c',
    'inflate.c', 'inftrees.c', 'trees.c', 'zutil.c', 'compress.c',
    'uncompr.c', 'gzclose.c', 'gzlib.c', 'gzread.c', 'gzwrite.c'
)
$objects = @()
foreach ($name in $sources) {
    $object = Join-Path $build (([IO.Path]::GetFileNameWithoutExtension($name)) + '.o')
    & $gcc -O3 -D_LARGEFILE64_SOURCE=1 -I $source -c (Join-Path $source $name) -o $object
    if ($LASTEXITCODE -ne 0) {
        throw "zlib compile failed for $name with exit $LASTEXITCODE"
    }
    $objects += $object
}

$library = Join-Path $output 'libz.a'
if (Test-Path -LiteralPath $library) {
    Remove-Item -LiteralPath $library -Force
}
& $ar rcs $library @objects
if ($LASTEXITCODE -ne 0) {
    throw "zlib archive creation failed with exit $LASTEXITCODE"
}
foreach ($object in $objects) {
    if ([IO.File]::Exists($object)) {
        [IO.File]::Delete($object)
    }
}

Write-Output "zlib_version=1.3.1"
Write-Output "archive_sha256=$expectedArchiveHash"
Write-Output "library_sha256=$((Get-FileHash -Algorithm SHA256 -LiteralPath $library).Hash)"
