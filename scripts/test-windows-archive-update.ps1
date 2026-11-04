# Run only on a disposable, native Windows Actions runner.
[CmdletBinding(DefaultParameterSetName='Fixture')]
param(
    [Parameter(ParameterSetName='Published', Mandatory)]
    [ValidatePattern('(?-i)\Av(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z')]
    [string]$StarterTag,
    [Parameter(ParameterSetName='Published', Mandatory)]
    [ValidatePattern('(?-i)\Av(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z')]
    [string]$SuccessorTag,
    [ValidateSet('powershell.exe','pwsh.exe')][string] $PublicClient = 'powershell.exe',
    [Parameter(ParameterSetName='Published', Mandatory)][ValidateSet('PowerShell','Install.cmd')][string] $PublicRoute,
    [switch] $PrivateVerifier
)
$ErrorActionPreference = 'Stop'
if ($env:RUNNER_OS -ne 'Windows' -or $env:GITHUB_ACTIONS -ne 'true') {
    throw 'Windows archive qualification requires a disposable Windows runner.'
}
$published = $PSCmdlet.ParameterSetName -eq 'Published'
if ($published -and [version]($StarterTag.Substring(1)) -ge [version]($SuccessorTag.Substring(1))) {
    throw 'The successor tag must be newer than the starter tag.'
}
$account = 'OpenAIArchiveTest'
$machineAccount = 'OpenAIPathTest'
if ((Get-LocalUser -Name $account -ErrorAction SilentlyContinue) -or (!$published -and (Get-LocalUser -Name $machineAccount -ErrorAction SilentlyContinue))) {
    throw 'The disposable fixture account already exists.'
}
$root = Join-Path $env:ProgramData ('openai-cli-archive-test-' + [guid]::NewGuid())
$goarch = (go env GOARCH).Trim()
$versionLine = Get-Content -LiteralPath 'internal/app/version.go' -Raw
if ($versionLine -notmatch 'const Version = "([0-9]+\.[0-9]+\.[0-9]+)"' -or $env:GITHUB_SHA -notmatch '^[0-9a-f]{40}$') {
    throw 'Missing the candidate version or source commit.'
}
$version = [regex]::Match($versionLine, 'const Version = "([^"]+)"').Groups[1].Value
$stamp = 'github.com/openai/openai-cli/internal/updatenotice.'
$ldflags = "-s -w -X ${stamp}releaseVersion=$version -X ${stamp}releaseCommit=$env:GITHUB_SHA"
$secret = ConvertTo-SecureString ('T4!' + [guid]::NewGuid().ToString('N')) -AsPlainText -Force
$machineUser = $null
$machineSecret = $null
$user = New-LocalUser -Name $account -Password $secret -UserMayNotChangePassword
try {
    New-Item -ItemType Directory -Path $root | Out-Null
    $sid = $user.Sid.Value
    icacls $root /inheritance:r /grant:r '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-18:(OI)(CI)F' "*${sid}:(OI)(CI)RX" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not protect the fixture directory.' }

    if (!$published) {
        $machineSecret = ConvertTo-SecureString ('T4!' + [guid]::NewGuid().ToString('N')) -AsPlainText -Force
        $machineUser = New-LocalUser -Name $machineAccount -Password $machineSecret -UserMayNotChangePassword
        $machineCredential = [PSCredential]::new(".\$machineAccount",$machineSecret)
        $machineSid = $machineUser.Sid.Value
        icacls $root /grant "*${machineSid}:(OI)(CI)RX" | Out-Null
        if ($LASTEXITCODE -ne 0) {throw 'Could not grant the independent account read-only fixture access.'}
    }

    go test -c -o "$root/archive.test.exe" ./cmd/openai
    if ($LASTEXITCODE -ne 0) { throw 'Could not compile the native entrypoint tests.' }
    Copy-Item -LiteralPath 'scripts/test-windows-console-paste.ps1' -Destination $root
    if ($published) {
        Copy-Item -LiteralPath 'scripts/install-windows-release.ps1' -Destination "$root/acquire.ps1"
        Copy-Item -LiteralPath 'scripts/test-windows-published-starter.ps1' -Destination $root
        @{starter=$StarterTag; successor=$SuccessorTag; client=$PublicClient; route=$PublicRoute; privateVerifier=[bool]$PrivateVerifier} | ConvertTo-Json | Set-Content -LiteralPath "$root/tags.json"
    } else {
        # The hosted runner owns build caches. The standard user receives only
        # read/execute access to the compiled probe, candidate, and ZIP helper.
        go build -ldflags $ldflags -o "$root/openai.exe" ./cmd/openai/main.go
        if ($LASTEXITCODE -ne 0) { throw 'Could not build the release-stamped candidate.' }
        & ./scripts/test-windows-release-acquisition.ps1 -Candidate "$root/openai.exe" -Tag "v$version" -Commit $env:GITHUB_SHA -Arch $goarch
        $olderVersion = Join-Path $root 'older-version.go'
        ($versionLine.Replace('"' + $version + '"', '"0.0.1"')) | Set-Content -LiteralPath $olderVersion -Encoding utf8NoBOM
        $overlay = Join-Path $root 'older-version-overlay.json'
        $replace = @{}
        $replace[(Resolve-Path 'internal/app/version.go').Path] = $olderVersion
        @{Replace = $replace} | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath $overlay -Encoding utf8NoBOM
        $oldFlags = "-s -w -X ${stamp}releaseVersion=0.0.1 -X ${stamp}releaseCommit=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        go build -overlay $overlay -ldflags $oldFlags -o "$root/older-openai.exe" ./cmd/openai/main.go
        if ($LASTEXITCODE -ne 0) { throw 'Could not build the older release-stamped fixture.' }
        # Keep the real public layout, including the manual and completions.
        # Change only the two entries supplied by this feature.
        $digests = @{amd64='3d6410fe897df350b6cd60eabc1ef4be896711fad040d3d7959ade3a3400fe2e'; arm64='a9171c78958eeaffd941c4d8fff49c7f9864f57e1568f0951b77d8f5cfcb3540'}
        $baseline = Join-Path $root 'before.zip'
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "https://github.com/openai/openai-cli/releases/download/v1.48.1/openai_1.48.1_windows_$goarch.zip" -OutFile $baseline
        } catch {throw 'Could not download the public Windows baseline archive.'}
        if ((Get-FileHash -LiteralPath $baseline).Hash.ToLowerInvariant() -cne $digests[$goarch]) {throw 'Public Windows baseline archive checksum mismatch.'}
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $candidateZip = Join-Path $root "openai_$($version)_windows_$goarch.zip"
        Copy-Item -LiteralPath $baseline -Destination $candidateZip
        $zip = [IO.Compression.ZipFile]::Open($candidateZip,'Update')
        try {
            $old = $zip.GetEntry('openai.exe')
            if (!$old -or $zip.GetEntry('Install.cmd')) {throw 'Unexpected baseline Windows archive layout.'}
            $legacy = Join-Path $root 'baseline-openai.exe'
            [IO.Compression.ZipFileExtensions]::ExtractToFile($old,$legacy)
            Microsoft.PowerShell.Management\Set-Content -LiteralPath $legacy -Stream Zone.Identifier -Encoding ASCII -NoNewline -Value "[ZoneTransfer]`r`nZoneId=3`r`n" -ErrorAction Stop
            $old.Delete()
            $null = [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,"$root/openai.exe",'openai.exe')
            $null = [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,(Resolve-Path 'Install.cmd').Path,'Install.cmd')
        } finally {$zip.Dispose()}
        $extracted = Join-Path $root 'extracted'
        [IO.Compression.ZipFile]::ExtractToDirectory($candidateZip,$extracted)
        Microsoft.PowerShell.Management\Set-Content -LiteralPath (Join-Path $extracted 'openai.exe') -Stream Zone.Identifier -Encoding ASCII -NoNewline -Value "[ZoneTransfer]`r`nZoneId=3`r`n" -ErrorAction Stop
        Copy-Item -LiteralPath 'docs/installation.md' -Destination "$extracted/installation.md"
        Microsoft.PowerShell.Management\Set-Content -LiteralPath $candidateZip -Stream Zone.Identifier -Encoding ASCII -NoNewline -Value "[ZoneTransfer]`r`nZoneId=3`r`n" -ErrorAction Stop
        Set-Content -LiteralPath "$root/fixture-zip.txt" -Value ([IO.Path]::GetFileName($candidateZip))
        Set-Content -LiteralPath "$root/fixture-tag.txt" -Value "v$version"
        $archiveTests = '^(TestWindowsArchive|TestPreparedImageAfterTrust$)'
        go test -count=1 -v ./internal/updatenotice -run $archiveTests
        if ($LASTEXITCODE -ne 0) { throw 'Native archive security or executable-lock tests failed.' }
        $env:OPENAI_CLI_NATIVE_SHELL_BINARY = Join-Path $root 'openai.exe'
        $env:OPENAI_CLI_NATIVE_SHELL_SHA256 = (Get-FileHash -Path $env:OPENAI_CLI_NATIVE_SHELL_BINARY -Algorithm SHA256).Hash.ToLowerInvariant()
        & "$root/archive.test.exe" '-test.run=^TestMainWindowsArchive(InstallHelp|ConsoleCapture)$' '-test.v'
        $helpResult = $LASTEXITCODE
        Remove-Item Env:OPENAI_CLI_NATIVE_SHELL_BINARY, Env:OPENAI_CLI_NATIVE_SHELL_SHA256
        $LASTEXITCODE = $helpResult
        if ($LASTEXITCODE -ne 0) { throw 'Native install command help, error handling, or console capture failed.' }
        if ($goarch -eq 'amd64') {
            $priorArch = $env:GOARCH
            try {
                $env:GOARCH = '386'
                go test -c -o "$root/archive-386.test.exe" ./internal/updatenotice
                if ($LASTEXITCODE -ne 0) { throw 'Could not compile the Windows x86 tests.' }
                & "$root/archive-386.test.exe" "-test.run=$archiveTests" '-test.v'
                if ($LASTEXITCODE -ne 0) { throw 'Windows x86 compatibility execution failed.' }
            } finally {
                $env:GOARCH = $priorArch
            }
        }
    }

    $results = New-Item -ItemType Directory -Path "$root/results"
    icacls $results.FullName /grant:r "*${sid}:(OI)(CI)M" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not allow the fixture account to write results.' }
    @'
