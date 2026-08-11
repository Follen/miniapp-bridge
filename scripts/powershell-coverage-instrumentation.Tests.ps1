Set-StrictMode -Version Latest

Describe 'PowerShell cross-process coverage instrumentation' {
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
                    commands_analyzed = $count
                    commands_executed = $count
                    commands_missed = 0
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
