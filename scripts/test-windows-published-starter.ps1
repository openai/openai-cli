# Read-only public qualification in the disposable standard account.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $Tag,
    [Parameter(Mandatory)][ValidateSet('powershell.exe','pwsh.exe')][string] $Client,
    [switch] $PrivateVerifier
)
$ErrorActionPreference = 'Stop'
if ($Tag -cnotmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {throw 'Invalid starter tag.'}
$base = [Environment]::GetFolderPath('LocalApplicationData')
$bin = Join-Path $base 'OpenAI\CLI\bin'
$target = Join-Path $bin 'openai.exe'
if ((Test-Path -LiteralPath $bin) -or ($env:Path -split ';') -contains $bin) {throw 'The starter requires a pristine user profile.'}

# The initial acquisition may select A. The enrolled CLI discovers B itself.
$recipe = Join-Path $PSScriptRoot 'acquire.ps1'
$shell = (Get-Command $Client -CommandType Application | Select-Object -First 1).Path
$priorPath = $env:Path
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
    & "$PSScriptRoot/test-windows-console-paste.ps1" -Client $shell -TextPath $recipe -ExpectOneVerifierDownload:$PrivateVerifier -ExpectedExe $target -ExpectedVersion ('openai version '+$Tag.Substring(1))
} finally {
    $env:Path = $priorPath
    $env:OPENAI_CLI_WINDOWS_INSTALL_TAG = $priorTag
}
# Authenticate the observed A separately, after the fresh user's literal paste.
$trusted = & "$PSScriptRoot/acquire.ps1" -ReleaseTag $Tag -VerifyOnly | ConvertFrom-Json
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
