# Optional executable suggestions. Dot-source after your other shell configuration.
# The callback receives only command identity. It never reconstructs or runs a command.
& {
    if ($PSVersionTable.PSVersion.Major -lt 7) {
        [Console]::Error.WriteLine('Executable suggestions require PowerShell 7 or newer.')
        return
    }
    $variables = $ExecutionContext.SessionState.PSVariable
    $existing = $variables.Get('global:__openai_suggestions_state')
    if ($null -ne $existing) {
        if ($existing.Value.Active -and $existing.Value.App -cne '__APPNAME__') {
            [Console]::Error.WriteLine('Run openai_suggestions_disable before enabling suggestions for another executable.')
        }
        if ($existing.Value.Active) { return }
    }
    $invoke = $ExecutionContext.InvokeCommand
    foreach ($command in $invoke.GetCommands('openai_suggestions_disable', [System.Management.Automation.CommandTypes]::All, $false)) {
        if ($command.CommandType -eq [System.Management.Automation.CommandTypes]::Function -and
            $null -ne $existing -and [object]::ReferenceEquals($command.ScriptBlock, $existing.Value.Disable)) { break }
        [Console]::Error.WriteLine('Executable suggestions conflict with an existing shell command. Keep the existing handler and use CLI help.')
        return
    }
    # GetCommands performs literal lookup without invoking command-not-found callbacks.
    # Check normal precedence so a shadowing alias or function cannot pose as the CLI.
    $available = $false
    foreach ($command in $invoke.GetCommands('__APPNAME__', [System.Management.Automation.CommandTypes]::All, $false)) {
        $available = $command.CommandType -eq [System.Management.Automation.CommandTypes]::Application -and
            [IO.File]::Exists($command.Path)
        break
    }
    if (-not $available) {
        [Console]::Error.WriteLine('Executable suggestions require __APPNAME__ on PATH. Install the CLI before enabling suggestions.')
        return
    }
    $state = @{
        App = '__APPNAME__'
        Prior = $invoke.CommandNotFoundAction
        Invoke = $invoke
        Variables = $variables
        Active = $true
        Hook = $null
        Disable = $null
    }
    $state.Hook = [System.EventHandler[System.Management.Automation.CommandLookupEventArgs]] {
        param($sender, $eventArgs)
        # Preserve callback ordering, resolution, output, and exception behavior.
        if ($null -ne $state.Prior) {
            $state.Prior.Invoke($sender, $eventArgs)
        }
        if (-not $state.Active -or $null -ne $eventArgs.Command -or $eventArgs.StopSearch) {
            return
        }
        $name = $eventArgs.CommandName
        if ($null -eq $name -or $name.Length -gt 69) { return }
        # Windows users may spell the native executable with its .exe extension.
        # Normalize only this terminal suffix; paths and other suffixes still fail.
        if ($IsWindows -and $null -ne $name -and $name.EndsWith('.exe', [StringComparison]::OrdinalIgnoreCase)) {
            $name = $name.Substring(0, $name.Length - 4)
        }
        if ($name.Length -gt 65 -or $name -cnotmatch '\A[A-Za-z0-9_-]{1,65}\z') {
            return
        }
        $comparison = [StringComparison]::Ordinal
        $regexOptions = [System.Text.RegularExpressions.RegexOptions]::CultureInvariant
        if ($IsWindows) {
            $comparison = [StringComparison]::OrdinalIgnoreCase
            $regexOptions = $regexOptions -bor [System.Text.RegularExpressions.RegexOptions]::IgnoreCase
        }
        if ([string]::Equals($name, '__APPNAME__', $comparison) -or
            -not [regex]::IsMatch($name, '__SUGGESTION_REGEX__', $regexOptions)) { return }
        foreach ($command in $state.Invoke.GetCommands('__APPNAME__', [System.Management.Automation.CommandTypes]::All, $false)) {
            if ($command.CommandType -eq [System.Management.Automation.CommandTypes]::Application -and
                [IO.File]::Exists($command.Path)) {
                # Direct stderr adds no ErrorRecord and does not change LASTEXITCODE.
                [Console]::Error.WriteLine('Command not found. Did you mean: __APPNAME__?')
                [Console]::Error.WriteLine('Keep the remaining arguments unchanged.')
            }
            break
        }
    }.GetNewClosure()
    $state.Disable = {
        $state.Active = $false
        if ([object]::ReferenceEquals($state.Invoke.CommandNotFoundAction, $state.Hook)) {
            $state.Invoke.CommandNotFoundAction = $state.Prior
        }
        # Keep an inert, owned entrypoint for repeated disable and later re-enable.
        # A replacement callback may retain this closure; its saved delegate stays valid.
    }.GetNewClosure()
    Microsoft.PowerShell.Management\Set-Item -LiteralPath Function:global:openai_suggestions_disable -Value $state.Disable
    $variables.Set('global:__openai_suggestions_state', $state)
    $invoke.CommandNotFoundAction = $state.Hook
}
