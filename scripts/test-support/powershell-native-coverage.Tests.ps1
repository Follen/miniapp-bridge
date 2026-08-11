$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$scripts = Join-Path $repo 'scripts'

function Get-TestSHA256([string]$Path) {
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToUpperInvariant()
}

function Invoke-ExpectedFailure([scriptblock]$Action, [string]$Pattern) {
    $message = ''
    try { & $Action }
    catch { $message = $_.Exception.Message }
    $message | Should Match $Pattern
}

function New-NativeReleaseFixture([string]$Root, [string]$DumpbinMode = 'success') {
    $runtime = Join-Path $Root 'runtime'
    $out = Join-Path $Root 'out'
    New-Item -ItemType Directory -Force -Path $runtime,$out | Out-Null
    $files = @{
        dll = Join-Path $runtime 'miniapp-frida.dll'
        license = Join-Path $Root 'LICENSE'
        copying = Join-Path $Root 'COPYING'
        library = Join-Path $Root 'COPYING.LIB'
        zlib = Join-Path $Root 'ZLIB_LICENSE'
        notices = Join-Path $Root 'THIRD_PARTY_NOTICES.md'
    }
    foreach ($path in $files.Values) { [IO.File]::WriteAllText($path, 'fixture') }
    $dumpbin = Join-Path $Root 'dumpbin.cmd'
    $exports = @(
        'mb_abi_version', 'mb_native_version', 'mb_frida_core_version', 'mb_zlib_version',
        'mb_zlib_compress', 'mb_zlib_decompress', 'mb_bytes_free', 'mb_device_open',
        'mb_device_enumerate', 'mb_processes_free', 'mb_device_attach', 'mb_device_close',
        'mb_runtime_shutdown', 'mb_session_load_script', 'mb_session_detach', 'mb_script_post',
        'mb_script_unload', 'mb_error_free'
    )
    $lines = @('@echo off')
    if ($DumpbinMode -eq 'header-failure') {
        $lines += 'exit /b 7'
    }
    elseif ($DumpbinMode -eq 'wrong-machine') {
        $lines += 'if "%2"=="/headers" echo 14C machine (x86)'
        $lines += 'exit /b 0'
    }
    elseif ($DumpbinMode -eq 'export-failure') {
        $lines += 'if "%2"=="/headers" echo 8664 machine (x64)'
        $lines += 'if "%2"=="/exports" exit /b 9'
        $lines += 'exit /b 0'
    }
    else {
        $lines += 'if "%2"=="/headers" echo 8664 machine (x64)'
        if ($DumpbinMode -eq 'bad-exports') {
            $lines += 'if "%2"=="/exports" echo 1 00000000 00000000 mb_unexpected'
        }
        else {
            $ordinal = 1
            foreach ($export in $exports) {
                $lines += ('if "%2"=="/exports" echo {0} 00000000 00000000 {1}' -f $ordinal, $export)
                $ordinal++
            }
        }
        $lines += 'exit /b 0'
    }
    [IO.File]::WriteAllLines($dumpbin, $lines)
    return [pscustomobject]@{ Runtime = $runtime; Out = $out; Files = $files; Dumpbin = $dumpbin }
}

function Invoke-NativeReleaseFixture($Fixture, [hashtable]$Extra = @{}) {
    $parameters = @{
        RuntimeDirectory = $Fixture.Runtime; OutputDirectory = $Fixture.Out
        LicenseFile = $Fixture.Files.license; FridaCopyingFile = $Fixture.Files.copying
        FridaLibraryLicenseFile = $Fixture.Files.library; ZlibLicenseFile = $Fixture.Files.zlib
        ThirdPartyNoticesFile = $Fixture.Files.notices; DumpbinPath = $Fixture.Dumpbin
    }
    foreach ($key in $Extra.Keys) { $parameters[$key] = $Extra[$key] }
    & (Join-Path $scripts 'native-release.ps1') @parameters
}

