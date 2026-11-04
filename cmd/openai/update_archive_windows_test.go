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
		if mode == "install" {
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
	windowsInstallStart = "Installing OpenAI CLI for your account..."
	windowsInstallDone  = "OpenAI CLI setup finished."
)

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

// Capture both stdout and stderr from a new native console without any input.
func TestMainWindowsArchiveConsoleCapture(t *testing.T) {
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
	result := struct {
		State       string
		Exit        *uint32
		NativeError uint32
	}{State: "launch-failed"}
	if err = cmd.Start(); err == nil {
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
function global:Add-OpenAIPasteTrace([string]$record) {
    [IO.File]::AppendAllText($env:OPENAI_WINDOWS_PASTE_TRACE,$record+[Environment]::NewLine)
}
function global:PSConsoleHostReadLine {
    $global:reads++
    if ($global:reads -eq 1) {
        [IO.File]::AppendAllText($env:OPENAI_WINDOWS_PASTE_TRACE,'first-read'+[Environment]::NewLine)
        if ($Host.Name -ne 'ConsoleHost' -or (Get-ExecutionPolicy) -ne 'Restricted') {return 'exit 32'}
        if ([Console]::IsInputRedirected -or [Console]::IsOutputRedirected) {return 'exit 36'}
        $location = (Get-Location).ProviderPath
        $cwdOK = $location -ieq $env:OPENAI_WINDOWS_PASTE_CWD
        Add-OpenAIPasteTrace ('cwd='+$cwdOK)
        if (!$cwdOK) {return 'exit 37'}
        if ($env:OPENAI_WINDOWS_PASTE_SOURCE_SHA -and $env:OPENAI_WINDOWS_PASTE_PROBE -eq 'Paste') {
            $source = Join-Path $location 'openai.exe'
            $sourceState = 'missing'
            if ([IO.File]::Exists($source)) {
                $stream = $null
                $hasher = [Security.Cryptography.SHA256]::Create()
                try {
                    $stream = [IO.File]::OpenRead($source)
                    $hash = [BitConverter]::ToString($hasher.ComputeHash($stream)).Replace('-','')
                    $sourceState = if ($hash -ieq $env:OPENAI_WINDOWS_PASTE_SOURCE_SHA) {'verified'} else {'different'}
                } catch {$sourceState = 'unreadable'} finally {if ($stream) {$stream.Dispose()}; $hasher.Dispose()}
            }
            Add-OpenAIPasteTrace ('source='+$sourceState)
            if ($sourceState -ne 'verified') {return 'exit 37'}
        }
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
            $historyState = if ($observed -cne $wanted) {'different'} elseif ($history.ExecutionStatus -eq 'Completed') {'completed'} else {'failed'}
        }
        $pathFirst = ($env:PATH -split [regex]::Escape([IO.Path]::PathSeparator),2)[0] -ieq [IO.Path]::GetDirectoryName($env:OPENAI_WINDOWS_MANAGED_EXE)
        $selected = Microsoft.PowerShell.Core\Get-Command openai -ErrorAction SilentlyContinue
        $selection = if (!$selected) {'missing'} elseif ($selected.CommandType -ne 'Application') {'shadowed'} elseif ($selected.Path -ine $env:OPENAI_WINDOWS_MANAGED_EXE) {'different'} else {'expected'}
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
