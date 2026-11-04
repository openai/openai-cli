# Read-only public qualification in the disposable standard account.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $Tag,
    [Parameter(Mandatory)][ValidateSet('powershell.exe','pwsh.exe')][string] $Client,
    [Parameter(Mandatory)][ValidateSet('PowerShell','Install.cmd')][string] $Route,
    [switch] $PrivateVerifier
)
$ErrorActionPreference = 'Stop'
if ($Tag -cnotmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {throw 'Invalid starter tag.'}
$base = [Environment]::GetFolderPath('LocalApplicationData')
$bin = Join-Path $base 'OpenAI\CLI\bin'
$target = Join-Path $bin 'openai.exe'
if ((Test-Path -LiteralPath $bin) -or ($env:Path -split ';') -contains $bin) {throw 'The starter requires a pristine user profile.'}

# Bind both the extracted launcher and executable to the authenticated public ZIP.
function Expand-AuthenticatedStarter([string]$zip, [string]$destination, $trusted) {
    if ([IO.FileInfo]::new($zip).Length -ne $trusted.assetSize -or
        (Get-FileHash -LiteralPath $zip).Hash.ToLowerInvariant() -cne $trusted.archiveSha256) {
        throw 'The Explorer ZIP differs from the authenticated public archive.'
    }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [IO.Compression.ZipFile]::OpenRead($zip)
    try {
        foreach ($name in 'Install.cmd','openai.exe') {
            $entries = @($archive.Entries | Where-Object { $_.FullName -ieq $name })
            if ($entries.Count -ne 1 -or $entries[0].FullName -cne $name -or $entries[0].Length -le 0) {throw 'The public ZIP lacks one required root file.'}
        }
        $stream = $archive.GetEntry('Install.cmd').Open()
        try {$launcherHash = (Get-FileHash -InputStream $stream).Hash} finally {$stream.Dispose()}
    } finally {$archive.Dispose()}
    [IO.Compression.ZipFile]::ExtractToDirectory($zip,$destination)
    $launcher = Join-Path $destination 'Install.cmd'
    $exe = Join-Path $destination 'openai.exe'
    if ((Get-FileHash -LiteralPath $launcher).Hash -cne $launcherHash -or
        (Get-FileHash -LiteralPath $exe).Hash.ToLowerInvariant() -cne $trusted.executableSha256) {
        throw 'Extracted Windows files differ from authenticated release A.'
    }
    # ZipFile extraction does not propagate Explorer's attachment mark.
    foreach ($file in $zip,$launcher,$exe) {
        [IO.File]::WriteAllText($file+':Zone.Identifier',"[ZoneTransfer]`r`nZoneId=3`r`n",[Text.Encoding]::ASCII)
    }
}

function Invoke-PublishedLauncher($trusted) {
    $escapedVersion = [regex]::Escape($Tag.Substring(1))
    if ($trusted.tag -cne $Tag -or $trusted.assetName -cnotmatch "^openai_${escapedVersion}_windows_(amd64|arm64)\.zip$" -or
        $trusted.assetSize -le 0 -or $trusted.archiveSha256 -cnotmatch '^[0-9a-f]{64}$') {throw 'Invalid authenticated Windows asset identity.'}
    $work = Join-Path $base ('OpenAI-public-zip-'+[guid]::NewGuid())
    $null = [IO.Directory]::CreateDirectory($work)
    $keys = 'OPENAI_ARCHIVE_TEST_CONSOLE','OPENAI_ARCHIVE_TEST_MODE','OPENAI_ARCHIVE_TEST_SCREEN','OPENAI_WINDOWS_REQUIRE_PRIVATE_GH'
    $before = @{}
    foreach ($key in $keys) {$before[$key] = [Environment]::GetEnvironmentVariable($key)}
    try {
        $zip = Join-Path $work 'public.zip'
        $extracted = Join-Path $work 'extracted'
        $curl = Join-Path ([Environment]::GetFolderPath('System')) 'curl.exe'
        & $curl --disable --silent --fail --location --proto '=https' --proto-redir '=https' --max-time 150 --max-filesize $trusted.assetSize --output $zip "https://github.com/openai/openai-cli/releases/download/$Tag/$($trusted.assetName)"
        if ($LASTEXITCODE -ne 0) {throw 'Could not download the authenticated Explorer ZIP.'}
        Expand-AuthenticatedStarter $zip $extracted $trusted
        $env:OPENAI_ARCHIVE_TEST_CONSOLE = '1'
        $env:OPENAI_ARCHIVE_TEST_MODE = 'install'
        $env:OPENAI_ARCHIVE_TEST_SCREEN = Join-Path $work 'screen.txt'
        if ($PrivateVerifier) {$env:OPENAI_WINDOWS_REQUIRE_PRIVATE_GH = '1'}
        $arguments = '-test.run=^TestMainWindowsArchiveConsoleOwner$ -- Install.cmd'
        $owner = Start-Process (Join-Path $PSScriptRoot 'archive.test.exe') -WorkingDirectory $extracted -ArgumentList $arguments -WindowStyle Hidden -PassThru
        try {
            if (!$owner.WaitForExit(180000)) {$owner.Kill(); throw 'The public Install.cmd did not finish without a keypress.'}
            if ($owner.ExitCode -ne 0) {throw 'The authenticated public Install.cmd failed.'}
        } finally {$owner.Dispose()}
        $screen = [IO.File]::ReadAllText($env:OPENAI_ARCHIVE_TEST_SCREEN)
        foreach ($line in 'Installing OpenAI CLI for your account...','OpenAI CLI setup finished.') {
            if (!$screen.Contains($line)) {throw 'The public Install.cmd omitted its status.'}
        }
        if ($PrivateVerifier -and [regex]::Matches($screen,[regex]::Escape('Downloading GitHub CLI for automatic update verification...')).Count -ne 1) {
            throw 'A fresh public ZIP installation must acquire its verifier once.'
        }
    } finally {
        foreach ($key in $keys) {[Environment]::SetEnvironmentVariable($key,$before[$key])}
        [IO.Directory]::Delete($work,$true)
    }
}

# The initial acquisition may select A. The enrolled CLI discovers B itself.
$recipe = Join-Path $PSScriptRoot 'acquire.ps1'
$shell = (Get-Command $Client -CommandType Application | Select-Object -First 1).Path
$priorPath = $env:Path
$trusted = $null
if ($Route -ceq 'Install.cmd') {
    $trusted = & $recipe -ReleaseTag $Tag -VerifyOnly | ConvertFrom-Json
    if (Test-Path -LiteralPath $bin) {throw 'Independent verification enrolled the pristine account.'}
}
$priorTag = $env:OPENAI_CLI_WINDOWS_INSTALL_TAG
try {
    $env:OPENAI_CLI_WINDOWS_INSTALL_TAG = $Tag
    if ($PrivateVerifier) {
        $system = [Environment]::GetFolderPath('System')
        $env:Path = [string]::Join(';',@($priorPath -split ';' | Where-Object {
            $entry = $_.Trim('"')
            $entry -and !(Test-Path -LiteralPath (Join-Path $entry 'gh.exe'))
        }))
        if (($env:Path -split ';') -notcontains $system -or (Get-Command gh -ErrorAction SilentlyContinue)) {throw 'Could not isolate a normal Windows PATH without GitHub CLI.'}
    }
    if ($Route -ceq 'PowerShell') {
        & "$PSScriptRoot/test-windows-console-paste.ps1" -Client $shell -TextPath $recipe -ExpectOneVerifierDownload:$PrivateVerifier -ExpectedExe $target -ExpectedVersion ('openai version '+$Tag.Substring(1))
    } else {
        $callerPath = $env:Path
        Invoke-PublishedLauncher $trusted
        if ($env:Path -cne $callerPath) {throw 'The launcher changed the invoking process environment.'}
    }
} finally {
    $env:Path = $priorPath
    $env:OPENAI_CLI_WINDOWS_INSTALL_TAG = $priorTag
}
# Authenticate the observed A separately, after the fresh user's literal paste.
if (!$trusted) {$trusted = & $recipe -ReleaseTag $Tag -VerifyOnly | ConvertFrom-Json}
if ($trusted.tag -cne $Tag -or $trusted.executableSha256 -cnotmatch '^[a-f0-9]{64}$') {throw 'Independent starter verification failed.'}
if ((Get-FileHash -LiteralPath $target).Hash.ToLowerInvariant() -cne $trusted.executableSha256) {throw 'Installed starter differs from independently authenticated A.'}
$acl = Get-Acl -LiteralPath $bin
$sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
if (!$acl.AreAccessRulesProtected -or $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -cne $sid) {throw 'The enrolled directory is not private.'}
foreach ($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
    if ($rule.AccessControlType -eq 'Allow' -and $rule.IdentityReference.Value -notin $sid,'S-1-5-18','S-1-5-32-544') {throw 'The enrolled directory grants an unrelated identity access.'}
}
$images = @($target)
if ($PrivateVerifier) {
    $gh = Join-Path $bin ('.update-verifier\gh-'+$trusted.verifierVersion+'.exe')
    if ((Get-FileHash -LiteralPath $gh).Hash.ToLowerInvariant() -cne $trusted.verifierSha256) {throw 'Private verifier differs from its upstream executable pin.'}
    $images += $gh
}
foreach ($image in $images) {
    if ([IO.File]::ReadAllText($image+':Zone.Identifier') -notmatch '(?m)^ZoneId=[34]\r?$') {throw 'A downloaded executable lost its Internet security zone.'}
}
return $trusted.executableSha256