function New-PackageFixture([string]$Root) {
    $input = Join-Path $Root 'dist'
    $native = Join-Path $input 'native'
    $out = Join-Path $input 'release'
    New-Item -ItemType Directory -Force -Path $input,$native,(Join-Path $Root 'licenses\frida-17.3.2'),(Join-Path $Root 'third_party\zlib\src-1.3.1') | Out-Null
    $dll = Join-Path $input 'miniapp-frida.dll'
    [IO.File]::WriteAllText($dll, 'MZ fixture dll')
    $dllHash = Get-TestSHA256 $dll
    $manifest = [ordered]@{ schema='miniapp-bridge.native-manifest.v1'; nativeVersion='17.3.2-abi1'; fridaCoreVersion='17.3.2'; zlibVersion='1.3.1'; abiVersion=1; os='windows'; arch='amd64'; dll='miniapp-frida.dll'; size=(Get-Item $dll).Length; sha256=$dllHash; requiredExports=@('mb_abi_version') }
    [IO.File]::WriteAllText((Join-Path $input 'manifest.json'), ($manifest | ConvertTo-Json -Compress))
    $assetName = 'miniapp-frida-native-17.3.2-abi1-windows-amd64.zip'
    $asset = Join-Path $native $assetName
    [IO.File]::WriteAllText($asset, 'native archive')
    [IO.File]::WriteAllText((Join-Path $native 'SHA256SUMS'), "$(Get-TestSHA256 $asset)  $assetName`n")
    $fixtureFiles = @{
        (Join-Path $input 'miniapp-bridge.exe') = 'MZ executable'; (Join-Path $Root 'README.md') = 'readme'
        (Join-Path $Root 'README.zh.md') = 'readme zh'; (Join-Path $Root 'LICENSE') = 'license'
        (Join-Path $Root 'licenses\frida-17.3.2\COPYING') = 'copying'; (Join-Path $Root 'licenses\frida-17.3.2\COPYING.LIB') = 'copying lib'
        (Join-Path $Root 'THIRD_PARTY_NOTICES.md') = 'notices'; (Join-Path $Root 'third_party\zlib\src-1.3.1\LICENSE') = 'zlib'
    }
    foreach ($entry in $fixtureFiles.GetEnumerator()) { [IO.File]::WriteAllText($entry.Key, $entry.Value) }
    return [pscustomobject]@{ Root=$Root; Input=$input; Native=$native; Out=$out; Manifest=(Join-Path $input 'manifest.json'); Asset=$asset }
}

function Invoke-PackageFixture($Fixture, [hashtable]$Extra = @{}) {
    $parameters = @{ Version='v0.0.1'; RepositoryRoot=$Fixture.Root; InputDirectory=$Fixture.Input; NativeDirectory=$Fixture.Native; OutputDirectory=$Fixture.Out }
    foreach ($key in $Extra.Keys) { $parameters[$key] = $Extra[$key] }
    & (Join-Path $scripts 'package-windows-release.ps1') @parameters
}

function New-NativePrepareFixture([string]$Root, [object]$ManifestOverride = $null) {
    $cache = Join-Path $Root 'cache'
    $destination = Join-Path $Root 'destination'
    $payload = Join-Path $Root 'payload'
    New-Item -ItemType Directory -Force -Path $cache,$destination,$payload | Out-Null
    $dll = Join-Path $payload 'miniapp-frida.dll'
    [IO.File]::WriteAllText($dll, 'MZ native prepare fixture')
    $exports = @(
        'mb_abi_version', 'mb_native_version', 'mb_frida_core_version', 'mb_zlib_version',
        'mb_zlib_compress', 'mb_zlib_decompress', 'mb_bytes_free', 'mb_device_open',
        'mb_device_enumerate', 'mb_processes_free', 'mb_device_attach', 'mb_device_close',
        'mb_runtime_shutdown', 'mb_session_load_script', 'mb_session_detach', 'mb_script_post',
        'mb_script_unload', 'mb_error_free'
    )
    $manifest = if ($null -ne $ManifestOverride) { $ManifestOverride } else {
        [ordered]@{ schema='miniapp-bridge.native-manifest.v1'; nativeVersion='17.3.2-abi1'; fridaCoreVersion='17.3.2'; zlibVersion='1.3.1'; abiVersion=1; os='windows'; arch='amd64'; dll='miniapp-frida.dll'; size=(Get-Item $dll).Length; sha256=(Get-TestSHA256 $dll); requiredExports=$exports }
    }
    $manifestText = if ($manifest -is [string]) { $manifest } else { $manifest | ConvertTo-Json -Compress }
    [IO.File]::WriteAllText((Join-Path $payload 'manifest.json'), $manifestText)
    $archive = Join-Path $cache 'miniapp-frida-native-17.3.2-abi1-windows-amd64.zip'
    Compress-Archive -LiteralPath (Join-Path $payload 'manifest.json'),$dll -DestinationPath $archive -Force
    return [pscustomobject]@{ Cache=$cache; Destination=$destination; Archive=$archive; Hash=(Get-TestSHA256 $archive); Payload=$payload }
}

