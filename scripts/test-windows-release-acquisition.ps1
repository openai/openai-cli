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
    $names = 'Test-ProtocolText','Test-PositiveInteger','Test-RecordFields','Test-ReleaseTime','Confirm-CurrentRelease','Set-InternetZone','Select-InstalledCLI','Get-Sha','Expand-Executable','Wait-ForPublication','Confirm-ReleaseOrder','Read-Release','Wait-ForSelectedRelease','Confirm-PublishedZIP','Invoke-SetupProcess','Test-Publication','Read-GoLength','Confirm-ReleaseExecutable'
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
    . (Join-Path $PSScriptRoot 'test-windows-download-zone.ps1')
    function Assert-InternetZone([string]$path) {
        if ((Get-WindowsDownloadZone $path) -ne 3) {throw 'Unexpected download zone bytes.'}
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
    [IO.File]::WriteAllText($src,@'
package main

import (
    "os"
    "path/filepath"
    "strconv"
)

func main() {
    exe, err := os.Executable()
    if err == nil {
        identity, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "identity.txt"))
        if err == nil {
            os.Stdout.Write(identity)
            return
        }
    }
    status, _ := strconv.Atoi(os.Getenv("OPENAI_FIXTURE_EXIT"))
    os.Exit(status)
}
'@)
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
    # Run the same real JSON types in Windows PowerShell and PowerShell 7.
    function Test-PublisherMetadata([string]$fixturePath) {
        $fixture = ConvertFrom-Json -InputObject ([IO.File]::ReadAllText($fixturePath))
        $repo = 'https://api.github.com/repos/openai/openai-cli'
        $workflow = $fixture.workflow
        function Get-PublicResource([string]$url) {
            if (![string]::Equals($url,"$repo/actions/runs/23/attempts/2",[StringComparison]::Ordinal)) { throw 'Unexpected publisher endpoint.' }
            return $record
        }
        function Assert-Decision([string]$json,[string]$expected) {
            $record = ConvertFrom-Json -InputObject $json
            $queue = New-Object System.Collections.Queue
            $verified = Test-Publication $fixture.item $fixture.selected $queue
            $actual = if ($verified) {'verified'} elseif ($queue.Count) {'pending'} else {'rejected'}
            if ($actual -cne $expected) { throw "Publisher metadata decision was $actual instead of $expected." }
        }
        $good = ConvertTo-Json -InputObject $fixture.run -Compress -Depth 5
        Assert-Decision $good 'verified'
        $pending = $good.Replace('"completed"','"in_progress"').Replace('"success"','null')
        Assert-Decision $pending 'pending'
        foreach ($id in 'true','"23"','23.0','[23]','0','-23','null') {
            Assert-Decision ($good.Replace('"id":23','"id":' + $id)) 'rejected'
        }
        foreach ($attempt in 'true','"2"','2.0','[2]','0','null') {
            Assert-Decision ($pending.Replace('"run_attempt":2','"run_attempt":' + $attempt)) 'rejected'
        }
        foreach ($conclusion in 'true','"success"','0','""','"succ\u0000ess"') {
            Assert-Decision ($pending.Replace('"conclusion":null','"conclusion":' + $conclusion)) 'rejected'
        }
        foreach ($conclusion in 'null','true','["success"]','"succ\u00adess"','"success\u0000"') {
            Assert-Decision ($good.Replace('"success"',$conclusion)) 'rejected'
        }
        $absent = ConvertFrom-Json -InputObject $pending
        $absent.PSObject.Properties.Remove('conclusion')
        Assert-Decision (ConvertTo-Json -Compress -Depth 5 -InputObject $absent) 'rejected'
        Assert-Decision ($pending.Replace('"conclusion":','"Conclusion":')) 'rejected'
        Assert-Decision ($good.Replace('"id":23','"ID":23')) 'rejected'
        Assert-Decision ($good.Replace('"repository":{"full_name":"openai/openai-cli"}','"repository":[{"full_name":"openai/openai-cli"}]')) 'rejected'
        Assert-Decision ($pending.Replace('in_progress','in_\u00adprogress')) 'rejected'
        Assert-Decision ($good.Replace('publish-release.yml','publish-\u00adrelease.yml')) 'rejected'
        Assert-Decision ($good.Replace('openai/openai-cli','openai/openai-\u0000cli')) 'rejected'
        $validItem = ConvertTo-Json -InputObject $fixture.item -Depth 10 -Compress
        foreach ($container in 'result','signature','statement','certificate','subject','digest') {
            $fixture.item = ConvertFrom-Json -InputObject $validItem
            $result = $fixture.item.verificationResult
            switch ($container) {
                'result'      { $fixture.item.verificationResult = @($result) }
                'signature'   { $result.signature = @($result.signature) }
                'statement'   { $result.statement = @($result.statement) }
                'certificate' { $result.signature.certificate = @($result.signature.certificate) }
                'subject'     { $result.statement.subject = $result.statement.subject[0] }
                'digest'      { $result.statement.subject[0].digest = @($result.statement.subject[0].digest) }
            }
            Assert-Decision $good 'rejected'
        }
        $fixture.item = ConvertFrom-Json -InputObject $validItem
        $fixture.item.verificationResult.statement.subject[0].name += [char]0x00ad
        Assert-Decision $good 'rejected'
    }
    $metadataFixture = Join-Path $work 'publisher.json'
    $golden = @{workflow=$workflow; run=$run; item=$item; selected=$selected}
    [IO.File]::WriteAllText($metadataFixture,(ConvertTo-Json -InputObject $golden -Depth 10 -Compress))
    Test-PublisherMetadata $metadataFixture
    & {
        $release = $release.Clone()
        foreach ($invalid in @($null,23,@{name=42},@{name="bad`0name"},@{name=''})) {
            $release.assets = @($asset,$invalid)
            Assert-Rejected { Read-Release 'releases/latest' }
        }
        foreach ($field in 'id','size','updated_at','digest') {
            $invalid = $asset.Clone()
            $invalid[$field] = $true
            $release.assets = @($invalid)
            Assert-Rejected { Read-Release 'releases/latest' }
        }
        $publication = @{clock=[Diagnostics.Stopwatch]::new(); polls=0; changes=0; tag=$Tag; id=11}
        function Start-Sleep { throw 'Unexpected immutable release wait.' }
        $lookalike = $asset.Clone()
        $lookalike.name += [char]0x00ad
        $release.immutable = $true
        $release.assets = @($lookalike)
        Assert-Rejected { Read-Release 'releases/latest' -WaitForZIP }
        if ($publication.polls) { throw 'An immutable release waited for a missing ZIP.' }
        $release.assets = @($asset)
        $release.immutable = 'false'
        Assert-Rejected { Read-Release 'releases/latest' }
    }
    & {
        $originalResource = ${function:Get-PublicResource}
        function Get-PublicResource([string]$url) {
            if ($url -ceq $badURL) { return (ConvertFrom-Json -InputObject $badJSON) }
            return & $originalResource $url
        }
        $commitObject = '{"type":"commit","sha":"' + $Commit + '"}'
        foreach ($badURL in "$repo/git/ref/tags/$Tag","$repo/git/tags/$('e'*40)") {
            foreach ($badJSON in ('{"Object":' + $commitObject + '}'),('{"object":[' + $commitObject + ']}'),('{"object":' + $commitObject.Replace('"type"','"Type"') + '}')) {
                Assert-Rejected { Read-Release 'releases/latest' }
            }
        }
    }
    & {
        $publication = @{clock=[Diagnostics.Stopwatch]::new(); polls=0}
        $counts = @{signature=0; publisher=0}
        function Start-Sleep { throw 'Unexpected wait for invalid metadata.' }
        function Get-PublicResource([string]$url) {
            if ($url -ceq "$repo/actions/runs/23/attempts/2") { $counts.publisher++; return $run }
            return (ConvertFrom-Json -InputObject $envelope)
        }
        function Invoke-SetupProcess { $counts.signature++; return $resultJSON }
        foreach ($envelope in '{"ATTESTATIONS":[]}','{"attestations":[{"Bundle":{}}]}','{"attestations":[{"bundle":[{}]}]}') {
            Assert-Rejected { Confirm-PublishedZIP $selected 'fixture-verifier' }
        }
        if ($publication.polls -or $counts.signature) { throw 'Malformed attestation metadata reached a wait or the verifier.' }
        $envelope = '{"attestations":[{"bundle":{}}]}'
        $justItem = ConvertTo-Json -InputObject $item -Depth 10 -Compress
        foreach ($resultJSON in $justItem,('[[' + $justItem + ']]')) {
            Assert-Rejected { Confirm-PublishedZIP $selected 'fixture-verifier' }
        }
        if ($publication.polls -or $counts.publisher -or $counts.signature -ne 2) { throw 'Malformed verifier result escaped its fixture.' }
    }
    foreach ($stage in 'attestation','publisher') {
        & {
            $originalResource = ${function:Get-PublicResource}
            $publication = @{clock=[Diagnostics.Stopwatch]::new(); polls=0; changes=0; tag=$Tag; id=11}
            $counts = @{wait=0; pinned=0; signature=0; latest=0}
            function Start-Sleep { $counts.wait++ }
            function Get-PublicResource([string]$url) {
                if ($url -ceq "$repo/releases/latest") { $counts.latest++; throw 'Pinned release switched to latest.' }
                if ($url -ceq "$repo/releases/11") {
                    $counts.pinned++
                    $replacement = $release.Clone()
                    $replacement.id = 12
                    return $replacement
                }
                if ($url -ceq "$repo/attestations/sha256:$digest") {
                    if ($stage -ceq 'attestation') { return @{attestations=@()} }
                    return @{attestations=@(@{bundle=@{fixture='bundle'}})}
                }
                if ($url -ceq "$repo/actions/runs/23/attempts/2") {
                    $publishing = $run.Clone()
                    $publishing.status = 'queued'
                    $publishing.conclusion = $null
                    return $publishing
                }
                return & $originalResource $url
            }
            function Invoke-SetupProcess {
                $counts.signature++
                return ConvertTo-Json -Compress -Depth 12 -InputObject @($item)
            }
            Assert-Rejected { Confirm-PublishedZIP $selected 'fixture-verifier' }
            $signatures = if ($stage -ceq 'attestation') {0} else {1}
            if ($counts.wait -ne 4 -or $counts.pinned -ne 1 -or $counts.latest -or $counts.signature -ne $signatures) {
                throw "Pinned release withdrawal escaped the $stage wait control."
            }
        }
    }
    # Drive the real publication waits with local metadata and a synthetic verifier.
    & {
        $ReleaseTag = ''
        $originalResource = ${function:Get-PublicResource}
        $publication = @{clock=[Diagnostics.Stopwatch]::new(); polls=0; changes=0; tag=''; id=0}
        $calls = @{sleep=0; signature=0}
        function Start-Sleep([int]$Milliseconds) {
            if ($Milliseconds -le 0 -or $Milliseconds -gt 90000) { throw 'Unexpected publication polling interval.' }
            $calls.sleep++
        }
        function Get-PublicResource([string]$url,[int]$limit,[string]$file,[switch]$AllowMissingAttestation) {
            if ($url -ceq "$repo/releases/latest" -and $calls.sleep -lt 1) {
                $missing = $release.Clone()
                $missing.assets = @()
                return $missing
            }
            if ($url -ceq "$repo/attestations/sha256:$digest") {
                if (!$AllowMissingAttestation) { throw 'Expected only the digest-specific absence exception.' }
                if ($calls.sleep -lt 2) { return @{attestations=@()} }
                return @{attestations=@(@{bundle=@{fixture='bundle'}})}
            }
            if ($url -ceq "$repo/actions/runs/23/attempts/2" -and $calls.sleep -lt 3) {
                $publishing = $run.Clone()
                $publishing.status = 'in_progress'
                $publishing.conclusion = $null
                return $publishing
            }
            return & $originalResource $url
        }
        function Invoke-SetupProcess([string]$path,[string]$arguments,[bool]$verifier) {
            if (!$verifier) { throw 'Unexpected enrollment from the publication check.' }
            $calls.signature++
            return ConvertTo-Json -Compress -Depth 12 -InputObject @($item)
        }
        $pending = Read-Release 'releases/latest' -WaitForZIP
        $newer = Confirm-PublishedZIP $pending 'fixture-verifier'
        if ($newer -or $calls.sleep -ne 3 -or $calls.signature -ne 1 -or $publication.polls -ne 3) {
            throw 'The release did not resume from each publication stage.'
        }
        $publication.polls = 20
        Assert-Rejected { Wait-ForPublication $Tag }
        if ($calls.sleep -ne 3) { throw 'The publication wait exceeded its shared budget.' }
    }
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
        [IO.File]::WriteAllText((Join-Path $work 'selection-paste.txt'),$blocks[0].Groups[1].Value)
        function Test-SelectedExecutable([string]$fixture) {
            $work = [IO.Path]::GetDirectoryName($fixture)
            $root = $fixture + '.paths'
            $bin = Join-Path $root 'OpenAI\CLI\bin'
            $similar = Join-Path $root ("Open"+[char]0xAD+"AI\CLI\bin")
            $portable = Join-Path $root 'extracted'
            $fake = Join-Path $work 'exit-fixture/openai.exe'
            foreach ($entry in @(@($bin,'expected'),@($similar,'shadow'),@($portable,'enrolled'))) {
                $null = [IO.Directory]::CreateDirectory($entry[0])
                [IO.File]::Copy($fake,(Join-Path $entry[0] 'openai.exe'))
                [IO.File]::WriteAllText((Join-Path $entry[0] 'identity.txt'),$entry[1])
            }
            function Assert-ShadowRejected([scriptblock]$select) {
                try { & $select | Out-Null } catch {
                    if ($_.Exception.Message -notmatch 'overrides openai') {throw}
                    return
                }
                throw 'A shadowing command passed executable selection.'
            }
            $guide = [scriptblock]::Create([IO.File]::ReadAllText((Join-Path $work 'selection-paste.txt')))
            $priorPath = $env:Path
            $localData = $env:LOCALAPPDATA
            Push-Location -LiteralPath $portable
            try {
                $env:LOCALAPPDATA = $root
                foreach ($select in $guide,{Select-InstalledCLI $root | Out-Null; openai --version}) {
                    $env:Path = "$similar;$priorPath"
                    if ((openai --version) -cne 'shadow') {throw 'Distinct executable fixture unavailable.'}
                    if ((& $select) -cne 'expected') {throw 'Selected a different executable.'}
                    $upper = $bin.ToUpperInvariant() + ';' + $priorPath
                    $env:Path = $upper
                    if ((& $select) -cne 'expected' -or ![string]::Equals($env:Path,$upper,[StringComparison]::Ordinal)) {
                        throw 'Changed an already-selected directory with different letter casing.'
                    }
                    $managed = Join-Path $bin 'openai.exe'
                    [IO.File]::Move($managed,$managed + '.saved')
                    try {
                        $env:Path = "$bin;$similar;$priorPath"
                        Assert-ShadowRejected $select
                    } finally {[IO.File]::Move($managed + '.saved',$managed)}
                    Set-Alias -Name openai -Value Get-Location -Scope Local
                    try {Assert-ShadowRejected $select} finally {Remove-Item -LiteralPath Alias:\openai}
                }
            } finally {
                Pop-Location
                $env:Path = $priorPath
                $env:LOCALAPPDATA = $localData
            }
        }
        $selector = (Get-Command Select-InstalledCLI -CommandType Function).ScriptBlock.Ast.Extent.Text
        $pathTest = (Get-Command Test-SelectedExecutable -CommandType Function).ScriptBlock.Ast.Extent.Text
        $zoneFunction = ($defs | Where-Object Name -ceq 'Set-InternetZone').Extent.Text
        $zoneReader = (Get-Command Get-WindowsDownloadZone -CommandType Function).ScriptBlock.Ast.Extent.Text
        $zoneAssertion = (Get-Command Assert-InternetZone -CommandType Function).ScriptBlock.Ast.Extent.Text
        $metadataChecks = ($defs | Where-Object Name -in 'Test-Publication','Test-ProtocolText','Test-PositiveInteger','Test-RecordFields').Extent.Text -join [Environment]::NewLine
        $metadataTest = (Get-Command Test-PublisherMetadata -CommandType Function).ScriptBlock.Ast.Extent.Text
        $driver = Join-Path $work 'console-paste.ps1'
        $driverBody = @'
