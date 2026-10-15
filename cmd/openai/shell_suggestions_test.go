package main

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainShellSuggestionsRequireSeparateConsent(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			home := t.TempDir()
			environment := pickerSetupProcessEnv(home)
			ordinary := runMainDispatchWithEnv(t, shell, environment, "openai", "@completion", shell)
			disabled := runMainDispatchWithEnv(t, shell, environment, "openai", "@completion", shell, "--suggest-executable=false")
			if ordinary.code != 0 || ordinary.stdout == "" || ordinary.stderr != "" || disabled.code != 0 || disabled.stderr != "" || disabled.stdout != ordinary.stdout {
				t.Fatalf("ordinary completion changed: ordinary=%+v disabled=%+v", ordinary, disabled)
			}
			if strings.Contains(ordinary.stdout, "openai_suggestions_disable") {
				t.Fatal("ordinary completion enabled executable suggestions")
			}
			enabled := runMainDispatchWithEnv(t, shell, environment, "openai", "@completion", shell, "--suggest-executable")
			if enabled.code != 0 || enabled.stderr != "" || !strings.Contains(enabled.stdout, "openai_suggestions_disable") ||
				strings.Contains(enabled.stdout, ordinary.stdout) || strings.Contains(enabled.stdout, "__complete") {
				t.Fatalf("explicit suggestions did not emit a standalone hook: %+v", enabled)
			}
			for _, configuration := range [][]string{
				{"OPENAI_BASE_URL=://synthetic-invalid-url"},
				{"OPENAI_MTLS_CLIENT_CERT_FILE=synthetic-missing-cert", "OPENAI_MTLS_CLIENT_KEY_FILE=synthetic-missing-key"},
			} {
				env := append(append([]string(nil), environment...), configuration...)
				got := runMainDispatchWithEnv(t, shell, env, "openai", "@completion", shell, "--suggest-executable")
				if got.code != 0 || got.stderr != "" || got.stdout != enabled.stdout {
					t.Fatalf("local suggestions inspected request configuration: %+v", got)
				}
			}
			if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
				t.Fatalf("printing suggestions changed persistent configuration: entries=%v error=%v", entries, err)
			}
		})
	}
}

