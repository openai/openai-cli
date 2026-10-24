# Run only after the original trace completes. This process does not identify a prior process's loaded modules.
param([Parameter(Mandatory=$true)][string]$PwshHome)
$ErrorActionPreference = 'Stop'
function Within-Root([string]$Path, [string]$Root) {
    if ([string]::IsNullOrEmpty($Path) -or [string]::IsNullOrEmpty($Root)) { return $false }
    $prefix = [IO.Path]::GetFullPath($Root).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    return [IO.Path]::GetFullPath($Path).StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)
}
function Safe-Version($Value) {
    $text = [string]$Value
    if ($text -match '^[0-9]{1,5}(\.[0-9]{1,10}){1,3}([-+][A-Za-z0-9.]{1,32})?$') { return $text }
    return 'unavailable'
}
function File-Evidence([string]$Path, [string]$ExpectedName) {
    $result = @{ role='unavailable'; sha256='unavailable'; bytes=0 }
    if ([string]::IsNullOrEmpty($Path)) { return $result }
    if (-not [IO.Path]::GetFileName($Path).Equals($ExpectedName, [StringComparison]::OrdinalIgnoreCase)) {
        $result.role = 'unexpected_name'; return $result
    }
    if (Within-Root $Path ([IO.Path]::Combine($PSHOME, 'Modules'))) { $result.role = 'engine_modules' }
    elseif (Within-Root $Path $PSHOME) { $result.role = 'engine_home' }
    elseif (Within-Root $Path ([IO.Path]::Combine($PwshHome, 'Modules'))) { $result.role = 'pwsh_modules' }
    elseif (Within-Root $Path $PwshHome) { $result.role = 'pwsh_home' }
    elseif (Within-Root $Path ([IO.Path]::Combine($env:SystemRoot, 'Microsoft.NET', 'assembly'))) { $result.role = 'windows_gac' }
    elseif (Within-Root $Path ([IO.Path]::Combine($env:SystemRoot, 'assembly'))) { $result.role = 'windows_gac' }
    else { $result.role = 'other'; return $result }
    $file = [IO.FileInfo]::new($Path)
    if (-not $file.Exists -or $file.Length -gt 67108864) { return $result }
    if (($file.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { return $result }
    $stream = [IO.File]::OpenRead($Path)
    $hasher = $null
    try {
        $hasher = [Security.Cryptography.SHA256]::Create()
        $result.sha256 = [BitConverter]::ToString($hasher.ComputeHash($stream)).Replace('-', '').ToLowerInvariant()
        $result.bytes = $stream.Length
    } finally { if ($null -ne $hasher) { $hasher.Dispose() }; $stream.Dispose() }
    return $result
}
function Emit-Result($Result) {
    $parts = [Collections.Generic.List[string]]::new()
    foreach ($key in $Result.Keys) {
        if ($key -notmatch '^[a-z][a-z0-9_]{0,49}$') { throw 'invalid_safe_key' }
        $value = $Result[$key]
        if ($value -is [bool]) { $text = $value.ToString().ToLowerInvariant() }
        elseif ($value -is [int] -or $value -is [long]) { $text = $value.ToString([Globalization.CultureInfo]::InvariantCulture) }
        else {
            $text = [string]$value
            if ($text -notmatch '^[A-Za-z0-9_.+-]{1,96}$') { throw 'invalid_safe_value' }
            $text = '"' + $text + '"'
        }
        $parts.Add('"' + $key + '":' + $text)
    }
    $json = '{' + [string]::Join(',', $parts) + '}'
    if ([Text.Encoding]::UTF8.GetByteCount($json) -gt 4096) { throw 'safe_json_limit' }
    [Console]::Out.WriteLine($json)
}
$result = @{
    schema=1; scope='separate_process_post_trace'; status='starting'; error_type='none';
    engine_version='unavailable'; edition='unavailable'; process64=[Environment]::Is64BitProcess;
    host='unavailable'; host_pid=0; host_role='unavailable'; host_sha256='unavailable'; host_bytes=0;
    engine_assembly_version='unavailable'; engine_role='unavailable'; engine_sha256='unavailable'; engine_bytes=0;
    utility_loaded_before_lookup=0; command='Out-File'; module='Microsoft.PowerShell.Utility';
    module_version='unavailable'; manifest_role='unavailable'; manifest_sha256='unavailable'; manifest_bytes=0;
    implementation='unavailable'; assembly_version='unavailable'; assembly_role='unavailable';
    assembly_sha256='unavailable'; assembly_bytes=0
}
$exitCode = 75
try {
    $process = [Diagnostics.Process]::GetCurrentProcess()
    try { $hostPath = $process.MainModule.FileName; $result.host_pid = $process.Id } finally { $process.Dispose() }
    $hostName = [IO.Path]::GetFileName($hostPath).ToLowerInvariant()
    if ($hostName -eq 'powershell.exe' -or $hostName -eq 'pwsh.exe') {
        $hostFile = File-Evidence $hostPath $hostName
        $result.host = $hostName; $result.host_role = $hostFile.role
        $result.host_sha256 = $hostFile.sha256; $result.host_bytes = $hostFile.bytes
    }
    $result.engine_version = Safe-Version $PSVersionTable.PSVersion
    $edition = [string]$PSVersionTable.PSEdition
    if ($edition -eq 'Desktop' -or $edition -eq 'Core') { $result.edition = $edition }
    $engine = File-Evidence ([PSObject].Assembly.Location) 'System.Management.Automation.dll'
    $result.engine_assembly_version = Safe-Version ([PSObject].Assembly.GetName().Version)
    $result.engine_role = $engine.role; $result.engine_sha256 = $engine.sha256; $result.engine_bytes = $engine.bytes
    $loaded = @(Get-Module -Name Microsoft.PowerShell.Utility)
    $result.utility_loaded_before_lookup = $loaded.Count
    $command = Get-Command Out-File -ErrorAction Stop
    if ($command.Name -ne 'Out-File' -or $command.CommandType -ne 'Cmdlet' -or $command.ModuleName -ne 'Microsoft.PowerShell.Utility') {
        $result.status = 'unexpected_cmdlet'
        $exitCode = 76
    } else {
        $manifest = File-Evidence $command.Module.Path 'Microsoft.PowerShell.Utility.psd1'
        $result.module_version = Safe-Version $command.Module.Version
        $result.manifest_role = $manifest.role; $result.manifest_sha256 = $manifest.sha256; $result.manifest_bytes = $manifest.bytes
        if ($null -eq $command.ImplementingType) {
            $result.status = 'implementation_unavailable'
        } else {
            $assembly = $command.ImplementingType.Assembly
            if ($assembly.GetName().Name -ne 'Microsoft.PowerShell.Commands.Utility') {
                $result.status = 'unexpected_assembly'
                $exitCode = 77
            } else {
                $implementation = File-Evidence $assembly.Location 'Microsoft.PowerShell.Commands.Utility.dll'
                $result.implementation = 'Microsoft.PowerShell.Commands.Utility'
                $result.assembly_version = Safe-Version $assembly.GetName().Version
                $result.assembly_role = $implementation.role
                $result.assembly_sha256 = $implementation.sha256
                $result.assembly_bytes = $implementation.bytes
                $result.status = 'observed'
                $exitCode = 0
            }
        }
    }
} catch {
    $type = $_.Exception.GetType().Name
    if ($type -notmatch '^[A-Za-z][A-Za-z0-9]{0,80}$') { $type = 'unavailable' }
    $result.status = 'failed'
    $result.error_type = $type
}
Emit-Result $result
exit $exitCode
