//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

// Call this only after ordinary enrollment under the disposable standard account.
func verifyWindowsOrdinaryUpdateAdmission(t *testing.T, managed string) {
	t.Helper()
	requests, env := windowsUpdateAPI(t, os.Environ())
	attempts := make(chan string, 8)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case attempts <- req.Method + " " + req.Host:
		default:
		}
		http.Error(w, "offline fixture", http.StatusServiceUnavailable)
	}))
	defer proxy.Close()
	env = append(env, "HTTPS_PROXY="+proxy.URL, "HTTP_PROXY="+proxy.URL, "NO_PROXY=localhost,127.0.0.1")
	state := filepath.Join(filepath.Dir(managed), ".update", "state.json")
	machine := exec.CommandContext(t.Context(), managed, "models", "retrieve", "fixture-model", "--format", "json")
	machine.Env = env
	out, err := machine.Output()
	if err != nil || !json.Valid(out) || !strings.Contains(string(out), "fixture-model") {
		t.Fatalf("redirected JSON API command: %v", err)
	}
	seed, err := os.ReadFile(state)
	var initial struct{ Checked, Starting int64 }
	if err != nil && !errors.Is(err, os.ErrNotExist) || err == nil && (json.Unmarshal(seed, &initial) != nil || initial.Checked != 0 || initial.Starting != 0) {
		t.Fatal("machine command or setup launched the updater", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	runWindowsUpdateConsole(t, ctx, managed, t.TempDir(), env)
	select {
	case attempt := <-attempts:
		if attempt != "CONNECT api.github.com:443" {
			t.Fatalf("unexpected release request: %s", attempt)
		}
	case <-ctx.Done():
		t.Fatal("ordinary API command never attempted release discovery")
	}
	raw, err := os.ReadFile(state)
	var receipt struct {
		Checked int64 `json:"checked"`
	}
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.Checked <= 0 || requests.Load() != 2 {
		t.Fatal("missing automatic update receipt or incorrect foreground API request count", err)
	}
}

// Only explicitly whitelisted system paths can reach the real CLI and its
// worker. API traffic stays on loopback in both fixture and public runs.
func windowsUpdateAPI(t *testing.T, environment []string) (*atomic.Int32, []string) {
	t.Helper()
	var requests atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" || req.URL.Path != "/v1/models/fixture-model" {
			http.Error(w, "unexpected fixture request", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"fixture-model","object":"model","created":1,"owned_by":"fixture"}`)
	}))
	t.Cleanup(api.Close)

	var env []string
	allowed := map[string]bool{"SYSTEMROOT": true, "WINDIR": true, "PATH": true, "PATHEXT": true, "USERPROFILE": true, "LOCALAPPDATA": true, "APPDATA": true, "TEMP": true, "TMP": true, "COMSPEC": true, "USERNAME": true, "USERDOMAIN": true}
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if ok && allowed[strings.ToUpper(key)] {
			env = append(env, entry)
		}
	}
	env = append(env, "OPENAI_API_KEY=sk-fake-windows-fixture", "OPENAI_BASE_URL="+api.URL+"/v1")
	return &requests, env
}

func runWindowsUpdateConsole(t *testing.T, ctx context.Context, managed, work string, env []string) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	owner := exec.CommandContext(ctx, binary, "-test.run=^TestMainWindowsArchiveConsoleOwner$", "--", managed)
	owner.Env = append(env, "OPENAI_ARCHIVE_TEST_CONSOLE=1")
	owner.Dir = work
	owner.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	if out, err := owner.CombinedOutput(); err != nil {
		t.Fatalf("native console command: %v %s", err, out)
	}
}

func TestMainWindowsArchiveConsoleOwner(t *testing.T) {
	if os.Getenv("OPENAI_ARCHIVE_TEST_CONSOLE") != "1" {
		t.Skip("helper for the disposable standard user's native console")
	}
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		t.Fatal("missing native console target")
	}
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) {
		t.Fatal("expected native console handles")
	}
	target := os.Args[len(os.Args)-1]
	if os.Getenv("OPENAI_WINDOWS_REQUIRE_PRIVATE_GH") == "1" {
		if _, err := exec.LookPath("gh"); !errors.Is(err, exec.ErrNotFound) {
			t.Fatal("ordinary request can resolve a system verifier", err)
		}
	}
	mode := os.Getenv("OPENAI_ARCHIVE_TEST_MODE")
	if mode == "paste" {
		runRestrictedWindowsPaste(t, target, input, output)
		return
	}
	args := []string{"models", "retrieve", "fixture-model"}
	if mode == "install" {
		if !strings.EqualFold(filepath.Base(target), "Install.cmd") {
			t.Fatal("invalid console installer")
		}
		args = []string{"/d", "/c", `.\Install.cmd`}
		target = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	} else if mode != "" {
		selected, err := exec.LookPath("openai")
		if err != nil || !strings.EqualFold(filepath.Clean(selected), filepath.Clean(target)) {
			t.Fatal("fresh shell did not select the enrolled CLI", err)
		}
		target = selected
		switch mode {
		case "request":
		case "bare":
			args = nil
		default:
			t.Fatal("invalid public console scenario")
		}
	}
	timeout := 25 * time.Second
	if mode == "install" {
		// A pristine public installation can fetch its verifier before returning.
		timeout = 150 * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, target, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, output
	runErr := cmd.Run()
	if path := os.Getenv("OPENAI_ARCHIVE_TEST_SCREEN"); path != "" {
		var screen string
		if mode == "install" {
			cells, width, err := readOutputConsoleCells(output)
			if err != nil {
				t.Fatal(err)
			}
			screen = windowsArchiveConsoleRows(cells, width)
			if reportPath := os.Getenv("OPENAI_ARCHIVE_TEST_LAUNCH_REPORT"); reportPath != "" {
				report := windowsLauncherPresentation(cells, width)
				report.Exit = -1
				if cmd.ProcessState != nil {
					report.Exit = cmd.ProcessState.ExitCode()
				}
				data, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(reportPath, data, 0600); err != nil {
					t.Fatal(err)
				}
				t.Logf("Install.cmd console: %+v", report)
			}
		} else {
			// API and confirmation checks search the continuous screen text.
			screen, err = readOutputConsole(output)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(path, []byte(screen), 0600); err != nil {
			t.Fatal(err)
		}
		// Published qualification checks presentation after it records the actual upgrade.
		if mode == "install" && os.Getenv("OPENAI_ARCHIVE_TEST_LAUNCH_REPORT") == "" {
			statuses, err := windowsInstallerTranscript(screen)
			if runErr != nil || err != nil {
				t.Fatalf("Install.cmd exit=%v; status=%v; recognized=%q", runErr, err, statuses)
			}
		}
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
}

// ReadConsoleOutputCharacterW returns adjacent UTF-16 cells without newlines.
// Recover physical rows before decoding: a surrogate pair occupies two cells.
func windowsArchiveConsoleRows(cells []uint16, width int) string {
	var screen strings.Builder
	for len(cells) > 0 {
		n := min(width, len(cells))
		screen.WriteString(strings.TrimRight(string(utf16.Decode(cells[:n])), " "))
		screen.WriteByte('\n')
		cells = cells[n:]
	}
	return screen.String()
}

const (
	windowsInstallStart     = "Installing OpenAI CLI for your account..."
	windowsInstallDone      = "OpenAI CLI setup finished."
	windowsVerifierDownload = "Downloading GitHub CLI for automatic update verification..."
)

type windowsLauncherReport struct {
	Width, Exit, Starts, Finishes, Downloads int
	StatusOrder                              bool
}

func windowsLauncherPresentation(cells []uint16, width int) windowsLauncherReport {
	report := windowsLauncherReport{Width: width}
	// Compare complete messages from a row boundary, including padding after the last character.
	// A full preceding row can continue onto this row. Do not count a suffix of that text.
	continuation := false
	for len(cells) > 0 {
		if !continuation {
			for _, message := range []string{windowsInstallStart, windowsInstallDone, windowsVerifierDownload} {
				// A message that fills its last row must also end before the next visible text.
				span := (len(message)/width + 1) * width
				text := string(utf16.Decode(cells[:min(span, len(cells))]))
				if strings.TrimRight(text, " ") != message {
					continue
				}
				switch message {
				case windowsInstallStart:
					report.Starts++
				case windowsInstallDone:
					report.Finishes++
					report.StatusOrder = report.Starts == 1
				case windowsVerifierDownload:
					report.Downloads++
				}
				break
			}
		}
		row := cells[:min(width, len(cells))]
		continuation = row[len(row)-1] != ' '
		cells = cells[len(row):]
	}
	report.StatusOrder = report.StatusOrder && report.Starts == 1 && report.Finishes == 1
	return report
}

func windowsInstallerTranscript(screen string) ([]string, error) {
	var statuses []string
	visible := 0
	for _, row := range strings.Split(screen, "\n") {
		line := strings.TrimSpace(row)
		if line != "" {
			visible++
		}
		if line == windowsInstallStart || line == windowsInstallDone {
			statuses = append(statuses, line)
		}
	}
	if len(statuses) != 2 || statuses[0] != windowsInstallStart || statuses[1] != windowsInstallDone {
		// Include only known text and counts. Other rows can contain paths.
		return statuses[:min(len(statuses), 4)], fmt.Errorf("expected one start then one finish; observed %d visible rows, %d recognized, %d start text matches, %d finish text matches", visible, len(statuses), strings.Count(screen, windowsInstallStart), strings.Count(screen, windowsInstallDone))
	}
	return statuses, nil
}

func TestMainWindowsArchiveBracketedLauncher(t *testing.T) {
	dir := os.Getenv("OPENAI_CLI_WINDOWS_BRACKETED_ZIP")
	if dir == "" {
		t.Skip("requires a standard-account extraction of the candidate ZIP")
	}
	requireWindowsArchiveAccount(t)
	if !filepath.IsAbs(dir) || !strings.Contains(filepath.Base(dir), "[") {
		t.Fatal("expected a bracketed extraction directory")
	}
	screen := filepath.Join(t.TempDir(), "screen.txt")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	env := append(os.Environ(), "OPENAI_ARCHIVE_TEST_MODE=install", "OPENAI_ARCHIVE_TEST_SCREEN="+screen)
	runWindowsUpdateConsole(t, ctx, filepath.Join(dir, "Install.cmd"), dir, env)
	output, err := os.ReadFile(screen)
	if err != nil {
		t.Fatal("could not read the bracketed launcher screen")
	}
	if _, err := windowsInstallerTranscript(string(output)); err != nil {
		t.Fatal(err)
	}
	t.Log("Install.cmd started and finished in the bracketed ZIP directory without a keypress.")
}

// Capture both stdout and stderr from a new native console without any input.
func TestMainWindowsArchiveConsoleCapture(t *testing.T) {
	t.Run("wrapped launcher messages", func(t *testing.T) {
		for _, width := range []int{16, 25, 26, 41, 50, 59, 80} {
			rows := func(lines ...string) []uint16 {
				var cells []uint16
				for _, line := range lines {
					padded := line + strings.Repeat(" ", width-len(line)%width)
					cells = append(cells, utf16.Encode([]rune(padded))...)
				}
				return cells
			}
			messages := rows(windowsInstallStart, windowsVerifierDownload, windowsVerifierDownload, windowsInstallDone)
			report := windowsLauncherPresentation(messages, width)
			if report.Downloads != 2 || !report.StatusOrder {
				t.Fatalf("width %d: %+v", width, report)
			}
			bad := rows(windowsInstallDone, windowsInstallStart, strings.Repeat("x", width)+windowsVerifierDownload, windowsVerifierDownload+" extra")
			report = windowsLauncherPresentation(bad, width)
			if report.Downloads != 0 || report.StatusOrder {
				t.Fatalf("accepted embedded text or reversed status at width %d: %+v", width, report)
			}
		}
	})
	work := t.TempDir()
	launcher := filepath.Join(work, "Install.cmd")
	const detail = "synthetic installer detail"
	script := "@echo off\r\necho " + windowsInstallStart + "\r\necho " + detail + " 1>&2\r\necho " + windowsInstallDone + "\r\nexit /b 0\r\n"
	if err := os.WriteFile(launcher, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	capture := filepath.Join(work, "screen.txt")
	runWindowsUpdateConsole(t, ctx, launcher, work, append(os.Environ(), "OPENAI_ARCHIVE_TEST_MODE=install", "OPENAI_ARCHIVE_TEST_SCREEN="+capture))
	screen, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(screen), windowsInstallStart+"\n"+detail+"\n"+windowsInstallDone+"\n") {
		t.Fatal("real console failed to preserve separate stdout and stderr rows")
	}
}

// Start a console owner even when the calling standard-user job has redirected
// handles. The owner supplies the native ConsoleHost input and output handles.
func TestMainWindowsArchivePasteHost(t *testing.T) {
	if os.Getenv("OPENAI_WINDOWS_PASTE_RESULT") == "" {
		t.Skip("requires the disposable ConsoleHost driver")
	}
	client := os.Getenv("OPENAI_WINDOWS_PASTE_CLIENT")
	dir := os.Getenv("OPENAI_WINDOWS_PASTE_CWD")
	if !filepath.IsAbs(dir) {
		t.Fatal("expected the caller's absolute filesystem location")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute+15*time.Second)
	defer cancel()
	runWindowsUpdateConsole(t, ctx, client, dir, append(os.Environ(), "OPENAI_ARCHIVE_TEST_MODE=paste"))
}

func runRestrictedWindowsPaste(t *testing.T, client string, input, output *os.File) {
	t.Helper()
	requireWindowsArchiveAccount(t)
	base := strings.ToLower(filepath.Base(client))
	if !filepath.IsAbs(client) || (base != "powershell.exe" && base != "pwsh.exe") {
		t.Fatal("expected an absolute PowerShell client")
	}
	diagnostics, err := os.OpenFile(os.Getenv("OPENAI_WINDOWS_PASTE_STDERR"), os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal("cannot open private paste diagnostics")
	}
	defer diagnostics.Close()
	timeout := 5 * time.Minute
	if os.Getenv("OPENAI_WINDOWS_PASTE_PROBE") != "Paste" {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	// os/exec retains the process handle returned by CreateProcess. Keep the
	// native exit result even for clients that exit immediately. Only stderr
	// is redirected, to count the public recipe's actual verifier downloads.
	cmd := exec.CommandContext(ctx, client, "-NoLogo", "-NoProfile", "-NoExit", "-ExecutionPolicy", "Restricted", "-Command", windowsPasteDriver)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, diagnostics
	wanted, err := os.Stat(os.Getenv("OPENAI_WINDOWS_PASTE_CWD"))
	if err != nil {
		t.Fatal("cannot inspect requested native launch directory")
	}
	actual, err := os.Stat(".")
	if err != nil {
		t.Fatal("cannot inspect inherited native launch directory")
	}
	result := struct {
		OwnerCWD    bool
		State       string
		Exit        *uint32
		NativeError uint32
	}{State: "launch-failed", OwnerCWD: os.SameFile(actual, wanted)}
	if !result.OwnerCWD {
		result.State = "directory-mismatch"
	} else if err = cmd.Start(); err == nil {
		err = cmd.Wait()
		result.State = "exit-unavailable"
		var exitErr *exec.ExitError
		if ctx.Err() != nil {
			result.State = "timeout"
		} else if (err == nil || errors.As(err, &exitErr)) && cmd.ProcessState != nil {
			if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
				result.State, result.Exit = "exited", &status.ExitCode
			}
		}
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		result.NativeError = uint32(errno)
	}
	report, err := json.Marshal(result)
	if err != nil || os.WriteFile(os.Getenv("OPENAI_WINDOWS_PASTE_RESULT"), report, 0600) != nil {
		t.Fatal("cannot record native PowerShell process status")
	}
}

// Only this fixed bootstrap is a process argument. The literal setup paste is
// returned by the first ConsoleHost read, under the effective client policy.
const windowsPasteDriver = `$fixtureHost = if ($Host.Name -ceq 'ConsoleHost') {'ConsoleHost'} else {'other'}
[IO.File]::WriteAllLines($env:OPENAI_WINDOWS_PASTE_TRACE,[string[]]@('initialized',('host='+$fixtureHost),('policy='+(Get-ExecutionPolicy)),('stdin='+[Console]::IsInputRedirected),('stdout='+[Console]::IsOutputRedirected),('stderr='+[Console]::IsErrorRedirected)))
$global:reads = 0
$global:openaiStartMeasured = $false
function global:Add-OpenAIPasteTrace([string]$record) {
    [IO.File]::AppendAllText($env:OPENAI_WINDOWS_PASTE_TRACE,$record+[Environment]::NewLine)
}
function global:Test-OpenAIFolder([string]$phase,[string]$expected,[bool]$pathMatches) {
    if (!$expected) {return $pathMatches}
    $result = 'provider'
    try {
        $location = Microsoft.PowerShell.Management\Get-Location
        if ($location.Provider.Name -ceq 'FileSystem') {
            # Read from the actual provider location, independent of Win32 cwd and wildcard expansion.
            $path = [IO.Path]::Combine($location.ProviderPath,'.openai-qualification-folder')
            $attributes = [IO.File]::GetAttributes($path)
            if ($attributes -band [IO.FileAttributes]::ReparsePoint) {$result = 'reparse'}
            elseif ($attributes -band [IO.FileAttributes]::Directory) {$result = 'directory'}
            elseif ([IO.FileInfo]::new($path).Length -ne 32) {$result = 'length'}
            else {
                $id = [IO.File]::ReadAllText($path)
                if ($id -cnotmatch '\A[0-9a-f]{32}\z') {$result = 'format'}
                elseif ([string]::Equals($id,$expected,[StringComparison]::Ordinal)) {$result = 'match'}
                else {$result = 'different'}
            }
        }
    } catch {
        $result = 'unreadable'
        for ($cause=$_.Exception; $cause; $cause=$cause.InnerException) {
            if ($cause -is [IO.FileNotFoundException] -or $cause -is [IO.DirectoryNotFoundException]) {$result = 'missing'; break}
            if ($cause -is [UnauthorizedAccessException] -or $cause -is [Security.SecurityException]) {$result = 'denied'; break}
        }
    }
    Add-OpenAIPasteTrace ($phase+'Marker='+$result)
    return $result -ceq 'match'
}
function global:Get-OpenAIRelativeSource {
    if (!$env:OPENAI_WINDOWS_PASTE_SOURCE_SHA -or $env:OPENAI_WINDOWS_PASTE_PROBE -notin 'Paste','ObserveDirectory','WrongDirectory') {
        return [pscustomobject]@{State='unchecked'; AtTarget=$false}
    }
    $application = Microsoft.PowerShell.Core\Get-Command .\openai.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (!$application) {return [pscustomobject]@{State='missing'; AtTarget=$false}}
    $expected = Join-Path $env:OPENAI_WINDOWS_PASTE_TARGET 'openai.exe'
    $atTarget = [string]::Equals($application.Path,$expected,[StringComparison]::OrdinalIgnoreCase)
    $stream = $null
    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        $stream = [IO.File]::OpenRead($application.Path)
        $hash = [BitConverter]::ToString($hasher.ComputeHash($stream)).Replace('-','')
        $state = if ([string]::Equals($hash,$env:OPENAI_WINDOWS_PASTE_SOURCE_SHA,[StringComparison]::OrdinalIgnoreCase)) {'verified'} else {'different'}
    } catch {$state='unreadable'} finally {if ($stream) {$stream.Dispose()}; $hasher.Dispose()}
    return [pscustomobject]@{State=$state; AtTarget=$atTarget}
}
function global:PSConsoleHostReadLine {
    if (!$global:openaiStartMeasured) {
        $global:openaiStartMeasured = $true
        $initial = (Get-Location).ProviderPath
        $nativeCwd = [string]::Equals([Environment]::CurrentDirectory,$env:OPENAI_WINDOWS_PASTE_CWD,[StringComparison]::OrdinalIgnoreCase)
        $initialProvider = [string]::Equals($initial,$env:OPENAI_WINDOWS_PASTE_CWD,[StringComparison]::OrdinalIgnoreCase)
        $initialDirectory = Test-OpenAIFolder 'initial' $env:OPENAI_WINDOWS_PASTE_START_ID $initialProvider
        $global:initialSource = Get-OpenAIRelativeSource
        foreach ($field in "nativeCwd=$nativeCwd","initialProvider=$initialProvider","initialDirectory=$initialDirectory",("initialSource="+$global:initialSource.State),("initialSourcePath="+$global:initialSource.AtTarget)) {Add-OpenAIPasteTrace $field}
        if ($env:OPENAI_WINDOWS_PASTE_LOCATION -ceq 'Literal') {
            if (!$initialDirectory -or $Host.Name -ne 'ConsoleHost' -or (Get-ExecutionPolicy) -ne 'Restricted' -or [Console]::IsInputRedirected -or [Console]::IsOutputRedirected) {return 'exit 37'}
            Add-OpenAIPasteTrace 'literal-location-input'
            return 'Microsoft.PowerShell.Management\Set-Location -LiteralPath $env:OPENAI_WINDOWS_PASTE_TARGET -ErrorAction Stop'
        }
    }
    $global:reads++
    if ($global:reads -eq 1) {
        Add-OpenAIPasteTrace 'first-read'
        if ($Host.Name -ne 'ConsoleHost' -or (Get-ExecutionPolicy) -ne 'Restricted') {return 'exit 32'}
        if ([Console]::IsInputRedirected -or [Console]::IsOutputRedirected) {return 'exit 36'}
        $location = (Get-Location).ProviderPath
        $cwdOK = [string]::Equals($location,$env:OPENAI_WINDOWS_PASTE_TARGET,[StringComparison]::OrdinalIgnoreCase)
        $source = if ($env:OPENAI_WINDOWS_PASTE_LOCATION -ceq 'Literal') {Get-OpenAIRelativeSource} else {$global:initialSource}
        $finalDirectory = Test-OpenAIFolder 'final' $env:OPENAI_WINDOWS_PASTE_TARGET_ID $cwdOK
        foreach ($field in "cwd=$cwdOK","finalDirectory=$finalDirectory",("source="+$source.State),("sourcePath="+$source.AtTarget)) {Add-OpenAIPasteTrace $field}
        if ($env:OPENAI_WINDOWS_PASTE_PROBE -ceq 'ObserveDirectory') {Add-OpenAIPasteTrace 'directory-observed'; return 'exit 0'}
        if (!$finalDirectory) {return 'exit 37'}
        if ($env:OPENAI_WINDOWS_PASTE_SOURCE_SHA -and $env:OPENAI_WINDOWS_PASTE_PROBE -in 'Paste','WrongDirectory' -and
            ($source.State -cne 'verified' -or (!$env:OPENAI_WINDOWS_PASTE_TARGET_ID -and !$source.AtTarget))) {return 'exit 37'}
        $global:Error.Clear()
        $global:LASTEXITCODE = 0
        $global:openaiLiteral = [IO.File]::ReadAllText($env:OPENAI_WINDOWS_PASTE_TEXT)
        Add-OpenAIPasteTrace 'input-ready'
        return $global:openaiLiteral
    }
    if ($global:reads -eq 2) {
        $native = $global:LASTEXITCODE
        $hasErrors = $global:Error.Count -gt 0
        Add-OpenAIPasteTrace 'second-read'
        # The global error list also contains handled exceptions. Interactive
        # history records whether this exact submitted command actually failed.
        $history = Microsoft.PowerShell.Core\Get-History -Count 1
        $historyState = 'missing'
        if ($history) {
            $trim = [char[]](13,10)
            $observed = $history.CommandLine.Replace(([string][char]13+[char]10),[string][char]10).TrimEnd($trim)
            $wanted = $global:openaiLiteral.Replace(([string][char]13+[char]10),[string][char]10).TrimEnd($trim)
            $historyState = if (![string]::Equals($observed,$wanted,[StringComparison]::Ordinal)) {'different'} elseif ($history.ExecutionStatus -eq 'Completed') {'completed'} else {'failed'}
        }
        $pathFirst = [string]::Equals(($env:PATH -split [regex]::Escape([IO.Path]::PathSeparator),2)[0],[IO.Path]::GetDirectoryName($env:OPENAI_WINDOWS_MANAGED_EXE),[StringComparison]::OrdinalIgnoreCase)
        $selected = Microsoft.PowerShell.Core\Get-Command openai -ErrorAction SilentlyContinue
        $selection = if (!$selected) {'missing'} elseif ($selected.CommandType -ne 'Application') {'shadowed'} elseif (![string]::Equals($selected.Path,$env:OPENAI_WINDOWS_MANAGED_EXE,[StringComparison]::OrdinalIgnoreCase)) {'different'} else {'expected'}
        $versionState = 'unchecked'
        if ($selection -eq 'expected') {
            $observedVersion = & $selected.Path --version
            $versionState = if ($LASTEXITCODE -ne 0) {'command-failed'} elseif (@($observedVersion).Count -eq 1 -and $observedVersion -ceq $env:OPENAI_WINDOWS_STARTER_VERSION) {'expected'} else {'different'}
        }
        $nativeText = if ($native -is [int] -or $native -is [long]) {[string]$native} else {'unavailable'}
        foreach ($field in "history=$historyState","errorRecords=$hasErrors","native=$nativeText","pathFirst=$pathFirst","selection=$selection","version=$versionState") {Add-OpenAIPasteTrace $field}
        if ($historyState -ne 'completed' -or $nativeText -ne '0' -or !$pathFirst -or $selection -ne 'expected') {return 'exit 33'}
        if ($versionState -ne 'expected') {return 'exit 34'}
        return '[IO.File]::WriteAllText($env:OPENAI_WINDOWS_PASTE_RECEIPT,$env:OPENAI_WINDOWS_PASTE_CHALLENGE); exit 0'
    }
    return 'exit 35'
}`
