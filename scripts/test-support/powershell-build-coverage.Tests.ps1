Describe 'Focused build script command coverage' {
    BeforeAll {
        $script:scripts = Split-Path -Parent $PSScriptRoot
        function script:Invoke-CoveredScript {
            param([string]$Name, [hashtable]$Parameters = @{})
            try {
                $output = & (Join-Path $script:scripts $Name) @Parameters 2>&1
                return [pscustomobject]@{ ExitCode = 0; Output = @($output) }
            } catch {
                return [pscustomobject]@{ ExitCode = 1; Output = @($_) }
            }
        }
        function script:New-CmdFixture {
            param([string]$Path, [string]$Body)
            [IO.File]::WriteAllText($Path, "@echo off`r`n$Body`r`n", [Text.Encoding]::ASCII)
        }
        function script:Invoke-WithPath {
            param([string]$Path, [scriptblock]$Body)
            $oldPath = $env:PATH
            try {
                $env:PATH = $Path
                & $Body
            } finally {
                $env:PATH = $oldPath
            }
        }
        if ($env:MINIAPP_BRIDGE_PS_COVERAGE_RUN_GO -ne '1') { return }
        & (Join-Path $PSScriptRoot 'powershell-coverage-hook.ps1') -Start `
            -ScriptPath (Join-Path $scripts 'build-windows.ps1') `
            -CoverageDirectory $env:MINIAPP_BRIDGE_PS_COVERAGE_DIR `
            -ScopeFile $env:MINIAPP_BRIDGE_PS_COVERAGE_SCOPE_FILE
    }

    It 'generates address configs and rejects empty and malformed inputs' {
        $case = Join-Path ([IO.Path]::GetTempPath()) ('miniapp-bridge-address-coverage-' + [guid]::NewGuid().ToString('N'))
        try {
            $valid = Join-Path $case 'valid'
            $empty = Join-Path $case 'empty'
            $invalid = Join-Path $case 'invalid'
            New-Item -ItemType Directory -Force -Path $valid,$empty,$invalid | Out-Null
            $config = [ordered]@{
                Version = 7
                LoadStartHookOffset = '0x1'
                CDPFilterHookOffset = '0x2'
                SceneOffsets = @(1,2,3,4,5,6)
            } | ConvertTo-Json
            [IO.File]::WriteAllText((Join-Path $valid 'addresses.7.json'), $config)
            [IO.File]::WriteAllText((Join-Path $invalid 'addresses.8.json'), $config)

            $output = Join-Path $case 'configs_generated.go'
            $result = Invoke-CoveredScript -Name 'generate-address-configs.ps1' -Parameters @{ SourceDir = $valid; Output = $output }
            if ($result.ExitCode -ne 0) { throw ($result.Output | Out-String) }
            (Test-Path -LiteralPath $output) | Should -Be $true
            (Invoke-CoveredScript -Name 'generate-address-configs.ps1').ExitCode | Should -Be 0
            (Invoke-CoveredScript -Name 'generate-address-configs.ps1' -Parameters @{ SourceDir = $empty; Output = $output }).ExitCode | Should -Not -Be 0
            (Invoke-CoveredScript -Name 'generate-address-configs.ps1' -Parameters @{ SourceDir = $invalid; Output = $output }).ExitCode | Should -Not -Be 0

            $fakeGofmt = Join-Path $case 'gofmt.cmd'
            New-CmdFixture -Path $fakeGofmt -Body 'exit /b 23'
            Invoke-WithPath -Path "$case;$env:PATH" -Body {
                (Invoke-CoveredScript -Name 'generate-address-configs.ps1' -Parameters @{ SourceDir = $valid; Output = $output }).ExitCode | Should -Not -Be 0
            }
        } finally {
            Remove-Item -LiteralPath $case -Recurse -Force -ErrorAction SilentlyContinue
        }
    }

    It 'runs the C shim coverage driver with its real toolchain' {
        $report = Join-Path ([IO.Path]::GetTempPath()) ('miniapp-bridge-cshim-focused-' + [guid]::NewGuid().ToString('N') + '.json')
        try {
            $result = Invoke-CoveredScript -Name 'cshim-coverage.ps1' -Parameters @{ ReportPath = $report }
            if ($result.ExitCode -ne 0) { throw ($result.Output | Out-String) }
            (Test-Path -LiteralPath $report) | Should -Be $true
            (Invoke-CoveredScript -Name 'cshim-coverage.ps1').ExitCode | Should -Be 0
        } finally {
            Remove-Item -LiteralPath $report -Force -ErrorAction SilentlyContinue
        }
    }

    It 'runs the script integration fixtures' {
        $output = @(& go test ./scripts -count=1 -timeout 240s 2>&1)
        if ($LASTEXITCODE -ne 0) {
            throw "go test ./scripts failed`n$($output -join [Environment]::NewLine)"
        }
    }

    It 'runs the deterministic build orchestrators' {
        $oldCoverageActive = $env:MINIAPP_BRIDGE_PS_COVERAGE_ACTIVE
        try {
            # Prevent a focused invocation from recursively starting the full
            # PowerShell coverage runner from inside coverage-gate.ps1.
            $env:MINIAPP_BRIDGE_PS_COVERAGE_ACTIVE = '1'
            foreach ($name in @('build-frida-shim.ps1', 'coverage-gate.ps1', 'build-windows.ps1')) {
                $result = Invoke-CoveredScript -Name $name
                if ($result.ExitCode -ne 0) {
                    throw "$name failed`n$($result.Output | Out-String)"
                }
            }
        } finally {
            $env:MINIAPP_BRIDGE_PS_COVERAGE_ACTIVE = $oldCoverageActive
        }
    }
}