// Setup uses the built CLI. A sentinel replaces it on PATH during failed lookup.
func TestMainShellSuggestionsNative(t *testing.T) {
	cliDirectory, _ := buildNativeShellCLI(t)
	bash := os.Getenv("OPENAI_CLI_TEST_BASH")
	if bash == "" {
		bash = "bash"
	}
	shells := []nativeShell{
		{name: "bash", executable: bash, args: []string{"--noprofile", "--norc", "-i", "-c"}},
		{name: "zsh", executable: "zsh", args: []string{"-f", "-i", "-c"}},
		{name: "fish", executable: "fish", args: []string{"--no-config", "--private", "--interactive", "-c"}},
		{name: "pwsh", executable: "pwsh", args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
	}
	if runtime.GOOS == "windows" {
		shells = shells[3:]
	}
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range shells {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required native shell %s is unavailable: %v", shell.name, err)
				}
				t.Skipf("native %s is unavailable", shell.name)
			}
			shell.executable = path
			work, home := t.TempDir(), t.TempDir()
			configuration := filepath.Join(home, "config")
			for _, profile := range []string{".bashrc", ".zshrc", "config/fish/config.fish", "config/powershell/Microsoft.PowerShell_profile.ps1"} {
				name := filepath.Join(home, filepath.FromSlash(profile))
				if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name, []byte("# synthetic existing profile\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := shellSuggestionConfiguration(t, home)
			sentinelDirectory := filepath.Join(work, "sentinel")
			if err := os.Mkdir(sentinelDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
			sentinelName := "openai"
			sentinel := "#!/bin/sh\nprintf launched >>\"$SUGGEST_EXEC_LOG\"\nexit 98\n"
			basePath := "/usr/bin:/bin"
			if runtime.GOOS == "windows" {
				sentinelName += ".cmd"
				sentinel = "@echo off\r\necho launched>>\"%SUGGEST_EXEC_LOG%\"\r\nexit /b 98\r\n"
				basePath = filepath.Join(os.Getenv("SystemRoot"), "System32") + string(os.PathListSeparator) + os.Getenv("SystemRoot")
			}
			if err := os.WriteFile(filepath.Join(sentinelDirectory, sentinelName), []byte(sentinel), 0o700); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				http.Error(w, "unexpected synthetic request", http.StatusInternalServerError)
			}))
			defer server.Close()
			environment := []string{
				"HOME=" + home, "USERPROFILE=" + home, "ZDOTDIR=" + home,
				"XDG_CONFIG_HOME=" + configuration, "APPDATA=" + configuration,
				"XDG_CACHE_HOME=" + filepath.Join(work, "cache"), "LOCALAPPDATA=" + filepath.Join(work, "cache"),
				"XDG_DATA_HOME=" + filepath.Join(work, "data"), "TMPDIR=" + work, "TMP=" + work, "TEMP=" + work,
				"PATH=" + cliDirectory + string(os.PathListSeparator) + basePath,
				"HISTFILE=" + os.DevNull, "LANG=C", "LC_ALL=C", "TERM=dumb", "NO_COLOR=1",
				"OPENAI_API_KEY=synthetic-native-suggestion-key", "OPENAI_BASE_URL=" + server.URL,
				"POWERSHELL_TELEMETRY_OPTOUT=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1", "POWERSHELL_UPDATECHECK=Off",
				"SUGGEST_CLI_DIRECTORY=" + cliDirectory, "SUGGEST_SENTINEL_DIRECTORY=" + sentinelDirectory,
				"SUGGEST_BASE_PATH=" + basePath, "SUGGEST_EXEC_LOG=" + filepath.Join(work, "backend-called"),
			}
			if runtime.GOOS == "windows" {
				environment = append(environment, "SystemRoot="+os.Getenv("SystemRoot"), "WINDIR="+os.Getenv("WINDIR"), "PATHEXT="+os.Getenv("PATHEXT"))
			}
			program := shellSuggestionNativeUnix
			if shell.name == "fish" {
				program = shellSuggestionNativeFish
			} else if shell.name == "pwsh" {
				program = shellSuggestionNativePowerShell
			} else {
				handler := "command_not_found_handle"
				if shell.name == "zsh" {
					handler = "command_not_found_handler"
				}
				program = strings.NewReplacer("__SHELL__", shell.name, "__HANDLER__", handler).Replace(program)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, shell.executable, append(slices.Clone(shell.args), program)...)
			command.WaitDelay = time.Second
			command.Dir, command.Env = work, environment
			command.Stdin = strings.NewReader("SYNTHETIC_UNREAD_STDIN\n")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err = command.Run()
			if ctx.Err() != nil {
				t.Fatalf("native %s exceeded its deadline: stdout=%q stderr=%q", shell.name, stdout.String(), stderr.String())
			}
			if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 && shell.name == "bash" {
				if slices.Contains(required, "bash") {
					t.Fatal("required Bash 4+ unavailable; set OPENAI_CLI_TEST_BASH")
				}
				t.Skip("Bash 4+ unavailable; set OPENAI_CLI_TEST_BASH")
			}
			if err != nil || strings.ReplaceAll(stdout.String(), "\r\n", "\n") != "STDIN:SYNTHETIC_UNREAD_STDIN\n" {
				t.Fatalf("native setup failed: error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "SYNTHETIC_PRIVATE_ARGUMENT") {
				t.Errorf("shell diagnostics exposed a private argument: %q", stderr.String())
			}
			read := func(name string) string {
				t.Helper()
				if shell.name == "fish" && strings.HasSuffix(name, ".err") {
					// Fish invokes missing-command callbacks outside command redirections.
					stage := strings.TrimSuffix(name, ".err")
					begin, end := "BEGIN:"+stage+"\n", "END:"+stage+"\n"
					captured := strings.ReplaceAll(stderr.String(), "\r\n", "\n")
					if strings.Count(captured, begin) != 1 || strings.Count(captured, end) != 1 {
						t.Fatalf("missing Fish stage markers for %s: %q", stage, captured)
					}
					_, captured, _ = strings.Cut(captured, begin)
					captured, _, _ = strings.Cut(captured, end)
					return captured
				}
				data, err := os.ReadFile(filepath.Join(work, name))
				if err != nil {
					t.Fatal(err)
				}
				return strings.ReplaceAll(string(data), "\r\n", "\n")
			}
			for _, stage := range []string{"setup1", "setup2", "disable1", "disable2", "setup3", "setup4", "disable3", "disable4"} {
				if got := read(stage + ".status"); got != "0\n" {
					t.Errorf("%s failed: %q; shell stderr=%q", stage, got, stderr.String())
				}
			}
			const advice = "Command not found. Did you mean: openai?\nKeep the remaining arguments unchanged.\n"
			for _, stage := range []string{"active", "reenabled"} {
				wantError, wantStatus := advice, "127\n"
				if shell.name == "fish" {
					wantError = "PRIOR\n" + advice
				} else if shell.name == "pwsh" {
					wantStatus = "CommandNotFoundException:73\n"
				}
				if got := read(stage + ".status"); got != wantStatus || read(stage+".out") != "" || read(stage+".err") != wantError {
					t.Errorf("%s changed lookup: status=%q stdout=%q stderr=%q", stage, got, read(stage+".out"), read(stage+".err"))
				}
			}
			wantDisabled, wantLater := "127\n", "42\n"
			if shell.name == "pwsh" {
				wantDisabled = "CommandNotFoundException:73\n"
			} else if shell.name == "fish" {
				wantLater = "127\n"
			}
			if got := read("disabled.status"); got != wantDisabled || read("disabled.out") != "" || strings.Contains(read("disabled.err"), "Did you mean:") {
				t.Errorf("disable did not restore lookup: status=%q stdout=%q stderr=%q", got, read("disabled.out"), read("disabled.err"))
			}
			if got := read("later.status"); got != wantLater || read("later.out") != "" || read("later.err") != "LATER\n" {
				t.Errorf("disable replaced a later handler: status=%q stdout=%q stderr=%q", got, read("later.out"), read("later.err"))
			}
			if shell.name == "fish" {
				want := "BEGIN:active\nPRIOR\n" + advice + "END:active\n" +
					"BEGIN:disabled\nPRIOR\nEND:disabled\n" +
					"BEGIN:reenabled\nPRIOR\n" + advice + "END:reenabled\n" +
					"BEGIN:later\nLATER\nEND:later\n"
				if got := strings.ReplaceAll(stderr.String(), "\r\n", "\n"); got != want {
					t.Errorf("Fish emitted unexpected process diagnostics: %q", got)
				}
			}
			arguments := []string{"opneai", "images", "generate", "--api-key=SYNTHETIC_PRIVATE_ARGUMENT", "", "space value", "quote\"value", "$(touch replayed)", "line\nvalue"}
			if got := read("later.args"); got != strings.Join(arguments, "\x00")+"\x00" {
				t.Errorf("later handler received changed arguments: %q", got)
			}
			for _, marker := range []string{"backend-called", "replayed"} {
				if _, err := os.Stat(filepath.Join(work, marker)); !os.IsNotExist(err) {
					t.Errorf("shell replayed a command: %s: %v", marker, err)
				}
			}
			if requests.Load() != 0 {
				t.Errorf("local setup or failed lookup made %d API requests", requests.Load())
			}
			if after := shellSuggestionConfiguration(t, home); !maps.Equal(before, after) {
				t.Errorf("native setup modified shell configuration: before=%v after=%v", before, after)
			}
		})
	}
}

