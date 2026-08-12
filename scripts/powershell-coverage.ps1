param(
    [string]$TestPath,
    [string]$ReportPath,
    [string[]]$CoveragePath,
    [string]$CoveragePathJson,
    [string[]]$TestName,
    [string[]]$ShardReportPath,
    [string]$ShardReportPathJson
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path

function ConvertFrom-JsonArrayArgument {
    param(
        [string]$Value,
        [string]$Name
    )
    $json = $Value
    if (Test-Path -LiteralPath $Value -PathType Leaf) {
        $json = Get-Content -LiteralPath $Value -Raw
    }
    try {
        $parsed = ConvertFrom-Json -InputObject $json
        if ($parsed -is [Array]) {
            foreach ($item in $parsed) { Write-Output $item }
        } else {
            Write-Output $parsed
        }
    } catch {
        throw "$Name must be valid inline JSON or a path to a JSON file"
    }
}

if (-not [string]::IsNullOrWhiteSpace($CoveragePathJson)) {
    if ($null -ne $CoveragePath -and $CoveragePath.Count -gt 0) { throw 'CoveragePath and CoveragePathJson are mutually exclusive' }
    $CoveragePath = @(ConvertFrom-JsonArrayArgument -Value $CoveragePathJson -Name 'CoveragePathJson')
}
if (-not [string]::IsNullOrWhiteSpace($ShardReportPathJson)) {
    if ($null -ne $ShardReportPath -and $ShardReportPath.Count -gt 0) { throw 'ShardReportPath and ShardReportPathJson are mutually exclusive' }
    $ShardReportPath = @(ConvertFrom-JsonArrayArgument -Value $ShardReportPathJson -Name 'ShardReportPathJson')
}
$testPathWasExplicit = -not [string]::IsNullOrWhiteSpace($TestPath)
$coveragePathWasExplicit = $null -ne $CoveragePath -and $CoveragePath.Count -gt 0
$helperPath = (Resolve-Path (Join-Path $PSScriptRoot 'test-support\powershell-coverage-hook.ps1')).Path
$helperLeaf = [IO.Path]::GetFileName($helperPath)
$hookMarker = 'MINIAPP_BRIDGE_PS_COVERAGE_HOOK'

if ([string]::IsNullOrWhiteSpace($TestPath)) {
    $TestPath = Join-Path $PSScriptRoot 'powershell-coverage.Tests.ps1'
} else {
    $TestPath = [IO.Path]::GetFullPath($TestPath)
}
if ([string]::IsNullOrWhiteSpace($ReportPath)) {
    $ReportPath = Join-Path $repo 'ci-artifacts\powershell-coverage.log'
} else {
    $ReportPath = [IO.Path]::GetFullPath($ReportPath)
}
if ($null -eq $CoveragePath -or $CoveragePath.Count -eq 0) {
    # Keep the scope stable: helper files live below test-support and are not
    # production scripts. The runner itself is orchestration, not a target;
    # excluding it also prevents a run from instrumenting its own source while
    # it is executing. The explicit list is still accepted for CI shards.
    $CoveragePath = @(Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.ps1' -File |
        Where-Object { $_.Name -notlike '*.Tests.ps1' -and $_.FullName -ne $PSCommandPath } |
        Sort-Object FullName |
        ForEach-Object { $_.FullName })
} else {
    $CoveragePath = @($CoveragePath | ForEach-Object { [IO.Path]::GetFullPath($_) })
}

if (-not (Test-Path -LiteralPath $TestPath -PathType Leaf)) {
    throw "PowerShell coverage test file is missing: $TestPath"
}
if ($CoveragePath.Count -eq 0) { throw 'PowerShell coverage scope is empty' }
$scriptRoot = (Resolve-Path -LiteralPath $PSScriptRoot).Path
foreach ($path in $CoveragePath) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "PowerShell coverage source is missing: $path"
    }
    $full = (Resolve-Path -LiteralPath $path).Path
    if (-not $full.StartsWith($scriptRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw "PowerShell coverage source is outside scripts/: $full"
    }
}

$pester = Get-Module -ListAvailable Pester |
    Where-Object { $_.Version -ge [version]'3.4.0' } |
    Sort-Object Version -Descending |
    Select-Object -First 1
