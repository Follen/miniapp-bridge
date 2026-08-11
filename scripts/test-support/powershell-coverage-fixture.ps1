param(
    [string]$Name,
    [switch]$Fail
)

$prefix = 'hello'
if ($Fail) {
    throw 'fixture failure'
}
Write-Output "$prefix $Name"
