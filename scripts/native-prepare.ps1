param(
    [switch]$Offline,
    [ValidateRange(1, 600)][int]$LockTimeoutSeconds = 120,
    [ValidateRange(1, 10)][int]$DownloadAttempts = 3,
    [ValidateRange(1, 900)][int]$DownloadTimeoutSeconds = 300,
    [ValidateRange(0, 60)][int]$DownloadRetrySeconds = 5,
    [string]$Version = '17.3.2-abi1.1',
    [string]$CacheDirectory = '',
    [string]$DestinationDirectory = '',
    [string]$SourceURL = '',
    [string]$ExpectedArchiveSHA256 = '',
    [ValidateRange(2, 20)][int]$RetentionCount = 2,
    [switch]$Rollback,
    [string]$CanaryCommand = ''
)

$ErrorActionPreference = 'Stop'
$expectedArchiveSHA = ([string]$ExpectedArchiveSHA256).Trim().ToUpperInvariant()
if (-not $expectedArchiveSHA) {
    throw 'ExpectedArchiveSHA256 is required for native preparation and rollback'
}
if ($expectedArchiveSHA -and $expectedArchiveSHA -notmatch '^[0-9A-F]{64}$') {
    throw 'ExpectedArchiveSHA256 must contain exactly 64 hexadecimal characters'
}

$asset = "miniapp-frida-native-$Version-windows-amd64.zip"
$cache = if ($CacheDirectory) { [IO.Path]::GetFullPath($CacheDirectory) } else { Join-Path $env:LOCALAPPDATA "miniapp-bridge\native\$Version\windows-amd64" }
$destination = if ($DestinationDirectory) { [IO.Path]::GetFullPath($DestinationDirectory) } else { (Get-Location).Path }
$stateRoot = Join-Path $destination '.native-runtime'
$versionsRoot = Join-Path $stateRoot 'versions'
$trustRoot = Join-Path $stateRoot 'trust'
$destinationLockPath = Join-Path $stateRoot 'update.lock'
$journalPath = Join-Path $stateRoot 'transaction.json'
$currentPath = Join-Path $stateRoot 'current.json'
$rollbackPath = Join-Path $stateRoot 'rollback.json'
$url = if ($SourceURL) { $SourceURL } else { "https://github.com/Follen/miniapp-bridge/releases/download/native-v$Version/$asset" }
$archive = Join-Path $cache $asset
$lockPath = "$archive.lock"
$partial = "$archive.partial"
$stage = Join-Path $cache "$asset.extracting"
$dllName = 'miniapp-frida.dll'
$retainedArchiveName = 'source.zip'
$installed = Join-Path $destination $dllName
$installedManifest = Join-Path $destination 'manifest.json'
$temporaryDLL = "$installed.partial"
$temporaryManifest = "$installedManifest.partial"
$publishID = [guid]::NewGuid().ToString('N')
$backupDLL = "$installed.backup-$publishID"
$backupManifest = "$installedManifest.backup-$publishID"
$lock = $null
$destinationLock = $null
$timer = [Diagnostics.Stopwatch]::StartNew()

function Get-SHA256([string]$Path) {
    $stream = [IO.File]::OpenRead($Path)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha256.ComputeHash($stream))).Replace('-', '')
    }
    finally {
        $sha256.Dispose()
        $stream.Dispose()
    }
}

function Test-IntegerValue($Value) {
    return (($Value -is [byte]) -or ($Value -is [sbyte]) -or
        ($Value -is [int16]) -or ($Value -is [uint16]) -or
        ($Value -is [int32]) -or ($Value -is [uint32]) -or
        ($Value -is [int64]) -or ($Value -is [uint64]))
}

function Invoke-VerifiedDownload {
    param(
        [string]$URL,
        [string]$Destination,
        [string]$ExpectedSHA256,
        [string]$DisplayName
    )

    $webRequest = Get-Command Invoke-WebRequest
    $lastError = $null
    for ($attempt = 1; $attempt -le $DownloadAttempts; $attempt++) {
        Remove-Item -LiteralPath $Destination -Force -ErrorAction SilentlyContinue
        $parameters = @{
            UseBasicParsing = $true
            Uri             = $URL
            OutFile         = $Destination
            ErrorAction     = 'Stop'
        }
        if ($webRequest.Parameters.ContainsKey('ConnectionTimeoutSeconds')) {
            $parameters.ConnectionTimeoutSeconds = [Math]::Min(30, $DownloadTimeoutSeconds)
            $parameters.OperationTimeoutSeconds = $DownloadTimeoutSeconds
        } elseif ($webRequest.Parameters.ContainsKey('TimeoutSec')) {
            $parameters.TimeoutSec = $DownloadTimeoutSeconds
        }

        Write-Host "Downloading $DisplayName attempt=$attempt/$DownloadAttempts"
        try {
            Invoke-WebRequest @parameters
            $downloadHash = Get-SHA256 $Destination
            if ($downloadHash -cne $ExpectedSHA256) {
                throw "native archive SHA-256 mismatch: expected $ExpectedSHA256, got $downloadHash"
            }
            return
        } catch {
            $lastError = $_
            Remove-Item -LiteralPath $Destination -Force -ErrorAction SilentlyContinue
            if ($attempt -lt $DownloadAttempts) {
                Write-Warning "native archive download attempt $attempt failed: $($_.Exception.Message)"
                if ($DownloadRetrySeconds -gt 0) {
                    Start-Sleep -Seconds $DownloadRetrySeconds
                }
            }
        }
    }
    throw "native archive download failed after $DownloadAttempts attempts: $($lastError.Exception.Message)"
}

