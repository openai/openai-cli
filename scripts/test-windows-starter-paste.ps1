# Run only in the dedicated, disposable Actions account. Enrollment writes HKCU.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Candidate, [Parameter(Mandatory)][string]$Tag, [Parameter(Mandatory)][string]$Template)
$ErrorActionPreference='Stop'
$account=[Security.Principal.WindowsIdentity]::GetCurrent()
if ($env:GITHUB_ACTIONS -ne 'true' -or $account.Name.Split('\')[-1] -ne 'OpenAIArchiveTest' -or
    ([Security.Principal.WindowsPrincipal]$account).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {throw 'Refusing setup outside the disposable standard account.'}
Add-Type -AssemblyName System.IO.Compression.FileSystem
$tmp=Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) ('Windows-starter-fixture-'+[Guid]::NewGuid())
$null=[IO.Directory]::CreateDirectory($tmp)
try {
    $path=Join-Path $tmp 'synthetic.zip'
    $z=[IO.Compression.ZipFile]::Open($path,'Create')
    try {$null=[IO.Compression.ZipFileExtensions]::CreateEntryFromFile($z,$Candidate,'openai.exe','Fastest')} finally {$z.Dispose()}
    $hash=(Get-FileHash -LiteralPath $path).Hash.ToLowerInvariant()
    $raw=[IO.File]::ReadAllText($Template).Replace('__STABLE_TAG__',$Tag)
    foreach($placeholder in '__AMD64_SHA256__','__ARM64_SHA256__','__386_SHA256__') {$raw=$raw.Replace($placeholder,$hash)}
    $tokens,$errors=$null,$null
    $ast=[Management.Automation.Language.Parser]::ParseInput($raw,[ref]$tokens,[ref]$errors)
    $transport=$ast.Find({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Get-StarterBytes'},$true)
    if($errors.Count -or !$transport -or $raw -match 'api\.github\.com|/releases/latest|/git/ref/'){throw 'Cannot locate the starter transport.'}
    # Use only the expected versioned public asset route. No metadata or other
    # address is supplied. Every hash, extraction, enrollment and PATH step runs.
    $offline=@'
function Get-StarterBytes([string]$url) {
    if($url -cne "https://github.com/openai/openai-cli/releases/download/$tag/openai_$($tag.Substring(1))_windows_$arch.zip") {throw 'Unexpected network route.'}
    return ,[IO.File]::ReadAllBytes($env:OPENAI_CLI_STARTER_TEST_ZIP)
}
'@
    $ext=$transport.Extent
    $raw=$raw.Remove($ext.StartOffset,$ext.EndOffset-$ext.StartOffset).Insert($ext.StartOffset,$offline)
    $script=Join-Path $tmp 'starter.ps1'
    $proxyBefore=@{}
    foreach($name in 'HTTP_PROXY','HTTPS_PROXY','NO_PROXY') { $proxyBefore[$name]=[Environment]::GetEnvironmentVariable($name); [Environment]::SetEnvironmentVariable($name,'http://127.0.0.1:9') }
    $env:NO_PROXY='localhost,127.0.0.1'
    $prior=$env:OPENAI_CLI_STARTER_TEST_ZIP
    $env:OPENAI_CLI_STARTER_TEST_ZIP=$path
    try {
        [IO.File]::WriteAllText($script,$raw.Replace($hash,('f'*64)))
        $rejected=$false
        try {& $script|Out-Null} catch {$rejected=$_.Exception.Message -eq 'The published Windows ZIP checksum did not match.'}
        if(!$rejected){throw 'An incorrect starter pin reached enrollment.'}
        [IO.File]::WriteAllText($script,$raw)
        $actual=& $script
        $managed=Join-Path $env:LOCALAPPDATA 'OpenAI\CLI\bin\openai.exe'
        if($actual -cne "openai version $($Tag.Substring(1))" -or (Get-Command openai).Path -ine $managed -or
            (Get-FileHash $managed).Hash -ne (Get-FileHash $Candidate).Hash -or
            [IO.File]::ReadAllText($managed+':Zone.Identifier') -notmatch 'ZoneId=3') {throw 'The pinned starter did not qualify.'}
    } finally {
        $env:OPENAI_CLI_STARTER_TEST_ZIP=$prior
        foreach($name in $proxyBefore.Keys) {[Environment]::SetEnvironmentVariable($name,$proxyBefore[$name])}
    }
    'Pinned Windows starter passed with release metadata unavailable.'
} finally {[IO.Directory]::Delete($tmp,$true)}
