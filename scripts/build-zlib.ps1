param(
    [switch]$Offline,
    [ValidateRange(1, 10)][int]$DownloadAttempts = 3,
    [ValidateRange(1, 900)][int]$DownloadTimeoutSeconds = 300,
    [ValidateRange(1, 3600)][int]$DownloadTotalTimeoutSeconds = 900,
    [ValidateRange(0, 60)][int]$DownloadRetrySeconds = 5,
    [string]$SourceURL = '',
    [string]$CacheDirectory = '',
    [string]$SourceDirectory = '',
    [string]$OutputDirectory = '',
    [string]$ExpectedArchiveSHA256 = '9A93B2B7DFDAC77CEBA5A558A580E74667DD6FEDE4585B91EEFB60F03B72DF23'
)

$ErrorActionPreference = 'Stop'

$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$archiveURL = if ($SourceURL) { $SourceURL } else { 'https://zlib.net/fossils/zlib-1.3.1.tar.gz' }
$cache = if ($CacheDirectory) { [IO.Path]::GetFullPath($CacheDirectory) } else { Join-Path $repo 'third_party\downloads\cache' }
$archive = Join-Path $cache 'zlib-1.3.1.tar.gz'
$partialArchive = "$archive.partial"
$source = if ($SourceDirectory) { [IO.Path]::GetFullPath($SourceDirectory) } else { Join-Path $repo 'third_party\zlib\src-1.3.1' }
$output = if ($OutputDirectory) { [IO.Path]::GetFullPath($OutputDirectory) } else { Join-Path $repo 'third_party\zlib\lib\windows-x86_64' }
$expectedArchiveHash = ([string]$ExpectedArchiveSHA256).Trim().ToUpperInvariant()
if ($expectedArchiveHash -notmatch '^[0-9A-F]{64}$') {
    throw 'ExpectedArchiveSHA256 must contain exactly 64 hexadecimal characters'
}

function Invoke-BoundedDownload {
    param(
        [string]$URL,
        [string]$Destination,
        [int]$TimeoutSeconds
    )

    $connectTimeoutSeconds = [Math]::Min(30, $TimeoutSeconds)
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if ($null -ne $curl) {
        & $curl.Source `
            --fail `
            --location `
            --silent `
            --show-error `
            --connect-timeout $connectTimeoutSeconds `
            --max-time $TimeoutSeconds `
            --output $Destination `
            --url $URL
        if ($LASTEXITCODE -ne 0) {
            throw "curl.exe exited with code $LASTEXITCODE"
        }
        return
    }

    Add-Type -AssemblyName System.Net.Http
    $handler = [System.Net.Http.HttpClientHandler]::new()
    $client = [System.Net.Http.HttpClient]::new($handler)
    $response = $null
    try {
        $client.Timeout = [TimeSpan]::FromSeconds($TimeoutSeconds)
        $client.DefaultRequestHeaders.UserAgent.ParseAdd('miniapp-bridge-zlib-bootstrap/1')
        $response = $client.GetAsync(
            $URL,
            [System.Net.Http.HttpCompletionOption]::ResponseContentRead
        ).GetAwaiter().GetResult()
        $response.EnsureSuccessStatusCode()
        $bytes = $response.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
        [IO.File]::WriteAllBytes($Destination, $bytes)
    }
    finally {
        if ($null -ne $response) { $response.Dispose() }
        $client.Dispose()
        $handler.Dispose()
    }
}

function Invoke-VerifiedDownload {
    param(
        [string]$URL,
        [string]$Destination,
        [string]$ExpectedSHA256
    )

    $lastError = $null
    $timer = [Diagnostics.Stopwatch]::StartNew()
    for ($attempt = 1; $attempt -le $DownloadAttempts; $attempt++) {
        $remaining = $DownloadTotalTimeoutSeconds - $timer.Elapsed.TotalSeconds
        if ($remaining -le 0) { break }
        $attemptTimeout = [Math]::Max(1, [Math]::Min($DownloadTimeoutSeconds, [Math]::Ceiling($remaining)))
        Remove-Item -LiteralPath $Destination -Force -ErrorAction SilentlyContinue
        Write-Host "Downloading zlib-1.3.1.tar.gz attempt=$attempt/$DownloadAttempts"
        try {
            Invoke-BoundedDownload -URL $URL -Destination $Destination -TimeoutSeconds $attemptTimeout
            $downloadHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $Destination).Hash
            if ($downloadHash -cne $ExpectedSHA256) {
                throw "downloaded zlib 1.3.1 archive hash mismatch: expected $ExpectedSHA256, got $downloadHash"
            }
            return
        }
        catch {
            $lastError = $_
            Remove-Item -LiteralPath $Destination -Force -ErrorAction SilentlyContinue
            if ($attempt -lt $DownloadAttempts -and ($DownloadTotalTimeoutSeconds - $timer.Elapsed.TotalSeconds) -gt 0) {
                Write-Warning "zlib archive download attempt $attempt failed: $($_.Exception.Message)"
                if ($DownloadRetrySeconds -gt 0) {
                    Start-Sleep -Seconds $DownloadRetrySeconds
                }
            }
        }
    }
    if ($null -ne $lastError) {
        throw "zlib archive download failed after $DownloadAttempts attempts: $($lastError.Exception.Message)"
    }
    throw "zlib archive download exceeded total timeout of $DownloadTotalTimeoutSeconds seconds"
}

New-Item -ItemType Directory -Force -Path $cache | Out-Null
$archiveIsValid = (Test-Path -LiteralPath $archive) -and
    ((Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash -eq $expectedArchiveHash)
if (!$archiveIsValid) {
    if ($Offline) {
        throw "zlib archive cache is unavailable or invalid in offline mode (zlib 1.3.1): $archive"
    }
    try {
        Remove-Item -LiteralPath $partialArchive -Force -ErrorAction SilentlyContinue
        Invoke-VerifiedDownload -URL $archiveURL -Destination $partialArchive -ExpectedSHA256 $expectedArchiveHash
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
