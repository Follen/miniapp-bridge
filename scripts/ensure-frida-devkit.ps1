param(
    [switch]$Offline,
    [ValidateRange(1, 600)]
    [int]$LockTimeoutSeconds = 120,
    [ValidateRange(1, 10)]
    [int]$DownloadAttempts = 3,
    [ValidateRange(1, 900)]
    [int]$DownloadTimeoutSeconds = 300,
    [ValidateRange(0, 60)]
    [int]$DownloadRetrySeconds = 5,
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
$devkitParent = Split-Path -Parent $devkit

function Test-ExpectedHash {
    param([string]$Path, [string]$Expected)
    return (Test-Path -LiteralPath $Path) -and ((Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash -eq $Expected)
}

function Move-DirectoryAtomically {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Source,
        [Parameter(Mandatory = $true)]
        [string]$Destination
    )

    # Directory.Move is a same-volume rename and never degrades to copy/delete.
    [System.IO.Directory]::Move($Source, $Destination)
}

function Stop-ProcessTreeBounded {
    param([System.Diagnostics.Process]$Process)

    $killTreeMethod = $Process.GetType().GetMethod('Kill', [type[]]@([bool]))
    if ($null -ne $killTreeMethod) {
        try {
            [void]$killTreeMethod.Invoke($Process, @($true))
            return
        } catch {}
    }

    # Windows PowerShell 5.1 runs on .NET Framework, whose Process.Kill has no
    # process-tree overload. taskkill keeps child processes from retaining the
    # download handles after the parent curl process is terminated.
    $taskkill = Get-Command taskkill.exe -ErrorAction SilentlyContinue
    if ($null -ne $taskkill) {
        $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
        $startInfo.FileName = $taskkill.Source
        $startInfo.Arguments = "/PID $($Process.Id) /T /F"
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true
        $killer = [System.Diagnostics.Process]::new()
        $killer.StartInfo = $startInfo
        try {
            if ($killer.Start()) {
                $stdoutTask = $killer.StandardOutput.ReadToEndAsync()
                $stderrTask = $killer.StandardError.ReadToEndAsync()
                if (-not $killer.WaitForExit(5000)) {
                    try { $killer.Kill() } catch {}
                    throw 'taskkill.exe exceeded process-tree termination timeout'
                }
                [void]$stdoutTask.GetAwaiter().GetResult()
                [void]$stderrTask.GetAwaiter().GetResult()
            }
        } finally {
            $killer.Dispose()
        }
    }

    if (-not $Process.HasExited) {
        try { $Process.Kill() } catch {}
    }
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
        $curlArguments = @(
            '--fail', '--location', '--silent', '--show-error',
            '--connect-timeout', [string]$connectTimeoutSeconds,
            '--max-time', [string]$TimeoutSeconds,
            '--output', $Destination,
            '--url', $URL
        )
        $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
        $startInfo.FileName = $curl.Source
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true
        $argumentListProperty = $startInfo.GetType().GetProperty('ArgumentList')
        if ($null -ne $argumentListProperty) {
            foreach ($argument in $curlArguments) {
                [void]$startInfo.ArgumentList.Add([string]$argument)
            }
        } else {
            # Windows PowerShell 5.1 lacks ProcessStartInfo.ArgumentList. These
            # arguments contain no embedded quotes, so quoting whitespace is
            # sufficient for the URL and destination paths used here.
            $startInfo.Arguments = ($curlArguments | ForEach-Object {
                $value = [string]$_
                if ($value -match '[\s"]') { '"' + $value.Replace('"', '\"') + '"' } else { $value }
            }) -join ' '
        }

        $process = [System.Diagnostics.Process]::new()
        $process.StartInfo = $startInfo
        try {
            if (-not $process.Start()) { throw 'curl.exe did not start' }
            $stdoutTask = $process.StandardOutput.ReadToEndAsync()
            $stderrTask = $process.StandardError.ReadToEndAsync()
            $waitMilliseconds = [int][Math]::Min([int]::MaxValue, $TimeoutSeconds * 1000)
            if (-not $process.WaitForExit($waitMilliseconds)) {
                Stop-ProcessTreeBounded -Process $process
                if (-not $process.WaitForExit(5000)) {
                    throw 'curl.exe did not exit after hard-timeout termination'
                }
                throw "curl.exe exceeded hard timeout of ${TimeoutSeconds}s"
            }
            $stdout = $stdoutTask.GetAwaiter().GetResult()
            $stderr = $stderrTask.GetAwaiter().GetResult()
            if ($process.ExitCode -ne 0) {
                $detail = if ([string]::IsNullOrWhiteSpace($stderr)) { $stdout.Trim() } else { $stderr.Trim() }
                throw "curl.exe exited with code $($process.ExitCode): $detail"
            }
        } finally {
            $process.Dispose()
        }
        return
    }

    Add-Type -AssemblyName System.Net.Http
    $handler = [System.Net.Http.HttpClientHandler]::new()
    $client = [System.Net.Http.HttpClient]::new($handler)
    $response = $null
    try {
        $client.Timeout = [TimeSpan]::FromSeconds($TimeoutSeconds)
        $client.DefaultRequestHeaders.UserAgent.ParseAdd('miniapp-bridge-native-bootstrap/1')
        # ResponseContentRead keeps the total timeout active until the complete
        # body has been buffered, including a peer that only trickles bytes.
        $response = $client.GetAsync(
            $URL,
            [System.Net.Http.HttpCompletionOption]::ResponseContentRead
        ).GetAwaiter().GetResult()
        $response.EnsureSuccessStatusCode()
        $bytes = $response.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
        [System.IO.File]::WriteAllBytes($Destination, $bytes)
    } finally {
        if ($null -ne $response) { $response.Dispose() }
        $client.Dispose()
        $handler.Dispose()
    }
}

