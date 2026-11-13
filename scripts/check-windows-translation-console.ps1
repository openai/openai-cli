# Call Confirm-757ConsoleResult with go test -json records and the go test exit code.
function Confirm-757ConsoleResult {
    param([object[]]$Events, [int]$GoExit)
    $pkg = 'github.com/openai/openai-cli/cmd/openai'
    $suite = 'TestWindows757ShippingTranslation'
    $cases = 'microphone', 'file', 'typed', 'opening', 'retry401', 'retry403', 'exit401', 'cancel'
    $alternatives = $cases -join '|'
    $records = @{}
    foreach ($case in $cases) { $records[$case] = @{ receipt = 0; pass = 0; fail = 0; skip = 0 } }
    $codes = [Collections.Generic.HashSet[string]]::new()
    $known = @{
        'console child required watchdog termination' = 'OBSERVER_WATCHDOG'
        'terminated console child could not be reaped' = 'OBSERVER_REAP'
        'console observer child did not exit successfully' = 'OBSERVER_EXIT'
        'ConPTY close exceeded its watchdog' = 'CONPTY_CLOSE_TIMEOUT'
        'ConPTY output drain failed' = 'CONPTY_DRAIN_ERROR'
        'ConPTY output did not close after teardown' = 'CONPTY_DRAIN_TIMEOUT'
        'synthetic key bytes appeared in console output' = 'CAPTURE_REDACTION'
        'console output exceeded the capture budget; privacy evidence is incomplete' = 'CAPTURE_LIMIT'
        'owned console worker could not be reaped' = 'WORKER_REAP'
        'console worker watchdog terminated the owned job' = 'WORKER_WATCHDOG'
    }
    $suitePass = 0
    $packagePass = 0
    $unexpectedStatus = $false
    foreach ($event in $Events) {
        if ($event.Package -cne $pkg) { continue }
        if ($event.Action -ceq 'fail' -or $event.Action -ceq 'skip') { $unexpectedStatus = $true }
        if ($event.Action -ceq 'pass' -and -not $event.Test) { $packagePass++ }
        if ($event.Test -cne $suite) { continue }
        if ($event.Action -ceq 'pass') { $suitePass++ }
        if ($event.Action -cne 'output') { continue }
        foreach ($line in ([string]$event.Output -split '\r?\n')) {
            # t.Log(workerOutput) prefixes the first nested line with the controller location.
            $line = $line -creplace '^\s*translation757_windows_test\.go:\d+:\s*(?=--- (?:PASS|FAIL|SKIP):)', ''
            if ($line -cmatch "^\s*--- (PASS|FAIL|SKIP): ${suite}/($alternatives) \([0-9]+(?:\.[0-9]+)?s\)\s*$") {
                $records[$Matches[2]][$Matches[1].ToLowerInvariant()]++
                continue
            }
            if ($line -cmatch '^\s*translation757_windows_test\.go:\d+: PASS757 ([a-z0-9]+)\s*$') {
                if ($cases -ccontains $Matches[1]) { $records[$Matches[1]].receipt++ }
                else { $unexpectedStatus = $true }
                continue
            }
            if ($line -cnotmatch '^\s*(?:translation757_windows_test\.go|main_admin_setup_console_windows_test\.go|testing\.go):\d+: (.+)$') { continue }
            $message = $Matches[1]
            if ($known.ContainsKey($message)) { [void]$codes.Add($known[$message]) }
            elseif ($message.StartsWith('TempDir RemoveAll cleanup: ', [StringComparison]::Ordinal)) { [void]$codes.Add('TEMP_DIR_CLEANUP') }
            elseif ($message -cmatch '^native console worker failed: exit status [0-9]+$') { [void]$codes.Add('WORKER_EXIT') }
        }
    }
    $passed = $GoExit -eq 0 -and $suitePass -eq 1 -and $packagePass -eq 1 -and -not $unexpectedStatus -and $codes.Count -eq 0
    foreach ($name in $cases) {
        $r = $records[$name]
        $receipt = if ($r.receipt -eq 1) { 'ONE' } elseif ($r.receipt -eq 0) { 'MISSING' } else { 'DUPLICATE' }
        $result = if ($r.fail -gt 0) { 'FAIL' } elseif ($r.skip -gt 0) { 'SKIP' }
            elseif ($r.pass -eq 1) { 'PASS' } elseif ($r.pass -eq 0) { 'MISSING' } else { 'DUPLICATE' }
        if ($receipt -cne 'ONE' -or $result -cne 'PASS') { $passed = $false }
        Write-Output "CONSOLE757 case=$name receipt=$receipt result=$result"
    }
    foreach ($code in ($codes | Sort-Object)) { Write-Output "CONSOLE757 reason=$code" }
    if (-not $passed) {
        if (!$codes.Count) { Write-Output 'CONSOLE757 reason=UNCLASSIFIED' }
        throw 'CONSOLE757 gate=FAIL'
    }
    Write-Output 'CONSOLE757 gate=PASS'
}