if (-not $pester) {
    throw 'PowerShell coverage requires Pester 3.4.0 or newer; no fallback or synthetic percentage is accepted'
}
Import-Module Pester -RequiredVersion $pester.Version -Force
$invoke = Get-Command Invoke-Pester -ErrorAction Stop

function Get-Utf8Source {
    param([string]$Path)
    $bytes = [IO.File]::ReadAllBytes($Path)
    $bom = $bytes.Length -ge 3 -and
        $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF
    $offset = if ($bom) { 3 } else { 0 }
    $length = $bytes.Length - $offset
    $text = [Text.Encoding]::UTF8.GetString($bytes, $offset, $length)
    return [pscustomobject]@{ Text = $text; Bom = $bom }
}

function Set-Utf8Source {
    param(
        [string]$Path,
        [string]$Text,
        [bool]$Bom
    )
    $utf8 = [Text.UTF8Encoding]::new($false)
    $payload = $utf8.GetBytes($Text)
    if (-not $Bom) {
        [IO.File]::WriteAllBytes($Path, $payload)
        return
    }
    $withBom = New-Object byte[] ($payload.Length + 3)
    $withBom[0] = 0xEF
    $withBom[1] = 0xBB
    $withBom[2] = 0xBF
    [Array]::Copy($payload, 0, $withBom, 3, $payload.Length)
    [IO.File]::WriteAllBytes($Path, $withBom)
}

function Get-ParamBlockOffsets {
    param([string]$Text)
    $tokens = $null
    $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseInput(
        $Text,
        [ref]$tokens,
        [ref]$errors
    )
    if ($null -ne $errors -and $errors.Count -gt 0) {
        throw 'PowerShell coverage source failed to parse before instrumentation'
    }
    if ($null -eq $ast.ParamBlock) {
        return [pscustomobject]@{ Open = -1; Close = -1 }
    }
    $start = $ast.ParamBlock.Extent.StartOffset
    # ParamBlock.Extent.EndOffset can include the complete script body on
    # Windows PowerShell. Starting at the param keyword and balancing tokens
    # gives the actual closing parenthesis in both PowerShell implementations.
    $paramTokens = @($tokens | Where-Object {
            $_.Extent.StartOffset -ge $start
        })
    $openToken = $paramTokens | Where-Object { $_.Kind -eq 'LParen' } | Select-Object -First 1
    if ($null -eq $openToken) {
        throw 'PowerShell coverage parameter block has no opening parenthesis'
    }
    $depth = 0
    foreach ($token in $paramTokens | Where-Object { $_.Extent.StartOffset -ge $openToken.Extent.StartOffset }) {
        if ($token.Kind -eq 'LParen') {
            $depth++
        } elseif ($token.Kind -eq 'RParen') {
            $depth--
            if ($depth -eq 0) {
                return [pscustomobject]@{
                    Open = [int]$openToken.Extent.StartOffset
                    Close = [int]$token.Extent.StartOffset
                }
            }
        }
    }
    throw 'PowerShell coverage parameter block has no closing parenthesis'
}

