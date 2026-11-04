# Run with a local release-stamped candidate. No request reaches a public service.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $Candidate,
    [Parameter(Mandatory)][string] $Tag,
    [Parameter(Mandatory)][string] $Commit,
    [Parameter(Mandatory)][ValidateSet('amd64','arm64','386')][string] $Arch
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression.FileSystem
$recipe = Join-Path $PSScriptRoot 'install-windows-release.ps1'
$tokens, $parseErrors = $null, $null
$ast = [Management.Automation.Language.Parser]::ParseFile($recipe,[ref]$tokens,[ref]$parseErrors)
if ($parseErrors.Count) { throw 'PowerShell rejected the Windows release acquisition script.' }
$work = Join-Path ([IO.Path]::GetTempPath()) ('openai-acquisition-test-' + [Guid]::NewGuid().ToString('N'))
$null = [IO.Directory]::CreateDirectory($work)
try {
    # Execute the reviewed local function definitions with a synthetic metadata
    # provider. The shipped script exposes no fixture URL or verifier override.
    $names = 'Set-InternetZone','Get-Sha','Expand-Executable','Read-Release','Test-Publication','Read-GoLength','Confirm-ReleaseExecutable'
    $defs = @($ast.FindAll({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -in $names},$true))
    if ($defs.Count -ne $names.Count) { throw 'The acquisition fixture is missing production code.' }
    $local = Join-Path $work 'local-functions.ps1'
    [IO.File]::WriteAllText($local,($defs.Extent.Text -join [Environment]::NewLine))
    . $local
    function Assert-Rejected([scriptblock]$probe) {
        try { & $probe | Out-Null } catch { return }
        throw 'A negative release fixture qualified.'
    }
    $repo = 'https://api.github.com/repos/openai/openai-cli'
    $workflow = 'https://github.com/openai/openai-cli/.github/workflows/publish-release.yml@refs/heads/main'
    $stable = '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
    $ReleaseTag = $Tag
    $name = "openai_$($Tag.Substring(1))_windows_$Arch.zip"
    $digest = 'd'*64
    $publisherHead = 'b'*40
    $asset = @{name=$name; id=21; size=524288; updated_at='2026-01-01T00:00:00Z'; digest="sha256:$digest"}
    $release = @{id=11; tag_name=$Tag; draft=$false; prerelease=$false; published_at='2026-01-01T00:00:00Z'; assets=@($asset)}
    $run = @{id=23; run_attempt=2; status='completed'; conclusion='success'; event='workflow_dispatch'; head_branch='main'; head_sha=$publisherHead; path='.github/workflows/publish-release.yml@main'; repository=@{full_name='openai/openai-cli'}}
    function Get-PublicResource([string]$url) {
        if ($url -ceq "$repo/releases/latest" -or $url -ceq "$repo/releases/11" -or $url -ceq "$repo/releases/tags/$Tag") { return $release }
        if ($url -ceq "$repo/git/ref/tags/$Tag") { return @{object=@{type='tag';sha=('e'*40)}} }
        if ($url -ceq "$repo/git/tags/$('e'*40)") { return @{object=@{type='commit';sha=$Commit}} }
        if ($url -ceq "$repo/actions/runs/23/attempts/2") { return $run }
        throw 'Unexpected public endpoint in offline fixture.'
    }
    $selected = Read-Release 'releases/latest'
    if ($selected.tag -cne $Tag -or $selected.commit -cne $Commit -or $selected.digest -cne $digest -or $selected.name -cne $name) { throw 'Unexpected release selection.' }
    $release.draft = $true
    Assert-Rejected { Read-Release 'releases/latest' }
    $release.draft = $false
    $release.assets = @($asset,$asset)
    Assert-Rejected { Read-Release 'releases/latest' }
    $release.assets = @($asset)
    $subject = @{name=$name;digest=@{sha256=$digest}}
    $certificate = @{subjectAlternativeName=$workflow;sourceRepositoryDigest=$publisherHead;runInvocationURI='https://github.com/openai/openai-cli/actions/runs/23/attempts/2'}
    $item = @{verificationResult=@{signature=@{certificate=$certificate};statement=@{subject=@($subject)}}}
    if (!(Test-Publication $item $selected)) { throw 'Matching completed publisher failed verification.' }
    $run.conclusion = 'failure'
    if (Test-Publication $item $selected) { throw 'A failed publisher qualified.' }
    $run.conclusion = 'success'
    $subject.digest.sha256 = 'c'*64
    if (Test-Publication $item $selected) { throw 'A different attested subject qualified.' }
    $subject.digest.sha256 = $digest
    $certificate.subjectAlternativeName = 'https://github.com/untrusted/fixture'
    if (Test-Publication $item $selected) { throw 'An unrelated signer qualified.' }

    Confirm-ReleaseExecutable $Candidate $selected
    $wrong = @{tag=$Tag; commit=('f'*40)}
    Assert-Rejected { Confirm-ReleaseExecutable $Candidate $wrong }
    $zip = Join-Path $work 'release.zip'
    $out = Join-Path $work 'openai.exe'
    $z = [IO.Compression.ZipFile]::Open($zip,'Create')
    try {
        $null = $z.CreateEntry('../escape')
        $null = [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($z,$Candidate,'openai.exe','Fastest')
    } finally { $z.Dispose() }
    Expand-Executable $zip 'openai.exe' $out
    if ((Get-Sha $out) -cne (Get-Sha $Candidate) -or [IO.File]::Exists((Join-Path $work '../escape')) -or
        [IO.File]::ReadAllText($out + ':Zone.Identifier') -notmatch 'ZoneId=3') { throw 'Incorrect ZIP extraction or download zone.' }
    $z = [IO.Compression.ZipFile]::Open($zip,'Update')
    try { $null = $z.CreateEntry('openai.exe') } finally { $z.Dispose() }
    Assert-Rejected { Expand-Executable $zip 'openai.exe' (Join-Path $work 'duplicate.exe') }
    if ([Environment]::OSVersion.Platform -eq 'Win32NT') {
        # Feed the exact raw text through each real interactive ConsoleHost
        # parser. A synthetic CPU or the elevated runner stops network access.
        $driver = Join-Path $work 'console-paste.ps1'
        $driverBody = @'
if ([Console]::IsInputRedirected) { exit 71 }
$global:rawSetup = [IO.File]::ReadAllText($env:OPENAI_CLI_PASTE_FIXTURE)
$global:reads = 0
function global:Get-CimInstance { [pscustomobject]@{Architecture=999} }
function global:PSConsoleHostReadLine {
    $global:reads++
    if ($global:reads -eq 1) { $global:Error.Clear(); return $global:rawSetup }
    if ($global:reads -eq 2) {
        return 'if ($global:Error.Count -eq 1 -and $global:Error[0].Exception.Message -in "Unsupported Windows architecture.","Run Windows setup in a terminal without administrator privileges.") {exit 0}; exit 72'
    }
    return 'exit 73'
}
'@
        [IO.File]::WriteAllText($driver,$driverBody)
        $prior = $env:OPENAI_CLI_PASTE_FIXTURE
        try {
            $env:OPENAI_CLI_PASTE_FIXTURE = $recipe
            foreach ($shell in 'powershell.exe','pwsh.exe') {
                $exe = (Get-Command $shell -CommandType Application).Path | Select-Object -First 1
                $child = Start-Process $exe -ArgumentList ("-NoLogo -NoProfile -NoExit -File `""+$driver+"`"") -WindowStyle Hidden -PassThru
                try {
                    if (!$child.WaitForExit(20000)) { $child.Kill(); throw "Interactive paste timed out in $shell." }
                    if ($child.ExitCode -ne 0) { throw "Interactive paste did not reach the expected offline refusal in $shell." }
                } finally { $child.Dispose() }
            }
        } finally { $env:OPENAI_CLI_PASTE_FIXTURE = $prior }
    }
    Write-Output 'Windows release selection, stamp, publisher, extraction, and rejection fixtures passed.'
} finally { [IO.Directory]::Delete($work,$true) }
