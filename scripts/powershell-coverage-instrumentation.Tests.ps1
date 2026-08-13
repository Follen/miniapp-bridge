Set-StrictMode -Version Latest

Describe 'PowerShell cross-process coverage instrumentation' {
    BeforeAll {
        $script:nativeDriver = Join-Path $PSScriptRoot 'cshim-coverage.ps1'
        $nativeHook = Join-Path $PSScriptRoot 'test-support\powershell-coverage-hook.ps1'
        $nativeScope = Join-Path $TestDrive 'native-scope.json'
        $nativeEvents = Join-Path $TestDrive 'native-events'
        [IO.File]::WriteAllText($nativeScope, (@($script:nativeDriver) | ConvertTo-Json -Compress))
        & $nativeHook -Start -ScriptPath $script:nativeDriver -CoverageDirectory $nativeEvents -ScopeFile $nativeScope
    }

    It 'uses a process-unique safe ledger name and preserves event PID' {
        $hook = Join-Path $PSScriptRoot 'test-support\powershell-coverage-hook.ps1'
        $fixture = Join-Path $PSScriptRoot 'test-support\powershell-coverage-fixture.ps1'
        $scope = Join-Path $TestDrive 'scope.json'
        $events = Join-Path $TestDrive 'events'
        [IO.File]::WriteAllText($scope, (@($fixture) | ConvertTo-Json -Compress))

        $hostPath = (Get-Process -Id $PID).Path
        $command = "& '$($hook.Replace("'", "''"))' -Start -ScriptPath '$($fixture.Replace("'", "''"))' -CoverageDirectory '$($events.Replace("'", "''"))' -ScopeFile '$($scope.Replace("'", "''"))'; & cmd.exe /d /c exit 0; & '$($fixture.Replace("'", "''"))' world | Out-Null; if (`$LASTEXITCODE -ne 0) { exit 42 }"
        foreach ($index in 1..2) {
            & $hostPath -NoProfile -NonInteractive -Command $command
            $LASTEXITCODE | Should Be 0
        }

        $ledgers = @(Get-ChildItem -LiteralPath $events -Filter '*.jsonl' -File)
        $ledgers.Count | Should Be 2
        ($ledgers.Name | Select-Object -Unique).Count | Should Be 2
        foreach ($ledger in $ledgers) {
            $ledger.Name | Should Match '^powershell-[0-9]+-[0-9a-f]{32}\.jsonl$'
            $start = Get-Content -LiteralPath $ledger.FullName -First 1 | ConvertFrom-Json
            $start.kind | Should Be 'start'
            $ledger.Name | Should Match ("^powershell-$($start.pid)-")
        }
    }

    It 'preserves positional arguments and records both branch statements' {
        $fixture = Join-Path $PSScriptRoot 'test-support\powershell-coverage-fixture.ps1'
        (& $fixture 'world') | Should Be 'hello world'
        $thrown = $false
        try {
            & $fixture 'world' -Fail
        } catch {
            $thrown = $true
            $_.Exception.Message | Should Be 'fixture failure'
        }
        $thrown | Should Be $true
    }

    It 'preserves native exit codes while coverage breakpoints execute' {
        $report = Join-Path $TestDrive 'cshim.json'
        try {
            & $script:nativeDriver -ReportPath $report | Out-Null
            $LASTEXITCODE | Should Be 0
            $result = Get-Content -LiteralPath $report -Raw | ConvertFrom-Json
            $result.result | Should Be 'passed'
            $result.line_percent | Should Be 100
            $result.function_percent | Should Be 100
        } finally {
            $coverageState = Get-Variable -Name '__MiniappBridgePowerShellCoverageState' -Scope Global -ValueOnly -ErrorAction SilentlyContinue
            if ($null -ne $coverageState) {
                foreach ($breakpoint in @($coverageState.Breakpoints)) {
                    Remove-PSBreakpoint -Breakpoint $breakpoint -ErrorAction SilentlyContinue
                }
                $coverageState.Writer.Dispose()
                Remove-Variable -Name '__MiniappBridgePowerShellCoverageState' -Scope Global -ErrorAction SilentlyContinue
            }
        }
    }

    It 'merges only complete non-overlapping source-bound shard reports' {
        $runner = Join-Path $PSScriptRoot 'powershell-coverage.ps1'
        $sources = @(
            Join-Path $PSScriptRoot 'test-support\powershell-coverage-fixture.ps1'
            Join-Path $PSScriptRoot 'test-support\breakpoint-fixture.ps1'
        )
        $repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
        $reports = @()
        try {
            foreach ($source in $sources) {
                $tokens = $null
                $errors = $null
                $ast = [System.Management.Automation.Language.Parser]::ParseFile(
                    $source,
                    [ref]$tokens,
                    [ref]$errors
                )
                $count = @($ast.FindAll({
                            param($node)
                            $node -is [System.Management.Automation.Language.CommandBaseAst]
                        }, $true)).Count
                $relative = [IO.Path]::GetFullPath($source).Substring($repo.Length + 1).Replace('\', '/')
                $reportPath = Join-Path $TestDrive (([IO.Path]::GetFileNameWithoutExtension($source)) + '.json')
                $report = [ordered]@{
                    schema = 'miniapp_bridge.coverage.v1'
                    language = 'PowerShell'
                    command_percent = 100.0
                    source_count = 1
                    commands_analyzed = $count
                    commands_executed = $count
                    commands_missed = 0
                    failed_tests = 0
                    source_manifest = @([ordered]@{
                            path = $relative
                            sha256 = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant()
                            commands_analyzed = $count
                        })
                    result = 'passed'
                }
                [IO.File]::WriteAllText($reportPath, ($report | ConvertTo-Json -Depth 6 -Compress))
                $reports += $reportPath
            }

            $mergedPath = Join-Path $TestDrive 'merged.json'
            & $runner -CoveragePath $sources -ShardReportPath $reports -ReportPath $mergedPath | Out-Null
            $merged = Get-Content -LiteralPath $mergedPath -Raw | ConvertFrom-Json
            if ($merged.result -ne 'passed' -or $merged.source_count -ne 2 -or
                $merged.commands_executed -ne $merged.commands_analyzed) {
                throw 'merged PowerShell coverage report did not pass its totals'
            }

            $overlapError = $null
            try { & $runner -CoveragePath $sources -ShardReportPath @($reports[0], $reports[0]) -ReportPath $mergedPath | Out-Null } catch { $overlapError = $_ }
            if ($null -eq $overlapError -or $overlapError.Exception.Message -notlike '*overlap source*') {
                throw 'overlapping PowerShell coverage shards were accepted'
            }

            $drifted = Get-Content -LiteralPath $reports[1] -Raw | ConvertFrom-Json
            $drifted.source_manifest[0].sha256 = '00'
            [IO.File]::WriteAllText($reports[1], ($drifted | ConvertTo-Json -Depth 6 -Compress))
            $driftError = $null
            try { & $runner -CoveragePath $sources -ShardReportPath $reports -ReportPath $mergedPath | Out-Null } catch { $driftError = $_ }
            if ($null -eq $driftError -or $driftError.Exception.Message -notlike '*source changed after shard execution*') {
                throw 'PowerShell coverage source drift was accepted'
            }
        } finally {
            foreach ($path in @($reports)) {
                Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
            }
        }
    }

}
