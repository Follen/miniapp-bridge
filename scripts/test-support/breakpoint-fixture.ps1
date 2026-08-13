param(
    [object]$Hook = $(
        $action = { Write-Output 'ACTION-HIT' }.GetNewClosure()
        Set-PSBreakpoint -Script $PSCommandPath -Line 5 -Column 1 -Action $action | Out-Null
    )
)
Write-Output 'BODY'
