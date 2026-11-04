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
        Copy-Item -LiteralPath 'scripts/windows-starter.ps1.tmpl','scripts/test-windows-published-starter.ps1' -Destination $root
        @{starter=$StarterTag; successor=$SuccessorTag; client=$PublicClient; privateVerifier=[bool]$PrivateVerifier} | ConvertTo-Json | Set-Content -LiteralPath "$root/tags.json"
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
        Copy-Item -LiteralPath 'Install.cmd' -Destination "$root/Install.cmd"
        Copy-Item -LiteralPath 'docs/installation.md' -Destination "$root/installation.md"
        Copy-Item -LiteralPath 'scripts/windows-starter.ps1.tmpl' -Destination "$root/windows-starter.ps1.tmpl"
        Copy-Item -LiteralPath 'scripts/test-windows-starter-paste.ps1' -Destination "$root/test-windows-starter-paste.ps1"
        Set-Content -LiteralPath "$root/fixture-tag.txt" -Value "v$version"
        go test -count=1 -v ./internal/updatenotice -run '^TestWindowsArchive'
        if ($LASTEXITCODE -ne 0) { throw 'Native archive security or executable-lock tests failed.' }
        $env:OPENAI_CLI_NATIVE_SHELL_BINARY = Join-Path $root 'openai.exe'
        $env:OPENAI_CLI_NATIVE_SHELL_SHA256 = (Get-FileHash -Path $env:OPENAI_CLI_NATIVE_SHELL_BINARY -Algorithm SHA256).Hash.ToLowerInvariant()
        & "$root/archive.test.exe" '-test.run=^TestMainWindowsArchiveInstallHelp$' '-test.v'
        $helpResult = $LASTEXITCODE
        Remove-Item Env:OPENAI_CLI_NATIVE_SHELL_BINARY, Env:OPENAI_CLI_NATIVE_SHELL_SHA256
        $LASTEXITCODE = $helpResult
        if ($LASTEXITCODE -ne 0) { throw 'Native install command help or error handling failed.' }
        if ($goarch -eq 'amd64') {
            $priorArch = $env:GOARCH
            try {
                $env:GOARCH = '386'
                go test -c -o "$root/archive-386.test.exe" ./internal/updatenotice
                if ($LASTEXITCODE -ne 0) { throw 'Could not compile the Windows x86 tests.' }
                & "$root/archive-386.test.exe" '-test.run=^TestWindowsArchive' '-test.v'
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
    $env:OPENAI_WINDOWS_PUBLIC_STARTER_SHA = & "$PSScriptRoot/test-windows-published-starter.ps1" -Tag $tags.starter -Client $tags.client -PrivateVerifier:([bool]$tags.privateVerifier)
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
    $observed | ConvertTo-Json -Compress | Add-Content -LiteralPath "$PSScriptRoot/results/test.log"
    exit 0
}
$env:OPENAI_CLI_WINDOWS_ARCHIVE_FIXTURE = '1'
$env:OPENAI_CLI_WINDOWS_ARCHIVE_OLDER_BINARY = Join-Path $PSScriptRoot 'older-openai.exe'
$env:OPENAI_CLI_WINDOWS_ARCHIVE_BINARY = Join-Path $PSScriptRoot 'openai.exe'
$env:OPENAI_CLI_WINDOWS_ARCHIVE_SCRIPT = Join-Path $PSScriptRoot 'Install.cmd'
if (Test-Path -LiteralPath "$PSScriptRoot/machine-conflict.txt") {
    $env:OPENAI_CLI_WINDOWS_ARCHIVE_CONFLICT = (Get-Content -LiteralPath "$PSScriptRoot/machine-conflict.txt" -Raw).Trim()
    & "$PSScriptRoot/archive.test.exe" '-test.run=^TestMainWindowsArchiveMachinePathConflict$' '-test.v' *> "$PSScriptRoot/results/test.log"
    exit $LASTEXITCODE
}
& "$PSScriptRoot/archive.test.exe" '-test.run=^TestMainWindowsArchiveOrdinarySetup$' '-test.v' *> "$PSScriptRoot/results/test.log"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$fixtureTag = (Get-Content -LiteralPath "$PSScriptRoot/fixture-tag.txt" -Raw).Trim()
& "$PSScriptRoot/test-windows-starter-paste.ps1" -Candidate $env:OPENAI_CLI_WINDOWS_ARCHIVE_BINARY -Tag $fixtureTag -Template "$PSScriptRoot/windows-starter.ps1.tmpl"
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
    }
    if ($published) {
        Invoke-StandardQualification 'TestMainWindowsArchivePublishedUpgrade'
    } else {
        Invoke-StandardQualification 'TestMainWindowsArchiveOrdinarySetup'
        # Place one inert fixture beside an installed CI tool. Never modify
        # machine PATH, pre-existing programs, or Windows system directories.
        $originalMachinePath = [Environment]::GetEnvironmentVariable('Path','Machine')
        $machineEntries = [Environment]::ExpandEnvironmentVariables($originalMachinePath) -split ';'
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
