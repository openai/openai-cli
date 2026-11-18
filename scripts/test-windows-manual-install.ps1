# Verify the README's installation paths on a disposable Windows runner.
param(
    [ValidateSet('Host', 'Install', 'Use', 'Upgrade', 'Check')][string]$Phase = 'Host',
    [ValidateSet('Zip', 'Go')][string]$Method = 'Zip'
)

$ErrorActionPreference = 'Stop'
$older = '1.54.0'
$newer = '1.55.0'
$folder = 'Programs\OpenAI CLI'

function Assert-Version([string]$command, [string]$version) {
    $line = & $command --version
    if ($LASTEXITCODE -ne 0 -or $line -cne "openai version $version") {
        throw "Expected $version from $command. Received: $line"
    }
}

if ($Phase -ne 'Host') {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'The installation must run under an ordinary account.'
    }
    $local = [Environment]::GetFolderPath('LocalApplicationData')
    $env:USERPROFILE = [Environment]::GetFolderPath('UserProfile')
    $env:LOCALAPPDATA = $local
    $env:APPDATA = [Environment]::GetFolderPath('ApplicationData')
    $env:TEMP = Join-Path $local 'Temp'
    $env:TMP = $env:TEMP
    $null = [IO.Directory]::CreateDirectory($env:TEMP)
    $bin = if ($Method -eq 'Go') { Join-Path $env:USERPROFILE 'go\bin' } else { Join-Path $local $folder }
    $exe = Join-Path $bin 'openai.exe'
    $managed = Join-Path $local 'OpenAI\CLI'
    $results = Join-Path $local 'manual-install-evidence'
    $receipt = Join-Path $results 'older-hash.txt'
    try {
        if (Test-Path -LiteralPath $managed) { throw 'Found a managed CLI in the test account.' }
        # A newly logged-on user gets machine Path followed by that user's Path.
        $env:Path = [Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')
        if ($Method -eq 'Go') {
            $goDir = Get-Content -LiteralPath "$PSScriptRoot\go-directory.txt"
            $env:Path = "$goDir;$env:Path"
            $env:GOBIN = $bin
            $locations = @(go env GOBIN GOPATH)
            if ($LASTEXITCODE -ne 0 -or $locations[0] -ine $bin) { throw 'Go selected a different destination.' }
        }
        if ($Phase -in 'Install','Upgrade') {
            if ($Phase -eq 'Install') {
                if ((Get-Command openai -ErrorAction SilentlyContinue) -or (Test-Path -LiteralPath $bin)) {
                    throw 'The fresh account already has OpenAI CLI.'
                }
                $null = New-Item -ItemType Directory -Path $results
            }
            $version = if ($Phase -eq 'Install') { $older } else { $newer }
            if ($Method -eq 'Zip') {
                $extracted = Join-Path $env:TEMP "openai-$version"
                Expand-Archive -LiteralPath "$PSScriptRoot\$version.zip" -DestinationPath $extracted
                $null = [IO.Directory]::CreateDirectory($bin)
                Copy-Item -LiteralPath "$extracted\openai.exe" -Destination $exe -Force
            } else {
                $target = Get-Content -LiteralPath "$PSScriptRoot\go-target.txt"
                if ($Phase -eq 'Install') { go install "github.com/openai/openai-cli/cmd/openai@v$older" }
                else { go install $target }
                if ($LASTEXITCODE -ne 0) { throw "Go installation failed with exit $LASTEXITCODE." }
                $metadata = @(go version -m $exe)
                $module = $metadata | Select-String '^\s*mod\s+github\.com/openai/openai-cli\s+v([0-9]+\.[0-9]+\.[0-9]+)\s+h1:'
                if ($LASTEXITCODE -ne 0 -or @($module).Count -ne 1) { throw 'Cannot verify the installed Go module.' }
                $version = $module.Matches[0].Groups[1].Value
                if ($Phase -eq 'Upgrade') {
                    if ([version]$version -le [version]$older) { throw 'Expected Go to install a newer release.' }
                    $version | Set-Content -LiteralPath "$results\newer-version.txt"
                } elseif ($version -cne $older) { throw 'Go installed a different starter release.' }
            }
            Set-Location -LiteralPath $bin
            Assert-Version '.\openai.exe' $version
            if ($Phase -eq 'Install') {
                $userPath = [Environment]::GetEnvironmentVariable('Path','User')
                [Environment]::SetEnvironmentVariable('Path', "$bin;$userPath", 'User')
                (Get-FileHash -LiteralPath $exe).Hash | Set-Content -LiteralPath $receipt
                "Install openai version $version SHA256=$((Get-FileHash -LiteralPath $exe).Hash)" | Set-Content -LiteralPath "$results\Install.txt"
            }
        } else {
            $selected = Get-Command openai
            if ($selected.CommandType -ne 'Application' -or $selected.Path -ine $exe) { throw 'PowerShell selected another command.' }
            $version = if ($Phase -eq 'Use') { $older } elseif ($Method -eq 'Zip') { $newer } else {
                Get-Content -LiteralPath "$results\newer-version.txt"
            }
            Assert-Version 'openai' $version
            if ($Phase -eq 'Use') {
                foreach ($key in 'CI','GITHUB_ACTIONS','GITLAB_CI','TF_BUILD','BUILDKITE','JENKINS_URL','TEAMCITY_VERSION','TERM') {
                    [Environment]::SetEnvironmentVariable($key,$null)
                }
                if (Get-ChildItem Env:OPENAI_CLI_*) { throw 'Expected an account with default CLI settings.' }
                # The hosted shell can redirect its own handles. Give the CLI a native console.
                Add-Type -Name ManualInstallConsole -Namespace Native -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("kernel32.dll", SetLastError=true)]
public static extern bool AllocConsole();
'@
                $null = [Native.ManualInstallConsole]::AllocConsole()
                $env:OPENAI_ARCHIVE_TEST_CONSOLE = '1'
                $env:OPENAI_ARCHIVE_TEST_MODE = 'bare'
                1..3 | ForEach-Object {
                    # The owner opens both consoles read/write. GetConsoleMode also needs read access on stdout and stderr.
                    $run = & "$PSScriptRoot\console.test.exe" '-test.run=^TestMainWindowsArchiveConsoleOwner$' '-test.v' '--' $exe
                    if ($LASTEXITCODE -ne 0 -or !($run -cmatch '^--- PASS: TestMainWindowsArchiveConsoleOwner ')) {
                        throw "The native console check failed: $($run -join ' ')"
                    }
                }
                $notice = @(Get-ChildItem -Path "$local\openai-cli\update-notice-*-windows-*\state.json")
                if ($notice.Count -ne 1 -or (Get-Content $notice[0].FullName -Raw | ConvertFrom-Json).checked -le 0) {
                    throw 'The ordinary interactive command did not run its release check.'
                }
                Assert-Version 'openai' $older
            }
            $hash = (Get-FileHash -LiteralPath $exe).Hash
            $unchanged = $hash -ceq (Get-Content -LiteralPath $receipt)
            if ($unchanged -ne ($Phase -eq 'Use') -or (Test-Path -LiteralPath $managed)) {
                throw 'Unexpected installation identity after running the CLI.'
            }
            "$Method $Phase openai version $version SHA256=$hash" | Set-Content -LiteralPath "$results\$Phase.txt"
        }
    } catch {
        [IO.File]::WriteAllText((Join-Path $local 'manual-install-error.txt'), "$Phase at line $($_.InvocationInfo.ScriptLineNumber): $($_.Exception.Message)")
        exit 1
    }
    exit 0
}

