# Feed literal text to an interactive ConsoleHost. Use a process-local Restricted
# policy to match Windows client defaults without changing any host policy.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $Client,
    [Parameter(Mandatory)][string] $TextPath,
    [Parameter(Mandatory)][string] $ExpectedExe,
    [Parameter(Mandatory)][string] $ExpectedVersion,
    [switch] $ExpectOneVerifierDownload
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
        $global:LASTEXITCODE = 0
        return [IO.File]::ReadAllText($env:OPENAI_WINDOWS_PASTE_TEXT)
    }
    if ($global:reads -eq 2) {
        return 'if ($Error.Count -or $LASTEXITCODE -ne 0 -or (Get-Command openai).Path -ine $env:OPENAI_WINDOWS_MANAGED_EXE) {exit 33}; $v=openai --version; if ($LASTEXITCODE -ne 0 -or $v -cne $env:OPENAI_WINDOWS_STARTER_VERSION) {exit 34}; exit 0'
    }
    return 'exit 35'
}
'@
    $argsText = '-NoLogo -NoProfile -NoExit -ExecutionPolicy Restricted -Command "'+$driver+'"'
    $capture = [IO.Path]::GetTempFileName()
    try {
        $terminal = Start-Process $Client -ArgumentList $argsText -WindowStyle Hidden -RedirectStandardError $capture -PassThru
        try {
            if (!$terminal.WaitForExit(300000)) {$terminal.Kill(); throw 'The public ConsoleHost paste timed out.'}
            if ($terminal.ExitCode -ne 0) {throw "The public ConsoleHost paste failed with code $($terminal.ExitCode)."}
        } finally {$terminal.Dispose()}
        if ($ExpectOneVerifierDownload) {
            $lines = [IO.File]::ReadAllLines($capture)
            if (@($lines -ceq 'Downloading the pinned GitHub CLI verifier...').Count -ne 1 -or
                @($lines -ceq 'Downloading GitHub CLI for automatic update verification...').Count -ne 0) {
                throw 'A fresh setup must download GitHub CLI exactly once.'
            }
        }
    } finally {[IO.File]::Delete($capture)}
