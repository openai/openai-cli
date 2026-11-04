# Feed literal text to an interactive ConsoleHost. Use a process-local Restricted
# policy to match Windows client defaults without changing any host policy.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $Client,
    [Parameter(Mandatory)][string] $TextPath,
    [Parameter(Mandatory)][string] $ExpectedExe,
    [Parameter(Mandatory)][string] $ExpectedVersion
)
$ErrorActionPreference = 'Stop'
    $env:OPENAI_WINDOWS_PASTE_TEXT = $TextPath
    $env:OPENAI_WINDOWS_MANAGED_EXE = $ExpectedExe
    $env:OPENAI_WINDOWS_STARTER_VERSION = $ExpectedVersion
    # ConsoleHost receives the user's literal text as one interactive input.
    # The stricter process policy does not change the machine or user policy.
    $driver = @'
$global:reads = 0
function global:PSConsoleHostReadLine {
    $global:reads++
    if ($global:reads -eq 1) {
        if ($Host.Name -ne 'ConsoleHost' -or (Get-ExecutionPolicy) -ne 'Restricted') {return 'exit 32'}
        $global:Error.Clear()
        return [IO.File]::ReadAllText($env:OPENAI_WINDOWS_PASTE_TEXT)
    }
    if ($global:reads -eq 2) {
        return 'if ($Error.Count -or $LASTEXITCODE -ne 0 -or (Get-Command openai).Path -ine $env:OPENAI_WINDOWS_MANAGED_EXE) {exit 33}; $v=openai --version; if ($LASTEXITCODE -ne 0 -or $v -cne $env:OPENAI_WINDOWS_STARTER_VERSION) {exit 34}; exit 0'
    }
    return 'exit 35'
}
'@
    $argsText = '-NoLogo -NoProfile -NoExit -ExecutionPolicy Restricted -Command "'+$driver+'"'
    $terminal = Start-Process $Client -ArgumentList $argsText -WindowStyle Hidden -PassThru
    try {
        if (!$terminal.WaitForExit(300000)) {$terminal.Kill(); throw 'The public ConsoleHost paste timed out.'}
        if ($terminal.ExitCode -ne 0) {throw "The public ConsoleHost paste failed with code $($terminal.ExitCode)."}
    } finally {$terminal.Dispose()}