function Get-NativeArchiveEntrySHA256 {
    param([string]$Path, [string]$EntryName)
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($Path)
    try {
        $entries = @($zip.Entries | Where-Object { $_.FullName.Replace('\', '/') -ceq $EntryName })
        if ($entries.Count -ne 1) {
            throw "native retained archive must contain exactly one root $EntryName"
        }
        $stream = $entries[0].Open()
        $sha256 = [Security.Cryptography.SHA256]::Create()
        try {
            return ([BitConverter]::ToString($sha256.ComputeHash($stream))).Replace('-', '')
        }
        finally {
            $sha256.Dispose()
            $stream.Dispose()
        }
    }
    finally { $zip.Dispose() }
}

if (-not ('MiniAppBridge.NativeFileIdentity' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;

namespace MiniAppBridge {
    public sealed class NativeFileIdentity {
        [StructLayout(LayoutKind.Sequential)]
        private struct BY_HANDLE_FILE_INFORMATION {
            public uint FileAttributes;
            public System.Runtime.InteropServices.ComTypes.FILETIME CreationTime;
            public System.Runtime.InteropServices.ComTypes.FILETIME LastAccessTime;
            public System.Runtime.InteropServices.ComTypes.FILETIME LastWriteTime;
            public uint VolumeSerialNumber;
            public uint FileSizeHigh;
            public uint FileSizeLow;
            public uint NumberOfLinks;
            public uint FileIndexHigh;
            public uint FileIndexLow;
        }

        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        private static extern IntPtr CreateFileW(string name, uint access, uint share, IntPtr security,
            uint disposition, uint flags, IntPtr template);
        [DllImport("kernel32.dll", SetLastError = true)]
        private static extern bool GetFileInformationByHandle(IntPtr handle, out BY_HANDLE_FILE_INFORMATION info);
        [DllImport("kernel32.dll")]
        private static extern bool CloseHandle(IntPtr handle);
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        private static extern IntPtr LoadLibraryExW(string fileName, IntPtr file, uint flags);
        [DllImport("kernel32.dll", CharSet = CharSet.Ansi, SetLastError = true)]
        private static extern IntPtr GetProcAddress(IntPtr module, string name);
        [DllImport("kernel32.dll")]
        private static extern bool FreeLibrary(IntPtr module);

        public static string Read(string path, bool directory, out uint links, out bool reparse) {
            const uint FILE_READ_ATTRIBUTES = 0x80;
            const uint FILE_SHARE_READ = 0x1;
            const uint OPEN_EXISTING = 3;
            const uint FILE_FLAG_BACKUP_SEMANTICS = 0x02000000;
            const uint FILE_FLAG_OPEN_REPARSE_POINT = 0x00200000;
            IntPtr handle = CreateFileW(path, FILE_READ_ATTRIBUTES, FILE_SHARE_READ, IntPtr.Zero,
                OPEN_EXISTING, (directory ? FILE_FLAG_BACKUP_SEMANTICS : 0) | FILE_FLAG_OPEN_REPARSE_POINT,
                IntPtr.Zero);
            if (handle == new IntPtr(-1)) throw new Win32Exception(Marshal.GetLastWin32Error());
            try {
                BY_HANDLE_FILE_INFORMATION info;
                if (!GetFileInformationByHandle(handle, out info))
                    throw new Win32Exception(Marshal.GetLastWin32Error());
                links = info.NumberOfLinks;
                reparse = (info.FileAttributes & 0x400) != 0;
                return info.VolumeSerialNumber.ToString("X8") + ":" +
                    info.FileIndexHigh.ToString("X8") + info.FileIndexLow.ToString("X8");
            } finally { CloseHandle(handle); }
        }

        public static string[] MissingExports(string path, string[] exports) {
            const uint DONT_RESOLVE_DLL_REFERENCES = 0x1;
            IntPtr module = LoadLibraryExW(path, IntPtr.Zero, DONT_RESOLVE_DLL_REFERENCES);
            if (module == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
            try {
                System.Collections.Generic.List<string> missing =
                    new System.Collections.Generic.List<string>();
                foreach (string name in exports) {
                    if (GetProcAddress(module, name) == IntPtr.Zero) missing.Add(name);
                }
                return missing.ToArray();
            } finally { FreeLibrary(module); }
        }
    }
}
'@
}

function Get-NativeFileIdentity {
    param([string]$Path, [switch]$Directory)
    [uint32]$links = 0
    $reparse = $false
    $identity = [MiniAppBridge.NativeFileIdentity]::Read(
        [IO.Path]::GetFullPath($Path), [bool]$Directory, [ref]$links, [ref]$reparse)
    return [pscustomobject]@{ Identity = $identity; Links = $links; Reparse = $reparse }
}

function Assert-NativeIdentity([string]$Path, $Expected, [switch]$Directory) {
    $actual = Get-NativeFileIdentity -Path $Path -Directory:$Directory
    if ($actual.Reparse) { throw "native path must not be a reparse point: $Path" }
    if (-not $Directory -and $actual.Links -ne 1) { throw "native file must have exactly one hard link: $Path" }
    if ($null -ne $Expected -and $actual.Identity -cne $Expected.Identity) {
        throw "native file identity changed during publication: $Path"
    }
    return $actual
}

function Assert-NativeSignature([string]$Path) {
    $signature = Get-AuthenticodeSignature -LiteralPath $Path
    if ($null -eq $signature -or $signature.Status -eq [Management.Automation.SignatureStatus]::NotSigned) {
        return [pscustomobject]@{ SignerThumbprint = ''; TimestampThumbprint = '' }
    }
    if ($signature.Status -ne [Management.Automation.SignatureStatus]::Valid) {
        throw "native Authenticode signature is invalid: $Path"
    }
    if ($null -eq $signature.SignerCertificate -or -not $signature.SignerCertificate.Thumbprint) {
        throw "native Authenticode signer certificate is missing: $Path"
    }
    if ($null -eq $signature.TimeStamperCertificate -or -not $signature.TimeStamperCertificate.Thumbprint) {
        throw "native Authenticode trusted timestamp is missing: $Path"
    }
    $signerChain = [Security.Cryptography.X509Certificates.X509Chain]::new()
    $timestampChain = [Security.Cryptography.X509Certificates.X509Chain]::new()
    try {
        foreach ($chain in @($signerChain, $timestampChain)) {
            $chain.ChainPolicy.RevocationMode = [Security.Cryptography.X509Certificates.X509RevocationMode]::Online
            $chain.ChainPolicy.VerificationFlags = [Security.Cryptography.X509Certificates.X509VerificationFlags]::NoFlag
        }
        if (-not $signerChain.Build($signature.SignerCertificate)) {
            throw "native Authenticode signer certificate is not trusted: $Path"
        }
        if (-not $timestampChain.Build($signature.TimeStamperCertificate)) {
            throw "native Authenticode timestamp certificate is not trusted: $Path"
        }
        return [pscustomobject]@{
            SignerThumbprint = $signature.SignerCertificate.Thumbprint.ToUpperInvariant()
            TimestampThumbprint = $signature.TimeStamperCertificate.Thumbprint.ToUpperInvariant()
        }
    }
    finally { $signerChain.Dispose(); $timestampChain.Dispose() }
}

function Get-NativeRequiredExports {
    return @(
        'mb_abi_version', 'mb_native_version', 'mb_frida_core_version', 'mb_zlib_version',
        'mb_zlib_compress', 'mb_zlib_decompress', 'mb_bytes_free', 'mb_device_open',
        'mb_device_enumerate', 'mb_processes_free', 'mb_device_attach', 'mb_device_close',
        'mb_runtime_shutdown', 'mb_session_load_script', 'mb_session_detach', 'mb_script_post',
        'mb_script_unload', 'mb_error_free'
    )
}

function Assert-NativeExports([string]$Path, [string[]]$RequiredExports) {
    $missing = @([MiniAppBridge.NativeFileIdentity]::MissingExports($Path, $RequiredExports))
    if ($missing.Count -ne 0) {
        throw "native DLL required exports missing: $($missing -join ', ')"
    }
}

function Read-NativeManifest([string]$Path) {
    try { $manifest = Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { throw "native manifest is not valid JSON: $($_.Exception.Message)" }
    if ($null -eq $manifest -or $manifest -is [array]) { throw 'native manifest must be a JSON object' }
    return $manifest
}

function Assert-NativeVersion {
    param(
        [string]$VersionDirectory,
        [switch]$Published,
        [switch]$RequireTrust,
        [string]$ExpectedTrustSHA256 = ''
    )

    $directoryPath = [IO.Path]::GetFullPath($VersionDirectory).TrimEnd('\')
    $versionsPath = [IO.Path]::GetFullPath($versionsRoot).TrimEnd('\')
    if (-not $Published -and [IO.Path]::GetDirectoryName($directoryPath).TrimEnd('\') -cne $versionsPath) {
        throw "native version directory is outside retained versions root: $VersionDirectory"
    }
    $directoryIdentity = Assert-NativeIdentity -Path $directoryPath -Directory
    $manifestPath = Join-Path $directoryPath 'manifest.json'
    $dllPath = Join-Path $directoryPath $dllName
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $dllPath -PathType Leaf)) {
        throw "native version directory is incomplete: $VersionDirectory"
    }
    $manifestIdentity = Assert-NativeIdentity -Path $manifestPath
    $dllIdentity = Assert-NativeIdentity -Path $dllPath
    $manifest = Read-NativeManifest $manifestPath
    $requiredFields = @('schema', 'nativeVersion', 'fridaCoreVersion', 'zlibVersion',
        'abiVersion', 'os', 'arch', 'dll', 'size', 'sha256', 'requiredExports')
    $propertyNames = @($manifest.PSObject.Properties | ForEach-Object { $_.Name })
    if ($propertyNames.Count -ne $requiredFields.Count) { throw 'native manifest fixed schema mismatch' }
    foreach ($field in $requiredFields) {
        if (-not ($propertyNames -ccontains $field)) { throw "native manifest missing required field: $field" }
    }
    $expectedStrings = [ordered]@{
        schema = 'miniapp-bridge.native-manifest.v1'; fridaCoreVersion = '17.3.2';
        zlibVersion = '1.3.1'; os = 'windows'; arch = 'amd64'; dll = $dllName
    }
    foreach ($field in $expectedStrings.Keys) {
        $value = $manifest.PSObject.Properties[$field].Value
        if (-not ($value -is [string]) -or $value -cne $expectedStrings[$field]) {
            throw "native manifest field mismatch: $field"
        }
    }
    if (-not ($manifest.nativeVersion -is [string]) -or -not $manifest.nativeVersion) {
        throw 'native manifest field mismatch: nativeVersion'
    }
    if (-not (Test-IntegerValue $manifest.abiVersion) -or [int64]$manifest.abiVersion -ne 1) {
        throw 'native manifest field mismatch: abiVersion'
    }
    $dllInfo = Get-Item -LiteralPath $dllPath
    if ($dllInfo.Length -le 0 -or -not (Test-IntegerValue $manifest.size) -or
        [int64]$manifest.size -ne [int64]$dllInfo.Length) { throw 'native manifest field mismatch: size' }
    $hash = Get-SHA256 $dllPath
    if (-not ($manifest.sha256 -is [string]) -or $manifest.sha256 -notmatch '^[0-9A-Fa-f]{64}$' -or
        $manifest.sha256.ToUpperInvariant() -cne $hash) { throw "native DLL SHA-256 mismatch: got $hash" }
    $requiredExports = @(Get-NativeRequiredExports)
    $actualExports = @($manifest.requiredExports)
    if (@($actualExports | Where-Object { -not ($_ -is [string]) }).Count -ne 0 -or
        $actualExports.Count -ne $requiredExports.Count -or
        @($requiredExports | Where-Object { -not ($actualExports -ccontains $_) }).Count -ne 0) {
        throw 'native manifest required export set mismatch'
    }
    Assert-NativeExports -Path $dllPath -RequiredExports $requiredExports
    $signatureEvidence = Assert-NativeSignature $dllPath
    if (-not $Published) {
        $expectedDirectoryName = "$($manifest.nativeVersion)-$($hash.Substring(0, 16))"
        if ([IO.Path]::GetFileName($directoryPath) -cne $expectedDirectoryName) {
            throw 'native version directory binding mismatch'
        }
    }
    $evidence = [pscustomobject]@{
        Directory = $directoryPath; ManifestPath = $manifestPath; DLLPath = $dllPath;
        ArchivePath = (Join-Path $directoryPath $retainedArchiveName)
        Hash = $hash; Manifest = $manifest; DirectoryIdentity = $directoryIdentity;
        ManifestIdentity = $manifestIdentity; DLLIdentity = $dllIdentity;
        Signature = $signatureEvidence
    }
    if ($RequireTrust) {
        $archiveIdentity = Assert-NativeTrustRecord -Evidence $evidence -ExpectedSHA256 $ExpectedTrustSHA256
        $evidence | Add-Member -NotePropertyName ArchiveIdentity -NotePropertyValue $archiveIdentity
    }
    return $evidence
}

function Get-NativeTrustPath([string]$VersionDirectory) {
    return Join-Path $trustRoot "$([IO.Path]::GetFileName([IO.Path]::GetFullPath($VersionDirectory).TrimEnd('\'))).json"
}

function Write-NativeTrustRecord($Evidence, [string]$ArchiveSHA256) {
    $path = Get-NativeTrustPath $Evidence.Directory
    if (Test-Path -LiteralPath $path) {
        Assert-NativeTrustRecord -Evidence $Evidence -ExpectedSHA256 $ArchiveSHA256 | Out-Null
        return
    }
    $archiveIdentity = Assert-NativeIdentity -Path $Evidence.ArchivePath
    $record = [ordered]@{
        schema = 'miniapp-bridge.native-trust.v1'; archiveSHA256 = $ArchiveSHA256
        versionDirectoryName = [IO.Path]::GetFileName($Evidence.Directory)
        manifestJSON = ($Evidence.Manifest | ConvertTo-Json -Compress -Depth 8)
        dllSHA256 = $Evidence.Hash; dllSize = [int64](Get-Item -LiteralPath $Evidence.DLLPath).Length
        signerThumbprint = $Evidence.Signature.SignerThumbprint
        timestampThumbprint = $Evidence.Signature.TimestampThumbprint
        directoryIdentity = $Evidence.DirectoryIdentity.Identity
        manifestIdentity = $Evidence.ManifestIdentity.Identity
        dllIdentity = $Evidence.DLLIdentity.Identity
        archiveIdentity = $archiveIdentity.Identity
    }
    Write-AtomicJSON -Path $path -Value $record
    Assert-NativeIdentity -Path $path | Out-Null
}

function Assert-NativeTrustRecord {
    param($Evidence, [string]$ExpectedSHA256 = '')
    $path = Get-NativeTrustPath $Evidence.Directory
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "native retained version has no trust record: $($Evidence.Directory)"
    }
    Assert-NativeIdentity -Path $path | Out-Null
    $record = Read-Pointer $path
    $archiveIdentity = Assert-NativeIdentity -Path $Evidence.ArchivePath
    $archiveSHA = Get-SHA256 $Evidence.ArchivePath
    $archiveManifestSHA = Get-NativeArchiveEntrySHA256 -Path $Evidence.ArchivePath -EntryName 'manifest.json'
    $archiveDLLSHA = Get-NativeArchiveEntrySHA256 -Path $Evidence.ArchivePath -EntryName $dllName
    $fields = @('schema', 'archiveSHA256', 'versionDirectoryName', 'manifestJSON', 'dllSHA256',
        'dllSize', 'signerThumbprint', 'timestampThumbprint', 'directoryIdentity', 'manifestIdentity',
        'dllIdentity', 'archiveIdentity')
    $names = @($record.PSObject.Properties | ForEach-Object Name)
    if ($names.Count -ne $fields.Count -or @($fields | Where-Object { -not ($names -ccontains $_) }).Count -ne 0) {
        throw 'native trust record fixed schema mismatch'
    }
    if ($record.schema -cne 'miniapp-bridge.native-trust.v1' -or
        ($ExpectedSHA256 -and $record.archiveSHA256 -cne $ExpectedSHA256) -or
        $archiveSHA -cne $record.archiveSHA256 -or
        $archiveManifestSHA -cne (Get-SHA256 $Evidence.ManifestPath) -or
        $archiveDLLSHA -cne $Evidence.Hash -or
        $record.versionDirectoryName -cne [IO.Path]::GetFileName($Evidence.Directory) -or
        $record.manifestJSON -cne ($Evidence.Manifest | ConvertTo-Json -Compress -Depth 8) -or
        $record.dllSHA256 -cne $Evidence.Hash -or
        -not (Test-IntegerValue $record.dllSize) -or [int64]$record.dllSize -ne [int64](Get-Item -LiteralPath $Evidence.DLLPath).Length -or
        $record.signerThumbprint -cne $Evidence.Signature.SignerThumbprint -or
        $record.timestampThumbprint -cne $Evidence.Signature.TimestampThumbprint -or
        $record.directoryIdentity -cne $Evidence.DirectoryIdentity.Identity -or
        $record.manifestIdentity -cne $Evidence.ManifestIdentity.Identity -or
        $record.dllIdentity -cne $Evidence.DLLIdentity.Identity -or
        $record.archiveIdentity -cne $archiveIdentity.Identity) {
        throw 'native retained version does not match its trust record'
    }
    return $archiveIdentity
}

function Write-AtomicJSON {
    param([string]$Path, [object]$Value)
    $temporary = "$Path.$([guid]::NewGuid().ToString('N')).partial"
    try {
        [IO.File]::WriteAllText($temporary, ($Value | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
        Move-Item -LiteralPath $temporary -Destination $Path -Force
    }
    finally {
        Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue
    }
}

function Read-Pointer([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $null }
    try { return Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { throw "invalid native transaction metadata: $Path`: $($_.Exception.Message)" }
}

function Publish-Version {
    param([string]$VersionDirectory)
    $sourceDLL = Join-Path $VersionDirectory $dllName
    $sourceManifest = Join-Path $VersionDirectory 'manifest.json'
    if (-not (Test-Path -LiteralPath $sourceDLL -PathType Leaf) -or
        -not (Test-Path -LiteralPath $sourceManifest -PathType Leaf)) {
        throw "native version directory is incomplete: $VersionDirectory"
    }
    Copy-Item -LiteralPath $sourceManifest -Destination $temporaryManifest -Force
    Copy-Item -LiteralPath $sourceDLL -Destination $temporaryDLL -Force
    if (Test-Path -LiteralPath $installed) { Remove-Item -LiteralPath $installed -Force }
    if (Test-Path -LiteralPath $installedManifest) { Remove-Item -LiteralPath $installedManifest -Force }
    Move-Item -LiteralPath $temporaryManifest -Destination $installedManifest -Force
    Move-Item -LiteralPath $temporaryDLL -Destination $installed -Force
}

function Open-NativeVersionLease {
    param([string]$VersionDirectory, [string]$ExpectedTrustSHA256 = '')
    $evidence = Assert-NativeVersion -VersionDirectory $VersionDirectory -RequireTrust `
        -ExpectedTrustSHA256 $ExpectedTrustSHA256
    $manifestLock = $null
    $dllLock = $null
    $archiveLock = $null
    try {
        $manifestLock = [IO.File]::Open($evidence.ManifestPath, [IO.FileMode]::Open,
            [IO.FileAccess]::Read, [IO.FileShare]::Read)
        $dllLock = [IO.File]::Open($evidence.DLLPath, [IO.FileMode]::Open,
            [IO.FileAccess]::Read, [IO.FileShare]::Read)
        $archiveLock = [IO.File]::Open($evidence.ArchivePath, [IO.FileMode]::Open,
            [IO.FileAccess]::Read, [IO.FileShare]::Read)
        Assert-NativeIdentity -Path $evidence.Directory -Expected $evidence.DirectoryIdentity -Directory | Out-Null
        Assert-NativeIdentity -Path $evidence.ManifestPath -Expected $evidence.ManifestIdentity | Out-Null
        Assert-NativeIdentity -Path $evidence.DLLPath -Expected $evidence.DLLIdentity | Out-Null
        Assert-NativeTrustRecord -Evidence $evidence -ExpectedSHA256 $ExpectedTrustSHA256 | Out-Null
        return [pscustomobject]@{
            Evidence = $evidence
            ManifestLock = $manifestLock
            DLLLock = $dllLock
            ArchiveLock = $archiveLock
            ExpectedTrustSHA256 = $ExpectedTrustSHA256
        }
    }
    catch {
        if ($null -ne $archiveLock) { $archiveLock.Dispose() }
        if ($null -ne $dllLock) { $dllLock.Dispose() }
        if ($null -ne $manifestLock) { $manifestLock.Dispose() }
        throw
    }
}

function Close-NativeVersionLease($Lease) {
    if ($null -eq $Lease) { return }
    if ($null -ne $Lease.ArchiveLock) { $Lease.ArchiveLock.Dispose() }
    if ($null -ne $Lease.DLLLock) { $Lease.DLLLock.Dispose() }
    if ($null -ne $Lease.ManifestLock) { $Lease.ManifestLock.Dispose() }
}

function Publish-VerifiedVersion($Lease) {
    $evidence = $Lease.Evidence
    Assert-NativeIdentity -Path $evidence.Directory -Expected $evidence.DirectoryIdentity -Directory | Out-Null
    Assert-NativeIdentity -Path $evidence.ManifestPath -Expected $evidence.ManifestIdentity | Out-Null
    Assert-NativeIdentity -Path $evidence.DLLPath -Expected $evidence.DLLIdentity | Out-Null
    Assert-NativeTrustRecord -Evidence $evidence -ExpectedSHA256 $Lease.ExpectedTrustSHA256 | Out-Null
    Publish-Version $evidence.Directory
    Assert-NativeIdentity -Path $evidence.ManifestPath -Expected $evidence.ManifestIdentity | Out-Null
    Assert-NativeIdentity -Path $evidence.DLLPath -Expected $evidence.DLLIdentity | Out-Null
}

function Assert-PublishedVersion($ExpectedEvidence) {
    $published = Assert-NativeVersion -VersionDirectory $destination -Published
    if ($published.Hash -cne $ExpectedEvidence.Hash -or
        (Get-SHA256 $published.ManifestPath) -cne (Get-SHA256 $ExpectedEvidence.ManifestPath)) {
        throw 'published hash or manifest differs from retained version'
    }
    return $published
}

function Open-NativePointerLease {
    param($Pointer, [string]$Context)
    if ($null -eq $Pointer -or -not ($Pointer.versionDirectory -is [string]) -or
        -not $Pointer.versionDirectory) {
        throw "native $Context pointer has no version directory"
    }
    if (-not ($Pointer.sha256 -is [string]) -or $Pointer.sha256 -notmatch '^[0-9A-Fa-f]{64}$') {
        throw "native $Context pointer has no valid DLL SHA-256"
    }
    if (-not ($Pointer.archiveSHA256 -is [string]) -or
        $Pointer.archiveSHA256 -notmatch '^[0-9A-Fa-f]{64}$') {
        throw "native $Context pointer has no archive SHA-256"
    }
    $lease = Open-NativeVersionLease -VersionDirectory ([string]$Pointer.versionDirectory) `
        -ExpectedTrustSHA256 $Pointer.archiveSHA256.ToUpperInvariant()
    if ($lease.Evidence.Hash -cne $Pointer.sha256.ToUpperInvariant()) {
        Close-NativeVersionLease $lease
        throw "native $Context retained version does not match pointer DLL SHA-256"
    }
    return $lease
}

function New-NativeCurrentPointer($Lease) {
    return [ordered]@{
        versionDirectory = $Lease.Evidence.Directory
        nativeVersion = $Lease.Evidence.Manifest.nativeVersion
        sha256 = $Lease.Evidence.Hash
        archiveSHA256 = (Get-SHA256 $Lease.Evidence.ArchivePath)
    }
}

function Recover-NativeTransaction {
    $journal = Read-Pointer $journalPath
    if ($null -eq $journal) { return }
    if ($journal.phase -in @('prepare', 'verify')) {
        if ($journal.stage -and (Test-Path -LiteralPath $journal.stage)) {
            Remove-Item -LiteralPath $journal.stage -Recurse -Force
        }
    }
    elseif ($journal.phase -eq 'publish') {
        if ($journal.previousVersionDirectory -and (Test-Path -LiteralPath $journal.previousVersionDirectory -PathType Container)) {
            $recoveryPointer = [pscustomobject]@{
                versionDirectory = [string]$journal.previousVersionDirectory
                sha256 = $journal.previousSHA256
                archiveSHA256 = $journal.previousArchiveSHA256
            }
            $recoveryLease = Open-NativePointerLease -Pointer $recoveryPointer -Context 'journal recovery'
            try {
                Publish-VerifiedVersion $recoveryLease
                Assert-PublishedVersion $recoveryLease.Evidence | Out-Null
                Write-AtomicJSON -Path $currentPath -Value (New-NativeCurrentPointer $recoveryLease)
            }
            finally { Close-NativeVersionLease $recoveryLease }
        }
        elseif ($journal.legacyBackupDLL -and $journal.legacyBackupManifest -and
            (Test-Path -LiteralPath $journal.legacyBackupDLL -PathType Leaf) -and
            (Test-Path -LiteralPath $journal.legacyBackupManifest -PathType Leaf)) {
            Copy-Item -LiteralPath $journal.legacyBackupManifest -Destination $temporaryManifest -Force
            Copy-Item -LiteralPath $journal.legacyBackupDLL -Destination $temporaryDLL -Force
            Remove-Item -LiteralPath $installed,$installedManifest -Force -ErrorAction SilentlyContinue
            Move-Item -LiteralPath $temporaryManifest -Destination $installedManifest -Force
            Move-Item -LiteralPath $temporaryDLL -Destination $installed -Force
            Remove-Item -LiteralPath $journal.legacyBackupDLL,$journal.legacyBackupManifest -Force
            Remove-Item -LiteralPath $currentPath -Force -ErrorAction SilentlyContinue
        }
        elseif ($journal.legacyBackupDLL -or $journal.legacyBackupManifest) {
            if (-not ((Test-Path -LiteralPath $installed -PathType Leaf) -and
                (Test-Path -LiteralPath $installedManifest -PathType Leaf))) {
                throw 'native interrupted legacy publication has no complete recovery source'
            }
            Remove-Item -LiteralPath $journal.legacyBackupDLL,$journal.legacyBackupManifest `
                -Force -ErrorAction SilentlyContinue
        }
        else {
            Remove-Item -LiteralPath $installed,$installedManifest,$currentPath -Force -ErrorAction SilentlyContinue
        }
    }
    Remove-Item -LiteralPath $journalPath -Force
}

function Invoke-Rollback {
    $record = Read-Pointer $rollbackPath
    $current = Read-Pointer $currentPath
    if ($record -and $current -and $record.to -ceq $current.versionDirectory) {
        $noopLease = Open-NativeVersionLease -VersionDirectory ([string]$record.to) `
            -ExpectedTrustSHA256 $expectedArchiveSHA
        try {
            Assert-PublishedVersion $noopLease.Evidence | Out-Null
            Write-Output "native_rollback=noop"
            return
        }
        finally { Close-NativeVersionLease $noopLease }
    }
    $candidates = @(Get-ChildItem -LiteralPath $versionsRoot -Directory -ErrorAction SilentlyContinue |
        Where-Object { $null -eq $current -or $_.FullName -cne $current.versionDirectory } |
        Sort-Object LastWriteTimeUtc -Descending)
    if ($candidates.Count -eq 0) { throw 'native rollback has no retained previous version' }
    $target = $candidates[0].FullName
    $targetEvidence = Assert-NativeVersion -VersionDirectory $target -RequireTrust `
        -ExpectedTrustSHA256 $expectedArchiveSHA
    $targetLease = $null
    $currentLease = $null
    try {
        try {
            $targetLease = Open-NativeVersionLease -VersionDirectory $target `
                -ExpectedTrustSHA256 $expectedArchiveSHA
        }
        catch { throw "native rollback source identity changed after validation: $($_.Exception.Message)" }
        if ($current -and $current.versionDirectory) {
            $currentLease = Open-NativePointerLease -Pointer $current -Context 'current'
        }

        $oldCurrentBytes = if (Test-Path -LiteralPath $currentPath -PathType Leaf) { [IO.File]::ReadAllBytes($currentPath) } else { $null }
        $oldRollbackBytes = if (Test-Path -LiteralPath $rollbackPath -PathType Leaf) { [IO.File]::ReadAllBytes($rollbackPath) } else { $null }
        try {
            Publish-VerifiedVersion $targetLease
            try { Assert-PublishedVersion $targetLease.Evidence | Out-Null }
            catch { throw "native rollback published validation failed: $($_.Exception.Message)" }
            Write-AtomicJSON -Path $currentPath -Value (New-NativeCurrentPointer $targetLease)
            Write-AtomicJSON -Path $rollbackPath -Value ([ordered]@{
                from = if ($current) { $current.versionDirectory } else { '' }
                to = $target
            })
            Write-Output "native_rollback=$target"
        }
        catch {
            $rollbackFailure = $_
            $restorationErrors = @()
            try {
                if ($null -ne $currentLease) {
                    Publish-VerifiedVersion $currentLease
                    Assert-PublishedVersion $currentLease.Evidence | Out-Null
                }
            }
            catch { $restorationErrors += $_.Exception.Message }
            try {
                if ($null -eq $oldCurrentBytes) { Remove-Item -LiteralPath $currentPath -Force -ErrorAction SilentlyContinue }
                else { [IO.File]::WriteAllBytes($currentPath, $oldCurrentBytes) }
            }
            catch { $restorationErrors += $_.Exception.Message }
            try {
                if ($null -eq $oldRollbackBytes) { Remove-Item -LiteralPath $rollbackPath -Force -ErrorAction SilentlyContinue }
                else { [IO.File]::WriteAllBytes($rollbackPath, $oldRollbackBytes) }
            }
            catch { $restorationErrors += $_.Exception.Message }
            if ($restorationErrors.Count -ne 0) {
                throw "native rollback failed: $($rollbackFailure.Exception.Message); restoration failed: $($restorationErrors -join '; ')"
            }
            throw $rollbackFailure
        }
    }
    finally {
        Close-NativeVersionLease $currentLease
        Close-NativeVersionLease $targetLease
    }
}

New-Item -ItemType Directory -Force -Path $cache,$destination,$stateRoot,$versionsRoot,$trustRoot | Out-Null
try {
    while ($null -eq $lock) {
        try {
            $lock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        }
        catch [IO.IOException] {
            if ($timer.Elapsed.TotalSeconds -ge $LockTimeoutSeconds) {
                throw "native cache lock timeout: $lockPath"
            }
            Start-Sleep -Milliseconds 100
        }
    }

    $destinationTimer = [Diagnostics.Stopwatch]::StartNew()
    while ($null -eq $destinationLock) {
        try {
            $destinationLock = [IO.File]::Open($destinationLockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        }
        catch [IO.IOException] {
            if ($destinationTimer.Elapsed.TotalSeconds -ge $LockTimeoutSeconds) {
                throw "native destination lock timeout: $destinationLockPath"
            }
            Start-Sleep -Milliseconds 100
        }
    }
    Recover-NativeTransaction
    if ($Rollback) {
        Invoke-Rollback
        return
    }

    if (-not (Test-Path -LiteralPath $archive -PathType Leaf)) {
        if ($Offline) {
            throw "native runtime cache unavailable in offline mode: $archive"
        }
        Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
        try {
            Invoke-VerifiedDownload -URL $url -Destination $partial -ExpectedSHA256 $expectedArchiveSHA -DisplayName $asset
            Move-Item -LiteralPath $partial -Destination $archive -Force
        }
        finally {
            Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
        }
    }

    # Verify every cache state immediately before opening the archive.
    $archiveHash = Get-SHA256 $archive
    if ($archiveHash -cne $expectedArchiveSHA) {
        throw "native archive SHA-256 mismatch: expected $expectedArchiveSHA, got $archiveHash"
    }

    if (Test-Path -LiteralPath $stage) {
        Remove-Item -LiteralPath $stage -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $stage | Out-Null

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($archive)
    try {
        $entryNames = @()
        foreach ($entry in $zip.Entries) {
            $name = $entry.FullName.Replace('\', '/')
            if ($name.StartsWith('/') -or $name -match '^[A-Za-z]:' -or $name -match '(^|/)\.\.?(/|$)') {
                throw "native archive path traversal: $name"
            }
            if (-not $name.EndsWith('/')) {
                $entryNames += $name
            }
        }
        if (@($entryNames | Where-Object { $_ -ceq 'manifest.json' }).Count -ne 1 -or
            @($entryNames | Where-Object { $_ -ceq $dllName }).Count -ne 1) {
            throw 'native archive must contain exactly one root manifest.json and miniapp-frida.dll'
        }
    }
    finally {
        $zip.Dispose()
    }

    Expand-Archive -LiteralPath $archive -DestinationPath $stage -Force
    $manifestPath = Join-Path $stage 'manifest.json'
    $dllPath = Join-Path $stage $dllName
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $dllPath -PathType Leaf)) {
        throw 'native archive missing manifest.json or miniapp-frida.dll'
    }

    try {
        $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
    }
    catch {
        throw "native manifest is not valid JSON: $($_.Exception.Message)"
    }
    if ($null -eq $manifest -or $manifest -is [array]) {
        throw 'native manifest must be a JSON object'
    }

    $requiredFields = @('schema', 'nativeVersion', 'fridaCoreVersion', 'zlibVersion',
        'abiVersion', 'os', 'arch', 'dll', 'size', 'sha256', 'requiredExports')
    $propertyNames = @($manifest.PSObject.Properties | ForEach-Object { $_.Name })
    foreach ($field in $requiredFields) {
        if (-not ($propertyNames -ccontains $field)) {
            throw "native manifest missing required field: $field"
        }
    }

    $expectedStrings = [ordered]@{
        schema           = 'miniapp-bridge.native-manifest.v1'
        nativeVersion    = $Version
        fridaCoreVersion = '17.3.2'
        zlibVersion      = '1.3.1'
        os               = 'windows'
        arch             = 'amd64'
        dll              = $dllName
    }
    foreach ($field in $expectedStrings.Keys) {
        $value = $manifest.PSObject.Properties[$field].Value
        if (-not ($value -is [string]) -or $value -cne $expectedStrings[$field]) {
            throw "native manifest field mismatch: $field"
        }
    }
    if (-not (Test-IntegerValue $manifest.abiVersion) -or [int64]$manifest.abiVersion -ne 1) {
        throw 'native manifest field mismatch: abiVersion'
    }

    $dllInfo = Get-Item -LiteralPath $dllPath
    if ($dllInfo.Length -le 0 -or -not (Test-IntegerValue $manifest.size) -or
        [int64]$manifest.size -ne [int64]$dllInfo.Length) {
        throw 'native manifest field mismatch: size'
    }
    $dllHash = Get-SHA256 $dllPath
    if (-not ($manifest.sha256 -is [string]) -or $manifest.sha256 -notmatch '^[0-9A-Fa-f]{64}$' -or
        $manifest.sha256.ToUpperInvariant() -cne $dllHash) {
        throw "native DLL SHA-256 mismatch: got $dllHash"
    }

    $requiredExports = @(
        'mb_abi_version', 'mb_native_version', 'mb_frida_core_version', 'mb_zlib_version',
        'mb_zlib_compress', 'mb_zlib_decompress', 'mb_bytes_free', 'mb_device_open',
        'mb_device_enumerate', 'mb_processes_free', 'mb_device_attach', 'mb_device_close',
        'mb_runtime_shutdown', 'mb_session_load_script', 'mb_session_detach', 'mb_script_post',
        'mb_script_unload', 'mb_error_free'
    )
    $actualExports = @($manifest.requiredExports)
    if (@($actualExports | Where-Object { -not ($_ -is [string]) }).Count -ne 0 -or
        $actualExports.Count -ne $requiredExports.Count -or
        @($requiredExports | Where-Object { -not ($actualExports -ccontains $_) }).Count -ne 0) {
        throw 'native manifest required export set mismatch'
    }

    $versionDirectory = Join-Path $versionsRoot "$Version-$($dllHash.Substring(0, 16))"
    $versionStage = "$versionDirectory.staging-$publishID"
    Write-AtomicJSON -Path $journalPath -Value ([ordered]@{ phase = 'prepare'; stage = $versionStage; previousVersionDirectory = '' })
    if (-not (Test-Path -LiteralPath $versionDirectory -PathType Container)) {
        New-Item -ItemType Directory -Path $versionStage | Out-Null
        Copy-Item -LiteralPath $manifestPath -Destination (Join-Path $versionStage 'manifest.json')
        Copy-Item -LiteralPath $dllPath -Destination (Join-Path $versionStage $dllName)
        Copy-Item -LiteralPath $archive -Destination (Join-Path $versionStage $retainedArchiveName)
        Write-AtomicJSON -Path $journalPath -Value ([ordered]@{ phase = 'verify'; stage = $versionStage; previousVersionDirectory = '' })
        if ((Get-SHA256 (Join-Path $versionStage $dllName)) -cne $dllHash) {
            throw 'native version directory DLL SHA-256 mismatch'
        }
        $stagedArchive = Join-Path $versionStage $retainedArchiveName
        if ((Get-SHA256 $stagedArchive) -cne $expectedArchiveSHA -or
            (Get-NativeArchiveEntrySHA256 -Path $stagedArchive -EntryName 'manifest.json') -cne
                (Get-SHA256 (Join-Path $versionStage 'manifest.json')) -or
            (Get-NativeArchiveEntrySHA256 -Path $stagedArchive -EntryName $dllName) -cne $dllHash) {
            throw 'native staged archive does not match the verified release'
        }
        Move-Item -LiteralPath $versionStage -Destination $versionDirectory
    }

    $versionEvidence = Assert-NativeVersion -VersionDirectory $versionDirectory
    if ((Get-SHA256 $versionEvidence.ArchivePath) -cne $expectedArchiveSHA -or
        (Get-NativeArchiveEntrySHA256 -Path $versionEvidence.ArchivePath -EntryName 'manifest.json') -cne (Get-SHA256 $versionEvidence.ManifestPath) -or
        (Get-NativeArchiveEntrySHA256 -Path $versionEvidence.ArchivePath -EntryName $dllName) -cne $versionEvidence.Hash) {
        throw 'native retained archive does not match the verified release'
    }
    Write-NativeTrustRecord -Evidence $versionEvidence -ArchiveSHA256 $expectedArchiveSHA
    Assert-NativeTrustRecord -Evidence $versionEvidence -ExpectedSHA256 $expectedArchiveSHA

    if (Test-Path -LiteralPath $installedManifest -PathType Container) {
        throw 'native destination manifest path is a directory'
    }
    if (Test-Path -LiteralPath $installed -PathType Container) {
        throw 'native destination DLL path is a directory'
    }

    $previous = Read-Pointer $currentPath
    $previousDirectory = if ($previous) { [string]$previous.versionDirectory } else { '' }
    $oldCurrentBytes = if (Test-Path -LiteralPath $currentPath -PathType Leaf) { [IO.File]::ReadAllBytes($currentPath) } else { $null }
    $oldRollbackBytes = if (Test-Path -LiteralPath $rollbackPath -PathType Leaf) { [IO.File]::ReadAllBytes($rollbackPath) } else { $null }
    $candidateLease = $null
    $previousLease = $null
    $hadLegacy = -not $previousDirectory -and
        (Test-Path -LiteralPath $installed -PathType Leaf) -and
        (Test-Path -LiteralPath $installedManifest -PathType Leaf)
    $publicationStarted = $false
    try {
        $candidateLease = Open-NativeVersionLease -VersionDirectory $versionDirectory `
            -ExpectedTrustSHA256 $expectedArchiveSHA
        if ($previousDirectory) {
            $previousLease = Open-NativePointerLease -Pointer $previous -Context 'previous'
        }
        elseif ((Test-Path -LiteralPath $installed -PathType Leaf) -xor
            (Test-Path -LiteralPath $installedManifest -PathType Leaf)) {
            throw 'native destination contains an incomplete legacy installation'
        }
        Write-AtomicJSON -Path $journalPath -Value ([ordered]@{
            phase = 'publish'
            stage = $versionStage
            previousVersionDirectory = $previousDirectory
            previousSHA256 = if ($previousLease) { $previousLease.Evidence.Hash } else { '' }
            previousArchiveSHA256 = if ($previousLease) { Get-SHA256 $previousLease.Evidence.ArchivePath } else { '' }
            legacyBackupDLL = if ($hadLegacy) { $backupDLL } else { '' }
            legacyBackupManifest = if ($hadLegacy) { $backupManifest } else { '' }
        })
        if ($hadLegacy) {
            Copy-Item -LiteralPath $installed -Destination $backupDLL
            Copy-Item -LiteralPath $installedManifest -Destination $backupManifest
        }
        $publicationStarted = $true
        Publish-VerifiedVersion $candidateLease
        Assert-PublishedVersion $candidateLease.Evidence | Out-Null
        Write-AtomicJSON -Path $currentPath -Value (New-NativeCurrentPointer $candidateLease)
        Remove-Item -LiteralPath $rollbackPath -Force -ErrorAction SilentlyContinue
        if ($CanaryCommand) {
            $global:LASTEXITCODE = 0
            & ([scriptblock]::Create($CanaryCommand))
            if ($LASTEXITCODE -ne 0) { throw "native canary failed with exit code $LASTEXITCODE" }
        }
    }
    catch {
        $publishFailure = $_
        $restorationErrors = @()
        try {
            if ($null -ne $previousLease) {
                Publish-VerifiedVersion $previousLease
                Assert-PublishedVersion $previousLease.Evidence | Out-Null
            }
            elseif ((Test-Path -LiteralPath $backupDLL -PathType Leaf) -and
                (Test-Path -LiteralPath $backupManifest -PathType Leaf)) {
                Copy-Item -LiteralPath $backupManifest -Destination $temporaryManifest -Force
                Copy-Item -LiteralPath $backupDLL -Destination $temporaryDLL -Force
                Remove-Item -LiteralPath $installed,$installedManifest -Force -ErrorAction SilentlyContinue
                Move-Item -LiteralPath $temporaryManifest -Destination $installedManifest -Force
                Move-Item -LiteralPath $temporaryDLL -Destination $installed -Force
            }
            elseif ($publicationStarted -and -not $hadLegacy) {
                Remove-Item -LiteralPath $installed,$installedManifest -Force -ErrorAction SilentlyContinue
            }
        }
        catch { $restorationErrors += $_.Exception.Message }
        try {
            if ($null -eq $oldCurrentBytes) { Remove-Item -LiteralPath $currentPath -Force -ErrorAction SilentlyContinue }
            else { [IO.File]::WriteAllBytes($currentPath, $oldCurrentBytes) }
        }
        catch { $restorationErrors += $_.Exception.Message }
        try {
            if ($null -eq $oldRollbackBytes) { Remove-Item -LiteralPath $rollbackPath -Force -ErrorAction SilentlyContinue }
            else { [IO.File]::WriteAllBytes($rollbackPath, $oldRollbackBytes) }
        }
        catch { $restorationErrors += $_.Exception.Message }
        try { Remove-Item -LiteralPath $journalPath -Force -ErrorAction SilentlyContinue }
        catch { $restorationErrors += $_.Exception.Message }
        if ($restorationErrors.Count -ne 0) {
            throw "native publication failed: $($publishFailure.Exception.Message); restoration failed: $($restorationErrors -join '; ')"
        }
        throw $publishFailure
    }
    finally {
        Close-NativeVersionLease $previousLease
        Close-NativeVersionLease $candidateLease
        Remove-Item -LiteralPath $backupDLL,$backupManifest -Force -ErrorAction SilentlyContinue
    }

    Write-AtomicJSON -Path $journalPath -Value ([ordered]@{ phase = 'cleanup'; stage = ''; previousVersionDirectory = $previousDirectory })
    $orderedDirectories = @(
        Get-Item -LiteralPath $versionDirectory
        if ($previousDirectory -and $previousDirectory -cne $versionDirectory -and (Test-Path -LiteralPath $previousDirectory)) {
            Get-Item -LiteralPath $previousDirectory
        }
        Get-ChildItem -LiteralPath $versionsRoot -Directory | Sort-Object LastWriteTimeUtc -Descending
    )
    $keep = @($orderedDirectories | Select-Object -ExpandProperty FullName -Unique | Select-Object -First $RetentionCount)
    foreach ($directory in @(Get-ChildItem -LiteralPath $versionsRoot -Directory)) {
        if ($keep -contains $directory.FullName) { continue }
        $trustPath = Get-NativeTrustPath $directory.FullName
        Remove-Item -LiteralPath $directory.FullName -Recurse -Force
        Remove-Item -LiteralPath $trustPath -Force -ErrorAction SilentlyContinue
    }
    Remove-Item -LiteralPath $journalPath -Force

    Write-Output "native_version=$Version"
    Write-Output "native_dll=$installed"
    Write-Output "native_manifest=$installedManifest"
    Write-Output "native_dll_sha256=$dllHash"
}
finally {
    if ($null -ne $destinationLock) {
        $destinationLock.Dispose()
        Remove-Item -LiteralPath $destinationLockPath -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $lock) {
        Remove-Item -LiteralPath $partial,$temporaryDLL,$temporaryManifest -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $stage) {
            Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
        }
        $lock.Dispose()
        Remove-Item -LiteralPath $lockPath -Force -ErrorAction SilentlyContinue
    }
}
