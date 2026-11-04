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
    $names = 'Set-InternetZone','Get-Sha','Expand-Executable','Read-Release','Invoke-SetupProcess','Test-Publication','Read-GoLength','Confirm-ReleaseExecutable'
    $defs = @($ast.FindAll({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -in $names},$true))
    if ($defs.Count -ne $names.Count) { throw 'The acquisition fixture is missing production code.' }
    $local = Join-Path $work 'local-functions.ps1'
    [IO.File]::WriteAllText($local,($defs.Extent.Text -join [Environment]::NewLine))
    . $local
    $windowsStreams = [Environment]::OSVersion.Platform -eq 'Win32NT'
    if (!$windowsStreams) {
        # Other filesystems can exercise ZIP and provenance checks. Windows verifies the real named stream below.
        Write-Output 'NTFS download zone checks require Windows.'
        function Set-InternetZone([string]$path) {if (![IO.File]::Exists($path)) {throw 'Missing downloaded file.'}}
    }
    function Assert-InternetZone([string]$path) {
        $mode = if ($PSVersionTable.PSVersion.Major -le 5) {@{Encoding='Byte'; ReadCount=0}} else {@{AsByteStream=$true; Raw=$true}}
        $bytes = [byte[]](Microsoft.PowerShell.Management\Get-Content -LiteralPath $path -Stream Zone.Identifier @mode -ErrorAction Stop)
        $expected = [Text.Encoding]::ASCII.GetBytes("[ZoneTransfer]`r`nZoneId=3`r`n")
        if ([Convert]::ToBase64String($bytes) -cne [Convert]::ToBase64String($expected)) {throw 'Unexpected download zone bytes.'}
    }
    function Assert-Rejected([scriptblock]$probe) {
        try { & $probe | Out-Null } catch { return }
        throw 'A negative release fixture qualified.'
    }
    $shell = (Get-Process -Id $PID).Path
    $variables = @{HTTPS_PROXY='https://proxy.invalid:443'; SSL_CERT_FILE='fixture-ca'; HTTP_PROXY='http://proxy.invalid:80'; NO_PROXY='fixture.invalid'; SSL_CERT_DIR='fixture-certs'; GH_TOKEN='fake'; OPENAI_API_KEY='fake'; OPENAI_PRIVATE_SETUP_FIXTURE='private'}
    $prior = @{}
    try {
        foreach ($key in $variables.Keys) {
            $prior[$key] = [Environment]::GetEnvironmentVariable($key)
            [Environment]::SetEnvironmentVariable($key,$variables[$key])
        }
        $probe = '-NoLogo -NoProfile -NonInteractive -Command "if ($env:HTTPS_PROXY -cne ''https://proxy.invalid:443'' -or $env:SSL_CERT_FILE -cne ''fixture-ca'' -or $env:HTTP_PROXY -cne ''http://proxy.invalid:80'' -or $env:NO_PROXY -cne ''fixture.invalid'' -or $env:SSL_CERT_DIR -cne ''fixture-certs'' -or $env:GH_TOKEN -or $env:OPENAI_API_KEY -or $env:OPENAI_PRIVATE_SETUP_FIXTURE) { exit 23 }; [Console]::Write(''transport-only'')"'
        foreach ($verifier in $true,$false) {
            if ((Invoke-SetupProcess $shell $probe $verifier) -cne 'transport-only') { throw 'Setup child transport isolation failed.' }
        }
    } finally {
        foreach ($key in $prior.Keys) { [Environment]::SetEnvironmentVariable($key,$prior[$key]) }
    }
    # Exercise the exact extracted-ZIP paste in a separate PowerShell process.
    $guide = [IO.File]::ReadAllText((Join-Path $PSScriptRoot '../docs/installation.md'))
    $blocks = @([regex]::Matches($guide,'(?ms)^```powershell\r?\n(.*?)^```') | Where-Object { $_.Groups[1].Value.Contains('.\openai.exe install --user') })
    if ($blocks.Count -ne 1) { throw 'The documented extracted ZIP paste is ambiguous.' }
    $fakeDir = [IO.Directory]::CreateDirectory((Join-Path $work 'exit-fixture')).FullName
    $src = Join-Path $fakeDir 'exit.go'
    $fake = Join-Path $fakeDir 'openai.exe'
    [IO.File]::WriteAllText($src,'package main; import ("os";"strconv"); func main() { n,_:=strconv.Atoi(os.Getenv("OPENAI_FIXTURE_EXIT")); os.Exit(n) }')
    & go build -o $fake $src
    if ($LASTEXITCODE -ne 0) {throw 'Could not build the paste failure fixture.'}
    $priorExit = $env:OPENAI_FIXTURE_EXIT
    Push-Location $fakeDir
    try {
        foreach ($status in '17','0') {
            $env:OPENAI_FIXTURE_EXIT = $status
            & $shell -NoLogo -NoProfile -NonInteractive -Command $blocks[0].Groups[1].Value *> $null
            if ($LASTEXITCODE -eq 0) { throw 'The installation paste reported success after missing or failed enrollment.' }
        }
        if ($windowsStreams) {
            Copy-Item -LiteralPath (Join-Path $PSScriptRoot '../Install.cmd') -Destination (Join-Path $fakeDir 'Install.cmd')
            $env:OPENAI_FIXTURE_EXIT = '17'
            '' | & "$env:SystemRoot\System32\cmd.exe" /d /c Install.cmd *> $null
            if ($LASTEXITCODE -ne 17) {throw 'Explorer setup did not preserve the real failure status.'}
        }
    } finally { Pop-Location; $env:OPENAI_FIXTURE_EXIT = $priorExit }
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
    if ((Get-Sha $out) -cne (Get-Sha $Candidate) -or [IO.File]::Exists((Join-Path $work '../escape'))) {
        throw 'Incorrect ZIP extraction.'
    }
    if ($windowsStreams) {Assert-InternetZone $out}
    $z = [IO.Compression.ZipFile]::Open($zip,'Update')
    try { $null = $z.CreateEntry('openai.exe') } finally { $z.Dispose() }
    Assert-Rejected { Expand-Executable $zip 'openai.exe' (Join-Path $work 'duplicate.exe') }
    if ([Environment]::OSVersion.Platform -eq 'Win32NT') {
        # Feed the exact raw text through each real interactive ConsoleHost
        # parser. A synthetic CPU or the elevated runner stops network access.
        $zoneFunction = ($defs | Where-Object Name -ceq 'Set-InternetZone').Extent.Text
        $zoneAssertion = (Get-Command Assert-InternetZone -CommandType Function).ScriptBlock.Ast.Extent.Text
        $driver = Join-Path $work 'console-paste.ps1'
        $driverBody = @'
if ([Console]::IsInputRedirected) { exit 71 }
try {
    $file = $env:OPENAI_CLI_ZONE_FIXTURE
    Set-InternetZone $file
    Assert-InternetZone $file
    $plain = [Text.Encoding]::ASCII.GetBytes("[ZoneTransfer]`r`nZoneId=3`r`n")
    $binary = if ($PSVersionTable.PSVersion.Major -le 5) {@{Encoding='Byte'}} else {@{AsByteStream=$true}}
    foreach ($case in 'bom','newline') {
        $invalid = if ($case -ceq 'bom') {@(239,187,191) + $plain} else {$plain + @(13,10)}
        Microsoft.PowerShell.Management\Set-Content -LiteralPath $file -Stream Zone.Identifier @binary -Value ([byte[]]$invalid) -ErrorAction Stop
        $rejected = $false
        try {Assert-InternetZone $file} catch {
            if ($_.Exception.Message -cne 'Unexpected download zone bytes.') {throw}
            $rejected = $true
        }
        if (!$rejected) {exit 74}
    }
    Set-InternetZone $file
    Assert-InternetZone $file
    [IO.File]::WriteAllText($file + '.verified','canonical, rejected BOM, rejected newline')
} catch {exit 75}
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
        [IO.File]::WriteAllText($driver,(($zoneFunction,$zoneAssertion,$driverBody) -join [Environment]::NewLine))
        $prior = $env:OPENAI_CLI_PASTE_FIXTURE
        $priorZone = $env:OPENAI_CLI_ZONE_FIXTURE
        try {
            $env:OPENAI_CLI_PASTE_FIXTURE = $recipe
            foreach ($shell in 'powershell.exe','pwsh.exe') {
                $zoneFile = Join-Path $work ("download [1] $shell")
                [IO.File]::WriteAllText($zoneFile,'original')
                $env:OPENAI_CLI_ZONE_FIXTURE = $zoneFile
                $exe = (Get-Command $shell -CommandType Application).Path | Select-Object -First 1
                $child = Start-Process $exe -ArgumentList ("-NoLogo -NoProfile -NoExit -File `""+$driver+"`"") -WindowStyle Hidden -PassThru
                try {
                    if (!$child.WaitForExit(20000)) { $child.Kill(); throw "Interactive paste timed out in $shell." }
                    if ($child.ExitCode -ne 0) { throw "The acquisition controls failed in $shell (exit $($child.ExitCode))." }
                } finally { $child.Dispose() }
                if (![IO.File]::Exists($zoneFile + '.verified') -or [IO.File]::ReadAllText($zoneFile + '.verified') -cne 'canonical, rejected BOM, rejected newline') {
                    throw "Missing download zone controls in $shell."
                }
                Assert-InternetZone $zoneFile
                if ([IO.File]::ReadAllText($zoneFile) -cne 'original') {throw "The zone write altered the main data stream in $shell."}
                Write-Output "$shell preserved the file, wrote the exact Internet zone, and rejected extra BOM and newline bytes."
            }
        } finally {
            $env:OPENAI_CLI_PASTE_FIXTURE = $prior
            $env:OPENAI_CLI_ZONE_FIXTURE = $priorZone
        }
    }
    # Verify the public launcher's extraction checks without running fixtures.
    $public = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'test-windows-published-starter.ps1'),[ref]$tokens,[ref]$parseErrors)
    $expand = $public.Find({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -ceq 'Expand-AuthenticatedStarter'},$true)
    if ($parseErrors.Count -or !$expand) {throw 'Cannot locate the public installer verification.'}
    . ([scriptblock]::Create($expand.Extent.Text))
    $publishedZip = Join-Path $work 'published.zip'
    $source = Join-Path $work 'portable-fixture.exe'
    [IO.File]::WriteAllText($source,'inert fixture')
    $z = [IO.Compression.ZipFile]::Open($publishedZip,'Create')
    try {$null = [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($z,$source,'openai.exe')} finally {$z.Dispose()}
    $receipt = @{assetSize=[IO.FileInfo]::new($publishedZip).Length; archiveSha256=(Get-Sha $publishedZip); executableSha256=(Get-Sha $source)}
    $destination = Join-Path $work 'published-extracted'
    Assert-Rejected {Expand-AuthenticatedStarter $publishedZip $destination $receipt}
    $z = [IO.Compression.ZipFile]::Open($publishedZip,'Update')
    try {$null = [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($z,(Join-Path $PSScriptRoot '../Install.cmd'),'Install.cmd')} finally {$z.Dispose()}
    Assert-Rejected {Expand-AuthenticatedStarter $publishedZip $destination $receipt}
    $receipt.assetSize = [IO.FileInfo]::new($publishedZip).Length
    $receipt.archiveSha256 = Get-Sha $publishedZip
    Expand-AuthenticatedStarter $publishedZip $destination $receipt
    if ((Get-Sha (Join-Path $destination 'Install.cmd')) -cne (Get-Sha (Join-Path $PSScriptRoot '../Install.cmd'))) {throw 'Public launcher bytes changed.'}
    $receipt.executableSha256 = '0'*64
    Assert-Rejected {Expand-AuthenticatedStarter $publishedZip (Join-Path $work 'wrong-executable') $receipt}
    Write-Output 'Windows release selection, stamp, publisher, extraction, and rejection fixtures passed.'
} finally { [IO.Directory]::Delete($work,$true) }