function Invoke-VerifiedDownload {
    param(
        [string]$URL,
        [string]$Destination,
        [string]$ExpectedSHA256,
        [string]$DisplayName
    )

    $lastError = $null
    for ($attempt = 1; $attempt -le $DownloadAttempts; $attempt++) {
        if (Test-Path -LiteralPath $Destination) { Remove-Item -LiteralPath $Destination -Force }

        Write-Host "Downloading $DisplayName attempt=$attempt/$DownloadAttempts"
        try {
            Invoke-BoundedDownload -URL $URL -Destination $Destination -TimeoutSeconds $DownloadTimeoutSeconds
            $downloadHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $Destination).Hash
            if ($downloadHash -ne $ExpectedSHA256) {
                throw "Frida SDK download SHA-256 mismatch: got $downloadHash, want $ExpectedSHA256"
            }
            return
        } catch {
            $lastError = $_
            if (Test-Path -LiteralPath $Destination) { Remove-Item -LiteralPath $Destination -Force }
            if ($attempt -lt $DownloadAttempts) {
                Write-Warning "Frida SDK download attempt $attempt failed: $($_.Exception.Message)"
                if ($DownloadRetrySeconds -gt 0) { Start-Sleep -Seconds $DownloadRetrySeconds }
            }
        }
    }
    throw "Frida SDK download failed after $DownloadAttempts attempts: $($lastError.Exception.Message)"
}

