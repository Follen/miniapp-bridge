param(
    [switch]$Offline,
    [ValidateRange(1, 600)]
    [int]$LockTimeoutSeconds = 120,
    [string]$ArchiveFileName = 'frida-core-devkit-17.3.2-windows-x86_64.tar.xz',
    [string]$SourceURL = '',
    [string]$CacheDirectory = '',
    [string]$DevkitDirectory = '',
    [string]$ExpectedArchiveSHA256 = '8AF15423D6E534626F91A67FAA0582E42C67A07A95A190F4C622695105549C72',
    [string]$ExpectedHeaderSHA256 = '6B4DEE14C19BDB03CAA4A25BE51564AA249BC1167AA8DED26F562E238D0B3462',
    [string]$ExpectedLibrarySHA256 = 'D763BCF99EFDE43A3DE4138B19D70EC64B586286413473EAA21E6C59B7410A30'
)

$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$version = '17.3.2'
$archiveName = $ArchiveFileName
$archiveURL = if ([string]::IsNullOrWhiteSpace($SourceURL)) {
    "https://github.com/frida/frida/releases/download/$version/$archiveName"
} else {
    $SourceURL
}
$archiveSHA256 = $ExpectedArchiveSHA256.ToUpperInvariant()
$headerSHA256 = $ExpectedHeaderSHA256.ToUpperInvariant()
$librarySHA256 = $ExpectedLibrarySHA256.ToUpperInvariant()
$downloadDir = if ([string]::IsNullOrWhiteSpace($CacheDirectory)) {
    Join-Path $root 'third_party\downloads'
} else {
    [System.IO.Path]::GetFullPath($CacheDirectory)
}
$devkit = if ([string]::IsNullOrWhiteSpace($DevkitDirectory)) {
    Join-Path $root "third_party\frida\devkit-$version"
} else {
    [System.IO.Path]::GetFullPath($DevkitDirectory)
}
$archive = Join-Path $downloadDir $archiveName
$header = Join-Path $devkit 'frida-core.h'
$library = Join-Path $devkit 'frida-core.lib'

function Test-ExpectedHash {
    param([string]$Path, [string]$Expected)
    return (Test-Path -LiteralPath $Path) -and ((Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash -eq $Expected)
}

New-Item -ItemType Directory -Force -Path $downloadDir | Out-Null
$lockPath = Join-Path $downloadDir "$archiveName.lock"
$lockStream = $null
$lockTimer = [System.Diagnostics.Stopwatch]::StartNew()
while ($null -eq $lockStream) {
    try {
        $lockStream = [System.IO.File]::Open(
            $lockPath,
            [System.IO.FileMode]::OpenOrCreate,
            [System.IO.FileAccess]::ReadWrite,
            [System.IO.FileShare]::None
        )
    } catch [System.IO.IOException] {
        if ($lockTimer.Elapsed.TotalSeconds -ge $LockTimeoutSeconds) {
            throw "Timed out waiting for Frida SDK cache lock after $LockTimeoutSeconds seconds: $lockPath"
        }
        Start-Sleep -Milliseconds 100
    }
}

$partialArchive = "$archive.partial"
$stagingDevkit = Join-Path $downloadDir "$archiveName.extracting"
try {
    $headerValid = Test-ExpectedHash -Path $header -Expected $headerSHA256
    $libraryValid = Test-ExpectedHash -Path $library -Expected $librarySHA256
    $archiveHash = 'not-present'
    $archiveStatus = 'not-present'

    if ($headerValid -and $libraryValid) {
        if (Test-Path -LiteralPath $archive) {
            $archiveHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash
            $archiveStatus = if ($archiveHash -eq $archiveSHA256) { 'verified' } else { 'invalid-unused' }
        }
    } else {
        $archiveValid = Test-ExpectedHash -Path $archive -Expected $archiveSHA256
        if (-not $archiveValid) {
            if ($Offline) {
                throw "Frida SDK cache is unavailable or invalid in offline mode: $devkit"
            }
            if (Test-Path -LiteralPath $partialArchive) { Remove-Item -LiteralPath $partialArchive -Force }
            Write-Host "Downloading $archiveName"
            try {
                Invoke-WebRequest -UseBasicParsing -Uri $archiveURL -OutFile $partialArchive
                $downloadHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $partialArchive).Hash
                if ($downloadHash -ne $archiveSHA256) {
                    throw "Frida SDK download SHA-256 mismatch: got $downloadHash, want $archiveSHA256"
                }
                Move-Item -LiteralPath $partialArchive -Destination $archive -Force
            } finally {
                if (Test-Path -LiteralPath $partialArchive) { Remove-Item -LiteralPath $partialArchive -Force }
            }
        }

        $archiveHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash
        if ($archiveHash -ne $archiveSHA256) {
            throw "Frida SDK archive SHA-256 mismatch: got $archiveHash, want $archiveSHA256"
        }
        $archiveStatus = 'verified'

        if (Test-Path -LiteralPath $stagingDevkit) { Remove-Item -LiteralPath $stagingDevkit -Recurse -Force }
        try {
            New-Item -ItemType Directory -Force -Path $stagingDevkit | Out-Null
            tar.exe -xJf $archive -C $stagingDevkit
            if ($LASTEXITCODE -ne 0) { throw "Frida SDK extraction failed with exit $LASTEXITCODE" }

            $stagingHeader = Join-Path $stagingDevkit 'frida-core.h'
            $stagingLibrary = Join-Path $stagingDevkit 'frida-core.lib'
            if (-not (Test-ExpectedHash -Path $stagingHeader -Expected $headerSHA256)) {
                throw 'Frida SDK header SHA-256 mismatch after extraction'
            }
            if (-not (Test-ExpectedHash -Path $stagingLibrary -Expected $librarySHA256)) {
                throw 'Frida SDK library SHA-256 mismatch after extraction'
            }
            if (Test-Path -LiteralPath $devkit) { Remove-Item -LiteralPath $devkit -Recurse -Force }
            Move-Item -LiteralPath $stagingDevkit -Destination $devkit
        } finally {
            if (Test-Path -LiteralPath $stagingDevkit) { Remove-Item -LiteralPath $stagingDevkit -Recurse -Force }
        }
    }

    if (-not (Test-ExpectedHash -Path $header -Expected $headerSHA256)) { throw 'Frida SDK header SHA-256 mismatch' }
    if (-not (Test-ExpectedHash -Path $library -Expected $librarySHA256)) { throw 'Frida SDK library SHA-256 mismatch' }
    Write-Output "frida_core_version=$version"
    Write-Output "archive_cache=$archiveStatus"
    Write-Output "archive_sha256=$archiveHash"
    Write-Output "archive_expected_sha256=$archiveSHA256"
    Write-Output "header_sha256=$headerSHA256"
    Write-Output "library_sha256=$librarySHA256"
} finally {
    if (Test-Path -LiteralPath $partialArchive) { Remove-Item -LiteralPath $partialArchive -Force }
    if (Test-Path -LiteralPath $stagingDevkit) { Remove-Item -LiteralPath $stagingDevkit -Recurse -Force }
    if ($null -ne $lockStream) { $lockStream.Dispose() }
}
