# Feed literal text to an interactive ConsoleHost with client-default Restricted policy.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $Client,
    [Parameter(Mandatory)][string] $TextPath,
    [Parameter(Mandatory)][string] $ExpectedExe,
    [Parameter(Mandatory)][string] $ExpectedVersion,
    [switch] $ExpectOneVerifierDownload,
    [ValidateSet('Paste','Exit0','Exit17')][string] $Probe = 'Paste',
    [string] $EvidenceLog
)
$ErrorActionPreference = 'Stop'
$clientName = [IO.Path]::GetFileName($Client)
if ($clientName -inotmatch '^(powershell|pwsh)\.exe$') {$clientName = 'other-client'}
$stage = 'prepare-fixture'
$reported = $false
$files = @{}
$prior = @{}
try {
    foreach ($key in 'STDERR','TRACE','RESULT','RECEIPT','RUNNER') {$files[$key] = [IO.Path]::GetTempFileName()}
    $challenge = [guid]::NewGuid().ToString('N')
    $values = @{
        OPENAI_WINDOWS_PASTE_CLIENT=$Client; OPENAI_WINDOWS_PASTE_PROBE=$Probe
        OPENAI_WINDOWS_PASTE_TEXT=$TextPath; OPENAI_WINDOWS_MANAGED_EXE=$ExpectedExe; OPENAI_WINDOWS_STARTER_VERSION=$ExpectedVersion
        OPENAI_WINDOWS_PASTE_CHALLENGE=$challenge; OPENAI_WINDOWS_PASTE_RECEIPT=$files.RECEIPT
        OPENAI_WINDOWS_PASTE_STDERR=$files.STDERR; OPENAI_WINDOWS_PASTE_TRACE=$files.TRACE; OPENAI_WINDOWS_PASTE_RESULT=$files.RESULT
    }
    foreach ($key in $values.Keys) {
        $prior[$key] = [Environment]::GetEnvironmentVariable($key)
        [Environment]::SetEnvironmentVariable($key,$values[$key])
    }
    $runner = Join-Path $PSScriptRoot 'archive.test.exe'
    $stage = 'start-console-owner'
    & $runner '-test.run=^TestMainWindowsArchivePasteHost$' *> $files.RUNNER
    $runnerExit = $LASTEXITCODE
    $state = 'no-process-result'
    $exitCode = $null
    $nativeError = 0
    $stage = 'read-process-result'
    $raw = [IO.File]::ReadAllText($files.RESULT)
    if ($raw) {
        $result = $raw | ConvertFrom-Json
        if ($result.State -cin 'exited','launch-failed','exit-unavailable','timeout') {
            $state = $result.State
            if ($null -ne $result.Exit) {$exitCode = [uint32]$result.Exit}
            $nativeError = [uint32]$result.NativeError
        }
    }

    # Retain only fixed stage names and enum values. Raw console and stderr
    # may contain private paths, environment values, or signed download URLs.
    $stage = 'validate-console'
    $trace = @([IO.File]::ReadAllLines($files.TRACE))
    $safeTrace = @($trace -cmatch '^(initialized|first-read|input-ready|second-read|host=(ConsoleHost|other)|policy=(Restricted|AllSigned|RemoteSigned|Unrestricted|Bypass|Undefined|Default)|std(in|out|err)=(True|False))$')
    $inputChecked = ($trace -ccontains 'initialized') -and ($trace -ccontains 'host=ConsoleHost') -and
        ($trace -ccontains 'policy=Restricted') -and ($trace -ccontains 'stdin=False') -and ($trace -ccontains 'stdout=False') -and
        ($trace -ccontains 'first-read') -and ($trace -ccontains 'input-ready')
    $readAgain = $trace -ccontains 'second-read'
    $receipt = [IO.File]::ReadAllText($files.RECEIPT) -ceq $challenge
    $lines = @([IO.File]::ReadAllLines($files.STDERR))
    $stderrClass = 'empty'
    if ($lines.Count) {
        $stderrClass = 'present'
        if ($lines -match 'ParserError|UnexpectedToken|MissingEndCurlyBrace') {$stderrClass = 'parser'}
        elseif ($lines -match 'PSSecurityException|AuthorizationManager') {$stderrClass = 'security'}
    }
    $reason = 'passed'
    if ($runnerExit -ne 0) {$reason = 'console-owner-failed'}
    elseif ($state -ne 'exited' -or $null -eq $exitCode) {$reason = 'no-numeric-exit'}
    elseif (!$inputChecked) {$reason = 'input-not-verified'}
    elseif ($Probe -ne 'Paste') {
        $expected = if ($Probe -eq 'Exit0') {0} else {17}
        if ($exitCode -ne $expected) {$reason = 'unexpected-control-exit'}
        elseif ($readAgain -or $receipt) {$reason = 'control-reached-validation'}
    } elseif ($exitCode -ne 0) {$reason = 'client-exit'}
    elseif (!$readAgain -or !$receipt) {$reason = 'missing-completion-receipt'}
    elseif ($ExpectOneVerifierDownload -and (@($lines -ceq 'Downloading the pinned GitHub CLI verifier...').Count -ne 1 -or
        @($lines -ceq 'Downloading GitHub CLI for automatic update verification...').Count -ne 0)) {$reason = 'verifier-download-count'}
    $codeText = if ($null -eq $exitCode) {'unavailable'} else {[string]$exitCode}
    $runnerText = if ($null -eq $runnerExit) {'unavailable'} else {[string]$runnerExit}
    $summary = "$clientName $Probe stage=$stage reason=$reason runner=$runnerText process=$state nativeError=$nativeError exit=$codeText trace=[$($safeTrace -join ',')] receipt=$receipt stderr=$stderrClass"
    if ($EvidenceLog) {Add-Content -LiteralPath $EvidenceLog -Value $summary}
    $reported = $true
    if ($reason -ne 'passed') {throw "Restricted ConsoleHost qualification failed: $summary"}
} catch {
    if (!$reported -and $EvidenceLog) {
        Add-Content -LiteralPath $EvidenceLog -Value "$clientName $Probe stage=$stage category=$($_.CategoryInfo.Category) process=unavailable exit=unavailable"
    }
    throw
} finally {
    foreach ($key in $prior.Keys) {[Environment]::SetEnvironmentVariable($key,$prior[$key])}
    foreach ($file in $files.Values) {[IO.File]::Delete($file)}
}