Describe 'Focused build error coverage' {
    BeforeAll {
        $script:scripts = Split-Path -Parent $PSScriptRoot
        function script:Invoke-CoveredScript {
            param([string]$Name, [hashtable]$Parameters = @{})
            try {
                $output = & (Join-Path $script:scripts $Name) @Parameters 2>&1
                return [pscustomobject]@{ ExitCode = 0; Output = @($output) }
            } catch {
                return [pscustomobject]@{ ExitCode = 1; Output = @($_) }
            }
        }
        function script:New-CmdFixture {
            param([string]$Path, [string]$Body)
            [IO.File]::WriteAllText($Path, "@echo off`r`n$Body`r`n", [Text.Encoding]::ASCII)
        }
        function script:Invoke-WithPath {
            param([string]$Path, [scriptblock]$Body)
            $oldPath = $env:PATH
            try {
                $env:PATH = $Path
                & $Body
            } finally {
                $env:PATH = $oldPath
            }
        }
        function script:Get-TextSHA256 {
            param([string]$Text)
            $algorithm = [Security.Cryptography.SHA256]::Create()
            try {
                $bytes = [Text.Encoding]::UTF8.GetBytes($Text)
                return -join @($algorithm.ComputeHash($bytes) | ForEach-Object { $_.ToString('X2') })
            } finally {
                $algorithm.Dispose()
            }
        }
    }

    It 'executes deterministic error diagnostics for build and coverage gates' {
        $case = Join-Path ([IO.Path]::GetTempPath()) ('miniapp-bridge-build-error-coverage-' + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Force -Path $case | Out-Null
        try {
            $mismatch = Invoke-CoveredScript -Name 'build-windows.ps1' -Parameters @{ CoverageExportMismatchFixture = $true }
            $mismatch.ExitCode | Should -Not -Be 0

            $shimInput = Join-Path $scripts 'cshim-coverage-fixtures\cshim-coverage-driver.c'
            $shimBackup = Join-Path $case 'cshim-coverage-driver.c.bak'
            Copy-Item -LiteralPath $shimInput -Destination $shimBackup -Force
            try {
                Remove-Item -LiteralPath $shimInput -Force
                (Invoke-CoveredScript -Name 'cshim-coverage.ps1' -Parameters @{ ReportPath = (Join-Path $case 'missing-input.json') }).ExitCode | Should -Not -Be 0
            } finally {
                Copy-Item -LiteralPath $shimBackup -Destination $shimInput -Force
            }

            $oldPath = $env:PATH
            try {
                $emptyPath = Join-Path $case 'empty-path'
                New-Item -ItemType Directory -Force -Path $emptyPath | Out-Null
                Invoke-WithPath -Path $emptyPath -Body {
                    (Invoke-CoveredScript -Name 'cshim-coverage.ps1' -Parameters @{ ReportPath = (Join-Path $case 'missing-tool.json') }).ExitCode | Should -Not -Be 0
                }

                $gccBad = Join-Path $case 'gcc.cmd'
                New-CmdFixture -Path $gccBad -Body 'echo.'
                (Invoke-CoveredScript -Name 'cshim-coverage.ps1' -Parameters @{ ReportPath = (Join-Path $case 'gcc-version.json'); GCCPath = $gccBad }).ExitCode | Should -Not -Be 0

                New-CmdFixture -Path $gccBad -Body 'echo 12.4.0'
                $gcovBad = Join-Path $case 'gcov.cmd'
                New-CmdFixture -Path $gcovBad -Body 'exit /b 7'
                (Invoke-CoveredScript -Name 'cshim-coverage.ps1' -Parameters @{ ReportPath = (Join-Path $case 'gcov-version.json'); GCCPath = $gccBad; GCovPath = $gcovBad }).ExitCode | Should -Not -Be 0

                New-CmdFixture -Path $gcovBad -Body 'echo gcov 11.2.0'
                (Invoke-CoveredScript -Name 'cshim-coverage.ps1' -Parameters @{ ReportPath = (Join-Path $case 'version-mismatch.json'); GCCPath = $gccBad; GCovPath = $gcovBad }).ExitCode | Should -Not -Be 0
            } finally {
                $env:PATH = $oldPath
            }

            $realGcc = (Get-Command gcc.exe -ErrorAction Stop).Source
            $realGccVersion = (& $realGcc -dumpfullversion).Trim()
            $gcovWrapper = Join-Path $case 'gcov-fake.exe'
            $gcovSource = Join-Path $case 'gcov-fake.go'
            $gcovProgram = @'
package main

import (
    "fmt"
    "os"
)

func main() {
    if len(os.Args) > 1 && os.Args[1] == "--version" {
        fmt.Println("gcov __GCC_VERSION__")
        return
    }
    switch os.Getenv("MINIAPP_BRIDGE_FAKE_GCOV_MODE") {
    case "line":
        fmt.Println("Function 'fixture'")
        fmt.Println("Lines executed:100.00% of 1")
        fmt.Println("Lines executed:99.00% of 100")
        fmt.Println("Branches executed:100.00% of 100")
    case "branch":
        fmt.Println("Function 'fixture'")
        fmt.Println("Lines executed:100.00% of 1")
        fmt.Println("Lines executed:100.00% of 100")
        fmt.Println("Branches executed:99.00% of 100")
    default:
        fmt.Println("Function 'fixture'")
        fmt.Println("Lines executed:0.00% of 1")
        fmt.Println("Lines executed:100.00% of 100")
        fmt.Println("Branches executed:100.00% of 100")
    }
}
'@
            $gcovProgram = $gcovProgram.Replace('__GCC_VERSION__', $realGccVersion)
            [IO.File]::WriteAllText($gcovSource, $gcovProgram, [Text.UTF8Encoding]::new($false))
            $buildFakeGcov = & go build -trimpath -o $gcovWrapper $gcovSource 2>&1
            if ($LASTEXITCODE -ne 0) { throw "fake gcov build failed: $($buildFakeGcov -join [Environment]::NewLine)" }
            $gcovCases = @(
                @{ Name = 'line'; Output = @("Function 'fixture'", 'Lines executed:100.00% of 1', 'Lines executed:99.00% of 100', 'Branches executed:100.00% of 100') },
                @{ Name = 'branch'; Output = @("Function 'fixture'", 'Lines executed:100.00% of 1', 'Lines executed:100.00% of 100', 'Branches executed:99.00% of 100') },
                @{ Name = 'function'; Output = @("Function 'fixture'", 'Lines executed:0.00% of 1', 'Lines executed:100.00% of 100', 'Branches executed:100.00% of 100') }
            )
            $oldFakeGcovMode = $env:MINIAPP_BRIDGE_FAKE_GCOV_MODE
            try {
                foreach ($gcovCase in $gcovCases) {
                    $env:MINIAPP_BRIDGE_FAKE_GCOV_MODE = $gcovCase.Name
                    (Invoke-CoveredScript -Name 'cshim-coverage.ps1' -Parameters @{
                            ReportPath = (Join-Path $case ($gcovCase.Name + '.json'))
                            GCCPath = $realGcc
                            GCovPath = $gcovWrapper
                        }).ExitCode | Should -Not -Be 0
                }
            } finally {
                if ($null -eq $oldFakeGcovMode) {
                    Remove-Item Env:MINIAPP_BRIDGE_FAKE_GCOV_MODE -ErrorAction SilentlyContinue
                } else {
                    $env:MINIAPP_BRIDGE_FAKE_GCOV_MODE = $oldFakeGcovMode
                }
            }

            $archiveFixture = Join-Path $scripts 'testdata\frida-fixture.tar.xz'
            $archiveHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archiveFixture).Hash
            $headerHash = Get-TextSHA256 -Text 'fixture frida-core header'
            $libraryHash = Get-TextSHA256 -Text 'fixture frida-core library'
            $newFridaCase = {
                param([string]$Name, [switch]$OldDevkit, [switch]$ValidDevkit)
                $root = Join-Path $case $Name
                $cache = Join-Path $root 'cache'
                $devkit = Join-Path $root 'devkit'
                New-Item -ItemType Directory -Force -Path $cache | Out-Null
                Copy-Item -LiteralPath $archiveFixture -Destination (Join-Path $cache 'fixture.tar.xz') -Force
                if ($OldDevkit -or $ValidDevkit) {
                    New-Item -ItemType Directory -Force -Path $devkit | Out-Null
                    $headerText = if ($ValidDevkit) { 'fixture frida-core header' } else { 'old header' }
                    $libraryText = if ($ValidDevkit) { 'fixture frida-core library' } else { 'old library' }
                    [IO.File]::WriteAllText((Join-Path $devkit 'frida-core.h'), $headerText, [Text.UTF8Encoding]::new($false))
                    [IO.File]::WriteAllText((Join-Path $devkit 'frida-core.lib'), $libraryText, [Text.UTF8Encoding]::new($false))
                }
                return [pscustomobject]@{ Root = $root; Cache = $cache; Devkit = $devkit }
            }
            $newFridaParameters = {
                param([object]$Paths)
                return @{
                    Offline = $true
                    ArchiveFileName = 'fixture.tar.xz'
                    CacheDirectory = $Paths.Cache
                    DevkitDirectory = $Paths.Devkit
                    ExpectedArchiveSHA256 = $archiveHash
                    ExpectedHeaderSHA256 = $headerHash
                    ExpectedLibrarySHA256 = $libraryHash
                    LockTimeoutSeconds = 1
                }
            }

            $lockPaths = & $newFridaCase 'frida-lock'
            $lockPath = Join-Path $lockPaths.Cache 'fixture.tar.xz.lock'
            $heldLock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
            try {
                (Invoke-CoveredScript -Name 'ensure-frida-devkit.ps1' -Parameters (& $newFridaParameters $lockPaths)).ExitCode | Should -Not -Be 0
            } finally {
                $heldLock.Dispose()
            }

            $unusedArchive = & $newFridaCase 'frida-invalid-unused' -ValidDevkit
            [IO.File]::WriteAllText((Join-Path $unusedArchive.Cache 'fixture.tar.xz'), 'invalid unused archive')
            (Invoke-CoveredScript -Name 'ensure-frida-devkit.ps1' -Parameters (& $newFridaParameters $unusedArchive)).ExitCode | Should -Be 0

            $candidatePaths = & $newFridaCase 'frida-seven-zip'
            $candidateParameters = & $newFridaParameters $candidatePaths
            $candidateParameters.ForceSevenZipCandidateDiscovery = $true
            $sevenZip = (Get-Command 7z.exe -ErrorAction Stop).Source
            $candidateParameters.SevenZipCandidatePath = $sevenZip
            (Invoke-CoveredScript -Name 'ensure-frida-devkit.ps1' -Parameters $candidateParameters).ExitCode | Should -Be 0

            $partialPaths = & $newFridaCase 'frida-partial'
            [IO.File]::WriteAllText((Join-Path $partialPaths.Cache 'fixture.tar.xz.partial'), 'stale partial')
            $partialParameters = & $newFridaParameters $partialPaths
            $partialParameters.Remove('Offline')
            $partialParameters.SourceURL = 'http://127.0.0.1:1/unreachable'
            $partialParameters.ExpectedArchiveSHA256 = ('0' * 64)
            $partialParameters.ForceHttpClient = $true
            $partialParameters.DownloadAttempts = 1
            $partialParameters.DownloadTimeoutSeconds = 1
            (Invoke-CoveredScript -Name 'ensure-frida-devkit.ps1' -Parameters $partialParameters).ExitCode | Should -Not -Be 0

            $driftPaths = & $newFridaCase 'frida-drift'
            $driftParameters = & $newFridaParameters $driftPaths
            $driftParameters.DriftArchiveAfterValidation = $true
            (Invoke-CoveredScript -Name 'ensure-frida-devkit.ps1' -Parameters $driftParameters).ExitCode | Should -Not -Be 0

            $headerPaths = & $newFridaCase 'frida-header-mismatch'
            $headerParameters = & $newFridaParameters $headerPaths
            $headerParameters.ExpectedHeaderSHA256 = ('0' * 64)
            (Invoke-CoveredScript -Name 'ensure-frida-devkit.ps1' -Parameters $headerParameters).ExitCode | Should -Not -Be 0

            $libraryPaths = & $newFridaCase 'frida-library-mismatch'
            $libraryParameters = & $newFridaParameters $libraryPaths
            $libraryParameters.ExpectedLibrarySHA256 = ('0' * 64)
            (Invoke-CoveredScript -Name 'ensure-frida-devkit.ps1' -Parameters $libraryParameters).ExitCode | Should -Not -Be 0

            $publishPaths = & $newFridaCase 'frida-publish' -OldDevkit
            $publishParameters = & $newFridaParameters $publishPaths
            $publishParameters.FailAfterPublish = $true
            (Invoke-CoveredScript -Name 'ensure-frida-devkit.ps1' -Parameters $publishParameters).ExitCode | Should -Not -Be 0

            $rollbackPaths = & $newFridaCase 'frida-rollback' -OldDevkit
            $rollbackParameters = & $newFridaParameters $rollbackPaths
            $rollbackParameters.FailAfterPublish = $true
            $rollbackParameters.FailRollback = $true
            (Invoke-CoveredScript -Name 'ensure-frida-devkit.ps1' -Parameters $rollbackParameters).ExitCode | Should -Not -Be 0

            $cleanupPaths = & $newFridaCase 'frida-cleanup' -OldDevkit
            $cleanupParameters = & $newFridaParameters $cleanupPaths
            $cleanupParameters.FailBackupCleanup = $true
            (Invoke-CoveredScript -Name 'ensure-frida-devkit.ps1' -Parameters $cleanupParameters).ExitCode | Should -Be 0

            $gateCodex = Join-Path (Split-Path $scripts -Parent) 'testdata\golden\reference_codex.json'
            $gateCodexBackup = Join-Path $case 'reference_codex.json.bak'
            Copy-Item -LiteralPath $gateCodex -Destination $gateCodexBackup -Force
            try {
                $codex = Get-Content -LiteralPath $gateCodex -Raw | ConvertFrom-Json
                $codex.debug_wrap = @()
                $codex | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $gateCodex -Encoding UTF8
                (Invoke-CoveredScript -Name 'coverage-gate.ps1').ExitCode | Should -Not -Be 0
            } finally {
                Copy-Item -LiteralPath $gateCodexBackup -Destination $gateCodex -Force
            }

            $matrixPath = Join-Path (Split-Path $scripts -Parent) 'docs\behavior-matrix.md'
            $matrixBackup = Join-Path $case 'behavior-matrix.md.bak'
            Copy-Item -LiteralPath $matrixPath -Destination $matrixBackup -Force
            try {
                (Get-Content -LiteralPath $matrixPath -Raw) -replace ([string][char]0x5DF2 + [char]0x5B9E + [char]0x73B0), 'fixture' | Set-Content -LiteralPath $matrixPath -Encoding UTF8
                (Invoke-CoveredScript -Name 'coverage-gate.ps1').ExitCode | Should -Not -Be 0
            } finally {
                Copy-Item -LiteralPath $matrixBackup -Destination $matrixPath -Force
            }

            $fakeGo = Join-Path $case 'go.cmd'
            $counter = Join-Path $case 'go-tool-counter.txt'
            foreach ($failureIndex in 1..5) {
                [IO.File]::WriteAllText($counter, '0', [Text.Encoding]::ASCII)
                New-CmdFixture -Path $fakeGo -Body @"
setlocal EnableDelayedExpansion
if not "%1"=="tool" exit /b 0
set /p count=<"$counter"
set /a count+=1
>"$counter" echo !count!
if "!count!"=="$failureIndex" goto fail
echo total: ^(statements^) 100.0%%
exit /b 0
:fail
echo total: ^(statements^) 99.0%%
exit /b 0
"@
                Invoke-WithPath -Path "$case;$env:PATH" -Body {
                    (Invoke-CoveredScript -Name 'coverage-gate.ps1').ExitCode | Should -Not -Be 0
                }
            }
        } finally {
            Remove-Item -LiteralPath $case -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

Describe 'Focused build Go fault coverage' {
    It 'runs process and legacy argument fault fixtures' {
        $output = @(& go test ./scripts `
                -run 'TestFridaBootstrapLegacyProcessArgumentsAndCurlFailure|TestFridaBootstrapCurlStderrDiagnostic|TestFridaBootstrapHardTimeoutTerminatesHungCurl' `
                -count=1 -timeout 120s 2>&1)
        if ($LASTEXITCODE -ne 0) {
            throw "focused Go fault fixtures failed`n$($output -join [Environment]::NewLine)"
        }
    }
}