function Invoke-NativePrepareFixture($Fixture, [hashtable]$Extra = @{}) {
    $parameters = @{ Offline=$true; CacheDirectory=$Fixture.Cache; DestinationDirectory=$Fixture.Destination; ExpectedArchiveSHA256=$Fixture.Hash }
    foreach ($key in $Extra.Keys) { $parameters[$key] = $Extra[$key] }
    & (Join-Path $scripts 'native-prepare.ps1') @parameters
}

Describe 'Native release PowerShell command coverage' {
    It 'runs the native preparation transaction integration matrix' {
        Push-Location $repo
        try {
            $output = @(& go test ./scripts -run '^TestNativePrepare' -count=1 -timeout 360s 2>&1)
            if ($LASTEXITCODE -ne 0) {
                throw "native prepare integration tests failed`n$($output -join [Environment]::NewLine)"
            }
        }
        finally {
            Pop-Location
        }
    }

    It 'runs the native archive release integration matrix' {
        Push-Location $repo
        try {
            $output = @(& go test ./scripts -run '^TestNativeRelease' -count=1 -timeout 360s 2>&1)
            if ($LASTEXITCODE -ne 0) {
                throw "native release integration tests failed`n$($output -join [Environment]::NewLine)"
            }
        }
        finally {
            Pop-Location
        }
    }

    It 'runs the Windows release package integration matrix' {
        Push-Location $repo
        try {
            $output = @(& go test ./scripts -run '^TestPackageWindowsRelease' -count=1 -timeout 360s 2>&1)
            if ($LASTEXITCODE -ne 0) {
                throw "Windows release package integration tests failed`n$($output -join [Environment]::NewLine)"
            }
        }
        finally {
            Pop-Location
        }
    }

    It 'covers native archive validation and tool failure modes' {
        $case = Join-Path $env:TEMP ('native-release-focused-' + [guid]::NewGuid().ToString('N'))
        try {
            $fixture = New-NativeReleaseFixture $case
            Invoke-NativeReleaseFixture $fixture | Out-Null
            Invoke-ExpectedFailure { Invoke-NativeReleaseFixture $fixture @{ DumpbinPath = (Join-Path $case 'missing-dumpbin.exe') } } 'dumpbin missing'

            foreach ($mode in @('header-failure','wrong-machine','export-failure','bad-exports')) {
                $modeRoot = Join-Path $case $mode
                $modeFixture = New-NativeReleaseFixture $modeRoot $mode
                $pattern = switch ($mode) {
                    'header-failure' { 'dumpbin /headers failed' }
                    'wrong-machine' { 'not a Windows amd64 PE image' }
                    'export-failure' { 'dumpbin /exports failed' }
                    default { 'native DLL export mismatch' }
                }
                Invoke-ExpectedFailure { Invoke-NativeReleaseFixture $modeFixture } $pattern
            }

            $missing = New-NativeReleaseFixture (Join-Path $case 'missing-input')
            Remove-Item -LiteralPath $missing.Files.license -Force
            Invoke-ExpectedFailure { Invoke-NativeReleaseFixture $missing } 'required release file missing'

            $locked = New-NativeReleaseFixture (Join-Path $case 'locked')
            $stream = [IO.File]::Open((Join-Path $locked.Out '.native-release.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
            try { Invoke-ExpectedFailure { Invoke-NativeReleaseFixture $locked @{ LockTimeoutSeconds = 1 } } 'output lock timeout' }
            finally { $stream.Dispose() }
        }
        finally { Remove-Item -LiteralPath $case -Recurse -Force -ErrorAction SilentlyContinue }
    }

    It 'covers package input rejection and transaction recovery modes' {
        $case = Join-Path $env:TEMP ('package-release-focused-' + [guid]::NewGuid().ToString('N'))
        try {
            $badJson = New-PackageFixture (Join-Path $case 'bad-json')
            [IO.File]::WriteAllText($badJson.Manifest, '{bad')
            Invoke-ExpectedFailure { Invoke-PackageFixture $badJson } 'manifest is not valid JSON'

            $missingField = New-PackageFixture (Join-Path $case 'missing-field')
            [IO.File]::WriteAllText($missingField.Manifest, '{"schema":"miniapp-bridge.native-manifest.v1"}')
            Invoke-ExpectedFailure { Invoke-PackageFixture $missingField } 'manifest missing required field'

            $platform = New-PackageFixture (Join-Path $case 'platform')
            $value = Get-Content $platform.Manifest -Raw | ConvertFrom-Json
            $value.os = 'linux'
            [IO.File]::WriteAllText($platform.Manifest, ($value | ConvertTo-Json -Compress))
            Invoke-ExpectedFailure { Invoke-PackageFixture $platform } 'manifest platform contract mismatch'

            $missingNative = New-PackageFixture (Join-Path $case 'missing-native')
            Remove-Item -LiteralPath $missingNative.Asset -Force
            Invoke-ExpectedFailure { Invoke-PackageFixture $missingNative } 'required native release file missing'

            $badSums = New-PackageFixture (Join-Path $case 'bad-sums')
            [IO.File]::WriteAllText((Join-Path $badSums.Native 'SHA256SUMS'), 'invalid')
            Invoke-ExpectedFailure { Invoke-PackageFixture $badSums } 'native SHA256SUMS must contain exactly one entry'

            $fileOutput = New-PackageFixture (Join-Path $case 'file-output')
            [IO.File]::WriteAllText($fileOutput.Out, 'file')
            Invoke-ExpectedFailure { Invoke-PackageFixture $fileOutput } 'output directory is a file'

            $recovery = New-PackageFixture (Join-Path $case 'recovery')
            Invoke-PackageFixture $recovery | Out-Null
            $parent = Split-Path -Parent $recovery.Out
            $backup = Join-Path $parent '.release.manual-backup'
            $stage = Join-Path $parent '.release.manual-stage'
            $discard = Join-Path $parent '.release.manual-discard'
            Move-Item -LiteralPath $recovery.Out -Destination $backup
            New-Item -ItemType Directory -Path $recovery.Out,$stage,$discard | Out-Null
            [IO.File]::WriteAllText((Join-Path $recovery.Out 'stale'), 'stale')
            [IO.File]::WriteAllText((Join-Path $parent '.release.transaction.json'), ([ordered]@{ phase='publish'; stage=$stage; backup=$backup; discard=$discard } | ConvertTo-Json -Compress))
            Invoke-PackageFixture $recovery @{ TestFailPoint='DuringStage' } 2>$null
        }
        catch {
            if ($_.Exception.Message -notmatch 'injected release packaging failure: DuringStage') { throw }
        }
        finally { Remove-Item -LiteralPath $case -Recurse -Force -ErrorAction SilentlyContinue }
    }

    It 'covers native preparation metadata and rollback rejection modes' {
        $case = Join-Path $env:TEMP ('native-prepare-focused-' + [guid]::NewGuid().ToString('N'))
        try {
            $empty = Join-Path $case 'empty'
            Invoke-ExpectedFailure { & (Join-Path $scripts 'native-prepare.ps1') -CacheDirectory (Join-Path $case 'cache') -DestinationDirectory $empty -Rollback } 'no retained previous version'

            $invalid = Join-Path $case 'invalid'
            New-Item -ItemType Directory -Force -Path (Join-Path $invalid '.native-runtime') | Out-Null
            [IO.File]::WriteAllText((Join-Path $invalid '.native-runtime\transaction.json'), '{bad')
            Invoke-ExpectedFailure { & (Join-Path $scripts 'native-prepare.ps1') -CacheDirectory (Join-Path $case 'cache2') -DestinationDirectory $invalid -Rollback } 'invalid native transaction metadata'

            $recover = Join-Path $case 'recover'
            $runtime = Join-Path $recover '.native-runtime'
            $stale = Join-Path $runtime 'stale-stage'
            New-Item -ItemType Directory -Force -Path $stale | Out-Null
            [IO.File]::WriteAllText((Join-Path $runtime 'transaction.json'), ([ordered]@{ phase='prepare'; stage=$stale; previousVersionDirectory='' } | ConvertTo-Json -Compress))
            Invoke-ExpectedFailure { & (Join-Path $scripts 'native-prepare.ps1') -CacheDirectory (Join-Path $case 'cache3') -DestinationDirectory $recover -Rollback } 'no retained previous version'
        }
        finally { Remove-Item -LiteralPath $case -Recurse -Force -ErrorAction SilentlyContinue }
    }

    It 'executes default path resolution branches without retaining state' {
        $case = Join-Path $env:TEMP ('native-default-paths-' + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Force -Path $case | Out-Null
        try {
            Push-Location $case
            try {
                Invoke-ExpectedFailure { & (Join-Path $scripts 'native-prepare.ps1') -Rollback } 'no retained previous version'
            }
            finally { Pop-Location }

            $package = New-PackageFixture (Join-Path $case 'package')
            Invoke-ExpectedFailure { Invoke-PackageFixture $package @{ OutputDirectory = 'C:\' } } 'output directory must have a parent and leaf name'

            $defaultRelease = Join-Path $repo 'dist\release'
            $hadDefaultRelease = Test-Path -LiteralPath $defaultRelease
            if (-not $hadDefaultRelease -and
                (Test-Path -LiteralPath (Join-Path $repo 'dist\miniapp-bridge.exe')) -and
                (Test-Path -LiteralPath (Join-Path $repo 'dist\native\SHA256SUMS'))) {
                & (Join-Path $scripts 'package-windows-release.ps1') -Version 'v0.0.1' | Out-Null
                if ($LASTEXITCODE -ne 0) { throw 'default package path fixture failed' }
            }
            if (-not $hadDefaultRelease) { Remove-Item -LiteralPath $defaultRelease -Recurse -Force -ErrorAction SilentlyContinue }
        }
        finally { Remove-Item -LiteralPath $case -Recurse -Force -ErrorAction SilentlyContinue }
    }

    It 'covers reachable native prepare recovery validation and cleanup branches' {
        $case = Join-Path $env:TEMP ('native-prepare-branches-' + [guid]::NewGuid().ToString('N'))
        $global:nativePrepareExpandMode = ''
        $global:nativePrepareCopyMode = ''
        Mock Expand-Archive {
            param([string]$LiteralPath, [string]$DestinationPath, [switch]$Force)
            if ($global:nativePrepareExpandMode -ne 'skip') {
                [IO.Compression.ZipFile]::ExtractToDirectory($LiteralPath, $DestinationPath)
            }
        }
        Mock Copy-Item {
            param([string]$LiteralPath, [string]$Destination, [switch]$Force, [switch]$Recurse)
            [IO.File]::Copy($LiteralPath, $Destination, [bool]$Force)
            if ($global:nativePrepareCopyMode -eq 'corrupt-version' -and
                [string]$Destination -like '*.staging-*\miniapp-frida.dll') {
                [IO.File]::AppendAllText([string]$Destination, 'corrupt')
            }
        }
        try {
            $stale = New-NativePrepareFixture (Join-Path $case 'stale')
            New-Item -ItemType Directory -Force -Path ($stale.Archive + '.extracting') | Out-Null
            Invoke-NativePrepareFixture $stale | Out-Null

            $noPrevious = New-NativePrepareFixture (Join-Path $case 'no-previous')
            $runtime = Join-Path $noPrevious.Destination '.native-runtime'
            New-Item -ItemType Directory -Force -Path $runtime | Out-Null
            [IO.File]::WriteAllText((Join-Path $runtime 'transaction.json'), ([ordered]@{ phase='publish'; stage=''; previousVersionDirectory='' } | ConvertTo-Json -Compress))
            Invoke-NativePrepareFixture $noPrevious | Out-Null

            $incomplete = New-NativePrepareFixture (Join-Path $case 'incomplete')
            $runtime = Join-Path $incomplete.Destination '.native-runtime'
            $badVersion = Join-Path $runtime 'versions\bad'
            New-Item -ItemType Directory -Force -Path $badVersion | Out-Null
            [IO.File]::WriteAllText((Join-Path $runtime 'transaction.json'), ([ordered]@{ phase='publish'; stage=''; previousVersionDirectory=$badVersion } | ConvertTo-Json -Compress))
            Invoke-ExpectedFailure { Invoke-NativePrepareFixture $incomplete } 'native version directory is incomplete'

            $missingExtract = New-NativePrepareFixture (Join-Path $case 'missing-extract')
            $global:nativePrepareExpandMode = 'skip'
            Invoke-ExpectedFailure { Invoke-NativePrepareFixture $missingExtract } 'native archive missing manifest.json or miniapp-frida.dll'
            $global:nativePrepareExpandMode = ''

            $arrayManifest = New-NativePrepareFixture (Join-Path $case 'array-manifest') '[]'
            Invoke-ExpectedFailure { Invoke-NativePrepareFixture $arrayManifest } 'native manifest must be a JSON object'

            $corrupt = New-NativePrepareFixture (Join-Path $case 'corrupt-version')
            $global:nativePrepareCopyMode = 'corrupt-version'
            Invoke-ExpectedFailure { Invoke-NativePrepareFixture $corrupt } 'version directory DLL SHA-256 mismatch'
            $global:nativePrepareCopyMode = ''

            foreach ($leaf in @('manifest.json','miniapp-frida.dll')) {
                $directoryPath = New-NativePrepareFixture (Join-Path $case ('directory-' + $leaf.Replace('.','-')))
                New-Item -ItemType Directory -Force -Path (Join-Path $directoryPath.Destination $leaf) | Out-Null
                Invoke-ExpectedFailure { Invoke-NativePrepareFixture $directoryPath } 'native destination .* path is a directory'
            }

            $publishFailure = New-NativePrepareFixture (Join-Path $case 'publish-failure')
            $oldFailure = $env:MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_PUBLISH_FAILURE
            try {
                $env:MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_PUBLISH_FAILURE = 'after-dll-backup'
                Invoke-ExpectedFailure { Invoke-NativePrepareFixture $publishFailure } 'injected native publish failure'
            }
            finally { $env:MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_PUBLISH_FAILURE = $oldFailure }

            $retention = New-NativePrepareFixture (Join-Path $case 'retention')
            $versions = Join-Path $retention.Destination '.native-runtime\versions'
            foreach ($name in @('old-a','old-b','old-c')) {
                $path = Join-Path $versions $name
                New-Item -ItemType Directory -Force -Path $path | Out-Null
                [IO.File]::WriteAllText((Join-Path $path 'old'), 'old')
            }
            Invoke-NativePrepareFixture $retention @{ RetentionCount=2 } | Out-Null
        }
        finally {
            $global:nativePrepareExpandMode = ''
            $global:nativePrepareCopyMode = ''
            Remove-Item -LiteralPath $case -Recurse -Force -ErrorAction SilentlyContinue
        }
    }

    It 'covers online native archive download retry and terminal failure' {
        $case = Join-Path $env:TEMP ('native-download-branches-' + [guid]::NewGuid().ToString('N'))
        $global:nativeDownloadAttempts = 0
        Mock Invoke-WebRequest {
            param([string]$Uri, [string]$OutFile, [object]$UseBasicParsing, [object]$ErrorAction, [int]$TimeoutSec, [int]$ConnectionTimeoutSeconds, [int]$OperationTimeoutSeconds)
            $global:nativeDownloadAttempts++
            [IO.File]::WriteAllText($OutFile, 'wrong download')
        }
        try {
            $fixture = New-NativePrepareFixture $case
            Remove-Item -LiteralPath $fixture.Archive -Force
            Invoke-ExpectedFailure {
                & (Join-Path $scripts 'native-prepare.ps1') -CacheDirectory $fixture.Cache -DestinationDirectory $fixture.Destination -SourceURL 'https://fixture.invalid/native.zip' -ExpectedArchiveSHA256 ('A' * 64) -DownloadAttempts 2 -DownloadRetrySeconds 1 -DownloadTimeoutSeconds 1
            } 'native archive download failed after 2 attempts'
            $global:nativeDownloadAttempts | Should Be 2
        }
        finally {
            $global:nativeDownloadAttempts = 0
            Remove-Item -LiteralPath $case -Recurse -Force -ErrorAction SilentlyContinue
        }
    }

    It 'covers native release default discovery and missing staged entry branches' {
        $case = Join-Path $env:TEMP ('native-release-branches-' + [guid]::NewGuid().ToString('N'))
        $global:nativeReleaseDiscoveryMode = ''
        Mock Get-Command {
            param([string]$Name, [switch]$All, [object]$ErrorAction)
            if ($global:nativeReleaseDiscoveryMode -eq 'none' -and $Name -eq 'dumpbin.exe') { return $null }
            foreach ($directory in @($env:PATH -split ';')) {
                $candidate = Join-Path $directory $Name
                if ([IO.File]::Exists($candidate)) { return [pscustomobject]@{ Source=$candidate } }
            }
            return $null
        }
        Mock Get-ChildItem {
            param([string]$LiteralPath, [string]$Filter, [switch]$Recurse, [object]$ErrorAction)
            if ($global:nativeReleaseDiscoveryMode -eq 'none' -and [string]$LiteralPath -like 'C:\Program Files*') { return @() }
            return @()
        }
        try {
            $defaults = New-NativeReleaseFixture (Join-Path $case 'defaults')
            Remove-Item -LiteralPath $defaults.Files.license -Force
            Invoke-ExpectedFailure {
                & (Join-Path $scripts 'native-release.ps1') -LicenseFile $defaults.Files.license -FridaCopyingFile $defaults.Files.copying -FridaLibraryLicenseFile $defaults.Files.library -ZlibLicenseFile $defaults.Files.zlib -ThirdPartyNoticesFile $defaults.Files.notices -DumpbinPath $defaults.Dumpbin
            } 'required release file missing'

            $none = New-NativeReleaseFixture (Join-Path $case 'none')
            $global:nativeReleaseDiscoveryMode = 'none'
            Invoke-ExpectedFailure { Invoke-NativeReleaseFixture $none @{ DumpbinPath='' } } 'dumpbin.exe for Hostx64/x64 was not found'
            $global:nativeReleaseDiscoveryMode = ''

            $pathTool = New-NativeReleaseFixture (Join-Path $case 'path-tool')
            $toolDir = Join-Path $case 'tool-path'
            New-Item -ItemType Directory -Force -Path $toolDir | Out-Null
            [IO.File]::Copy($env:ComSpec, (Join-Path $toolDir 'dumpbin.exe'), $true)
            $oldPath = $env:PATH
            try {
                $env:PATH = "$toolDir;$oldPath"
                Invoke-ExpectedFailure { Invoke-NativeReleaseFixture $pathTool @{ DumpbinPath='' } } 'not a Windows amd64 PE image|dumpbin /headers failed'
            }
            finally { $env:PATH = $oldPath }
        }
        finally {
            $global:nativeReleaseDiscoveryMode = ''
            Remove-Item -LiteralPath $case -Recurse -Force -ErrorAction SilentlyContinue
        }
    }

    It 'covers package checksum rollback and final cleanup failures' {
        $case = Join-Path $env:TEMP ('package-release-branches-' + [guid]::NewGuid().ToString('N'))
        $global:packageCopyMode = ''
        $global:packageContentMode = ''
        $global:packageMoveMode = ''
        $global:packageRemoveMode = ''
        $global:packageRemoveCount = 0
        Mock Copy-Item {
            param([string]$LiteralPath, [string]$Destination, [switch]$Force, [switch]$Recurse)
            if ($global:packageCopyMode -eq 'skip-license' -and (Split-Path -Leaf ([string]$Destination)) -eq 'LICENSE') { return }
            [IO.File]::Copy($LiteralPath, $Destination, [bool]$Force)
            if ($global:packageCopyMode -eq 'alter-compat' -and [string]$Destination -like '*native-compat*windows-amd64.zip') {
                [IO.File]::AppendAllText([string]$Destination, 'different')
            }
        }
        Mock Get-Content {
            param([string]$LiteralPath, [string]$Path, [switch]$Raw, [string]$Encoding)
            $path = [string]$LiteralPath
            if ($path -like '*staging-*\SHA256SUMS' -and $path -notlike '*native-compat*' -and $global:packageContentMode) {
                switch ($global:packageContentMode) {
                    'count' { return @() }
                    'invalid' { return @('invalid','invalid','invalid') }
                    'missing' {
                        Write-Output (('0' * 64) + '  absent.zip')
                        Write-Output (('0' * 64) + '  absent2.zip')
                        Write-Output (('0' * 64) + '  absent3.zip')
                        return
                    }
                    'mismatch' {
                        Write-Output (('0' * 64) + '  miniapp-bridge-v0.0.1-windows-amd64.zip')
                        Write-Output (('0' * 64) + '  miniapp-frida-native-17.3.2-abi1-windows-amd64.zip')
                        Write-Output (('0' * 64) + '  manifest.json')
                        return
                    }
                }
            }
            if ($path -like '*native-compat\SHA256SUMS' -and $global:packageCopyMode -eq 'alter-compat') {
                $directory = Split-Path -Parent $path
                $assetName = 'miniapp-frida-native-17.3.2-abi1-windows-amd64.zip'
                $assetPath = Join-Path $directory $assetName
                return @("$(Get-TestSHA256 $assetPath)  $assetName")
            }
            $readPath = if ($LiteralPath) { $LiteralPath } else { $Path }
            $text = [IO.File]::ReadAllText($readPath)
            if ($Raw) { return $text }
            return @($text -split '\r?\n')
        }
        Mock Move-Item {
            param([string]$LiteralPath, [string]$Destination, [switch]$Force)
            if ($global:packageMoveMode -eq 'rollback-fail' -and [string]$LiteralPath -like '*.backup-*' -and [string]$Destination -like '*\release') {
                throw 'injected rollback move failure'
            }
            if ([IO.Directory]::Exists($LiteralPath)) {
                [IO.Directory]::Move($LiteralPath, $Destination)
            }
            else {
                if ($Force -and [IO.File]::Exists($Destination)) { [IO.File]::Delete($Destination) }
                [IO.File]::Move($LiteralPath, $Destination)
            }
        }
        Mock Remove-Item {
            param([object[]]$LiteralPath, [switch]$Recurse, [switch]$Force, [object]$ErrorAction)
            foreach ($item in @($LiteralPath)) {
                $path = [string]$item
                if ($global:packageRemoveMode -and $path -like ('*.' + $global:packageRemoveMode + '-*')) {
                    $global:packageRemoveCount++
                    if ($global:packageRemoveCount -eq 1) { continue }
                }
                if ([IO.Directory]::Exists($path)) { [IO.Directory]::Delete($path, $true) }
                elseif ([IO.File]::Exists($path)) { [IO.File]::Delete($path) }
            }
        }
        try {
            $missingStage = New-PackageFixture (Join-Path $case 'missing-stage')
            $global:packageCopyMode = 'skip-license'
            Invoke-ExpectedFailure { Invoke-PackageFixture $missingStage } 'release entry missing: LICENSE'
            $global:packageCopyMode = ''

            foreach ($mode in @('count','invalid','missing','mismatch')) {
                $fixture = New-PackageFixture (Join-Path $case $mode)
                $global:packageContentMode = $mode
                $pattern = switch ($mode) {
                    'count' { 'checksum entry count mismatch' }
                    'invalid' { 'invalid checksum line' }
                    'missing' { 'checksum asset missing' }
                    default { 'checksum mismatch' }
                }
                Invoke-ExpectedFailure { Invoke-PackageFixture $fixture } $pattern
                $global:packageContentMode = ''
            }

            $compat = New-PackageFixture (Join-Path $case 'compat')
            $global:packageCopyMode = 'alter-compat'
            Invoke-ExpectedFailure { Invoke-PackageFixture $compat } 'native compatibility asset differs'
            $global:packageCopyMode = ''

            $rollback = New-PackageFixture (Join-Path $case 'rollback-fail')
            Invoke-PackageFixture $rollback | Out-Null
            $global:packageMoveMode = 'rollback-fail'
            Invoke-ExpectedFailure { Invoke-PackageFixture $rollback @{ TestFailPoint='AfterBackup' } } 'release packaging failed and rollback failed'
            $global:packageMoveMode = ''

            foreach ($kind in @('discard','backup')) {
                $cleanup = New-PackageFixture (Join-Path $case ('cleanup-' + $kind))
                if ($kind -eq 'backup') { Invoke-PackageFixture $cleanup | Out-Null }
                $global:packageRemoveMode = $kind
                $global:packageRemoveCount = 0
                if ($kind -eq 'discard') {
                    Invoke-ExpectedFailure { Invoke-PackageFixture $cleanup @{ TestFailPoint='AfterPublish' } } 'injected release packaging failure'
                }
                else {
                    Invoke-PackageFixture $cleanup | Out-Null
                }
                $global:packageRemoveMode = ''
            }
        }
        finally {
            $global:packageCopyMode = ''
            $global:packageContentMode = ''
            $global:packageMoveMode = ''
            $global:packageRemoveMode = ''
            Remove-Item -LiteralPath $case -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}