$ErrorActionPreference = 'Stop'
$env:USERPROFILE = [Environment]::GetFolderPath('UserProfile')
$env:LOCALAPPDATA = [Environment]::GetFolderPath('LocalApplicationData')
$env:APPDATA = [Environment]::GetFolderPath('ApplicationData')
$env:TEMP = Join-Path $env:LOCALAPPDATA 'Temp'
$env:TMP = $env:TEMP
[IO.Directory]::CreateDirectory($env:TEMP) | Out-Null
$env:PATH = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')
$env:GITHUB_ACTIONS = 'true'
if (Test-Path -LiteralPath "$PSScriptRoot/tags.json") {
    $tags = Get-Content -LiteralPath "$PSScriptRoot/tags.json" -Raw | ConvertFrom-Json
    $env:OPENAI_CLI_WINDOWS_PUBLISHED_VERIFY = '1'
    $env:OPENAI_WINDOWS_STARTER_TAG = $tags.starter
    $env:OPENAI_WINDOWS_SUCCESSOR_TAG = $tags.successor
    $env:OPENAI_WINDOWS_PUBLIC_RECEIPT = "$PSScriptRoot/results/observed.json"
    $target = Join-Path $env:LOCALAPPDATA 'OpenAI\CLI\bin\openai.exe'
    if (Test-Path -LiteralPath $target) { throw 'The verification account already contains an installation.' }
    $env:OPENAI_WINDOWS_PUBLIC_STARTER_SHA = & "$PSScriptRoot/test-windows-published-starter.ps1" -Tag $tags.starter -Client $tags.client -Route $tags.route -PrivateVerifier:([bool]$tags.privateVerifier)
    if ($tags.privateVerifier) { $env:OPENAI_WINDOWS_PRIVATE_VERIFIER='1' }
    & "$PSScriptRoot/archive.test.exe" '-test.run=^TestMainWindowsArchivePublishedUpgrade$' '-test.v' *> "$PSScriptRoot/results/test.log"
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    # Verify the successor independently after the observed upgrade.
    $verified = & "$PSScriptRoot/acquire.ps1" -ReleaseTag $tags.successor -VerifyOnly | ConvertFrom-Json
    $observed = Get-Content -LiteralPath $env:OPENAI_WINDOWS_PUBLIC_RECEIPT -Raw | ConvertFrom-Json
    if ($verified.tag -cne $observed.Successor -or $verified.commit -cne $observed.Commit -or
        $verified.executableSha256 -cne $observed.ExecutableSHA256 -or $verified.archiveSha256 -cne $observed.ArchiveSHA256) {
        throw 'The observed successor differs from the independently authenticated public release.'
    }
    $observed | Add-Member -NotePropertyName InstallRoute -NotePropertyValue $tags.route
    $observed | Add-Member -NotePropertyName PrivateVerifier -NotePropertyValue ([bool]$tags.privateVerifier)
    $observed | ConvertTo-Json -Compress | Add-Content -LiteralPath "$PSScriptRoot/results/test.log"
    exit 0
}
$env:OPENAI_CLI_WINDOWS_ARCHIVE_FIXTURE = '1'
$env:OPENAI_CLI_WINDOWS_ARCHIVE_OLDER_BINARY = Join-Path $PSScriptRoot 'older-openai.exe'
$env:OPENAI_CLI_WINDOWS_ARCHIVE_BASELINE = Join-Path $PSScriptRoot 'baseline-openai.exe'
$env:OPENAI_CLI_WINDOWS_ARCHIVE_BINARY = Join-Path $PSScriptRoot 'extracted/openai.exe'
$env:OPENAI_CLI_WINDOWS_ARCHIVE_SCRIPT = Join-Path $PSScriptRoot 'extracted/Install.cmd'
if (Test-Path -LiteralPath "$PSScriptRoot/machine-conflict.txt") {
    $env:OPENAI_CLI_WINDOWS_ARCHIVE_CONFLICT = (Get-Content -LiteralPath "$PSScriptRoot/machine-conflict.txt" -Raw).Trim()
    & "$PSScriptRoot/archive.test.exe" '-test.run=^TestMainWindowsArchiveMachinePathConflict$' '-test.v' *> "$PSScriptRoot/machine-results/test.log"
    exit $LASTEXITCODE
}
& "$PSScriptRoot/archive.test.exe" '-test.run=^TestMainWindowsArchiveOrdinarySetup$' '-test.v' *> "$PSScriptRoot/results/test.log"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$fixtureTag = (Get-Content -LiteralPath "$PSScriptRoot/fixture-tag.txt" -Raw).Trim()
$guide = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'extracted/installation.md'))
$section = [regex]::Match($guide, '(?ms)^## Windows\r?\n(.*?)(?=^## |\z)').Groups[1].Value
$blocks = [regex]::Matches($section, '(?ms)^```powershell\r?\n(.*?)^```\r?$')
if ($blocks.Count -ne 1) {throw 'Cannot identify the documented extracted-ZIP paste.'}
$text = [IO.Path]::GetTempFileName()
$early = [IO.Path]::GetTempFileName()
$nonzero = [IO.Path]::GetTempFileName()
$handled = [IO.Path]::GetTempFileName()
$thrown = [IO.Path]::GetTempFileName()
$failed = $false
$log = "$PSScriptRoot/results/test.log"
$downloads = Join-Path $env:USERPROFILE 'Downloads'
$null = [IO.Directory]::CreateDirectory($downloads)
$work = Join-Path $downloads ('OpenAI CLI [' + [guid]::NewGuid() + ']')
# Explorer inherits the user's normal Downloads permissions during extraction.
$ownerSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
function Read-ArchiveZone([string]$path) {
    $mode = if ($PSVersionTable.PSVersion.Major -le 5) {@{Encoding='Byte'; ReadCount=0}} else {@{AsByteStream=$true; Raw=$true}}
    $bytes = [byte[]](Microsoft.PowerShell.Management\Get-Content -LiteralPath $path -Stream Zone.Identifier @mode -ErrorAction Stop)
    return [Text.Encoding]::ASCII.GetString($bytes)
}
function Write-ZipFailure([string]$step, [Management.Automation.ErrorRecord]$failure) {
    $exception = $failure.Exception
    $cause = $exception.GetBaseException()
    Add-Content -LiteralPath $log -Value ("$step category=$($failure.CategoryInfo.Category) " +
        "exception=$($exception.GetType().Name) hresult=$($exception.HResult) cause=$($cause.GetType().Name) causeHresult=$($cause.HResult)")
}
$extractionStage = 'create-download-directory'
$created = $false
try {
    # Null security attributes inherit Downloads permissions. CreateDirectoryW
    # fails atomically on collision, so only its winning caller owns cleanup.
    Add-Type -Namespace OpenAIQualification -Name NativeDirectory -MemberDefinition '[DllImport("kernel32.dll", CharSet=CharSet.Unicode, ExactSpelling=true)] [return: MarshalAs(UnmanagedType.Bool)] public static extern bool CreateDirectoryW(string path, IntPtr securityAttributes);'
    $created = [OpenAIQualification.NativeDirectory]::CreateDirectoryW($work,[IntPtr]::Zero)
    if (!$created) {throw 'Could not claim the new Downloads directory.'}

    # A second caller must leave the first caller's nonempty directory intact.
    $sentinel = Join-Path $work 'collision-control'
    [IO.File]::WriteAllText($sentinel,'retain')
    if ([OpenAIQualification.NativeDirectory]::CreateDirectoryW($work,[IntPtr]::Zero) -or [IO.File]::ReadAllText($sentinel) -cne 'retain') {
        throw 'An existing Downloads directory passed the exclusive creation check.'
    }
    [IO.File]::Delete($sentinel)
    $extractionStage = 'read-zip-name'
    $name = (Get-Content -LiteralPath "$PSScriptRoot/fixture-zip.txt" -Raw).Trim()
    $versionPattern = [regex]::Escape($fixtureTag.Substring(1))
    if ($name -cnotmatch "^openai_${versionPattern}_windows_(amd64|arm64)\.zip\z") {throw 'Unexpected candidate archive name.'}
    $protectedZip = Join-Path $PSScriptRoot $name
    $localZip = Join-Path $work $name
    $extractionStage = 'inspect-zip-source'
    $source = Get-Item -LiteralPath $protectedZip -ErrorAction Stop
    if ($source.PSIsContainer -or $source.Attributes -band [IO.FileAttributes]::ReparsePoint -or $source.Length -le 0) {
        throw 'Expected a nonempty regular archive.'
    }
    $extractionStage = 'read-zip-owner'
    $owner = (Get-Acl -LiteralPath $protectedZip).GetOwner([Security.Principal.SecurityIdentifier]).Value
    $ownerClass = switch ($owner) {
        $ownerSid.Value {'standard-user'}
        'S-1-5-18'      {'system'}
        'S-1-5-32-544'  {'administrators'}
        default        {'other'}
    }
    Add-Content -LiteralPath $log -Value "Candidate ZIP name matched; source file present; owner=$ownerClass."
    $extractionStage = 'copy-zip'
    [IO.File]::Copy($protectedZip,$localZip)
    $extractionStage = 'hash-zip-source'
    $zipHash = (Get-FileHash -LiteralPath $protectedZip).Hash
    $extractionStage = 'hash-zip-copy'
    if ((Get-FileHash -LiteralPath $localZip).Hash -cne $zipHash) {throw 'The account received unexpected ZIP bytes.'}
    $extractionStage = 'read-source-zone'
    $zone = Read-ArchiveZone $protectedZip
    if ($zone -cne "[ZoneTransfer]`r`nZoneId=3`r`n") {throw 'The candidate ZIP lacks its Internet zone.'}
    $extracted = Join-Path $work 'Extracted'
    $extractionStage = 'extract-zip'
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [IO.Compression.ZipFile]::ExtractToDirectory($localZip,$extracted)
    $entry = Join-Path $extracted 'openai.exe'
    # ZipFile does not propagate Explorer's download mark. Preserve it explicitly.
    $zoneFiles = [ordered]@{zip=$localZip; cli=$entry; launcher=(Join-Path $extracted 'Install.cmd')}
    foreach ($item in $zoneFiles.GetEnumerator()) {
        $extractionStage = 'write-zone-' + $item.Key
        Microsoft.PowerShell.Management\Set-Content -LiteralPath $item.Value -Stream Zone.Identifier -Encoding ASCII -NoNewline -Value $zone -ErrorAction Stop
        $extractionStage = 'read-zone-' + $item.Key
        if ((Read-ArchiveZone $item.Value) -cne $zone) {throw 'The account lost the ZIP download zone.'}
    }
    $extractionStage = 'hash-candidate-cli'
    $expectedSha = (Get-FileHash -LiteralPath "$PSScriptRoot/openai.exe").Hash
    $extractionStage = 'hash-extracted-cli'
    if ((Get-FileHash -LiteralPath $entry).Hash -cne $expectedSha) {throw 'The extracted CLI differs from the candidate.'}
    $extractionStage = 'verify-extracted-owner'
    if ((Get-Acl -LiteralPath $entry).GetOwner([Security.Principal.SecurityIdentifier]).Value -cne $ownerSid.Value) {
        throw 'The extracted CLI belongs to another account.'
    }
    Add-Content -LiteralPath $log -Value 'Standard-user Downloads extraction verified the candidate bytes, owner and Internet zone.'
    [IO.File]::WriteAllText($early,'exit 0')
    [IO.File]::WriteAllText($nonzero,'exit 17')
    $selectManaged = '$env:Path = [IO.Path]::GetDirectoryName($env:OPENAI_WINDOWS_MANAGED_EXE) + [IO.Path]::PathSeparator + $env:Path; '
    [IO.File]::WriteAllText($handled,$selectManaged + 'try {throw "handled fixture"} catch {}; openai --version')
    [IO.File]::WriteAllText($thrown,$selectManaged + 'throw "unhandled fixture"')
    [IO.File]::WriteAllText($text,$blocks[0].Groups[1].Value,[Text.UTF8Encoding]::new($false))
    $extractionStage = 'run-clients'
    Push-Location -LiteralPath $extracted
    try {
        $managed = Join-Path $env:LOCALAPPDATA 'OpenAI\CLI\bin\openai.exe'
        foreach ($client in 'powershell.exe','pwsh.exe') {
            try {$shell = (Get-Command $client -CommandType Application -ErrorAction Stop | Select-Object -First 1).Path}
            catch {Add-Content -LiteralPath $log -Value "$client stage=client-discovery failed"; $failed=$true; continue}
            $params = @{Client=$shell; ExpectedSourceSha=$expectedSha; ExpectedExe=$managed; ExpectedVersion=('openai version '+$fixtureTag.Substring(1)); EvidenceLog=$log}
            $installed = $false
            foreach ($probe in 'Exit0','Exit17','Paste','Handled','Thrown') {
                if ($probe -in 'Handled','Thrown' -and !$installed) {continue}
                $source = switch ($probe) { 'Exit0' {$early}; 'Exit17' {$nonzero}; 'Paste' {$text}; 'Handled' {$handled}; 'Thrown' {$thrown} }
                $clientStage = 'driver'
                try {
                    & "$PSScriptRoot/test-windows-console-paste.ps1" @params -Probe $probe -TextPath $source
                    if ($probe -eq 'Paste') {
                        $clientStage = 'hash-installed-cli'
                        $bytesMatch = (Get-FileHash -LiteralPath $managed).Hash -ceq $expectedSha
                        $clientStage = 'verify-installed-zone'
                        $zoneKept = (Read-ArchiveZone $managed) -cin $zone,"[ZoneTransfer]`r`nZoneId=4`r`n"
                        Add-Content -LiteralPath $log -Value "$client installedBytesMatch=$bytesMatch installedZoneKept=$zoneKept"
                        if (!$bytesMatch -or !$zoneKept) {throw 'The documented paste did not preserve the enrolled image.'}
                        $installed = $true
                        Add-Content -LiteralPath $log -Value "$client Restricted paste verified the managed executable and version."
                    }
                } catch {
                    Write-ZipFailure "$client $probe stage=$clientStage" $_
                    $failed = $true
                }
            }
        }
    } finally {Pop-Location}
} catch {
    Write-ZipFailure "Standard-user ZIP stage=$extractionStage" $_
    throw 'The ordinary user could not qualify the extracted Windows ZIP.'
} finally {
    foreach ($file in $text,$early,$nonzero,$handled,$thrown) {[IO.File]::Delete($file)}
    if ($created) {[IO.Directory]::Delete($work,$true)}
}
if ($failed) {exit 1}
exit 0
'@ | Set-Content -LiteralPath "$root/ordinary.ps1" -Encoding utf8
    $credential = New-Object System.Management.Automation.PSCredential(".\$account", $secret)
    $shell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    function Invoke-StandardQualification([string]$expected, [PSCredential]$runAs = $credential) {
        $logPath = if ($expected -eq 'TestMainWindowsArchiveMachinePathConflict') {"$root/machine-results/test.log"} else {"$root/results/test.log"}
        $child = Start-Process -FilePath $shell -ArgumentList "-NoLogo -NoProfile -NonInteractive -File `"$root\ordinary.ps1`"" -WorkingDirectory $root -Credential $runAs -LoadUserProfile -UseNewEnvironment -Wait -PassThru
        # This independent record survives a failed published enrollment.
        if (Test-Path -LiteralPath "$root/results/paste.log") {Get-Content -LiteralPath "$root/results/paste.log"}
        if (!(Test-Path -LiteralPath $logPath)) {throw "The standard account failed before recording CLI evidence (exit $($child.ExitCode))."}
        $log = Get-Content -LiteralPath $logPath -Raw
        Write-Output $log
        if ($child.ExitCode -ne 0 -or !$log.Contains('--- PASS: ' + $expected)) {
            throw 'The standard-user entrypoint qualification did not pass.'
        }
        if ($expected -ceq 'TestMainWindowsArchiveOrdinarySetup') {
            foreach ($client in 'powershell.exe','pwsh.exe') {
                $marker = "$client Restricted paste verified the managed executable and version."
                if (@(($log -split '\r?\n') -ceq $marker).Count -ne 1) {throw "Missing $client documented paste verification."}
            }
        }
    }
    if ($published) {
        Invoke-StandardQualification 'TestMainWindowsArchivePublishedUpgrade'
    } else {
        $ordinaryFailed = $false
        try {Invoke-StandardQualification 'TestMainWindowsArchiveOrdinarySetup'} catch {
            $ordinaryFailed = $true
            Write-Output 'The ordinary setup qualification failed. Checking machine PATH in a separate account.'
        }
        $machineResults = New-Item -ItemType Directory -Path "$root/machine-results"
        icacls $machineResults.FullName /grant:r "*${machineSid}:(OI)(CI)M" | Out-Null
        if ($LASTEXITCODE -ne 0) {throw 'Could not grant the independent account access to its results.'}
        # Place one inert fixture beside an installed CI tool. Never modify
        # machine PATH, pre-existing programs, or Windows system directories.
        $originalMachinePath = [Environment]::GetEnvironmentVariable('Path','Machine')
        $machineEntries = @([Environment]::ExpandEnvironmentVariables($originalMachinePath) -split ';' | ForEach-Object {
            $entry = $_.Trim()
            if ($entry -match '^"([^"]*)"$') {$entry = $Matches[1]}
            $entry.TrimEnd('\','/')
        })
        $directories = foreach ($tool in 'gh.exe','git.exe','pwsh.exe') {
            $cmd = Get-Command -CommandType Application -Name $tool -ErrorAction SilentlyContinue | Select-Object -First 1
            if ($cmd) { [IO.Path]::GetDirectoryName($cmd.Path) }
        }
        $toolDir = $directories | Where-Object {
            $machineEntries -contains $_ -and ($_.StartsWith($env:ProgramFiles + '\',[StringComparison]::OrdinalIgnoreCase) -or $_ -ieq "$env:ProgramData\chocolatey\bin")
        } | Select-Object -First 1
        if (!$toolDir) { throw 'Missing a disposable hosted tool directory on machine PATH.' }
        $conflict = Join-Path $toolDir 'openai.exe'
        $held = [IO.File]::Open($conflict, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
        try {
            $foreign = [Text.Encoding]::ASCII.GetBytes('inert machine PATH fixture')
            $held.Write($foreign, 0, $foreign.Length)
            $held.Flush()
            Set-Content -LiteralPath "$root/machine-conflict.txt" -Value $conflict
            Invoke-StandardQualification 'TestMainWindowsArchiveMachinePathConflict' $machineCredential
            if ([Environment]::GetEnvironmentVariable('Path','Machine') -cne $originalMachinePath) { throw 'Machine PATH changed during enrollment.' }
        } finally {
            $held.Dispose()
            [IO.File]::Delete($conflict)
        }
        if ($ordinaryFailed) {throw 'Machine PATH qualification passed. Ordinary setup qualification failed.'}
    }
} finally {
    try {
        if ($machineUser) {Remove-LocalUser -SID $machineUser.Sid}
    } finally {
        if ($machineSecret) {$machineSecret.Dispose()}
        try {Remove-LocalUser -SID $user.Sid} finally {
            $secret.Dispose()
            if (Test-Path -LiteralPath $root) {Remove-Item -LiteralPath $root -Force -Recurse}
        }
    }
}