function Install-CoverageHooks {
    param(
        [string[]]$Paths,
        [string]$Helper,
        [string]$BackupDirectory,
        [string]$CoverageDirectory,
        [string]$ScopeFile
    )
    New-Item -ItemType Directory -Force -Path $BackupDirectory | Out-Null
    $backups = @()
    $script:CoverageInventory = @()
    $helperLeafName = [IO.Path]::GetFileName($Helper)
    try {
        foreach ($path in $Paths) {
            $source = Get-Utf8Source -Path $path
            $alreadyInjected = $source.Text -match '(?m)^\s*\[object\]\$__MiniappBridgeCoverageHook\s*=' -or
                $source.Text -match "(?m)^# $hookMarker\r?\n"
            if ($alreadyInjected) {
                throw "PowerShell source already contains a coverage hook: $path"
            }
            $backup = Join-Path $BackupDirectory (([guid]::NewGuid().ToString('N')) + '.bak')
            [IO.File]::WriteAllBytes($backup, [IO.File]::ReadAllBytes($path))
            $backups += [pscustomobject]@{ Path = $path; Backup = $backup }

            $newline = if ($source.Text.Contains("`r`n")) { "`r`n" } else { "`n" }
            $literalPath = ([IO.Path]::GetFullPath($path)).Replace("'", "''")
            $helperLiteral = ([IO.Path]::GetFullPath($Helper)).Replace("'", "''")
            $coverageLiteral = ([IO.Path]::GetFullPath($CoverageDirectory)).Replace("'", "''")
            $scopeLiteral = ([IO.Path]::GetFullPath($ScopeFile)).Replace("'", "''")
            $inventoryForScript = @(Get-StatementInventory -Paths @($path) -HelperLeafName $helperLeafName)
            if ($inventoryForScript.Count -eq 0) {
                throw "PowerShell coverage source has no executable commands: $path"
            }
            # Pass immutable paths in the generated command. Several Go tests
            # intentionally rebuild the child environment, so relying only on
            # inherited MINIAPP_BRIDGE_PS_COVERAGE_* variables loses coverage
            # and makes the production script fail before its real behavior.
            $hookCall = "& '$helperLiteral' -Start -ScriptPath '$literalPath' -CoverageDirectory '$coverageLiteral' -ScopeFile '$scopeLiteral'"
            $paramOffsets = Get-ParamBlockOffsets -Text $source.Text
            if ($paramOffsets.Open -ge 0) {
                # Append the hidden optional parameter so existing positional
                # arguments keep binding to the same production parameters.
                $prefix = $source.Text.Substring(0, $paramOffsets.Close)
                $suffix = $source.Text.Substring($paramOffsets.Close)
                $trimmed = $prefix.TrimEnd()
                $separator = if ($trimmed.EndsWith('(')) { $newline } else { ',' + $newline }
                $injection = $separator +
                    "    [object]`$__MiniappBridgeCoverageHook = `$($newline" +
                    "        $hookCall$newline" +
                    "    )$newline"
                $patched = $trimmed + $injection + $suffix
            } else {
                $patched = "# $hookMarker$newline" +
                    "$hookCall$newline" +
                    $source.Text
            }
            Set-Utf8Source -Path $path -Text $patched -Bom $source.Bom

            # Breakpoints use coordinates from the instrumented file, while
            # reports use coordinates from the restored production source.
            # Hook injection adds no production commands, so AST order is
            # stable and provides an exact mapping.
            $instrumentedInventory = @(Get-StatementInventory -Paths @($path) -HelperLeafName $helperLeafName)
            if ($instrumentedInventory.Count -ne $inventoryForScript.Count) {
                throw "PowerShell coverage hook changed command inventory: $path"
            }
            for ($index = 0; $index -lt $inventoryForScript.Count; $index++) {
                $inventoryForScript[$index].Key = $instrumentedInventory[$index].Key
                $script:CoverageInventory += $inventoryForScript[$index]
            }
        }
    } catch {
        foreach ($item in @($backups)) {
            if (Test-Path -LiteralPath $item.Backup -PathType Leaf) {
                [IO.File]::Copy($item.Backup, $item.Path, $true)
            }
        }
        throw
    }
    return $backups
}

function Restore-CoverageHooks {
    param([object[]]$Backups)
    foreach ($item in @($Backups)) {
        if ($null -ne $item -and (Test-Path -LiteralPath $item.Backup -PathType Leaf)) {
            [IO.File]::Copy($item.Backup, $item.Path, $true)
        }
    }
}

