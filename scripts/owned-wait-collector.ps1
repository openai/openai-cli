# Temporary experiment. Raw ETL, XML, and native-tool output remain private.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('Record', 'Export', 'Cleanup')][string]$Mode,
    [Parameter(Mandatory)][string]$OutputDirectory,
    [string]$ProfilePath
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows -or $PSVersionTable.PSVersion.Major -lt 7) {
    throw 'Native Windows PowerShell 7 is required.'
}
$root = [IO.Path]::GetFullPath($OutputDirectory)
if (-not [IO.Directory]::Exists($root)) { throw 'The caller must create the owned directory.' }
if (([IO.File]::GetAttributes($root) -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    throw 'The owned directory cannot be a reparse point.'
}
$privateTrace = Join-Path $root 'private-wait.etl'
$privateXml = Join-Path $root 'private-wait.xml'
$commands = [Collections.Generic.List[object]]::new()
$phase = 'prepare'
$status = [ordered]@{
    state = 'failed'; failure_phase = $null; error_type = $null; hresult = $null
    native_error_code = $null; reason = $null; commands = $commands; tools = @()
    started_utc = [DateTime]::UtcNow.ToString('O'); finished_utc = $null
}

function Save-Record([string]$Name, $Value) {
    if ($Name -notmatch '^[a-z0-9-]+\.json$') { throw 'Invalid summary name.' }
    $path = Join-Path $root $Name
    $temporary = $path + '.' + [Guid]::NewGuid().ToString('N') + '.tmp'
    $bytes = [Text.Encoding]::UTF8.GetBytes(($Value | ConvertTo-Json -Depth 12 -Compress))
    if ($bytes.Length -gt 1048576) { throw 'Summary exceeds its byte limit.' }
    $stream = [IO.File]::Open($temporary, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose() }
    try { [IO.File]::Move($temporary, $path) } finally {
        if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) }
    }
}

function Read-Record([string]$Name) {
    $path = Join-Path $root $Name
    if (([IO.FileInfo]::new($path)).Length -gt 1048576) { throw 'Input summary exceeds its byte limit.' }
    return [IO.File]::ReadAllText($path) | ConvertFrom-Json -Depth 12
}

function File-Bytes([string]$Path) {
    if (-not [IO.File]::Exists($Path)) { return [long]0 }
    return ([IO.FileInfo]::new($Path)).Length
}

function File-Digest([string]$Path) {
    $stream = [IO.File]::OpenRead($Path)
    $hash = [Security.Cryptography.SHA256]::Create()
    try { return [Convert]::ToHexString($hash.ComputeHash($stream)).ToLowerInvariant() }
    finally { $hash.Dispose(); $stream.Dispose() }
}

function Read-NativeText([string]$Path) {
    if ((File-Bytes $Path) -gt 1048576) { throw 'Native output exceeds its byte limit.' }
    $bytes = [IO.File]::ReadAllBytes($Path)
    if ($bytes.Length -ge 2 -and $bytes[0] -eq 255 -and $bytes[1] -eq 254) {
        return [Text.Encoding]::Unicode.GetString($bytes, 2, $bytes.Length - 2)
    }
    if ($bytes.Length -ge 4 -and $bytes[1] -eq 0 -and $bytes[3] -eq 0) {
        return [Text.Encoding]::Unicode.GetString($bytes)
    }
    return [Text.Encoding]::UTF8.GetString($bytes)
}

function Tool-Identity([string]$Name, [string]$Path) {
    if (-not [IO.File]::Exists($Path)) { Stop-Collection 'preinstalled_tool_unavailable' }
    return [ordered]@{
        name = $Name; version = [Diagnostics.FileVersionInfo]::GetVersionInfo($Path).FileVersion
        sha256 = File-Digest $Path
    }
}

