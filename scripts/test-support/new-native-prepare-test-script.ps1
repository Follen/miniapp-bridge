param(
    [Parameter(Mandatory)][string]$SourcePath,
    [Parameter(Mandatory)][string]$OutputPath
)

$ErrorActionPreference = 'Stop'
$source = [IO.File]::ReadAllText([IO.Path]::GetFullPath($SourcePath))
$tokens = $null
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseInput($source, [ref]$tokens, [ref]$errors)
if ($errors.Count -ne 0) { throw 'native prepare source does not parse' }

function Insert-FunctionPreamble([string]$Name, [string]$Preamble) {
    $function = $ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -ceq $Name }, $true)
    if ($function.Count -ne 1) { throw "expected exactly one $Name function" }
    $offset = $function[0].Body.Extent.StartOffset + 1
    $script:source = $script:source.Insert($offset, $Preamble)
}

# Replace in descending source order so the original AST offsets remain valid.
Insert-FunctionPreamble 'Assert-NativeExports' @'

    $testExports = [Environment]::GetEnvironmentVariable('MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_EXPORTS_STATUS')
    if ($testExports -ceq 'valid') { return }
    if ($testExports -ceq 'invalid') { throw 'native DLL required exports missing: injected' }
    if ($testExports) { throw 'invalid MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_EXPORTS_STATUS value' }
'@

$tokens = $null; $errors = $null
$ast = [Management.Automation.Language.Parser]::ParseInput($source, [ref]$tokens, [ref]$errors)
Insert-FunctionPreamble 'Assert-NativeSignature' @'

    $testSignature = [Environment]::GetEnvironmentVariable('MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_SIGNATURE_STATUS')
    if ($testSignature -ceq 'valid') { return [pscustomobject]@{ SignerThumbprint='TEST-SIGNER'; TimestampThumbprint='TEST-TIMESTAMP' } }
    if ($testSignature -ceq 'invalid') { throw "native Authenticode signature is invalid: $Path" }
    if ($testSignature -ceq 'missing') { return [pscustomobject]@{ SignerThumbprint=''; TimestampThumbprint='' } }
    if ($testSignature -ceq 'missing-timestamp') { throw "native Authenticode trusted timestamp is missing: $Path" }
    if ($testSignature) { throw 'invalid MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_SIGNATURE_STATUS value' }
'@

function Insert-Before([string]$Anchor, [string]$Text) {
    $index = $script:source.IndexOf($Anchor, [StringComparison]::Ordinal)
    if ($index -lt 0 -or $script:source.IndexOf($Anchor, $index + 1, [StringComparison]::Ordinal) -ge 0) {
        throw "test assembly anchor is not unique: $Anchor"
    }
    $script:source = $script:source.Insert($index, $Text)
}

Insert-Before "`$ErrorActionPreference = 'Stop'" @'
$rollbackFailure = [Environment]::GetEnvironmentVariable('MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_ROLLBACK_FAILURE')
if ($rollbackFailure -and $rollbackFailure -notin @('after-rollback-validation', 'after-rollback-publish')) {
    throw 'invalid MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_ROLLBACK_FAILURE value'
}
$publishFailure = [Environment]::GetEnvironmentVariable('MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_PUBLISH_FAILURE')
if ($publishFailure -and $publishFailure -notin @(
        'after-dll-backup', 'after-manifest-backup', 'after-manifest-publish', 'after-dll-publish')) {
    throw 'invalid MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_PUBLISH_FAILURE value'
}
'@

Insert-Before "    `$targetLease = `$null" @'
    if ([Environment]::GetEnvironmentVariable('MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_ROLLBACK_FAILURE') -ceq 'after-rollback-validation') {
        $replacement = "$($targetEvidence.DLLPath).rollback-replacement"
        try {
            [IO.File]::WriteAllBytes($replacement,[IO.File]::ReadAllBytes($targetEvidence.DLLPath))
            Move-Item -LiteralPath $replacement -Destination $targetEvidence.DLLPath -Force
        }
        finally { Remove-Item -LiteralPath $replacement -Force -ErrorAction SilentlyContinue }
    }
'@
Insert-Before "            try { Assert-PublishedVersion `$targetLease.Evidence | Out-Null }" @'
            if ([Environment]::GetEnvironmentVariable('MINIAPP_BRIDGE_TEST_NATIVE_PREPARE_ROLLBACK_FAILURE') -ceq 'after-rollback-publish') {
                [IO.File]::AppendAllText($installed,'rollback-publish-mismatch')
            }
'@

Insert-Before '        $publicationStarted = $true' @'
        if ($publishFailure -ceq 'after-dll-backup') { throw 'injected native publish failure: after-dll-backup' }
        if ($publishFailure -ceq 'after-manifest-backup') { throw 'injected native publish failure: after-manifest-backup' }
'@
Insert-Before '        Assert-PublishedVersion $candidateLease.Evidence | Out-Null' @'
        if ($publishFailure -ceq 'after-manifest-publish') { throw 'injected native publish failure: after-manifest-publish' }
        if ($publishFailure -ceq 'after-dll-publish') { throw 'injected native publish failure: after-dll-publish' }
'@

$output = [IO.Path]::GetFullPath($OutputPath)
[IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($output)) | Out-Null
[IO.File]::WriteAllText($output, $source, [Text.UTF8Encoding]::new($false))