function Expand-FridaArchive {
    param(
        [string]$Archive,
        [string]$Destination
    )

    $sevenZip = Get-Command 7z.exe -ErrorAction SilentlyContinue
    if ($null -eq $sevenZip) {
        foreach ($candidate in @(
            (Join-Path ${env:ProgramFiles} '7-Zip\7z.exe'),
            (Join-Path ${env:ProgramFiles(x86)} '7-Zip\7z.exe')
        )) {
            if (Test-Path -LiteralPath $candidate) {
                $sevenZip = [pscustomobject]@{ Source = $candidate }
                break
            }
        }
    }
    if ($null -ne $sevenZip) {
        # Some hosted Windows images have a tar/xz implementation that can stop
        # producing output while expanding this 322 MiB .lib.  7-Zip handles the
        # xz and tar layers separately and gives the runner a deterministic path.
        Write-Host "Extracting Frida SDK with 7z.exe: $Archive"
        & $sevenZip.Source x $Archive "-o$Destination" -y
        if ($LASTEXITCODE -ne 0) { throw "Frida SDK xz extraction failed with exit $LASTEXITCODE" }
        $tarArchive = Get-ChildItem -LiteralPath $Destination -Filter '*.tar' -File | Select-Object -First 1
        if ($null -eq $tarArchive) { throw "Frida SDK xz extraction did not produce a tar archive: $Destination" }
        & $sevenZip.Source x $tarArchive.FullName "-o$Destination" -y
        if ($LASTEXITCODE -ne 0) { throw "Frida SDK tar extraction failed with exit $LASTEXITCODE" }
        Remove-Item -LiteralPath $tarArchive.FullName -Force
        return
    }

    Write-Host "Extracting Frida SDK with tar.exe: $Archive"
    tar.exe -xJf $Archive -C $Destination
    if ($LASTEXITCODE -ne 0) { throw "Frida SDK extraction failed with exit $LASTEXITCODE" }
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
# Keep staging and backup beside the final directory so publication is a same-volume
# rename. This avoids Move-Item falling back to copy/delete semantics across volumes.
$stagingDevkit = "$devkit.extracting-$([guid]::NewGuid().ToString('N'))"
$backupDevkit = $null
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
            try {
                Invoke-VerifiedDownload -URL $archiveURL -Destination $partialArchive -ExpectedSHA256 $archiveSHA256 -DisplayName $archiveName
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
            Expand-FridaArchive -Archive $archive -Destination $stagingDevkit

            $stagingHeader = Join-Path $stagingDevkit 'frida-core.h'
            $stagingLibrary = Join-Path $stagingDevkit 'frida-core.lib'
            if (-not (Test-ExpectedHash -Path $stagingHeader -Expected $headerSHA256)) {
                throw 'Frida SDK header SHA-256 mismatch after extraction'
            }
            if (-not (Test-ExpectedHash -Path $stagingLibrary -Expected $librarySHA256)) {
                throw 'Frida SDK library SHA-256 mismatch after extraction'
            }

            # Publish only after the complete staging tree has been validated. The
            # previous install is first renamed to a same-directory backup, then the
            # new tree is renamed into place. Any publication failure restores it.
            New-Item -ItemType Directory -Force -Path $devkitParent | Out-Null
            if (Test-Path -LiteralPath $devkit) {
                $backupDevkit = "$devkit.backup-$([guid]::NewGuid().ToString('N'))"
                Move-DirectoryAtomically -Source $devkit -Destination $backupDevkit
            }
            try {
                Move-DirectoryAtomically -Source $stagingDevkit -Destination $devkit
            } catch {
                $publishError = $_.Exception.Message
                if (Test-Path -LiteralPath $devkit) {
                    Remove-Item -LiteralPath $devkit -Recurse -Force -ErrorAction SilentlyContinue
                }
                if ($null -ne $backupDevkit -and (Test-Path -LiteralPath $backupDevkit)) {
                    try {
                        Move-DirectoryAtomically -Source $backupDevkit -Destination $devkit
                    } catch {
                        throw "Frida SDK publication failed: $publishError; rollback failed: $($_.Exception.Message); backup retained at $backupDevkit"
                    }
                }
                throw "Frida SDK publication failed: $publishError"
            }
            if ($null -ne $backupDevkit -and (Test-Path -LiteralPath $backupDevkit)) {
                try {
                    Remove-Item -LiteralPath $backupDevkit -Recurse -Force -ErrorAction Stop
                } catch {
                    Write-Warning "Frida SDK published successfully but backup cleanup failed; retained at $backupDevkit"
                }
            }
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
