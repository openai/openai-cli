# Run only on a disposable, native Windows Actions runner.
$ErrorActionPreference = 'Stop'
if ($env:RUNNER_OS -ne 'Windows' -or $env:GITHUB_ACTIONS -ne 'true') {
    throw 'Windows archive qualification requires a disposable Windows runner.'
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

    # The hosted runner owns build caches. The standard user receives only
    # read/execute access to the compiled probe, candidate, and ZIP helper.
    go build -ldflags $ldflags -o "$root/openai.exe" ./cmd/openai/main.go
    if ($LASTEXITCODE -ne 0) { throw 'Could not build the release-stamped candidate.' }
    $olderVersion = Join-Path $root 'older-version.go'
    ($versionLine.Replace('"' + $version + '"', '"0.0.1"')) | Set-Content -LiteralPath $olderVersion -Encoding utf8NoBOM
    $overlay = Join-Path $root 'older-version-overlay.json'
    $replace = @{}
    $replace[(Resolve-Path 'internal/app/version.go').Path] = $olderVersion
    @{Replace = $replace} | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath $overlay -Encoding utf8NoBOM
    $oldFlags = "-s -w -X ${stamp}releaseVersion=0.0.1 -X ${stamp}releaseCommit=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    go build -overlay $overlay -ldflags $oldFlags -o "$root/older-openai.exe" ./cmd/openai/main.go
    if ($LASTEXITCODE -ne 0) { throw 'Could not build the older release-stamped fixture.' }
    go test -c -o "$root/archive.test.exe" ./cmd/openai
    if ($LASTEXITCODE -ne 0) { throw 'Could not compile the native entrypoint tests.' }
    Copy-Item -LiteralPath 'install.ps1' -Destination "$root/install.ps1"
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
$env:OPENAI_CLI_WINDOWS_ARCHIVE_FIXTURE = '1'
$env:OPENAI_CLI_WINDOWS_ARCHIVE_OLDER_BINARY = Join-Path $PSScriptRoot 'older-openai.exe'
$env:OPENAI_CLI_WINDOWS_ARCHIVE_BINARY = Join-Path $PSScriptRoot 'openai.exe'
$env:OPENAI_CLI_WINDOWS_ARCHIVE_SCRIPT = Join-Path $PSScriptRoot 'install.ps1'
& "$PSScriptRoot/archive.test.exe" '-test.run=^TestMainWindowsArchiveOrdinarySetup$' '-test.v' *> "$PSScriptRoot/results/test.log"
exit $LASTEXITCODE
'@ | Set-Content -LiteralPath "$root/ordinary.ps1" -Encoding utf8
    $credential = New-Object System.Management.Automation.PSCredential(".\$account", $secret)
    $shell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    $child = Start-Process -FilePath $shell -ArgumentList "-NoLogo -NoProfile -NonInteractive -File `"$root\ordinary.ps1`"" -WorkingDirectory $root -Credential $credential -LoadUserProfile -UseNewEnvironment -Wait -PassThru
    $log = Get-Content -LiteralPath "$root/results/test.log" -Raw
    Write-Output $log
    if ($child.ExitCode -ne 0 -or $log -notmatch '--- PASS: TestMainWindowsArchiveOrdinarySetup') {
        throw 'The standard-user entrypoint qualification did not pass.'
    }
} finally {
    Remove-LocalUser -SID $user.Sid
    $secret.Dispose()
    if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Force -Recurse }
}