if ([Console]::IsInputRedirected) { exit 71 }
try {
    $file = $env:OPENAI_CLI_ZONE_FIXTURE
    Test-PublisherMetadata (Join-Path ([IO.Path]::GetDirectoryName($file)) 'publisher.json')
    Set-InternetZone $file
    Assert-InternetZone $file
    $plain = [Text.Encoding]::ASCII.GetBytes("[ZoneTransfer]`r`nZoneId=3`r`n")
    $binary = if ($PSVersionTable.PSVersion.Major -le 5) {@{Encoding='Byte'}} else {@{AsByteStream=$true}}
    $restricted = [Text.Encoding]::ASCII.GetBytes("[ZoneTransfer]`r`nZoneId=4`r`n")
    Microsoft.PowerShell.Management\Set-Content -LiteralPath $file -Stream Zone.Identifier @binary -Value $restricted -ErrorAction Stop
    if ((Get-WindowsDownloadZone $file) -ne 4) {exit 74}
    foreach ($case in 'bom','newline','nul3','nul4') {
        $invalid = switch ($case) {
            'bom' { @(239,187,191) + $plain }
            'newline' { $plain + @(13,10) }
            'nul3' { [Text.Encoding]::ASCII.GetBytes("[ZoneTransfer]`r`nZone`0Id=3`r`n") }
            'nul4' { [Text.Encoding]::ASCII.GetBytes("[ZoneTransfer]`r`nZone`0Id=4`r`n") }
        }
        Microsoft.PowerShell.Management\Set-Content -LiteralPath $file -Stream Zone.Identifier @binary -Value ([byte[]]$invalid) -ErrorAction Stop
        if ((Get-WindowsDownloadZone $file) -ne 0) {exit 74}
    }
    $origin = $file + '.trusted'
    $copy = $file + '.private'
    [IO.File]::WriteAllText($origin,'trusted file')
    [IO.File]::WriteAllBytes($copy,[IO.File]::ReadAllBytes($origin))
    $strong = "[ZoneTransfer]`r`nZoneId=4`r`nHostUrl=https://fixture.invalid/private`r`n"
    Microsoft.PowerShell.Management\Set-Content -LiteralPath $origin -Stream Zone.Identifier -Value $strong -Encoding ASCII -NoNewline -ErrorAction Stop
    Set-InternetZone $copy $origin
    if ((Get-WindowsDownloadZone $copy) -ne 4) {exit 74}
    Set-InternetZone $origin
    Set-InternetZone $copy $origin
    if ((Get-WindowsDownloadZone $copy) -ne 4) {exit 74}
    $other = $file + '.another-copy'
    [IO.File]::WriteAllBytes($other,[IO.File]::ReadAllBytes($origin))
    $ordinary = "[ZoneTransfer]`r`nZoneId=3`r`nHostUrl=https://fixture.invalid/private`r`n"
    Microsoft.PowerShell.Management\Set-Content -LiteralPath $origin -Stream Zone.Identifier -Value $ordinary -Encoding ASCII -NoNewline -ErrorAction Stop
    Set-InternetZone $other $origin
    Assert-InternetZone $other
    $ambiguous = $ordinary + "ZoneId=4`r`n"
    Microsoft.PowerShell.Management\Set-Content -LiteralPath $origin -Stream Zone.Identifier -Value $ambiguous -Encoding ASCII -NoNewline -ErrorAction Stop
    Set-InternetZone $other $origin
    if ((Get-WindowsDownloadZone $other) -ne 4) {exit 74}
    Set-InternetZone $file
    Assert-InternetZone $file
    Test-SelectedExecutable $file
    [IO.File]::WriteAllText($file + '.verified','exact markers, rejected corrupt markers, preserved restricted copy, selected managed executable')
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
        [IO.File]::WriteAllText($driver,(($zoneFunction,$zoneReader,$zoneAssertion,$selector,$pathTest,$metadataChecks,$metadataTest,$driverBody) -join [Environment]::NewLine))
        $prior = $env:OPENAI_CLI_PASTE_FIXTURE
        $priorZone = $env:OPENAI_CLI_ZONE_FIXTURE
        try {
            $env:OPENAI_CLI_PASTE_FIXTURE = $recipe
            foreach ($shell in 'powershell.exe','pwsh.exe') {
                $zoneFile = Join-Path $work ("download [1] $shell")
                [IO.File]::WriteAllText($zoneFile,'original')
                $originalHash = (Get-FileHash -LiteralPath $zoneFile).Hash
                $env:OPENAI_CLI_ZONE_FIXTURE = $zoneFile
                $exe = (Get-Command $shell -CommandType Application).Path | Select-Object -First 1
                $child = Start-Process $exe -ArgumentList ("-NoLogo -NoProfile -NoExit -File `""+$driver+"`"") -WindowStyle Hidden -PassThru
                try {
                    if (!$child.WaitForExit(20000)) { $child.Kill(); throw "Interactive paste timed out in $shell." }
                    if ($child.ExitCode -ne 0) { throw "The acquisition controls failed in $shell (exit $($child.ExitCode))." }
                } finally { $child.Dispose() }
                if (![IO.File]::Exists($zoneFile + '.verified') -or [IO.File]::ReadAllText($zoneFile + '.verified') -cne 'exact markers, rejected corrupt markers, preserved restricted copy, selected managed executable') {
                    throw "Missing download zone controls in $shell."
                }
                Assert-InternetZone $zoneFile
                if ((Get-FileHash -LiteralPath $zoneFile).Hash -cne $originalHash) {throw "The zone write altered the main data stream in $shell."}
                Write-Output "$shell preserved the file, wrote the exact Internet zone, preserved a restricted verifier copy, and rejected BOM, newline and embedded NUL bytes."
                Write-Output "$shell selected the expected executable and rejected shadowing commands."
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