function Invoke-Tool([string]$Label, [string]$Tool, [string[]]$Arguments,
                     [int]$Seconds, [string]$WatchedPath = '', [long]$WatchedLimit = 0) {
    if ($Label -notmatch '^[a-z0-9-]+$') { throw 'Invalid command label.' }
    $outPath = Join-Path $root ('private-' + $Label + '.stdout')
    $errPath = Join-Path $root ('private-' + $Label + '.stderr')
    $stdout = $null; $stderr = $null; $process = $null; $started = $false
    $result = [ordered]@{
        label = $Label; exit_code = $null; timed_out = $false; byte_limit = $false
        elapsed_ms = 0; stdout_bytes = 0; stderr_bytes = 0; watched_bytes = 0
        error_type = $null; hresult = $null; native_error_code = $null; reported_error_codes = @()
    }
    $watch = [Diagnostics.Stopwatch]::StartNew()
    try {
        $stdout = [IO.File]::Open($outPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
        $stderr = [IO.File]::Open($errPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
        $info = [Diagnostics.ProcessStartInfo]::new($Tool)
        $info.WorkingDirectory = $root
        $info.UseShellExecute = $false
        $info.CreateNoWindow = $true
        $info.RedirectStandardOutput = $true
        $info.RedirectStandardError = $true
        foreach ($argument in $Arguments) { $info.ArgumentList.Add($argument) }
        $process = [Diagnostics.Process]::new()
        $process.StartInfo = $info
        $started = $process.Start()
        if (-not $started) { throw 'The native command did not start.' }
        $copies = [Threading.Tasks.Task[]]@(
            $process.StandardOutput.BaseStream.CopyToAsync($stdout),
            $process.StandardError.BaseStream.CopyToAsync($stderr)
        )
        while (-not $process.WaitForExit(50)) {
            $result.timed_out = $watch.Elapsed.TotalSeconds -ge $Seconds
            $result.byte_limit = (File-Bytes $outPath) -gt 1048576 -or (File-Bytes $errPath) -gt 1048576
            if ($WatchedPath -and (File-Bytes $WatchedPath) -gt $WatchedLimit) { $result.byte_limit = $true }
            if ($result.timed_out -or $result.byte_limit) {
                $process.Kill($true)
                break
            }
        }
        if (-not $process.WaitForExit(5000)) { throw 'The owned native command did not exit.' }
        if (-not [Threading.Tasks.Task]::WaitAll($copies, 5000)) { throw 'The owned command output did not close.' }
        $stdout.Flush(); $stderr.Flush()
        $stdout.Dispose(); $stdout = $null
        $stderr.Dispose(); $stderr = $null
        $result.timed_out = $result.timed_out -or $watch.Elapsed.TotalSeconds -ge $Seconds
        $result.exit_code = $process.ExitCode
        $result.stdout_bytes = File-Bytes $outPath
        $result.stderr_bytes = File-Bytes $errPath
        if ($WatchedPath) { $result.watched_bytes = File-Bytes $WatchedPath }
        if ($result.stdout_bytes -gt 1048576 -or $result.stderr_bytes -gt 1048576) { $result.byte_limit = $true }
        if ($WatchedPath -and $result.watched_bytes -gt $WatchedLimit) { $result.byte_limit = $true }
        if (-not $result.byte_limit) {
            $codes = [Text.RegularExpressions.Regex]::Matches(
                ((Read-NativeText $outPath) + "`n" + (Read-NativeText $errPath)),
                '(?im)(?:error(?:\s+code)?|hresult)\s*[:=]\s*(0x[0-9a-f]{8})')
            $result.reported_error_codes = @($codes | ForEach-Object { $_.Groups[1].Value.ToLowerInvariant() } | Select-Object -Unique -First 8)
        }
    } catch {
        $failure = $_.Exception
        while ($null -ne $failure.InnerException) { $failure = $failure.InnerException }
        $result.error_type = $failure.GetType().FullName
        $result.hresult = $failure.HResult
        if ($failure -is [ComponentModel.Win32Exception]) { $result.native_error_code = $failure.NativeErrorCode }
    } finally {
        if ($started -and $null -ne $process -and -not $process.HasExited) {
            try { $process.Kill($true); $null = $process.WaitForExit(5000) } catch { }
        }
        if ($null -ne $stdout) { $stdout.Dispose() }
        if ($null -ne $stderr) { $stderr.Dispose() }
        if ($null -ne $process) { $process.Dispose() }
        $result.elapsed_ms = [Math]::Round($watch.Elapsed.TotalMilliseconds, 3)
        $commands.Add($result)
    }
    return $result
}

function Require-Success($Result) {
    if ($Result.exit_code -ne 0 -or $Result.timed_out -or $Result.byte_limit -or $null -ne $Result.error_type) {
        $status.reason = 'bounded_native_command_failed'
        throw 'The bounded native command failed.'
    }
}

function Stop-Collection([string]$Reason) {
    if ($Reason -notmatch '^[a-z_]{1,80}$') { throw 'Invalid collection reason.' }
    $status.reason = $Reason
    throw 'Collection failed in the recorded phase.'
}

function Private-Text([string]$Label) {
    $outPath = Join-Path $root ('private-' + $Label + '.stdout')
    $errPath = Join-Path $root ('private-' + $Label + '.stderr')
    if ((File-Bytes $outPath) -gt 1048576 -or (File-Bytes $errPath) -gt 1048576) { throw 'Native output exceeds its byte limit.' }
    return (Read-NativeText $outPath) + "`n" + (Read-NativeText $errPath)
}

function Session-State([string]$Label) {
    $result = Invoke-Tool $Label $wpr @('-status', '-instancename', $instance) 15
    $result.session_state = 'unknown'
    if ($result.timed_out -or $result.byte_limit -or $null -ne $result.error_type) { return 'unknown' }
    $text = Private-Text $Label
    if ($result.exit_code -eq 0 -and $text -match '(?i)recording is in progress') { $result.session_state = 'active'; return 'active' }
    if ($text -match '(?i)WPR is not recording|There are no (trace )?profiles running') { $result.session_state = 'inactive'; return 'inactive' }
    return 'unknown'
}

function Failure-Record($Failure) {
    $status.failure_phase = $phase
    $exception = $Failure.Exception
    while ($null -ne $exception.InnerException) { $exception = $exception.InnerException }
    $status.error_type = $exception.GetType().FullName
    $status.hresult = $exception.HResult
    if ($null -eq $status.reason) { $status.reason = 'exception_in_named_phase' }
    if ($exception -is [ComponentModel.Win32Exception]) { $status.native_error_code = $exception.NativeErrorCode }
}

if ($Mode -eq 'Cleanup') {
    $status.cleanup = 'unconfirmed'
    try {
        $phase = 'verify_persisted_ownership'
        $confirmed = [IO.File]::Exists((Join-Path $root 'wait-trace-owner.json'))
        $owner = if ($confirmed) { Read-Record 'wait-trace-owner.json' } else { Read-Record 'wait-trace-request.json' }
        $status.ownership_evidence = if ($confirmed) { 'confirmed_start' } else { 'pending_start' }
        if ($owner.start_attempted -ne $true -or $owner.initially_inactive -ne $true -or
            $owner.instance -notmatch '^OpenAIStartup_[a-f0-9]{32}$' -or
            $owner.recorder_pid -le 0 -or $owner.profile_sha256 -notmatch '^[a-f0-9]{64}$') {
            $status.reason = 'invalid_ownership_record'
            throw 'Confirmed owned start is required.'
        }
        if ($confirmed -and $owner.start_confirmed -ne $true) { throw 'The confirmed ownership record is invalid.' }
        $instance = [string]$owner.instance
        $status.instance = $instance
        $wpr = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\Windows Performance Toolkit\wpr.exe'
        $status.tools = @(Tool-Identity 'wpr' $wpr)
        $phase = 'inspect_owned_session'
        $current = Session-State 'external-cleanup-status'
        if ($current -eq 'active') {
            if (-not $confirmed) { $status.ownership_evidence = 'pending_start_newly_active' }
            $phase = 'cancel_owned_session'
            $cancel = Invoke-Tool 'external-cancel-owned' $wpr @('-cancel', '-instancename', $instance) 15
            Require-Success $cancel
            $current = Session-State 'external-cancelled-status'
        }
        if ($current -ne 'inactive') {
            $status.reason = 'owned_session_state_unknown'
            throw 'The owned session cleanup is unconfirmed.'
        }
        $status.cleanup = 'confirmed'
        $status.state = 'cleaned'
    } catch { Failure-Record $_ }
    finally {
        $status.finished_utc = [DateTime]::UtcNow.ToString('O')
        Save-Record 'wait-cleanup-status.json' $status
    }
    if ($status.cleanup -ne 'confirmed') { exit 1 }
    exit 0
}

if ($Mode -eq 'Record') {
    $instance = 'OpenAIStartup_' + [Guid]::NewGuid().ToString('N')
    $wpr = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\Windows Performance Toolkit\wpr.exe'
    $owned = $false
    $startAttempted = $false
    $initiallyInactive = $false
    $status.instance = $instance
    $status.cleanup = 'not_started'
    $status.recording_start_utc = $null
    $status.recording_stop_utc = $null
    $status.ready_utc = $null
    $status.stop_requested = $false
    $status.events_lost = $null
    $status.loss_reported_collectors = 0
    $status.loss_metadata = 'unavailable'
    $status.stack_metadata = 'unavailable'
    $status.reported_stack_categories = @()
    $status.missing_stack_categories = @()
    $status.recording_window_expired = $false
    $status.evidence_acceptance = 'pending_reducer'
    $status.trace_bytes = 0
    $status.trace_sha256 = $null
    try {
        foreach ($name in @('wait-trace-ready.json', 'wait-trace-request.json', 'wait-trace-owner.json', 'wait-trace-status.json', 'wait-trace-stop.json', 'private-wait.etl')) {
            if ([IO.File]::Exists((Join-Path $root $name))) { throw 'An experiment output already exists.' }
        }
        $phase = 'prepare_profile'
        $profile = [IO.Path]::GetFullPath($ProfilePath)
        if (-not [IO.File]::Exists($profile)) { Stop-Collection 'recording_profile_unavailable' }
        $status.profile_sha256 = File-Digest $profile
        $status.tools = @(Tool-Identity 'wpr' $wpr)
        $validation = Invoke-Tool 'profile-validation' $wpr @('-profiles', $profile) 15
        Require-Success $validation
        if ((Private-Text 'profile-validation') -notmatch '\bOwnedStartup\b') { Stop-Collection 'expected_profile_absent' }
        $phase = 'prepare_controls'
        Add-Type -TypeDefinition @'
using System;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Threading;
public sealed class OwnedWaitControl {
    public string kind;
    public uint pid;
    public uint thread_id;
    public string start_utc;
    public string end_utc;
    public long start_qpc;
    public long end_qpc;
    public long qpc_frequency;
}
public static class OwnedWaitNative {
    [DllImport("kernel32.dll")] public static extern uint GetCurrentProcessId();
    [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
    [DllImport("kernel32.dll")] static extern bool QueryPerformanceCounter(out long value);
    [DllImport("kernel32.dll")] static extern bool QueryPerformanceFrequency(out long value);
    public static OwnedWaitControl Run(bool sleep) {
        var value = new OwnedWaitControl();
        value.kind = sleep ? "sleep" : "cpu";
        value.pid = GetCurrentProcessId();
        value.thread_id = GetCurrentThreadId();
        if (!QueryPerformanceFrequency(out value.qpc_frequency)) throw new InvalidOperationException();
        value.start_utc = DateTime.UtcNow.ToString("O");
        if (!QueryPerformanceCounter(out value.start_qpc)) throw new InvalidOperationException();
        if (sleep) Thread.Sleep(200);
        else { var clock = Stopwatch.StartNew(); while (clock.ElapsedMilliseconds < 200) { Thread.SpinWait(100); } }
        if (!QueryPerformanceCounter(out value.end_qpc)) throw new InvalidOperationException();
        value.end_utc = DateTime.UtcNow.ToString("O");
        if (value.thread_id != GetCurrentThreadId()) throw new InvalidOperationException();
        return value;
    }
}
'@
        $phase = 'initial_session_state'
        if ((Session-State 'initial-status') -ne 'inactive') { Stop-Collection 'initial_instance_not_inactive' }
        $initiallyInactive = $true
        $phase = 'start_recording'
        Save-Record 'wait-trace-request.json' ([ordered]@{
            instance = $instance; start_attempted = $true; initially_inactive = $true
            recorder_pid = $PID; profile_sha256 = $status.profile_sha256
            requested_utc = [DateTime]::UtcNow.ToString('O')
        })
        $startAttempted = $true
        $status.recording_start_utc = [DateTime]::UtcNow.ToString('O')
        $start = Invoke-Tool 'start' $wpr @('-start', ($profile + '!OwnedStartup'), '-instancename', $instance) 15
        Require-Success $start
        $owned = $true
        $status.cleanup = 'pending'
        Save-Record 'wait-trace-owner.json' ([ordered]@{
            instance = $instance; start_attempted = $true; start_confirmed = $true; initially_inactive = $true
            recorder_pid = $PID; profile_sha256 = $status.profile_sha256
            confirmed_utc = [DateTime]::UtcNow.ToString('O')
        })
        $phase = 'confirm_recording'
        if ((Session-State 'active-status') -ne 'active') { Stop-Collection 'owned_recording_not_active' }
        $collectors = Invoke-Tool 'active-collectors' $wpr @('-status', 'collectors', '-details', '-instancename', $instance) 15
        if ($collectors.exit_code -eq 0 -and -not $collectors.timed_out -and -not $collectors.byte_limit -and $null -eq $collectors.error_type) {
            $collectorText = Private-Text 'active-collectors'
            $stackMatch = [Text.RegularExpressions.Regex]::Match($collectorText, '(?is)System Stacks(?<stacks>.*?)(?:Collector Name|Providers|$)')
            $status.stack_metadata = 'unrecognized'
            if ($stackMatch.Success) {
                foreach ($stack in @('CSwitch', 'ReadyThread', 'SampledProfile')) {
                    if ($stackMatch.Groups['stacks'].Value -match ('(?m)^\s*' + $stack + '\s*$')) {
                        $status.reported_stack_categories += $stack
                    } else { $status.missing_stack_categories += $stack }
                }
                $status.stack_metadata = if ($status.missing_stack_categories.Count -eq 0) { 'complete' } else { 'partial' }
            }
        }
        $phase = 'run_controls'
        $controls = @([OwnedWaitNative]::Run($false), [OwnedWaitNative]::Run($true))
        $status.ready_utc = [DateTime]::UtcNow.ToString('O')
        $self = [Diagnostics.Process]::GetCurrentProcess()
        try { $createdUtc = $self.StartTime.ToUniversalTime().ToString('O') } finally { $self.Dispose() }
        Save-Record 'wait-trace-ready.json' ([ordered]@{
            ready = $true; instance = $instance; ready_utc = $status.ready_utc
            recorder_pid = $PID; recorder_created_utc = $createdUtc
            controls = $controls; profile_sha256 = $status.profile_sha256
        })
        $phase = 'wait_for_original_controls'
        $deadline = [Diagnostics.Stopwatch]::StartNew()
        $stopPath = Join-Path $root 'wait-trace-stop.json'
        while (-not [IO.File]::Exists($stopPath) -and $deadline.Elapsed.TotalSeconds -lt 45) {
            [Threading.Thread]::Sleep(25)
        }
        $status.stop_requested = [IO.File]::Exists($stopPath)
        $status.recording_window_expired = -not $status.stop_requested
        $phase = 'collect_final_status'
        $finalCollectors = Invoke-Tool 'final-collectors' $wpr @('-status', 'collectors', '-details', '-instancename', $instance) 15
        if ($finalCollectors.exit_code -eq 0 -and -not $finalCollectors.timed_out -and -not $finalCollectors.byte_limit -and $null -eq $finalCollectors.error_type) {
            $losses = [Text.RegularExpressions.Regex]::Matches((Private-Text 'final-collectors'), '(?im)^\s*Events Lost\s*:\s*([0-9]+)\s*$')
            $status.loss_reported_collectors = $losses.Count
            if ($losses.Count -gt 0) {
                $status.events_lost = [long]0
                foreach ($loss in $losses) { $status.events_lost += [long]$loss.Groups[1].Value }
                $status.loss_metadata = 'available'
            } else { $status.loss_metadata = 'unrecognized' }
        }
        $phase = 'stop_recording'
        $stop = Invoke-Tool 'stop' $wpr @('-stop', $privateTrace, '-skipPdbGen', '-instancename', $instance) 60 $privateTrace (256MB)
        $status.recording_stop_utc = [DateTime]::UtcNow.ToString('O')
        Require-Success $stop
        $phase = 'confirm_cleanup'
        if ((Session-State 'stopped-status') -ne 'inactive') { Stop-Collection 'owned_cleanup_unconfirmed' }
        $owned = $false
        $status.cleanup = 'confirmed'
        $status.trace_bytes = File-Bytes $privateTrace
        if ($status.trace_bytes -le 0 -or $status.trace_bytes -gt 256MB) { Stop-Collection 'invalid_trace_byte_count' }
        $status.trace_sha256 = File-Digest $privateTrace
        $status.state = 'recorded'
    } catch { Failure-Record $_ }
    finally {
        if ($startAttempted -and $initiallyInactive -and $status.cleanup -ne 'confirmed') {
            try {
                $cleanupState = Session-State 'cleanup-status'
                # This random name was inactive before our only start attempt.
                if ($cleanupState -eq 'active') { $owned = $true }
                if ($owned -and $cleanupState -ne 'inactive') {
                    $cancel = Invoke-Tool 'cancel-owned' $wpr @('-cancel', '-instancename', $instance) 15
                    Require-Success $cancel
                    $cleanupState = Session-State 'cancelled-status'
                }
                $status.cleanup = if ($cleanupState -eq 'inactive') { 'confirmed' } else { 'unconfirmed' }
            } catch { $status.cleanup = 'unconfirmed' }
        }
        if ($status.cleanup -eq 'unconfirmed') { $status.state = 'failed' }
        $status.finished_utc = [DateTime]::UtcNow.ToString('O')
        Save-Record 'wait-trace-status.json' $status
    }
    if ($status.state -ne 'recorded') { exit 1 }
    exit 0
}

try {
    $phase = 'verify_recording'
    $recording = Read-Record 'wait-trace-status.json'
    if ($recording.state -ne 'recorded' -or $recording.cleanup -ne 'confirmed') { throw 'The recording is incomplete.' }
    if ((File-Bytes $privateTrace) -le 0 -or (File-Bytes $privateTrace) -gt 256MB) { throw 'The trace has an invalid byte count.' }
    if ((File-Digest $privateTrace) -ne $recording.trace_sha256) { throw 'The trace identity changed.' }
    foreach ($name in @('private-wait.xml', 'private-wait-summary.txt', 'wait-export-status.json')) {
        if ([IO.File]::Exists((Join-Path $root $name))) { throw 'An export output already exists.' }
    }
    $phase = 'prepare_export'
    $tracerpt = Join-Path $env:SystemRoot 'System32\tracerpt.exe'
    $status.tools = @(Tool-Identity 'tracerpt' $tracerpt)
    $phase = 'export_xml'
    $xmlExport = Invoke-Tool 'xml-export' $tracerpt @($privateTrace, '-of', 'XML', '-o', $privateXml, '-rts', '-y') 45 $privateXml (512MB)
    Require-Success $xmlExport
    if ((File-Bytes $privateXml) -le 0) { throw 'The XML export is empty.' }
    $status.xml_bytes = File-Bytes $privateXml
    $status.xml_sha256 = File-Digest $privateXml
    $phase = 'export_summary'
    $summaryPath = Join-Path $root 'private-wait-summary.txt'
    $summaryExport = Invoke-Tool 'summary-export' $tracerpt @($privateTrace, '-of', 'XML', '-o', 'NUL', '-summary', $summaryPath, '-y') 45 $summaryPath (1MB)
    Require-Success $summaryExport
    $status.summary_bytes = File-Bytes $summaryPath
    $status.state = 'exported'
} catch { Failure-Record $_ }
finally {
    $status.finished_utc = [DateTime]::UtcNow.ToString('O')
    Save-Record 'wait-export-status.json' $status
}
if ($status.state -ne 'exported') { exit 1 }
