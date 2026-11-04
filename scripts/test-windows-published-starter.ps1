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

# Authenticate A independently. VerifyOnly deletes its temporary verifier.
$trusted = & "$PSScriptRoot/acquire.ps1" -ReleaseTag $Tag -VerifyOnly | ConvertFrom-Json
if ($trusted.tag -cne $Tag -or $trusted.executableSha256 -cnotmatch '^[a-f0-9]{64}$' -or (Test-Path -LiteralPath $bin)) {throw 'Independent starter verification failed.'}
$release = Invoke-RestMethod -Uri "https://api.github.com/repos/openai/openai-cli/releases/tags/$Tag" -Headers @{Accept='application/vnd.github+json'} -MaximumRedirection 0
$recipe = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'windows-starter.ps1.tmpl')).Replace('__STABLE_TAG__',$Tag)
$cpu = (Get-CimInstance Win32_Processor | Select-Object -First 1).Architecture
$native = switch ($cpu) {9 {'amd64'} 12 {'arm64'} default {throw 'Unexpected public runner architecture.'}}
foreach ($arch in 'amd64','arm64','386') {
    $name = "openai_$($Tag.Substring(1))_windows_$arch.zip"
    $assets = @($release.assets | Where-Object { $_.name -ceq $name })
    if ($assets.Count -ne 1 -or $assets[0].digest -cnotmatch '^sha256:([0-9a-f]{64})$') {throw 'Missing or ambiguous public starter digest.'}
    $digest = $Matches[1]
    if ($arch -ceq $native -and $digest -cne $trusted.archiveSha256) {throw 'The public starter changed during qualification.'}
    $recipe = $recipe.Replace(('__'+$arch.ToUpperInvariant()+'_SHA256__'),$digest)
}
$shell = (Get-Command $Client -CommandType Application | Select-Object -First 1).Path
$priorPath = $env:Path
$inputPath = Join-Path ([IO.Path]::GetTempPath()) ('openai-paste-'+[guid]::NewGuid()+'.txt')
[IO.File]::WriteAllText($inputPath,$recipe,[Text.UTF8Encoding]::new($false))
try {
    if ($PrivateVerifier) {
        $system = [Environment]::GetFolderPath('System')
        $env:Path = [string]::Join(';',@($priorPath -split ';' | Where-Object {
            $entry = $_.Trim('"')
            $entry -and !(Test-Path -LiteralPath (Join-Path $entry 'gh.exe'))
        }))
        if (($env:Path -split ';') -notcontains $system -or (Get-Command gh -ErrorAction SilentlyContinue)) {throw 'Could not isolate a normal Windows PATH without GitHub CLI.'}
    }
    & "$PSScriptRoot/test-windows-console-paste.ps1" -Client $shell -TextPath $inputPath -ExpectedExe $target -ExpectedVersion ('openai version '+$Tag.Substring(1))
} finally {
    $env:Path = $priorPath
    [IO.File]::Delete($inputPath)
}
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
