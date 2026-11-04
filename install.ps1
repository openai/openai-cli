# Run from the extracted official Windows ZIP in ordinary PowerShell.
# This script runs in the current PowerShell process so it can update its PATH.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$release = [IO.Path]::Combine($PSScriptRoot, 'openai.exe')
if (![IO.File]::Exists($release)) {
    throw 'Extract the Windows ZIP, then run install.ps1 beside openai.exe.'
}

Write-Host 'Installing OpenAI CLI for your account...'
$null = & $release install --user
if ($LASTEXITCODE -ne 0) {
    throw 'OpenAI CLI setup could not finish. See the installer message above.'
}

# Windows PowerShell can decode native stdout using a legacy code page.
# Read the same Unicode user folder that native setup validated.
$installed = [IO.Path]::Combine($env:LOCALAPPDATA, 'OpenAI\CLI\bin\openai.exe')
if (![IO.File]::Exists($installed)) {
    throw 'The installed executable could not be located in your local application directory.'
}
$bin = [IO.Path]::GetDirectoryName($installed)
if (![string]::Equals(($env:Path -split ';', 2)[0], $bin, [StringComparison]::OrdinalIgnoreCase)) {
    $env:Path = "$bin;$env:Path"
}
$selected = Microsoft.PowerShell.Core\Get-Command openai -ErrorAction SilentlyContinue
if (!$selected -or $selected.CommandType -ne 'Application' -or
    ![string]::Equals($selected.Path, $installed, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Another command named openai takes precedence in this shell. Use & (Join-Path $env:LOCALAPPDATA ''OpenAI\CLI\bin\openai.exe'').'
}
Write-Output 'OpenAI CLI is ready.'
