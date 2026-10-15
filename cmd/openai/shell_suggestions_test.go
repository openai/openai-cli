package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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
			if shell.name == "bash" || shell.name == "fish" {
				args := []string{"--noprofile", "--norc", "-c", `printf '%s\n' "${BASH_VERSINFO[0]}"`}
				if shell.name == "fish" {
					args = []string{"--no-config", "-c", `string split . -- $version`}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				version := exec.CommandContext(ctx, path, args...)
				version.WaitDelay = time.Second
				version.Dir = work
				version.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "PATH=/usr/bin:/bin", "LC_ALL=C"}
				output, err := version.Output()
				if err != nil {
					t.Fatalf("cannot inspect native %s version: %v", shell.name, err)
				}
				first, _, _ := strings.Cut(strings.TrimSpace(string(output)), "\n")
				major, err := strconv.Atoi(first)
				if err != nil {
					t.Fatalf("invalid native %s major version: %q", shell.name, first)
				}
				if major < 4 {
					if slices.Contains(required, shell.name) {
						t.Fatalf("required native %s needs version 4 or newer; found %d", shell.name, major)
					}
					t.Skipf("native %s %d does not support executable suggestions", shell.name, major)
				}
			}
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
			ignoredCache := ""
			if runtime.GOOS == "windows" && shell.name == "pwsh" {
				// PowerShell writes this native startup cache before executing the script.
				ignoredCache = filepath.FromSlash("AppData/Local/Microsoft/PowerShell/StartupProfileData-NonInteractive")
				// Keep parent directories in both snapshots to preserve their mode checks.
				if err := os.MkdirAll(filepath.Dir(filepath.Join(home, ignoredCache)), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			before := shellSuggestionConfiguration(t, home, ignoredCache)
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
			if err != nil || shell.name != "fish" && strings.ReplaceAll(stdout.String(), "\r\n", "\n") != "STDIN:SYNTHETIC_UNREAD_STDIN\n" {
				t.Fatalf("native setup failed: error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			}
			if shell.name != "fish" && strings.Contains(stderr.String(), "SYNTHETIC_PRIVATE_ARGUMENT") {
				t.Errorf("shell diagnostics exposed a private argument: %q", stderr.String())
			}
			read := func(name string) string {
				t.Helper()
				if shell.name == "fish" && (strings.HasSuffix(name, ".err") || strings.HasSuffix(name, ".out")) {
					// Fish invokes missing-command callbacks outside command redirections.
					stage := strings.TrimSuffix(strings.TrimSuffix(name, ".err"), ".out")
					begin, end := "BEGIN:"+stage+"\n", "END:"+stage+"\n"
					captured := stderr.String()
					if strings.HasSuffix(name, ".out") {
						captured = stdout.String()
					}
					captured = strings.ReplaceAll(captured, "\r\n", "\n")
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
			if shell.name == "fish" {
				if read("baseline.status") != "127\n" || read("later_baseline.status") != "127\n" ||
					strings.Count(read("baseline.err"), "PRIOR\n") != 1 ||
					strings.Count(read("later_baseline.err"), "LATER\n") != 1 ||
					strings.Contains(read("baseline.err")+read("later_baseline.err"), advice) {
					t.Fatalf("Fish native baselines did not execute the expected handlers: baseline=%q later=%q",
						read("baseline.err"), read("later_baseline.err"))
				}
			}
			for _, stage := range []string{"active", "reenabled"} {
				wantOutput, wantError, wantStatus := "", advice, "127\n"
				if shell.name == "fish" {
					// Preserve native source diagnostics, which can contain original arguments.
					// Only the added, fixed advice has a private-value omission contract.
					wantOutput = read("baseline.out")
					wantError = strings.Replace(read("baseline.err"), "PRIOR\n", "PRIOR\n"+advice, 1)
				} else if shell.name == "pwsh" {
					wantStatus = "CommandNotFoundException:73\n"
				}
				if got := read(stage + ".status"); got != wantStatus || read(stage+".out") != wantOutput || read(stage+".err") != wantError {
					t.Errorf("%s changed lookup: status=%q stdout=%q stderr=%q", stage, got, read(stage+".out"), read(stage+".err"))
				}
			}
			wantDisabled, wantLater, wantLaterOutput, wantLaterError := "127\n", "42\n", "", "LATER\n"
			if shell.name == "pwsh" {
				wantDisabled = "CommandNotFoundException:73\n"
			} else if shell.name == "fish" {
				wantLater = "127\n"
				wantLaterOutput, wantLaterError = read("later_baseline.out"), read("later_baseline.err")
			}
			wantDisabledOutput := ""
			if shell.name == "fish" {
				wantDisabledOutput = read("baseline.out")
				if read("disabled.err") != read("baseline.err") {
					t.Errorf("Fish disable changed native diagnostics: before=%q after=%q", read("baseline.err"), read("disabled.err"))
				}
			}
			if got := read("disabled.status"); got != wantDisabled || read("disabled.out") != wantDisabledOutput || strings.Contains(read("disabled.err"), "Did you mean:") {
				t.Errorf("disable did not restore lookup: status=%q stdout=%q stderr=%q", got, read("disabled.out"), read("disabled.err"))
			}
			if got := read("later.status"); got != wantLater || read("later.out") != wantLaterOutput || read("later.err") != wantLaterError {
				t.Errorf("disable replaced a later handler: status=%q stdout=%q stderr=%q", got, read("later.out"), read("later.err"))
			}
			if shell.name == "fish" {
				var wantOutput, wantError strings.Builder
				for _, stage := range []string{"baseline", "active", "disabled", "reenabled", "later_baseline", "later"} {
					wantOutput.WriteString("BEGIN:" + stage + "\n" + read(stage+".out") + "END:" + stage + "\n")
					wantError.WriteString("BEGIN:" + stage + "\n" + read(stage+".err") + "END:" + stage + "\n")
				}
				wantOutput.WriteString("STDIN:SYNTHETIC_UNREAD_STDIN\n")
				if got := strings.ReplaceAll(stdout.String(), "\r\n", "\n"); got != wantOutput.String() {
					t.Errorf("Fish changed stdin or emitted output outside lookup stages: %q", got)
				}
				if got := strings.ReplaceAll(stderr.String(), "\r\n", "\n"); got != wantError.String() {
					t.Errorf("Fish emitted unexpected process diagnostics: %q", got)
				}
			}
			arguments := []string{"opneai", "images", "generate", "--api-key=SYNTHETIC_PRIVATE_ARGUMENT", "", "space value", "quote\"value", "$(touch replayed)", "line\nvalue"}
			if got := read("later.args"); got != strings.Join(arguments, "\x00")+"\x00" {
				t.Errorf("later handler received changed arguments: %q", got)
			}
			if shell.name == "fish" {
				for _, stage := range []string{"baseline", "active", "disabled", "reenabled", "later_baseline"} {
					if got := read(stage + ".args"); got != strings.Join(arguments, "\x00")+"\x00" {
						t.Errorf("Fish %s handler received changed arguments: %q", stage, got)
					}
				}
			}
			for _, marker := range []string{"backend-called", "replayed"} {
				if _, err := os.Stat(filepath.Join(work, marker)); !os.IsNotExist(err) {
					t.Errorf("shell replayed a command: %s: %v", marker, err)
				}
			}
			if requests.Load() != 0 {
				t.Errorf("local setup or failed lookup made %d API requests", requests.Load())
			}
			if after := shellSuggestionConfiguration(t, home, ignoredCache); !maps.Equal(before, after) {
				t.Errorf("native setup modified shell configuration: before=%v after=%v", before, after)
			}
		})
	}
}

func shellSuggestionConfiguration(t *testing.T, home, ignoredCache string) map[string]string {
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
		if relative == ignoredCache && info.Mode().IsRegular() {
			return nil
		}
		files[relative] = info.Mode().String()
		if !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[relative] += fmt.Sprintf(" sha256:%x", sha256.Sum256(data))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestMainShellSuggestionsConfigurationSnapshot(t *testing.T) {
	const cache = "AppData/Local/Microsoft/PowerShell/StartupProfileData-NonInteractive"
	const profile = "config/powershell/Microsoft.PowerShell_profile.ps1"
	for _, test := range []struct {
		name         string
		changedPath  string
		initialCache bool
		includeCache bool
		wantEqual    bool
	}{
		{name: "cache_creation", changedPath: cache, wantEqual: true},
		{name: "cache_rewrite", changedPath: cache, initialCache: true, wantEqual: true},
		{name: "sibling_file", changedPath: cache + ".extra"},
		{name: "new_profile", changedPath: "Documents/PowerShell/Microsoft.PowerShell_profile.ps1"},
		{name: "existing_profile", changedPath: profile},
		{name: "profile_mode", changedPath: profile},
		{name: "cache_without_exclusion", changedPath: cache, includeCache: true},
		{name: "cache_directory", changedPath: cache},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			write := func(relative string, data []byte) {
				t.Helper()
				path := filepath.Join(home, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write(profile, []byte("# SYNTHETIC_PRIVATE_ORIGINAL_PROFILE\n"))
			cachePath := filepath.Join(home, filepath.FromSlash(cache))
			if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
				t.Fatal(err)
			}
			if test.initialCache {
				write(cache, []byte("\x00SYNTHETIC_PRIVATE_OLD_CACHE\xff"))
			}
			ignoredCache := filepath.FromSlash(cache)
			if test.includeCache {
				ignoredCache = ""
			}
			before := shellSuggestionConfiguration(t, home, ignoredCache)
			switch test.name {
			case "cache_directory":
				if err := os.Mkdir(cachePath, 0o700); err != nil {
					t.Fatal(err)
				}
			case "profile_mode":
				path := filepath.Join(home, filepath.FromSlash(profile))
				t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
				if err := os.Chmod(path, 0o400); err != nil {
					t.Fatal(err)
				}
			default:
				write(test.changedPath, []byte("\x00SYNTHETIC_PRIVATE_CHANGED_CONTENT\xff"))
			}
			after := shellSuggestionConfiguration(t, home, ignoredCache)
			if maps.Equal(before, after) != test.wantEqual {
				t.Fatalf("snapshot equality: want %v; before=%v after=%v", test.wantEqual, before, after)
			}
			if strings.Contains(fmt.Sprint(before, after), "SYNTHETIC_PRIVATE") {
				t.Fatal("configuration snapshot includes raw file content")
			}
			for parent := filepath.Dir(filepath.FromSlash(cache)); parent != "."; parent = filepath.Dir(parent) {
				if _, ok := after[parent]; !ok {
					t.Fatalf("configuration snapshot omitted cache parent %s", parent)
				}
			}
		})
	}
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
    printf '%s\000' $argv > "$suggestion_phase.args"
    printf 'PRIOR\n' >&2
    return 42
end
function invoke_typo
    opneai images generate '--api-key=SYNTHETIC_PRIVATE_ARGUMENT' '' 'space value' 'quote"value' '$(touch replayed)' 'line
value'
    printf '%s\n' $status > "$suggestion_phase.status"
end
# Keep both call sites fixed so native source diagnostics remain comparable.
for phase in baseline active disabled reenabled later_baseline later
    set -g suggestion_phase "$phase"
    set -gx PATH "$SUGGEST_CLI_DIRECTORY" /usr/bin /bin
    switch "$phase"
        case active
            openai @completion fish --suggest-executable | source
            printf '%s\n' $status > setup1.status
            openai @completion fish --suggest-executable | source
            printf '%s\n' $status > setup2.status
        case disabled
            openai_suggestions_disable
            printf '%s\n' $status > disable1.status
            openai_suggestions_disable
            printf '%s\n' $status > disable2.status
        case reenabled
            openai @completion fish --suggest-executable | source
            printf '%s\n' $status > setup3.status
        case later_baseline
            function fish_command_not_found
                printf '%s\000' $argv > "$suggestion_phase.args"
                printf 'LATER\n' >&2
                return 42
            end
        case later
            openai @completion fish --suggest-executable | source
            printf '%s\n' $status > setup4.status
            openai_suggestions_disable
            printf '%s\n' $status > disable3.status
            openai_suggestions_disable
            printf '%s\n' $status > disable4.status
    end
    set -gx PATH "$SUGGEST_SENTINEL_DIRECTORY" /usr/bin /bin
    printf 'BEGIN:%s\n' "$phase"
    printf 'BEGIN:%s\n' "$phase" >&2
    invoke_typo
    printf 'END:%s\n' "$phase"
    printf 'END:%s\n' "$phase" >&2
end
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
				if !strings.Contains(message, "Use openai '@completion' SHELL --suggest-executable.") {
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