function Get-StatementInventory {
    param(
        [string[]]$Paths,
        [string]$HelperLeafName
    )
    $inventory = @()
    $seen = @{}
    foreach ($path in $Paths) {
        $fullPath = [IO.Path]::GetFullPath($path)
        $tokens = $null
        $errors = $null
        $ast = [System.Management.Automation.Language.Parser]::ParseFile(
            $fullPath,
            [ref]$tokens,
            [ref]$errors
        )
        if ($null -ne $errors -and $errors.Count -gt 0) {
            throw "PowerShell coverage source failed to parse: $fullPath"
        }
        $statements = @($ast.FindAll({
                param($node)
                return $node -is [System.Management.Automation.Language.CommandBaseAst]
            }, $true))
        foreach ($statement in $statements) {
            $extent = $statement.Extent
            if ($extent.Text -match [regex]::Escape($HelperLeafName)) {
                continue
            }
            $ancestor = $statement
            $closingLoopCondition = $false
            while ($null -ne $ancestor.Parent) {
                if (($ancestor.Parent -is [System.Management.Automation.Language.DoWhileStatementAst] -or
                        $ancestor.Parent -is [System.Management.Automation.Language.DoUntilStatementAst]) -and
                    $ancestor.Parent.Condition -eq $ancestor) {
                    $closingLoopCondition = $true
                    break
                }
                $ancestor = $ancestor.Parent
            }
            if ($closingLoopCondition) { continue }
            $seenKey = '{0}|{1}' -f $fullPath.ToLowerInvariant(), $extent.StartOffset
            if ($seen.ContainsKey($seenKey)) {
                throw "PowerShell coverage AST has duplicate command location: ${fullPath}:$($extent.StartLineNumber):$($extent.StartColumnNumber)"
            }
            $seen[$seenKey] = $true
            $key = '{0}|{1}|{2}' -f $fullPath.ToLowerInvariant(), $extent.StartLineNumber, $extent.StartColumnNumber
            $parent = $statement.Parent
            while ($null -ne $parent -and
                $parent -isnot [System.Management.Automation.Language.FunctionDefinitionAst]) {
                $parent = $parent.Parent
            }
            $function = if ($null -eq $parent) { '' } else { [string]$parent.Name }
            $inventory += [pscustomobject]@{
                Key = $key
                File = $fullPath
                Line = [int]$extent.StartLineNumber
                StartColumn = [int]$extent.StartColumnNumber
                EndLine = [int]$extent.EndLineNumber
                EndColumn = [int]$extent.EndColumnNumber
                Function = $function
                Command = [string]$extent.Text
                Offset = [int]$extent.StartOffset
                StatementType = $statement.GetType().Name
            }
        }
    }
    return $inventory
}