func shellSuggestionConfiguration(t *testing.T, home string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		files[relative] = info.Mode().String()
		if !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[relative] += string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

const shellSuggestionNativeUnix = `
if [ '__SHELL__' = bash ]; then (( BASH_VERSINFO[0] >= 4 )) || exit 77; fi
source <(openai @completion __SHELL__ --suggest-executable)
printf '%s\n' "$?" > setup1.status
source <(openai @completion __SHELL__ --suggest-executable)
printf '%s\n' "$?" > setup2.status
export PATH="$SUGGEST_SENTINEL_DIRECTORY:$SUGGEST_BASE_PATH"
hash -r
opneai images generate '--api-key=SYNTHETIC_PRIVATE_ARGUMENT' '' 'space value' 'quote"value' '$(touch replayed)' 'line
value' > active.out 2> active.err
printf '%s\n' "$?" > active.status
openai_suggestions_disable
printf '%s\n' "$?" > disable1.status
openai_suggestions_disable
printf '%s\n' "$?" > disable2.status
opneai > disabled.out 2> disabled.err
printf '%s\n' "$?" > disabled.status
export PATH="$SUGGEST_CLI_DIRECTORY:$SUGGEST_BASE_PATH"
hash -r
source <(openai @completion __SHELL__ --suggest-executable)
printf '%s\n' "$?" > setup3.status
export PATH="$SUGGEST_SENTINEL_DIRECTORY:$SUGGEST_BASE_PATH"
hash -r
opneai images generate '--api-key=SYNTHETIC_PRIVATE_ARGUMENT' '' 'space value' 'quote"value' '$(touch replayed)' 'line
value' > reenabled.out 2> reenabled.err
printf '%s\n' "$?" > reenabled.status
function __HANDLER__ {
    printf '%s\000' "$@" > later.args
    printf 'LATER\n' >&2
    return 42
}
export PATH="$SUGGEST_CLI_DIRECTORY:$SUGGEST_BASE_PATH"
hash -r
source <(openai @completion __SHELL__ --suggest-executable)
printf '%s\n' "$?" > setup4.status
export PATH="$SUGGEST_SENTINEL_DIRECTORY:$SUGGEST_BASE_PATH"
hash -r
openai_suggestions_disable
printf '%s\n' "$?" > disable3.status
openai_suggestions_disable
printf '%s\n' "$?" > disable4.status
opneai images generate '--api-key=SYNTHETIC_PRIVATE_ARGUMENT' '' 'space value' 'quote"value' '$(touch replayed)' 'line
value' > later.out 2> later.err
printf '%s\n' "$?" > later.status
IFS= read -r remaining
printf 'STDIN:%s\n' "$remaining"
exit 0
`

const shellSuggestionNativeFish = `
function fish_command_not_found
    printf 'PRIOR\n' >&2
    return 42
end
openai @completion fish --suggest-executable | source
printf '%s\n' $status > setup1.status
openai @completion fish --suggest-executable | source
printf '%s\n' $status > setup2.status
set -gx PATH "$SUGGEST_SENTINEL_DIRECTORY" /usr/bin /bin
printf 'BEGIN:active\n' >&2
opneai images generate '--api-key=SYNTHETIC_PRIVATE_ARGUMENT' '' 'space value' 'quote"value' '$(touch replayed)' 'line
value' > active.out 2> active.err
printf '%s\n' $status > active.status
printf 'END:active\n' >&2
openai_suggestions_disable
printf '%s\n' $status > disable1.status
openai_suggestions_disable
printf '%s\n' $status > disable2.status
printf 'BEGIN:disabled\n' >&2
opneai > disabled.out 2> disabled.err
printf '%s\n' $status > disabled.status
printf 'END:disabled\n' >&2
set -gx PATH "$SUGGEST_CLI_DIRECTORY" /usr/bin /bin
openai @completion fish --suggest-executable | source
printf '%s\n' $status > setup3.status
set -gx PATH "$SUGGEST_SENTINEL_DIRECTORY" /usr/bin /bin
printf 'BEGIN:reenabled\n' >&2
opneai images generate '--api-key=SYNTHETIC_PRIVATE_ARGUMENT' '' 'space value' 'quote"value' '$(touch replayed)' 'line
value' > reenabled.out 2> reenabled.err
printf '%s\n' $status > reenabled.status
printf 'END:reenabled\n' >&2
function fish_command_not_found
    printf '%s\000' $argv > later.args
    printf 'LATER\n' >&2
    return 42
end
set -gx PATH "$SUGGEST_CLI_DIRECTORY" /usr/bin /bin
openai @completion fish --suggest-executable | source
printf '%s\n' $status > setup4.status
set -gx PATH "$SUGGEST_SENTINEL_DIRECTORY" /usr/bin /bin
openai_suggestions_disable
printf '%s\n' $status > disable3.status
openai_suggestions_disable
printf '%s\n' $status > disable4.status
printf 'BEGIN:later\n' >&2
opneai images generate '--api-key=SYNTHETIC_PRIVATE_ARGUMENT' '' 'space value' 'quote"value' '$(touch replayed)' 'line
value' > later.out 2> later.err
printf '%s\n' $status > later.status
printf 'END:later\n' >&2
read --local remaining
printf 'STDIN:%s\n' "$remaining"
exit 0
`

const shellSuggestionNativePowerShell = `
$ErrorActionPreference = 'Stop'
function Save-Status($name, [bool]$success) {
    if (-not $success) { throw ('Failed stage: ' + $name) }
    [IO.File]::WriteAllText((Join-Path $PWD ($name + '.status')), "0" + [char]10)
}
function Invoke-Typo($stage) {
    $global:LASTEXITCODE = 73
    $savedError = [Console]::Error
    $errorWriter = [IO.StreamWriter]::new((Join-Path $PWD ($stage + '.err')))
    [Console]::SetError($errorWriter)
    try {
        try {
            opneai images generate '--api-key=SYNTHETIC_PRIVATE_ARGUMENT' '' 'space value' 'quote"value' '$(touch replayed)' 'line
value' > ($stage + '.out')
            $statusText = [string]$global:LASTEXITCODE
        } catch {
            $statusText = $_.FullyQualifiedErrorId + ':' + $global:LASTEXITCODE
            [IO.File]::WriteAllText((Join-Path $PWD ($stage + '.out')), '')
        }
    } finally {
        [Console]::SetError($savedError)
        $errorWriter.Dispose()
    }
    [IO.File]::WriteAllText((Join-Path $PWD ($stage + '.status')), $statusText + [char]10)
}
openai '@completion' pwsh --suggest-executable | Out-String | Invoke-Expression
Save-Status setup1 $?
openai '@completion' pwsh --suggest-executable | Out-String | Invoke-Expression
Save-Status setup2 $?
$env:PATH = $env:SUGGEST_SENTINEL_DIRECTORY + [IO.Path]::PathSeparator + $env:SUGGEST_BASE_PATH
Invoke-Typo active
openai_suggestions_disable
Save-Status disable1 $?
openai_suggestions_disable
Save-Status disable2 $?
Invoke-Typo disabled
$env:PATH = $env:SUGGEST_CLI_DIRECTORY + [IO.Path]::PathSeparator + $env:SUGGEST_BASE_PATH
openai '@completion' pwsh --suggest-executable | Out-String | Invoke-Expression
Save-Status setup3 $?
$env:PATH = $env:SUGGEST_SENTINEL_DIRECTORY + [IO.Path]::PathSeparator + $env:SUGGEST_BASE_PATH
Invoke-Typo reenabled
$later = [System.EventHandler[System.Management.Automation.CommandLookupEventArgs]] {
    param($sender, $eventArgs)
    if ($eventArgs.CommandName -ceq 'opneai') {
        $eventArgs.CommandScriptBlock = {
            [IO.File]::WriteAllText((Join-Path $PWD 'later.args'), 'opneai' + [char]0 + [string]::Join([string][char]0, $args) + [char]0)
            [Console]::Error.WriteLine('LATER')
            $global:LASTEXITCODE = 42
        }
    }
}
$ExecutionContext.InvokeCommand.CommandNotFoundAction = $later
$env:PATH = $env:SUGGEST_CLI_DIRECTORY + [IO.Path]::PathSeparator + $env:SUGGEST_BASE_PATH
openai '@completion' pwsh --suggest-executable | Out-String | Invoke-Expression
Save-Status setup4 $?
$env:PATH = $env:SUGGEST_SENTINEL_DIRECTORY + [IO.Path]::PathSeparator + $env:SUGGEST_BASE_PATH
openai_suggestions_disable
Save-Status disable3 $?
openai_suggestions_disable
Save-Status disable4 $?
Invoke-Typo later
[Console]::Out.WriteLine('STDIN:' + [Console]::ReadLine())
exit 0
`

func TestMainShellSuggestionsRejectMixedPersistentSetup(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		for _, action := range []string{"--install-picker", "--uninstall-picker", "--automatic"} {
			t.Run(format+"/"+action, func(t *testing.T) {
				home := t.TempDir()
				profile := filepath.Join(home, "synthetic-private-profile")
				original := "# existing personal configuration\n"
				if err := os.WriteFile(profile, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				got := runMainDispatchWithEnv(t, "zsh", pickerSetupProcessEnv(home), "openai", "--format-error", format,
					"@completion", "zsh", "--suggest-executable", action, "--profile", profile)
				if got.code != 2 || got.stdout != "" || strings.Contains(got.stderr, home) || strings.Contains(got.stderr, "synthetic-private") {
					t.Fatalf("mixed setup lost failure or privacy: %+v", got)
				}
				message := got.stderr
				if format == "json" {
					var payload map[string]any
					if err := json.Unmarshal([]byte(got.stderr), &payload); err != nil || len(payload) != 1 {
						t.Fatalf("mixed setup did not emit one structured diagnostic: %v, %q", err, got.stderr)
					}
					message, _ = payload["message"].(string)
				}
				if !strings.Contains(message, "Use openai @completion SHELL --suggest-executable.") {
					t.Fatalf("mixed setup lost its repair instruction: %q", message)
				}
				if data, err := os.ReadFile(profile); err != nil || string(data) != original {
					t.Fatalf("mixed setup modified the profile: %q, %v", data, err)
				}
				if entries, err := os.ReadDir(home); err != nil || len(entries) != 1 {
					t.Fatalf("mixed setup created persistent state: %v, %v", entries, err)
				}
			})
		}
	}
}
