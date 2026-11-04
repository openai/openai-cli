# Feed literal text to an interactive ConsoleHost with client-default Restricted policy.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $Client,
    [Parameter(Mandatory)][string] $TextPath,
    [Parameter(Mandatory)][string] $ExpectedExe,
    [Parameter(Mandatory)][string] $ExpectedVersion,
    [switch] $ExpectOneVerifierDownload,
    [ValidatePattern('^[0-9a-fA-F]{64}$')][string] $ExpectedSourceSha,
    [ValidateSet('Paste','Exit0','Exit17','Handled','Thrown','ObserveDirectory','WrongDirectory')][string] $Probe = 'Paste',
    [string] $TargetDirectory = (Get-Location).ProviderPath,
    [switch] $EnterLiteralDirectory,
    [switch] $CheckFolderIdentity,
    [string] $EvidenceLog
)
$ErrorActionPreference = 'Stop'

# ConvertFrom-Json can return several integer types. Converting strings or
# booleans to uint32 would turn malformed receipts into genuine process exits.
function Read-NativeExitCode($value) {
    $integer = $value -is [int] -or $value -is [long] -or $value -is [uint32] -or $value -is [uint64]
    if ($integer -and $value -ge 0 -and $value -le [uint32]::MaxValue) {return [uint32]$value}
}
$clientName = [IO.Path]::GetFileName($Client)
if ($clientName -inotmatch '^(powershell|pwsh)\.exe$') {$clientName = 'other-client'}
$stage = 'prepare-fixture'
$reported = $false
$files = @{}
$prior = @{}
try {
    if ($Probe -eq 'Exit0') {
        # Exercise the current client's JSON parser before its zero-exit control.
        $stage = 'check-exit-format'
        foreach ($token in 'false','true','" "','""','"0"','0.5','0.0','-1','4294967296','[]','{}','null') {
            $record = ('{"Exit":' + $token + '}') | ConvertFrom-Json
            if ($null -ne (Read-NativeExitCode $record.Exit)) {throw 'A malformed process exit passed validation.'}
        }
        foreach ($token in '0','17','2147483648','4294967295') {
            $record = ('{"Exit":' + $token + '}') | ConvertFrom-Json
            if ((Read-NativeExitCode $record.Exit) -ne [uint32]$token) {throw 'A valid process exit failed validation.'}
        }
        $stage = 'prepare-fixture'
    }
    foreach ($key in 'STDERR','TRACE','RESULT','RECEIPT','RUNNER') {$files[$key] = [IO.Path]::GetTempFileName()}
    $challenge = [guid]::NewGuid().ToString('N')
    $startID = $targetID = ''
    if ($CheckFolderIdentity) {
        $startID = [IO.File]::ReadAllText((Join-Path (Get-Location).ProviderPath '.openai-qualification-folder'))
        $targetID = [IO.File]::ReadAllText((Join-Path $TargetDirectory '.openai-qualification-folder'))
        if ($startID -cnotmatch '\A[0-9a-f]{32}\z' -or $targetID -cnotmatch '\A[0-9a-f]{32}\z') {throw 'Invalid qualification directory identity.'}
    }
    $values = @{
        OPENAI_WINDOWS_PASTE_CLIENT=$Client; OPENAI_WINDOWS_PASTE_PROBE=$Probe
        OPENAI_WINDOWS_PASTE_START_ID=$startID; OPENAI_WINDOWS_PASTE_TARGET_ID=$targetID
        OPENAI_WINDOWS_PASTE_TARGET=$TargetDirectory; OPENAI_WINDOWS_PASTE_LOCATION=$(if ($EnterLiteralDirectory) {'Literal'} else {'Inherited'})
        OPENAI_WINDOWS_PASTE_CWD=(Get-Location).ProviderPath; OPENAI_WINDOWS_PASTE_SOURCE_SHA=$ExpectedSourceSha
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
    $ownerCwd = $false
    $ownerText = 'unavailable'
    $stage = 'read-process-result'
    $raw = [IO.File]::ReadAllText($files.RESULT)
    if ($raw) {
        $result = $raw | ConvertFrom-Json
        if ($result.State -cin 'exited','launch-failed','exit-unavailable','timeout','directory-mismatch') {
            $state = $result.State
            $exitCode = Read-NativeExitCode $result.Exit
            $nativeError = [uint32]$result.NativeError
            if ($result.OwnerCWD -is [bool]) {$ownerCwd = $result.OwnerCWD; $ownerText = [string]$ownerCwd}
        }
    }

    # Retain only fixed stage names and enum values. Raw console and stderr
    # may contain private paths, environment values, or signed download URLs.
    $stage = 'validate-console'
    $trace = @([IO.File]::ReadAllLines($files.TRACE))
    $safeTrace = @($trace -cmatch '^(initialized|first-read|input-ready|second-read|literal-location-input|directory-observed|host=(ConsoleHost|other)|policy=(Restricted|AllSigned|RemoteSigned|Unrestricted|Bypass|Undefined|Default)|std(in|out|err)=(True|False)|(cwd|nativeCwd|initialProvider|initialDirectory|finalDirectory|initialSourcePath|sourcePath|errorRecords|pathFirst)=(True|False)|(initialMarker|finalMarker)=(match|different|provider|reparse|directory|length|format|missing|denied|unreadable)|(initialSource|source)=(unchecked|verified|different|missing|unreadable)|history=(completed|failed|different|missing)|native=(unavailable|-?[0-9]{1,10})|selection=(missing|shadowed|different|expected)|version=(unchecked|expected|different|command-failed))$')
    $consoleChecked = ($trace -ccontains 'initialized') -and ($trace -ccontains 'host=ConsoleHost') -and
        ($trace -ccontains 'policy=Restricted') -and ($trace -ccontains 'stdin=False') -and ($trace -ccontains 'stdout=False') -and
        ($trace -ccontains 'first-read') -and $ownerCwd
    $startChecked = $consoleChecked -and ($trace -ccontains 'initialDirectory=True') -and [bool]($safeTrace -cmatch '^nativeCwd=') -and [bool]($safeTrace -cmatch '^initialProvider=')
    $inputChecked = $startChecked -and ($trace -ccontains 'input-ready') -and ($trace -ccontains 'finalDirectory=True') -and
        ([bool]$EnterLiteralDirectory -eq ($trace -ccontains 'literal-location-input'))
    $readAgain = $trace -ccontains 'second-read'
    $commandChecked = ($trace -ccontains 'history=completed') -and ($trace -ccontains 'native=0') -and
        ($trace -ccontains 'pathFirst=True') -and ($trace -ccontains 'selection=expected') -and ($trace -ccontains 'version=expected') -and
        ($Probe -ne 'Paste' -or !$ExpectedSourceSha -or (($trace -ccontains 'source=verified') -and ($CheckFolderIdentity -or ($trace -ccontains 'sourcePath=True'))))
    $receipt = [IO.File]::ReadAllText($files.RECEIPT) -ceq $challenge
    $lines = @([IO.File]::ReadAllLines($files.STDERR))
    $stderrClass = 'empty'
    if ($lines.Count) {
        $stderrClass = 'present'
        if ($lines -match 'ParserError|UnexpectedToken|MissingEndCurlyBrace') {$stderrClass = 'parser'}
        elseif ($lines -match 'PSSecurityException|AuthorizationManager') {$stderrClass = 'security'}
        elseif ($lines -match 'extract the official ZIP into a directory only your account can modify') {$stderrClass = 'source-permissions'}
        elseif ($lines -match 'Could not download the GitHub CLI verifier') {$stderrClass = 'verifier-download'}
        elseif ($lines -match 'OpenAI CLI setup could not finish') {$stderrClass = 'setup-failed'}
    }
    $reason = 'passed'
    if ($runnerExit -ne 0) {$reason = 'console-owner-failed'}
    elseif ($state -ceq 'directory-mismatch') {$reason = 'wrong-native-directory'}
    elseif ($state -ne 'exited' -or $null -eq $exitCode) {$reason = 'no-numeric-exit'}
    elseif ($Probe -eq 'ObserveDirectory') {
        $complete = $true
        foreach ($field in 'nativeCwd','initialProvider','initialDirectory','initialSourcePath','cwd','finalDirectory','sourcePath') {
            if (@($safeTrace -cmatch ('^' + $field + '=(True|False)$')).Count -ne 1) {$complete = $false}
        }
        foreach ($field in 'initialSource','source') {
            if (@($safeTrace -cmatch ('^' + $field + '=(verified|different|missing|unreadable)$')).Count -ne 1) {$complete = $false}
        }
        foreach ($field in 'initialMarker','finalMarker') {
            if (@($safeTrace -cmatch ('^' + $field + '=')).Count -ne 1) {$complete = $false}
        }
        if (!$consoleChecked -or !$complete -or $exitCode -ne 0 -or $receipt -or $readAgain -or ($trace -ccontains 'input-ready') -or
            ($trace -cnotcontains 'directory-observed')) {$reason='directory-observation-failed'}
        else {$reason='observed'}
    }
    elseif ($Probe -eq 'WrongDirectory') {
        if (!$startChecked -or $exitCode -ne 37 -or $receipt -or $readAgain -or ($trace -ccontains 'input-ready') -or
            ($trace -cnotcontains 'finalMarker=different') -or ($trace -cnotcontains 'finalDirectory=False') -or ($trace -cnotcontains 'source=verified') -or ($trace -cnotcontains 'sourcePath=False')) {$reason='wrong-directory-not-rejected'}
    }
    elseif (!$inputChecked) {$reason = 'input-not-verified'}
    elseif ($Probe -in 'Exit0','Exit17') {
        $expected = if ($Probe -eq 'Exit0') {0} else {17}
        if ($exitCode -ne $expected) {$reason = 'unexpected-control-exit'}
        elseif ($readAgain -or $receipt) {$reason = 'control-reached-validation'}
    } elseif ($Probe -eq 'Thrown') {
        # A prior healthy installation must never hide an unhandled paste failure.
        if ($exitCode -ne 33 -or !$readAgain -or $receipt -or ($trace -cnotcontains 'history=failed') -or
            ($trace -cnotcontains 'errorRecords=True') -or ($trace -cnotcontains 'native=0') -or ($trace -cnotcontains 'pathFirst=True') -or
            ($trace -cnotcontains 'selection=expected') -or ($trace -cnotcontains 'version=expected')) {$reason = 'unverified-throw'}
    } elseif ($exitCode -ne 0) {$reason = 'client-exit'}
    elseif (!$readAgain -or !$receipt) {$reason = 'missing-completion-receipt'}
    elseif (!$commandChecked) {$reason = 'unverified-paste'}
    elseif ($Probe -eq 'Handled' -and $trace -cnotcontains 'errorRecords=True') {$reason = 'missing-handled-error'}
    elseif ($ExpectOneVerifierDownload -and (@($lines -ceq 'Downloading the pinned GitHub CLI verifier...').Count -ne 1 -or
        @($lines -ceq 'Downloading GitHub CLI for automatic update verification...').Count -ne 0)) {$reason = 'verifier-download-count'}
    $codeText = if ($null -eq $exitCode) {'unavailable'} else {[string]$exitCode}
    $runnerText = if ($null -eq $runnerExit) {'unavailable'} else {[string]$runnerExit}
    $summary = "$clientName $Probe stage=$stage reason=$reason ownerCwd=$ownerText runner=$runnerText process=$state nativeError=$nativeError exit=$codeText trace=[$($safeTrace -join ',')] receipt=$receipt stderr=$stderrClass"
    if ($EvidenceLog) {Add-Content -LiteralPath $EvidenceLog -Value $summary}
    $reported = $true
    if ($reason -notin 'passed','observed') {throw "Restricted ConsoleHost qualification failed: $summary"}
} catch {
    if (!$reported -and $EvidenceLog) {
        Add-Content -LiteralPath $EvidenceLog -Value "$clientName $Probe stage=$stage category=$($_.CategoryInfo.Category) process=unavailable exit=unavailable"
    }
    throw
} finally {
    foreach ($key in $prior.Keys) {[Environment]::SetEnvironmentVariable($key,$prior[$key])}
    foreach ($file in $files.Values) {[IO.File]::Delete($file)}
}
