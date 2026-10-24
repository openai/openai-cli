# Temporary native experiment. Keep raw traces on the runner.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('Record', 'Report')][string]$Mode,
    [Parameter(Mandatory)][string]$OutputDirectory,
    [ValidateRange(30, 180)][int]$Seconds = 90
)
$ErrorActionPreference = 'Stop'
if (-not $IsWindows -or $PSVersionTable.PSVersion.Major -lt 7) {
    throw 'The collector requires native Windows PowerShell 7.'
}
$root = [IO.Path]::GetFullPath($OutputDirectory)
if (-not [IO.Directory]::Exists($root)) { throw 'The caller must create the owned output directory.' }
$tracePath = Join-Path $root 'private-defender.etl'
$statusPath = Join-Path $root 'trace-status.json'
$readyPath = Join-Path $root 'trace-ready.json'
function Save-Record([string]$Path, $Value) {
    $bytes = [Text.Encoding]::UTF8.GetBytes(($Value | ConvertTo-Json -Depth 8 -Compress))
    if ($bytes.Length -gt 1048576) { throw 'Summary exceeds its one-megabyte bound.' }
    $temporary = $Path + '.' + [Guid]::NewGuid().ToString('N') + '.tmp'
    $stream = [IO.File]::Open($temporary, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose() }
    try { [IO.File]::Move($temporary, $Path) } finally {
        if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) }
    }
}
function Read-Record([string]$Path) {
    $file = Get-Item -LiteralPath $Path
    if ($file.Length -gt 1048576) { throw 'Input summary exceeds its bound.' }
    return [IO.File]::ReadAllText($Path) | ConvertFrom-Json -Depth 10
}
function Read-Utc($Value) {
    if ($Value -is [DateTimeOffset]) { return $Value.ToUniversalTime() }
    if ($Value -is [DateTime]) { return [DateTimeOffset]::new($Value.ToUniversalTime()) }
    $parsed = [DateTimeOffset]::Parse([string]$Value, [Globalization.CultureInfo]::InvariantCulture)
    return $parsed.ToUniversalTime()
}
function Read-ScanUtc($Value) {
    # Defender -Raw encodes dates as FILETIME, using 100-nanosecond intervals.
    if ($Value -is [long] -or $Value -is [ulong] -or $Value -is [int]) {
        return [DateTimeOffset]::new([DateTime]::FromFileTimeUtc([long]$Value))
    }
    return Read-Utc $Value
}
function Read-ScanDurationMs($Value) {
    if ($Value -is [TimeSpan]) { $milliseconds = $Value.TotalMilliseconds }
    elseif ($Value -is [long] -or $Value -is [ulong] -or $Value -is [int] -or $Value -is [uint] -or $Value -is [double] -or $Value -is [decimal]) {
        $milliseconds = [decimal]$Value / 10000
    } else { throw 'Scan duration is not numeric.' }
    if ($milliseconds -lt 0 -or $milliseconds -gt 360000) { throw 'Scan duration is outside the observation bound.' }
    return $milliseconds
}
function Directory-Prefix([string]$Path) {
    return [IO.Path]::GetFullPath($Path).TrimEnd([char[]]@([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)) + [IO.Path]::DirectorySeparatorChar
}
function Find-Property($Object, [string[]]$Names) {
    foreach ($name in $Names) {
        if ($Object -is [Collections.IDictionary] -and $Object.Contains($name)) { return $Object[$name] }
        $property = $Object.PSObject.Properties[$name]
        if ($null -ne $property) { return $property.Value }
    }
    return $null
}
function Property-Schema($Object) {
    $names = if ($Object -is [Collections.IDictionary]) { @($Object.Keys) } else { @($Object.PSObject.Properties.Name) }
    foreach ($name in ($names | Select-Object -First 40)) {
        if ([string]$name -notmatch '^[A-Za-z_][A-Za-z0-9_]{0,63}$') { continue }
        $value = Find-Property $Object @([string]$name)
        [ordered]@{ name = [string]$name; type = if ($null -eq $value) { 'null_or_empty' } else { $value.GetType().FullName } }
    }
}
function Trace-State {
    $text = & "$env:SystemRoot\System32\wpr.exe" -status -instancename MSFT_MpPerformanceRecording 2>&1
    $script:lastWprExit = $LASTEXITCODE
    $combined = $text -join "`n"
    if ($LASTEXITCODE -eq 0 -and $combined -match 'recording is in progress') { return 'active' }
    if ($combined -match '(?i)WPR is not recording|There are no (trace )?profiles running') { return 'inactive' }
    return 'unknown'
}
function Toolkit-Inventory {
    foreach ($name in @('wpr.exe', 'wpaexporter.exe', 'xperf.exe', 'tracerpt.exe')) {
        $candidates = [Collections.Generic.List[string]]::new()
        $command = Get-Command $name -CommandType Application -ErrorAction SilentlyContinue
        if ($null -ne $command) { $candidates.Add($command.Source) }
        foreach ($base in @($env:ProgramFiles, ${env:ProgramFiles(x86)})) {
            if ($base) { $candidates.Add((Join-Path $base ('Windows Kits\10\Windows Performance Toolkit\' + $name))) }
        }
        $candidates.Add((Join-Path "$env:SystemRoot\System32" $name))
        $found = $false
        foreach ($candidate in ($candidates | Sort-Object -Unique)) {
            if (-not [IO.File]::Exists($candidate)) { continue }
            $found = $true
            $version = [Diagnostics.FileVersionInfo]::GetVersionInfo($candidate).FileVersion
            [ordered]@{ tool = $name; available = $true; path = $candidate; version = $version }
        }
        if (-not $found) { [ordered]@{ tool = $name; available = $false; path = $null; version = $null } }
    }
}
if ($Mode -eq 'Record') {
    foreach ($path in @($tracePath, $statusPath, $readyPath)) {
        if ([IO.File]::Exists($path)) { throw 'Recording output already exists.' }
    }
    $status = [ordered]@{
        kind = 'defender'; state = 'unavailable'; started_utc = $null; finished_utc = $null
        seconds = $Seconds; ready = $false; error_type = $null; trace_bytes = 0
        ready_utc = $null; recording_returned_utc = $null
        failure_phase = $null; reason = $null; hresult = $null; wpr_exit = $null; recording_job_state = $null
        native_error_code = $null; recorder_cleanup = 'not_started'
        event_loss = 'unavailable'; toolkit = @()
        worker_result = $null
        initial_trace_state = $null; final_trace_state = $null; helper_controls = 'pending'
        powershell_major = $PSVersionTable.PSVersion.Major
        process_architecture = [Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString()
    }
    $recording = $null
    $waitedForRecording = $false
    $phase = 'import_defender_module'
    try {
        $phase = 'verify_collector_helpers'
        $testParent = Directory-Prefix (Join-Path $root 'synthetic-parent')
        $testChild = Directory-Prefix (Join-Path $root 'synthetic-parent\child')
        $testSibling = Directory-Prefix (Join-Path $root 'synthetic-parent-sibling')
        if (-not $testChild.StartsWith($testParent, [StringComparison]::OrdinalIgnoreCase)) { throw 'Child containment control failed.' }
        if ($testSibling.StartsWith($testParent, [StringComparison]::OrdinalIgnoreCase)) { throw 'Sibling rejection control failed.' }
        $syntheticUtc = [DateTimeOffset]::new(2026, 1, 1, 0, 0, 0, [TimeSpan]::Zero)
        if ((Read-ScanUtc $syntheticUtc.UtcDateTime.ToFileTimeUtc()) -ne $syntheticUtc) { throw 'FILETIME conversion control failed.' }
        if ((Read-ScanDurationMs ([long]12345000)) -ne [decimal]1234.5) { throw 'Duration conversion control failed.' }
        $status.helper_controls = 'passed'
        $phase = 'inventory_toolkit'
        $status.toolkit = @(Toolkit-Inventory)
        # Force the current engine. Never launch Windows PowerShell compatibility.
        $phase = 'import_defender_module'
        Import-Module DefenderPerformance -SkipEditionCheck -ErrorAction Stop
        $phase = 'check_timed_recording'
        $command = Get-Command New-MpPerformanceRecording -Module DefenderPerformance -ErrorAction Stop
        if (-not $command.Parameters.ContainsKey('Seconds')) { throw 'Timed Defender recording is unavailable.' }
        $phase = 'locate_wpr'
        if (-not (Test-Path -LiteralPath "$env:SystemRoot\System32\wpr.exe")) { throw 'WPR is unavailable.' }
        $phase = 'check_existing_recording'
        $status.initial_trace_state = Trace-State
        if ($status.initial_trace_state -ne 'inactive') { throw 'The Defender recording state is not confirmed inactive.' }
        $phase = 'start_recording_thread'
        $status.started_utc = [DateTimeOffset]::UtcNow.ToString('O')
        $recording = Start-ThreadJob -ScriptBlock {
            param($Path, $Duration)
            $ErrorActionPreference = 'Stop'
            $result = [ordered]@{
                state = 'failed'; phase = 'import_defender_module'; reason = $null
                error_type = $null; hresult = $null; native_error_code = $null
                recording_requested_utc = $null; recording_returned_utc = $null
            }
            try {
                Import-Module DefenderPerformance -SkipEditionCheck -ErrorAction Stop
                $result.phase = 'record_defender_events'
                $result.recording_requested_utc = [DateTimeOffset]::UtcNow.ToString('O')
                $null = New-MpPerformanceRecording -RecordTo $Path -Seconds $Duration -ErrorAction Stop 3>$null 4>$null 5>$null 6>$null
                $result.recording_returned_utc = [DateTimeOffset]::UtcNow.ToString('O')
                $result.state = 'recorded'
            } catch {
                $result.reason = 'recording_worker_failed'
                $result.error_type = $_.Exception.GetType().FullName
                $result.hresult = $_.Exception.HResult
                if ($_.Exception -is [ComponentModel.Win32Exception]) { $result.native_error_code = $_.Exception.NativeErrorCode }
            }
            [pscustomobject]$result
        } -ArgumentList $tracePath, $Seconds
        $status.recorder_cleanup = 'unconfirmed'
        $phase = 'await_recording_ready'
        $watch = [Diagnostics.Stopwatch]::StartNew()
        while ($watch.Elapsed.TotalSeconds -lt 15) {
            if ($recording.State -in @('Completed', 'Failed', 'Stopped')) { break }
            if ((Trace-State) -eq 'active') {
                $status.ready = $true
                $status.ready_utc = [DateTimeOffset]::UtcNow.ToString('O')
                Save-Record $readyPath ([ordered]@{ kind = 'defender'; ready_utc = $status.ready_utc; seconds = $Seconds })
                break
            }
            Start-Sleep -Milliseconds 100
        }
        if (-not $status.ready) { throw 'The Defender trace never confirmed readiness.' }
        $phase = 'await_recording_completion'
        $waitedForRecording = $true
        $null = Wait-Job -Job $recording -Timeout ($Seconds + 30)
        if ($recording.State -ne 'Completed') { throw 'The Defender recording exceeded its completion bound.' }
        $phase = 'receive_recording_result'
        $returned = @(Receive-Job -Job $recording -ErrorAction Stop)
        $status.worker_result = $returned[-1]
        if ($status.worker_result.state -ne 'recorded') { throw 'The recording worker failed.' }
        $status.recording_returned_utc = $status.worker_result.recording_returned_utc
        $phase = 'check_trace_size'
        $status.trace_bytes = (Get-Item -LiteralPath $tracePath).Length
        if ($status.trace_bytes -gt 134217728) { throw 'The raw trace exceeds its analysis bound.' }
        $status.state = 'recorded'
    } catch {
        $status.error_type = $_.Exception.GetType().FullName
        $status.hresult = $_.Exception.HResult
        if ($_.Exception -is [ComponentModel.Win32Exception]) { $status.native_error_code = $_.Exception.NativeErrorCode }
        $status.failure_phase = $phase
        $status.reason = 'collector_operation_failed'
        $status.wpr_exit = $script:lastWprExit
        $status.state = 'unavailable'
    } finally {
        # The timed cmdlet owns its fixed instance. Never cancel that instance here.
        if ($null -ne $recording) {
            if (-not $waitedForRecording -and $recording.State -notin @('Completed', 'Failed', 'Stopped')) {
                $null = Wait-Job -Job $recording -Timeout ($Seconds + 30)
            }
            $status.recording_job_state = [string]$recording.State
            if ($null -eq $status.worker_result -and $recording.State -eq 'Completed') {
                $workerRecords = @(Receive-Job -Job $recording -ErrorAction SilentlyContinue)
                if ($workerRecords.Count -gt 0) { $status.worker_result = $workerRecords[-1] }
            }
            $status.final_trace_state = Trace-State
            if ($recording.State -eq 'Completed' -and $status.final_trace_state -eq 'inactive') { $status.recorder_cleanup = 'confirmed' }
            if ($recording.State -in @('Completed', 'Failed', 'Stopped')) { Remove-Job -Job $recording }
        }
        $status.finished_utc = [DateTimeOffset]::UtcNow.ToString('O')
        Save-Record $statusPath $status
    }
    exit 0
}

# The caller supplies exact identities from the nested native test records.
$owned = Read-Record (Join-Path $root 'owned-processes.json')
$start = Read-Utc $owned.test_start_utc
$end = Read-Utc $owned.test_end_utc
if ($end -le $start -or ($end - $start).TotalMinutes -gt 6) { throw 'Invalid observation window.' }
$roles = @{}
$roleNames = @('windows_powershell', 'cli', 'console_owner', 'shell_supervisor', 'native_producer', 'test_owner')
foreach ($entry in $owned.processes) {
    if ($entry.role -notin $roleNames -or [long]$entry.pid -le 0) { throw 'Invalid owned process identity.' }
    if ($roles.ContainsKey([string][long]$entry.pid)) { throw 'Duplicate owned PID requires lifetime-aware attribution.' }
    $roles[[string][long]$entry.pid] = [string]$entry.role
}
$tempPrefix = Directory-Prefix ([string]$owned.native_temp_root)
if (-not $tempPrefix.StartsWith((Directory-Prefix $root), [StringComparison]::OrdinalIgnoreCase)) {
    throw 'The native temporary directory is outside the owned directory.'
}
$helperPath = [IO.Path]::GetFullPath([string]$owned.helper_path)
$summary = [ordered]@{
    trace = Read-Record $statusPath
    observation_start_utc = $start.ToString('O'); observation_end_utc = $end.ToString('O')
    scan_report = 'unavailable'; scan_rows = @(); scan_report_truncated = $false
    scan_property_names = @(); engine_log = 'unavailable'; engine_events = @()
    trace_window = 'unverified'; scan_timing_complete = $false
    scan_property_types = @(); scan_report_error_type = $null; scan_report_hresult = $null
    engine_identity = 'pid_only_provisional'
    scan_container_schema = @()
    scan_report_phase = 'not_started'; scan_report_reason = $null
}
try {
    if ($summary.trace.state -eq 'recorded') {
        # Use a conservative endpoint. Recording starts after this request timestamp.
        $earliestEnd = (Read-Utc $summary.trace.started_utc).AddSeconds([int]$summary.trace.seconds)
        if ($start -ge (Read-Utc $summary.trace.ready_utc) -and $end -le $earliestEnd) {
            $summary.trace_window = 'covered'
        } else { $summary.trace_window = 'incomplete' }
        Import-Module DefenderPerformance -SkipEditionCheck -ErrorAction Stop
        $summary.scan_report_phase = 'query_scan_report'
        $report = Get-MpPerformanceReport -Path $tracePath -TopScans 256 -Raw -ErrorAction Stop
        $summary.scan_report_phase = 'validate_scan_schema'
        $summary.scan_container_schema = @(Property-Schema $report)
        $hasScans = ($report -is [Collections.IDictionary] -and $report.Contains('TopScans')) -or $null -ne $report.PSObject.Properties['TopScans']
        if (-not $hasScans) { throw 'The scan report schema is unavailable.' }
        $topScans = Find-Property $report @('TopScans')
        $scans = @($topScans | Where-Object { $null -ne $_ })
        $summary.scan_report_truncated = $scans.Count -ge 256
        $summary.scan_report = 'available'
        $summary.scan_report_phase = 'reduce_owned_scans'
        $safeRows = [Collections.Generic.List[object]]::new()
        foreach ($scan in $scans) {
            if ($null -eq $scan) { continue }
            if ($summary.scan_property_names.Count -eq 0) {
                $summary.scan_property_types = @(Property-Schema $scan)
                $summary.scan_property_names = @($summary.scan_property_types | ForEach-Object { $_.name })
            }
            $processID = Find-Property $scan @('ProcessId', 'ProcessID', 'PID')
            $filePath = Find-Property $scan @('FilePath', 'Path')
            $role = if ($null -ne $processID) { $roles[[string]$processID] } else { $null }
            $fileRole = $null
            if ($filePath -is [string]) {
                $full = [IO.Path]::GetFullPath($filePath)
                if ($full.Equals($helperPath, [StringComparison]::OrdinalIgnoreCase)) { $fileRole = 'test_executable' }
                elseif ($full.StartsWith($tempPrefix, [StringComparison]::OrdinalIgnoreCase)) {
                    $fileRole = if ([IO.Path]::GetFileName($full) -eq 'openai.exe') { 'cli_executable' } else { 'owned_fixture' }
                }
            }
            if ($null -eq $role -and $null -eq $fileRole) { continue }
            $row = [ordered]@{
                process_role = $role; file_role = $fileRole; process_id = $null
                process_identity = 'pid_only_provisional'; file_identity = if ($null -ne $fileRole) { 'exact_owned_path' } else { 'unavailable' }
                start_utc = $null; end_utc = $null; duration_ms = $null
                reason_code = $null; skip_reason_code = $null; scan_type_code = $null
            }
            if ($null -ne $role) { $row.process_id = [long]$processID }
            foreach ($field in @('start', 'end')) {
                $names = if ($field -eq 'start') { @('StartTime', 'ScanStartTime') } else { @('EndTime', 'ScanEndTime') }
                $value = Find-Property $scan $names
                if ($null -ne $value) {
                    try {
                        $instant = Read-ScanUtc $value
                        if ($instant -lt $start.AddMinutes(-6) -or $instant -gt $end.AddMinutes(6)) { throw 'Scan time exceeds observation bounds.' }
                        $row[$field + '_utc'] = $instant.ToString('O')
                    } catch { }
                }
            }
            $duration = Find-Property $scan @('Duration', 'ScanDuration')
            try { $row.duration_ms = Read-ScanDurationMs $duration } catch { }
            foreach ($item in @(
                @{ key = 'reason_code'; names = @('Reason', 'ScanReason') },
                @{ key = 'skip_reason_code'; names = @('SkipReason', 'ScanSkipReason') },
                @{ key = 'scan_type_code'; names = @('ScanType', 'Type') }
            )) {
                $code = Find-Property $scan $item.names
                if ($code -is [int] -or $code -is [long] -or $code -is [uint] -or $code -is [Enum]) {
                    $row[$item.key] = [long]$code
                }
            }
            $safeRows.Add($row)
        }
        $summary.scan_rows = $safeRows.ToArray()
        $summary.scan_timing_complete = $safeRows.Count -gt 0 -and @($safeRows | Where-Object {
            $null -eq $_.start_utc -or $null -eq $_.end_utc -or $null -eq $_.duration_ms
        }).Count -eq 0
    }
} catch {
    $summary.scan_report = 'unavailable'
    $summary.scan_report_error_type = $_.Exception.GetType().FullName
    $summary.scan_report_hresult = $_.Exception.HResult
    $summary.scan_report_reason = 'scan_report_failed'
}
$shellIDs = @($owned.processes | Where-Object role -eq 'windows_powershell' | ForEach-Object { [long]$_.pid })
$engineQueries = [Collections.Generic.List[object]]::new()
$engineEvents = [Collections.Generic.List[object]]::new()
foreach ($specification in @(
    @{ channel = 'Windows PowerShell'; provider = 'PowerShell'; ids = @(400, 403) },
    @{ channel = 'Microsoft-Windows-PowerShell/Operational'; provider = 'Microsoft-Windows-PowerShell'; ids = @(40961, 40962) }
)) {
    $query = [ordered]@{
        channel = $specification.channel; provider = $specification.provider; state = 'unavailable'
        phase = 'check_existing_engine_log'; reason = $null; error_type = $null; hresult = $null
        matched_events = 0; usable_pid_events = 0; attributed_events = 0
    }
    try {
        $log = Get-WinEvent -ListLog $specification.channel -ErrorAction Stop
        if (-not $log.IsEnabled) { $query.reason = 'existing_log_disabled'; continue }
        $query.phase = 'query_engine_events'
        $eventErrors = @()
        $events = @(Get-WinEvent -FilterHashtable @{
            LogName = $specification.channel; ProviderName = $specification.provider; Id = $specification.ids
            StartTime = $start.UtcDateTime; EndTime = $end.UtcDateTime
        } -MaxEvents 256 -ErrorAction SilentlyContinue -ErrorVariable eventErrors)
        $unexpectedErrors = @($eventErrors | Where-Object FullyQualifiedErrorId -notlike 'NoMatchingEventsFound*')
        if ($unexpectedErrors.Count -gt 0) { throw $unexpectedErrors[0] }
        $query.state = if ($events.Count -ge 256) { 'truncated' } else { 'available' }
        $query.matched_events = $events.Count
        foreach ($event in $events) {
            if ($event.ProviderName -ne $specification.provider) { continue }
            if ($null -eq $event.ProcessId -or [long]$event.ProcessId -le 0) { continue }
            $query.usable_pid_events++
            if ([long]$event.ProcessId -notin $shellIDs) { continue }
            $query.attributed_events++
            $engineEvents.Add([ordered]@{
                channel = $specification.channel; provider = $specification.provider
                event_id = $event.Id; process_id = $event.ProcessId; utc = $event.TimeCreated.ToUniversalTime().ToString('O')
            })
        }
        if ($query.state -eq 'available' -and $query.attributed_events -eq 0) {
            $query.state = 'available_without_attributed_events'
        }
    } catch {
        $query.reason = 'engine_event_query_failed'
        $query.error_type = $_.Exception.GetType().FullName
        $query.hresult = $_.Exception.HResult
    } finally { $engineQueries.Add($query) }
}
$summary.engine_log = 'see_per_channel_status'
$summary.engine_queries = $engineQueries.ToArray()
$summary.engine_events = $engineEvents.ToArray()
Save-Record (Join-Path $root 'startup-system-summary.json') $summary
# The caller retains only the reviewed summary and exact native test evidence.
if (Test-Path -LiteralPath $tracePath) { Remove-Item -LiteralPath $tracePath -Force }
