[CmdletBinding()]
param(
    [switch]$Start,
    [switch]$Hit,
    [Parameter(Mandatory = $true)]
    [string]$ScriptPath,
    [string]$CoverageDirectory,
    [string]$ScopeFile,
    [int]$Line,
    [int]$Column
)

$ErrorActionPreference = 'Stop'

# The hook is intentionally a no-op outside the coverage runner. Production
# scripts keep their normal behavior when invoked directly by a developer.
if (-not $Start -and -not $Hit) {
    return
}

if ([string]::IsNullOrWhiteSpace($CoverageDirectory)) {
    $CoverageDirectory = $env:MINIAPP_BRIDGE_PS_COVERAGE_DIR
}

$stateVariable = '__MiniappBridgePowerShellCoverageState'
$existing = Get-Variable -Name $stateVariable -Scope Global -ValueOnly -ErrorAction SilentlyContinue

if ($Hit) {
    if ($null -eq $existing) {
        return
    }
    try {
        $existing.Writer.WriteLine(([ordered]@{
                    schema = 'miniapp_bridge.powershell_coverage.v1'
                    kind = 'hit'
                    pid = [int]$PID
                    file = [string]$ScriptPath
                    line = [int]$Line
                    start_column = [int]$Column
                } | ConvertTo-Json -Compress))
    } catch {
        $existing.WriteFailed = $true
        [Console]::Error.WriteLine("PowerShell coverage ledger write failed: $($_.Exception.Message)")
    }
    return
}

if ($null -ne $existing) {
    return
}
if ([string]::IsNullOrWhiteSpace($ScopeFile)) {
    $ScopeFile = $env:MINIAPP_BRIDGE_PS_COVERAGE_SCOPE_FILE
}
if ([string]::IsNullOrWhiteSpace($CoverageDirectory)) {
    throw 'PowerShell coverage directory is missing'
}
if ([string]::IsNullOrWhiteSpace($ScopeFile) -or -not (Test-Path -LiteralPath $ScopeFile -PathType Leaf)) {
    throw 'PowerShell coverage scope manifest is missing'
}

$scopeValue = Get-Content -LiteralPath $ScopeFile -Raw -Encoding UTF8 | ConvertFrom-Json
$scope = @()
foreach ($scopeEntry in $scopeValue) {
    $scope += [string]$scopeEntry
}
if ($scope.Count -eq 0) {
    throw 'PowerShell coverage scope manifest is empty'
}
foreach ($scopePath in $scope) {
    $fullPath = [string]$scopePath
    if (-not (Test-Path -LiteralPath $fullPath -PathType Leaf)) {
        throw "PowerShell coverage source is missing: $fullPath"
    }
}

$directory = [string]$CoverageDirectory
New-Item -ItemType Directory -Force -Path $directory | Out-Null
$ledgerIdentity = [guid]::NewGuid().ToString('N')
$ledgerPath = Join-Path $directory ("powershell-$PID-$ledgerIdentity.jsonl")
$utf8 = [Text.UTF8Encoding]::new($false)
$writer = [IO.StreamWriter]::new($ledgerPath, $false, $utf8)
$writer.AutoFlush = $true

$state = [pscustomobject]@{
    Writer = $writer
    WriteFailed = $false
    LedgerPath = $ledgerPath
    Breakpoints = @()
}
Set-Variable -Name $stateVariable -Scope Global -Value $state

$writer.WriteLine(([ordered]@{
            schema = 'miniapp_bridge.powershell_coverage.v1'
            kind = 'start'
            pid = [int]$PID
            powershell = $PSVersionTable.PSVersion.ToString()
            script = [string]$ScriptPath
        } | ConvertTo-Json -Compress))
$writer.WriteLine(([ordered]@{
            schema = 'miniapp_bridge.powershell_coverage.v1'
            kind = 'ready'
            pid = [int]$PID
            scope_files = [int]$scope.Count
            ledger = $ledgerPath
        } | ConvertTo-Json -Compress))

$helperLeaf = [IO.Path]::GetFileName($PSCommandPath)
try {
    foreach ($scopePath in $scope) {
        $tokens = $null
        $parseErrors = $null
        $ast = [System.Management.Automation.Language.Parser]::ParseFile(
            $scopePath,
            [ref]$tokens,
            [ref]$parseErrors
        )
        if ($parseErrors.Count -gt 0) {
            throw "PowerShell coverage source failed to parse: $scopePath"
        }
        $commands = @($ast.FindAll({
                    param($node)
                    return $node -is [System.Management.Automation.Language.CommandBaseAst]
                }, $true))
        foreach ($command in $commands) {
            if ($command.Extent.Text -match [regex]::Escape($helperLeaf)) {
                continue
            }
            $ancestor = $command
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
            $breakpointFile = [IO.Path]::GetFullPath($scopePath)
            $breakpointLine = [int]$command.Extent.StartLineNumber
            $breakpointColumn = [int]$command.Extent.StartColumnNumber
            $action = {
                $savedLastExitCode = $global:LASTEXITCODE
                try {
                    $coverageState = Get-Variable -Name '__MiniappBridgePowerShellCoverageState' -Scope Global -ValueOnly -ErrorAction SilentlyContinue
                    if ($null -ne $coverageState) {
                        try {
                            $coverageState.Writer.WriteLine(([ordered]@{
                                        schema = 'miniapp_bridge.powershell_coverage.v1'
                                        kind = 'hit'
                                        pid = [int]$PID
                                        file = [string]$breakpointFile
                                        line = [int]$breakpointLine
                                        start_column = [int]$breakpointColumn
                                    } | ConvertTo-Json -Compress))
                        } catch {
                            $coverageState.WriteFailed = $true
                            [Console]::Error.WriteLine("PowerShell coverage ledger write failed: $($_.Exception.Message)")
                        }
                    }
                } finally {
                    $global:LASTEXITCODE = $savedLastExitCode
                }
            }.GetNewClosure()
            $lineOnly = $command -is [System.Management.Automation.Language.ThrowStatementAst]
            if (-not $lineOnly -and $command -is [System.Management.Automation.Language.CommandExpressionAst]) {
                $ancestor = $command.Parent
                while ($null -ne $ancestor) {
                    if ($ancestor -is [System.Management.Automation.Language.ReturnStatementAst] -or
                        $ancestor -is [System.Management.Automation.Language.ThrowStatementAst]) {
                        $lineOnly = $true
                        break
                    }
                    $ancestor = $ancestor.Parent
                }
            }
            if ($lineOnly) {
                $state.Breakpoints += Set-PSBreakpoint -Script $breakpointFile -Line $breakpointLine -Action $action
            } else {
                $state.Breakpoints += Set-PSBreakpoint -Script $breakpointFile -Line $breakpointLine -Column $breakpointColumn -Action $action
            }
        }
    }
} catch {
    foreach ($breakpoint in @($state.Breakpoints)) {
        Remove-PSBreakpoint -Breakpoint $breakpoint -ErrorAction SilentlyContinue
    }
    $writer.Dispose()
    Remove-Variable -Name $stateVariable -Scope Global -ErrorAction SilentlyContinue
    throw
}
