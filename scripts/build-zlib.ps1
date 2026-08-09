param(
    [switch]$Offline
)

$ErrorActionPreference = 'Stop'

$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$archiveURL = 'https://zlib.net/fossils/zlib-1.3.1.tar.gz'
$cache = Join-Path $repo 'third_party\downloads\cache'
$archive = Join-Path $cache 'zlib-1.3.1.tar.gz'
$partialArchive = "$archive.partial"
$source = Join-Path $repo 'third_party\zlib\src-1.3.1'
$output = Join-Path $repo 'third_party\zlib\lib\windows-x86_64'
$expectedArchiveHash = '9A93B2B7DFDAC77CEBA5A558A580E74667DD6FEDE4585B91EEFB60F03B72DF23'

New-Item -ItemType Directory -Force -Path $cache | Out-Null
$archiveIsValid = (Test-Path -LiteralPath $archive) -and
    ((Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash -eq $expectedArchiveHash)
if (!$archiveIsValid) {
    if ($Offline) {
        throw "zlib archive cache is unavailable or invalid in offline mode (zlib 1.3.1): $archive"
    }
    Remove-Item -LiteralPath $partialArchive -Force -ErrorAction SilentlyContinue
    try {
        Invoke-WebRequest -Uri $archiveURL -OutFile $partialArchive -UseBasicParsing
        if ((Get-FileHash -Algorithm SHA256 -LiteralPath $partialArchive).Hash -ne $expectedArchiveHash) {
            throw 'downloaded zlib 1.3.1 archive hash mismatch'
        }
        Move-Item -LiteralPath $partialArchive -Destination $archive -Force
    }
    finally {
        Remove-Item -LiteralPath $partialArchive -Force -ErrorAction SilentlyContinue
    }
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
$build = Join-Path $output ("obj-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $build | Out-Null

$library = Join-Path $output 'libz.a'
$stagedLibrary = Join-Path $build 'libz.a'
try {
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
        if (![IO.File]::Exists($object)) {
            throw "zlib compile did not produce object for ${name}: $object"
        }
        $objects += $object
    }

    & $ar rcs $stagedLibrary @objects
    if ($LASTEXITCODE -ne 0) {
        throw "zlib archive creation failed with exit $LASTEXITCODE"
    }
    if (![IO.File]::Exists($stagedLibrary)) {
        throw "zlib archive creation did not produce library: $stagedLibrary"
    }
    Move-Item -LiteralPath $stagedLibrary -Destination $library -Force

    Write-Output "zlib_version=1.3.1"
    Write-Output "archive_sha256=$expectedArchiveHash"
    Write-Output "library_sha256=$((Get-FileHash -Algorithm SHA256 -LiteralPath $library).Hash)"
}
finally {
    Remove-Item -LiteralPath $build -Recurse -Force -ErrorAction SilentlyContinue
}
