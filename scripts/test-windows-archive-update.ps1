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
if (Get-LocalUser -Name $account -ErrorAction SilentlyContinue) {
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
$user = New-LocalUser -Name $account -Password $secret -UserMayNotChangePassword
try {
    New-Item -ItemType Directory -Path $root | Out-Null
    $sid = $user.Sid.Value
    icacls $root /inheritance:r /grant:r '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-18:(OI)(CI)F' "*${sid}:(OI)(CI)RX" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not protect the fixture directory.' }

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
            [IO.File]::WriteAllText($legacy+':Zone.Identifier',"[ZoneTransfer]`r`nZoneId=3`r`n",[Text.Encoding]::ASCII)
            $old.Delete()
            $null = [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,"$root/openai.exe",'openai.exe')
            $null = [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,(Resolve-Path 'Install.cmd').Path,'Install.cmd')
        } finally {$zip.Dispose()}
        $extracted = Join-Path $root 'extracted'
        [IO.Compression.ZipFile]::ExtractToDirectory($candidateZip,$extracted)
        [IO.File]::WriteAllText((Join-Path $extracted 'openai.exe')+':Zone.Identifier',"[ZoneTransfer]`r`nZoneId=3`r`n",[Text.Encoding]::ASCII)
        Copy-Item -LiteralPath 'docs/installation.md' -Destination "$extracted/installation.md"
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
    & "$PSScriptRoot/archive.test.exe" '-test.run=^TestMainWindowsArchiveMachinePathConflict$' '-test.v' *> "$PSScriptRoot/results/test.log"
    exit $LASTEXITCODE
}
& "$PSScriptRoot/archive.test.exe" '-test.run=^TestMainWindowsArchiveOrdinarySetup$' '-test.v' *> "$PSScriptRoot/results/test.log"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$fixtureTag = (Get-Content -LiteralPath "$PSScriptRoot/fixture-tag.txt" -Raw).Trim()
$extracted = Join-Path $PSScriptRoot 'extracted'
$guide = [IO.File]::ReadAllText((Join-Path $extracted 'installation.md'))
$section = [regex]::Match($guide, '(?ms)^## Windows\r?\n(.*?)(?=^## |\z)').Groups[1].Value
$blocks = [regex]::Matches($section, '(?ms)^```powershell\r?\n(.*?)^```\r?$')
if ($blocks.Count -ne 1) {throw 'Cannot identify the documented extracted-ZIP paste.'}
$text = [IO.Path]::GetTempFileName()
$early = [IO.Path]::GetTempFileName()
try {
    [IO.File]::WriteAllText($early,'exit 0')
    [IO.File]::WriteAllText($text,$blocks[0].Groups[1].Value,[Text.UTF8Encoding]::new($false))
    Push-Location $extracted
    try {
        $managed = Join-Path $env:LOCALAPPDATA 'OpenAI\CLI\bin\openai.exe'
        foreach ($client in 'powershell.exe','pwsh.exe') {
            $shell = (Get-Command $client -CommandType Application | Select-Object -First 1).Path
            $params = @{Client=$shell; ExpectedExe=$managed; ExpectedVersion=('openai version '+$fixtureTag.Substring(1))}
            try {
                & "$PSScriptRoot/test-windows-console-paste.ps1" @params -TextPath $early
                throw 'Premature ConsoleHost exit passed qualification.'
            } catch {
                if ($_.Exception.Message -cne 'The ConsoleHost paste exited before its executable and version checks finished.') {
                    Add-Content -LiteralPath "$PSScriptRoot/results/test.log" -Value "$client early-exit control failed."
                    throw
                }
            }
            try {
                & "$PSScriptRoot/test-windows-console-paste.ps1" @params -TextPath $text
            } catch {
                $reason = 'driver failed'
                if ($_.Exception.Message -match '^The public ConsoleHost paste failed with code (-?[0-9]+)\.$') {$reason = "exit $($Matches[1])"}
                if ($_.Exception.Message -ceq 'The public ConsoleHost paste timed out.') {$reason = 'timeout'}
                if ($_.Exception.Message -ceq 'The ConsoleHost paste exited before its executable and version checks finished.') {$reason = 'missing completion receipt'}
                Add-Content -LiteralPath "$PSScriptRoot/results/test.log" -Value "$client Restricted paste failed ($reason)."
                throw
            }
            Add-Content -LiteralPath "$PSScriptRoot/results/test.log" -Value "$client Restricted paste verified the managed executable and version."
        }
    } finally {Pop-Location}
} finally {[IO.File]::Delete($text); [IO.File]::Delete($early)}
exit 0
'@ | Set-Content -LiteralPath "$root/ordinary.ps1" -Encoding utf8
    $credential = New-Object System.Management.Automation.PSCredential(".\$account", $secret)
    $shell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    function Invoke-StandardQualification([string]$expected) {
        $child = Start-Process -FilePath $shell -ArgumentList "-NoLogo -NoProfile -NonInteractive -File `"$root\ordinary.ps1`"" -WorkingDirectory $root -Credential $credential -LoadUserProfile -UseNewEnvironment -Wait -PassThru
        if (!(Test-Path -LiteralPath "$root/results/test.log")) {throw "The standard account failed before recording CLI evidence (exit $($child.ExitCode))."}
        $log = Get-Content -LiteralPath "$root/results/test.log" -Raw
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
        Invoke-StandardQualification 'TestMainWindowsArchiveOrdinarySetup'
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
            Invoke-StandardQualification 'TestMainWindowsArchiveMachinePathConflict'
            if ([Environment]::GetEnvironmentVariable('Path','Machine') -cne $originalMachinePath) { throw 'Machine PATH changed during enrollment.' }
        } finally {
            $held.Dispose()
            [IO.File]::Delete($conflict)
        }
    }
} finally {
    Remove-LocalUser -SID $user.Sid
    $secret.Dispose()
    if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Force -Recurse }
}