function Get-RepoRelativePath {
    param([string]$Path)
    $full = [IO.Path]::GetFullPath($Path)
    $root = ([IO.Path]::GetFullPath($repo)).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    if (-not $full.StartsWith($root, [StringComparison]::OrdinalIgnoreCase)) {
        throw "PowerShell coverage source is outside repository: $full"
    }
    return $full.Substring($root.Length).Replace('\', '/')
}

function Get-SourceManifest {
    param([string[]]$Paths)
    foreach ($path in $Paths) {
        $full = [IO.Path]::GetFullPath($path)
        $inventory = @(Get-StatementInventory -Paths @($full) -HelperLeafName $helperLeaf)
        [pscustomobject]@{
            path = Get-RepoRelativePath -Path $full
            sha256 = (Get-FileHash -LiteralPath $full -Algorithm SHA256).Hash.ToLowerInvariant()
            commands_analyzed = [int]$inventory.Count
        }
    }
}

function Get-CoverageEvents {
    param(
        [string]$Directory,
        [System.Collections.Generic.HashSet[string]]$HitKeys
    )
    $eventFiles = @(Get-ChildItem -LiteralPath $Directory -Filter '*.jsonl' -File -ErrorAction SilentlyContinue)
    if ($eventFiles.Count -eq 0) {
        throw 'PowerShell coverage produced no child-process ledgers'
    }
    $starts = 0
    $readEvents = 0
    foreach ($file in $eventFiles) {
        foreach ($line in @(Get-Content -LiteralPath $file.FullName -Encoding UTF8)) {
            if ([string]::IsNullOrWhiteSpace($line)) { continue }
            try { $event = $line | ConvertFrom-Json } catch { throw "Invalid PowerShell coverage event in $($file.FullName): $($_.Exception.Message)" }
            if ($event.schema -ne 'miniapp_bridge.powershell_coverage.v1') {
                throw "Unexpected PowerShell coverage event schema in $($file.FullName)"
            }
            $readEvents++
            if ($event.kind -eq 'start') {
                $starts++
            } elseif ($event.kind -eq 'hit') {
                $full = [IO.Path]::GetFullPath([string]$event.file)
                [void]$HitKeys.Add(('{0}|{1}|{2}' -f $full.ToLowerInvariant(), [int]$event.line, [int]$event.start_column))
            }
        }
    }
    if ($starts -eq 0) { throw 'PowerShell coverage ledgers contain no child start event' }
    return [pscustomobject]@{ Files = $eventFiles.Count; Starts = $starts; Events = $readEvents }
}

function Merge-CoverageShards {
    param(
        [string[]]$Reports,
        [string[]]$ExpectedPaths,
        [string]$OutputPath
    )
    if ($Reports.Count -lt 2) { throw 'PowerShell coverage merge requires at least two shard reports' }
    $expected = @{}
    foreach ($item in @(Get-SourceManifest -Paths $ExpectedPaths)) {
        if ($expected.ContainsKey($item.path)) { throw "PowerShell coverage scope contains duplicate source: $($item.path)" }
        $expected[$item.path] = $item
    }
    $merged = @{}
    $reportSummaries = @()
    foreach ($reportPath in $Reports) {
        $fullReport = [IO.Path]::GetFullPath($reportPath)
        if (-not (Test-Path -LiteralPath $fullReport -PathType Leaf)) { throw "PowerShell coverage shard report is missing: $fullReport" }
        try { $report = Get-Content -LiteralPath $fullReport -Raw | ConvertFrom-Json } catch { throw "PowerShell coverage shard report is invalid: $fullReport" }
        if ($report.schema -ne 'miniapp_bridge.coverage.v1' -or $report.language -ne 'PowerShell') { throw "PowerShell coverage shard report has unexpected schema: $fullReport" }
        if ($report.result -ne 'passed' -or [double]$report.command_percent -ne 100.0 -or [int]$report.commands_missed -ne 0) {
            throw "PowerShell coverage shard did not pass: $fullReport"
        }
        $manifest = @($report.source_manifest)
        if ($manifest.Count -eq 0) { throw "PowerShell coverage shard has no source manifest: $fullReport" }
        if ([int]$report.source_count -ne $manifest.Count) {
            throw "PowerShell coverage shard source count does not match its manifest: $fullReport"
        }
        $manifestAnalyzed = [int](($manifest | Measure-Object -Property commands_analyzed -Sum).Sum)
        if ([int]$report.commands_analyzed -ne $manifestAnalyzed -or
            [int]$report.commands_executed -ne $manifestAnalyzed -or
            [int]$report.failed_tests -ne 0) {
            throw "PowerShell coverage shard counters do not match its manifest: $fullReport"
        }
        foreach ($source in $manifest) {
            $relative = ([string]$source.path).Replace('\', '/')
            if (-not $expected.ContainsKey($relative)) { throw "PowerShell coverage shard contains out-of-scope source: $relative" }
            if ($merged.ContainsKey($relative)) { throw "PowerShell coverage shards overlap source: $relative" }
            $current = $expected[$relative]
            if ([string]$source.sha256 -ne [string]$current.sha256) { throw "PowerShell coverage source changed after shard execution: $relative" }
            if ([int]$source.commands_analyzed -ne [int]$current.commands_analyzed) { throw "PowerShell coverage command inventory changed: $relative" }
            $merged[$relative] = $source
        }
        $reportSummaries += [pscustomobject]@{
            path = $fullReport
            commands_analyzed = [int]$report.commands_analyzed
            commands_executed = [int]$report.commands_executed
        }
    }
    $missing = @($expected.Keys | Where-Object { -not $merged.ContainsKey($_) })
    if ($missing.Count -gt 0) { throw "PowerShell coverage shards omit sources: $($missing -join ', ')" }
    $analyzed = [int](($expected.Values | Measure-Object -Property commands_analyzed -Sum).Sum)
    $reportedAnalyzed = [int](($reportSummaries | Measure-Object -Property commands_analyzed -Sum).Sum)
    $reportedExecuted = [int](($reportSummaries | Measure-Object -Property commands_executed -Sum).Sum)
    if ($reportedAnalyzed -ne $analyzed -or $reportedExecuted -ne $analyzed) {
        throw "PowerShell coverage shard totals do not equal source inventory: reports=$reportedExecuted/$reportedAnalyzed expected=$analyzed/$analyzed"
    }
    $output = [ordered]@{
        schema = 'miniapp_bridge.coverage.v1'
        language = 'PowerShell'
        tool = 'Pester CommandBaseAst breakpoints + child ledger shard merge'
        test_path = 'shard-merge'
        source_count = [int]$expected.Count
        analyzed_files = [int]$expected.Count
        command_percent = [double](($reportedExecuted * 100.0) / $reportedAnalyzed)
        statement_percent = [double](($reportedExecuted * 100.0) / $reportedAnalyzed)
        commands_analyzed = $analyzed
        commands_executed = $reportedExecuted
        commands_missed = 0
        statements_analyzed = $analyzed
        statements_executed = $reportedExecuted
        statements_missed = 0
        threshold_percent = 100.0
        failed_tests = 0
        source_manifest = @($expected.Values | Sort-Object path)
        shard_reports = @($reportSummaries)
        result = 'passed'
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $OutputPath) | Out-Null
    [IO.File]::WriteAllText($OutputPath, ($output | ConvertTo-Json -Depth 8 -Compress), [Text.UTF8Encoding]::new($false))
    Get-Content -LiteralPath $OutputPath | Write-Output
    Write-Output "powershell_command_coverage=100.00%; statements=$analyzed/$analyzed; shards=$($Reports.Count); report=$OutputPath"
}

function Invoke-CoverageShardSet {
    $runner = $PSCommandPath
    $hostPath = (Get-Process -Id $PID).Path
    $artifactRoot = Join-Path $repo 'ci-artifacts\powershell'
    $buildReport = Join-Path $artifactRoot 'build\coverage.json'
    $nativeReport = Join-Path $artifactRoot 'native\coverage.json'
    $smokeReport = Join-Path $artifactRoot 'smoke\coverage.json'
    $buildCoverage = @(
        'build-frida-shim.ps1', 'build-windows.ps1', ('build-' + 'zlib.ps1'),
        'coverage-gate.ps1', 'cshim-coverage.ps1', 'ensure-frida-devkit.ps1',
        'generate-address-configs.ps1'
    ) | ForEach-Object { Join-Path $PSScriptRoot $_ }
    $nativeCoverage = @(
        'native-prepare.ps1', 'native-release.ps1', 'package-windows-release.ps1'
    ) | ForEach-Object { Join-Path $PSScriptRoot $_ }
    $smokeCoverage = @(Get-ChildItem -LiteralPath $PSScriptRoot -Filter ('smoke-' + '*.ps1') -File |
        Sort-Object FullName |
        Select-Object -ExpandProperty FullName)
    if ($smokeCoverage.Count -ne 1) { throw 'PowerShell smoke coverage requires exactly one deterministic smoke script' }

    $argumentRoot = Join-Path ([IO.Path]::GetTempPath()) ('miniapp-bridge-ps-coverage-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $argumentRoot | Out-Null
    try {
        $buildPaths = Join-Path $argumentRoot 'build-paths.json'
        $nativePaths = Join-Path $argumentRoot 'native-paths.json'
        $smokePaths = Join-Path $argumentRoot 'smoke-paths.json'
        [IO.File]::WriteAllText($buildPaths, ($buildCoverage | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
        [IO.File]::WriteAllText($nativePaths, ($nativeCoverage | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
        [IO.File]::WriteAllText($smokePaths, ($smokeCoverage | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))

        $shards = @(
            [pscustomobject]@{
                Name = 'build'
                Arguments = @('-TestPath', (Join-Path $PSScriptRoot 'test-support\powershell-build-coverage.Tests.ps1'),
                    '-CoveragePathJson', $buildPaths, '-ReportPath', $buildReport)
            },
            [pscustomobject]@{
                Name = 'native'
                Arguments = @('-TestPath', (Join-Path $PSScriptRoot 'test-support\powershell-native-coverage.Tests.ps1'),
                    '-CoveragePathJson', $nativePaths, '-ReportPath', $nativeReport)
            },
            [pscustomobject]@{
                Name = 'smoke'
                Arguments = @('-TestPath', (Join-Path $PSScriptRoot 'powershell-coverage.Tests.ps1'), '-TestName',
                    'PowerShell Windows smoke coverage fixture', '-CoveragePathJson', $smokePaths,
                    '-ReportPath', $smokeReport)
            }
        )
        foreach ($shard in $shards) {
            $childArguments = @($shard.Arguments)
            & $hostPath -NoProfile -File $runner @childArguments
            if ($LASTEXITCODE -ne 0) { throw "PowerShell $($shard.Name) shard failed with exit $LASTEXITCODE" }
        }

        $mergeReport = Join-Path $repo 'ci-artifacts\powershell-coverage.log'
        $mergePaths = Join-Path $argumentRoot 'merge-paths.json'
        [IO.File]::WriteAllText($mergePaths, (@($buildReport, $nativeReport, $smokeReport) | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
        & $hostPath -NoProfile -File $runner -ShardReportPathJson $mergePaths -ReportPath $mergeReport
        if ($LASTEXITCODE -ne 0) { throw "PowerShell shard merge failed with exit $LASTEXITCODE" }
    } finally {
        Remove-Item -LiteralPath $argumentRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}

if ($null -ne $ShardReportPath -and $ShardReportPath.Count -gt 0) {
    Merge-CoverageShards -Reports @($ShardReportPath) -ExpectedPaths @($CoveragePath) -OutputPath $ReportPath
    return
}

if (-not $testPathWasExplicit -and -not $coveragePathWasExplicit) {
    $defaultRunMutex = [Threading.Mutex]::new($false, 'Local\MiniappBridge.PowerShellCoverage.Default')
    $defaultRunMutexAcquired = $false
    try {
        try {
            $defaultRunMutexAcquired = $defaultRunMutex.WaitOne([TimeSpan]::FromMinutes(30))
        } catch [Threading.AbandonedMutexException] {
            $defaultRunMutexAcquired = $true
        }
        if (-not $defaultRunMutexAcquired) { throw 'Timed out waiting for another PowerShell coverage run to finish' }
        Invoke-CoverageShardSet
    } finally {
        if ($defaultRunMutexAcquired) { $defaultRunMutex.ReleaseMutex() }
        $defaultRunMutex.Dispose()
    }
    return
}

$reportDirectory = Split-Path -Parent $ReportPath
New-Item -ItemType Directory -Force -Path $reportDirectory | Out-Null
$sessionRoot = Join-Path $reportDirectory 'powershell-coverage-events'
if (Test-Path -LiteralPath $sessionRoot) {
    Remove-Item -LiteralPath $sessionRoot -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $sessionRoot | Out-Null
$backupRoot = Join-Path $sessionRoot 'source-backups'
$scopeManifest = Join-Path $sessionRoot 'scope.json'
$scopeJson = @($CoveragePath | ForEach-Object { [IO.Path]::GetFullPath($_) }) | ConvertTo-Json -Compress
[IO.File]::WriteAllText($scopeManifest, $scopeJson, [Text.UTF8Encoding]::new($false))

$envNames = @(
    'MINIAPP_BRIDGE_PS_COVERAGE_DIR',
    'MINIAPP_BRIDGE_PS_COVERAGE_HELPER',
    'MINIAPP_BRIDGE_PS_COVERAGE_SCOPE_FILE',
    'MINIAPP_BRIDGE_PS_COVERAGE_RUN_GO',
    'MINIAPP_BRIDGE_PS_COVERAGE_ACTIVE'
)
$oldEnvironment = @{}
foreach ($name in $envNames) { $oldEnvironment[$name] = [Environment]::GetEnvironmentVariable($name) }
$backups = @()
$hitKeys = New-Object 'System.Collections.Generic.HashSet[string]'
$inventory = @()
$eventSummary = $null
$sourceManifest = @(Get-SourceManifest -Paths $CoveragePath)
$testResult = $null
$failure = $null

try {
    $backups = Install-CoverageHooks -Paths $CoveragePath -Helper $helperPath -BackupDirectory $backupRoot -CoverageDirectory $sessionRoot -ScopeFile $scopeManifest
    $env:MINIAPP_BRIDGE_PS_COVERAGE_DIR = $sessionRoot
    $env:MINIAPP_BRIDGE_PS_COVERAGE_HELPER = $helperPath
    $env:MINIAPP_BRIDGE_PS_COVERAGE_SCOPE_FILE = $scopeManifest
    $env:MINIAPP_BRIDGE_PS_COVERAGE_RUN_GO = '1'
    $env:MINIAPP_BRIDGE_PS_COVERAGE_ACTIVE = '1'

    # Pester's legacy form is `Invoke-Pester -CodeCoverage $CoveragePath`.
    # It cannot see Go-launched child processes, so the authoritative profile
    # below uses the hook ledger instead of pretending the legacy result is
    # complete.
    if ($pester.Version.Major -ge 5 -and $invoke.Parameters.ContainsKey('Configuration')) {
        $configuration = [PesterConfiguration]::Default
        $configuration.Run.Path = @($TestPath)
        $configuration.Run.Exit = $false
        $configuration.Output.Verbosity = 'Detailed'
        $testResult = Invoke-Pester -Configuration $configuration
    } else {
        if ($null -ne $TestName -and $TestName.Count -gt 0) {
            $testResult = Invoke-Pester -Script $TestPath -TestName $TestName -PassThru
        } else {
            $testResult = Invoke-Pester -Script $TestPath -PassThru
        }
    }
    $failedTests = 0
    foreach ($property in 'FailedCount', 'Failed') {
        if ($testResult.PSObject.Properties.Name -contains $property) {
            $failedTests = [int]$testResult.$property
            break
        }
    }
    $testFailure = if ($failedTests -eq 0) { $null } else { "PowerShell coverage tests failed: $failedTests" }

    # Compatibility names are retained for consumers of the original report;
    # their values now come from AST statement points and real child hit events.
    $inventory = @($script:CoverageInventory)
    if ($inventory.Count -le 0) { throw 'PowerShell coverage AST contains no executable statements' }
    $eventSummary = Get-CoverageEvents -Directory $sessionRoot -HitKeys $hitKeys
    $analyzed = $inventory.Count
    $executed = @($inventory | Where-Object { $hitKeys.Contains($_.Key) }).Count
    $missed = @($inventory | Where-Object { -not $hitKeys.Contains($_.Key) })
    $missedCount = $missed.Count
    $percent = [math]::Round(($executed * 100.0) / $analyzed, 2)
    $legacyPesterCounters = [ordered]@{
        NumberOfCommandsAnalyzed = [int]$analyzed
        NumberOfCommandsExecuted = [int]$executed
        NumberOfCommandsMissed = [int]$missedCount
    }

    $report = [ordered]@{
        schema = 'miniapp_bridge.coverage.v1'
        language = 'PowerShell'
        tool = 'Pester CommandBaseAst breakpoints + child ledger'
        tool_version = $pester.Version.ToString()
        test_path = $TestPath
        source_count = $CoveragePath.Count
        analyzed_files = @($CoveragePath).Count
        command_percent = [double]$percent
        statement_percent = [double]$percent
        commands_analyzed = [int]$analyzed
        commands_executed = [int]$executed
        commands_missed = [int]$missedCount
        statements_analyzed = [int]$analyzed
        statements_executed = [int]$executed
        statements_missed = [int]$missedCount
        threshold_percent = 100.0
        child_ledger_files = [int]$eventSummary.Files
        child_processes = [int]$eventSummary.Starts
        hit_events = [int]$eventSummary.Events
        failed_tests = [int]$failedTests
        source_manifest = @($sourceManifest)
        missed_commands = @($missed | Select-Object File,Line,StartColumn,EndLine,EndColumn,Function,Command,StatementType)
        # A successful report has result = 'passed'; misses are never coerced.
        result = if ($failedTests -eq 0 -and $missedCount -eq 0 -and $percent -eq 100.0) { 'passed' } else { 'failed' }
    }
    $json = $report | ConvertTo-Json -Depth 8 -Compress
    [IO.File]::WriteAllText($ReportPath, $json, [Text.UTF8Encoding]::new($false))
    Get-Content -LiteralPath $ReportPath | Write-Output
    if ($null -ne $testFailure) { throw $testFailure }
    if ($missedCount -ne 0 -or $percent -ne 100.0) {
        $detail = ($missed | Select-Object -First 20 | ForEach-Object { "$($_.File):$($_.Line):$($_.StartColumn) $($_.Command)" }) -join '; '
        throw "PowerShell command coverage must be 100.00%; got $percent% ($executed/$analyzed), missed=$missedCount. $detail"
    }
    Write-Output "powershell_command_coverage=100.00%; statements=$executed/$analyzed; report=$ReportPath"
} catch {
    $failure = $_
    if (-not (Test-Path -LiteralPath $ReportPath -PathType Leaf)) {
        $failedReport = [ordered]@{
            schema = 'miniapp_bridge.coverage.v1'
            language = 'PowerShell'
            tool = 'Pester CommandBaseAst breakpoints + child ledger'
            result = 'failed'
            error = $_.Exception.Message
            source_count = @($CoveragePath).Count
        }
        $failedJson = $failedReport | ConvertTo-Json -Depth 8 -Compress
        [IO.File]::WriteAllText($ReportPath, $failedJson, [Text.UTF8Encoding]::new($false))
    }
} finally {
    Restore-CoverageHooks -Backups $backups
    foreach ($name in $envNames) {
        $oldValue = $oldEnvironment[$name]
        [Environment]::SetEnvironmentVariable($name, $oldValue)
    }
}

if ($null -ne $failure) { throw $failure }