if ($env:RUNNER_OS -ne 'Windows' -or $env:GITHUB_ACTIONS -ne 'true') { throw 'Use a disposable native Windows runner.' }
$readme = Get-Content -LiteralPath (Join-Path $PSScriptRoot '..\README.md') -Raw
foreach ($instruction in 'Choose **Extract All**', ('`%LOCALAPPDATA%\' + $folder + '`'), '`.\openai.exe --version`', '`openai --version`') {
    if (!$readme.Contains($instruction)) { throw "The README no longer contains $instruction. Check the manual installation test." }
}
$arch = switch ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
    X64 {'amd64'} Arm64 {'arm64'} default { throw 'Unsupported Windows architecture.' }
}
# Digests come from the official, published release assets. Verify before executing.
$digests = @{
    '1.54.0-amd64' = 'e5a64c7b222f6b9b51effa2cc012e000911ad3241ec6a3c87a70b56a0d875bcb'
    '1.54.0-arm64' = 'f7ddab9667324c115068acee88f2b2ac0423b6a05601ff1d2bf8a409aa570df0'
    '1.55.0-amd64' = '9fdd602a892ab62b2d2262adf5d2d8c5afe7a7aa0bc01c363edb48b862d7403b'
    '1.55.0-arm64' = '51325910e5780b0230428482253da5be6d9071712241ff80d7fc7a3f3bdbb0bc'
}
$root = Join-Path $env:ProgramData ('openai-manual-' + [guid]::NewGuid())
$account = 'OpenAIManual' + [guid]::NewGuid().ToString('N').Substring(0,7)
$password = ConvertTo-SecureString ('Qa9!' + [guid]::NewGuid().ToString('N')) -AsPlainText -Force
$user = New-LocalUser -Name $account -Password $password -UserMayNotChangePassword
try {
    $sid = $user.Sid.Value
    $null = New-Item -ItemType Directory -Path $root
    icacls $root /inheritance:r /grant:r '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-18:(OI)(CI)F' "*${sid}:(OI)(CI)RX" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not protect the fixture directory.' }
    Copy-Item -LiteralPath $PSCommandPath -Destination "$root\manual.ps1"
    $console = Join-Path $env:RUNNER_TEMP "manual-install-console-$env:GITHUB_SHA.test.exe"
    if (!(Test-Path -LiteralPath $console)) {
        go test -c -o $console ./cmd/openai
        if ($LASTEXITCODE -ne 0) { throw 'Could not compile the Windows console verification helper.' }
    }
    Copy-Item -LiteralPath $console -Destination "$root\console.test.exe"
    if ($Method -eq 'Go') {
        if ($readme -notmatch '(?m)^```sh\r?\ngo install (github\.com/openai/openai-cli/cmd/openai@latest)\r?\n```\r?$') {
            throw 'Expected the documented Go installation command.'
        }
        $Matches[1] | Set-Content -LiteralPath "$root\go-target.txt"
        $go = Get-Command go.exe -CommandType Application | Select-Object -First 1
        Split-Path -LiteralPath $go.Path | Set-Content -LiteralPath "$root\go-directory.txt"
    }
    $archives = if ($Method -eq 'Zip') { $older; $newer }
    foreach ($version in $archives) {
        $name = "openai_${version}_windows_${arch}.zip"
        $url = "https://github.com/openai/openai-cli/releases/download/v$version/$name"
        Invoke-WebRequest -Uri $url -OutFile "$root\$version.zip"
        $manifest = "https://github.com/openai/openai-cli/releases/download/v$version/openai_${version}_checksums.txt"
        Invoke-WebRequest -Uri $manifest -OutFile "$root\checksums.txt"
        $hash = (Get-FileHash -LiteralPath "$root\$version.zip").Hash.ToLowerInvariant()
        $entry = @(Get-Content -LiteralPath "$root\checksums.txt" | Where-Object { $_ -ceq "$hash  $name" })
        if ($entry.Count -ne 1 -or $hash -cne $digests["$version-$arch"]) {
            throw "Release archive digest mismatch for $name."
        }
    }
    $client = "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
    $credential = [PSCredential]::new(".\$account",$password)
    foreach ($action in 'Install','Use','Upgrade','Check') {
        $arguments = "-NoLogo -NoProfile -NonInteractive -File `"$root\manual.ps1`" -Phase $action -Method $Method"
        $process = Start-Process -FilePath $client -ArgumentList $arguments -WorkingDirectory $root -Credential $credential -UseNewEnvironment -LoadUserProfile -Wait -PassThru
        $profile = (Get-CimInstance Win32_UserProfile -Filter "SID='$sid'").LocalPath
        $local = Join-Path $profile 'AppData\Local'
        if ($process.ExitCode -ne 0) {
            $log = Join-Path $local 'manual-install-error.txt'
            if (Test-Path -LiteralPath $log) { Get-Content -LiteralPath $log }
            throw "Manual installation phase $action failed."
        }
        if ($action -in 'Install','Use','Check') { Get-Content -LiteralPath "$local\manual-install-evidence\$action.txt" }
    }
} finally {
    Remove-LocalUser -SID $user.SID
    if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force }
}
