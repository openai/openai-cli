package autocomplete

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests launch real shells, with isolated configuration and synthetic data.
// Missing required shells fail instead of becoming a zero-behavior pass.
func runSuggestionAdapter(t *testing.T, shell, setup, body, input string, values ...string) (string, string) {
	t.Helper()
	binary, err := exec.LookPath(shell)
	if err != nil {
		for _, required := range strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",") {
			if required == shell {
				t.Fatalf("required native shell %s is unavailable", shell)
			}
		}
		t.Skipf("native %s is unavailable", shell)
	}
	directory := t.TempDir()
	fishMajor := 0
	if shell == "fish" {
		allowUnsupported := false
		for _, value := range values {
			allowUnsupported = allowUnsupported || value == "SUGGEST_ALLOW_UNSUPPORTED_FISH=1"
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		version := exec.CommandContext(ctx, binary, "--no-config", "--version")
		version.Env = []string{"HOME=" + directory, "XDG_CONFIG_HOME=" + filepath.Join(directory, "config"), "LANG=C"}
		output, versionErr := version.CombinedOutput()
		require.NoError(t, versionErr, "Fish version probe failed: %s", output)
		require.NoError(t, ctx.Err())
		fields := strings.Fields(string(output))
		require.NotEmpty(t, fields, "Fish returned an empty version")
		major, parseErr := strconv.Atoi(strings.Split(fields[len(fields)-1], ".")[0])
		require.NoError(t, parseErr, "Fish returned an unsupported version format: %s", output)
		fishMajor = major
		if major < 4 && !allowUnsupported {
			for _, required := range strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",") {
				if required == "fish" {
					t.Fatalf("required native Fish 4.0 or newer is unavailable: %s", output)
				}
			}
			t.Skipf("native Fish 4.0 or newer is unavailable: %s", output)
		}
	}
	bin := filepath.Join(directory, "bin")
	require.NoError(t, os.Mkdir(bin, 0700))
	stub := "#!/bin/sh\nprintf launched >>\"$SUGGEST_EXEC_LOG\"\nexit 98\n"
	appName := "openai"
	for _, value := range values {
		if supplied, ok := strings.CutPrefix(value, "SUGGEST_APP_NAME="); ok {
			appName = supplied
		}
	}
	name := appName
	if runtime.GOOS == "windows" {
		name += ".cmd"
		stub = "@echo off\r\necho launched>>\"%SUGGEST_EXEC_LOG%\"\r\nexit /b 98\r\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(stub), 0700))
	style, extension := CompletionStyleFish, ".fish"
	args := []string{"--no-config", "--interactive"}
	useFishPTY := false
	for _, value := range values {
		if value == "SUGGEST_NONINTERACTIVE=1" {
			args = []string{"--no-config"}
		}
		if shell == "fish" && value == "SUGGEST_FISH_CANCELLATION_PTY=1" {
			useFishPTY = true
		}
	}
	if shell == "pwsh" {
		style, extension = CompletionStylePowershell, ".ps1"
		args = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File"}
	}
	script, err := RenderShellSuggestions(style, appName)
	require.NoError(t, err)
	for _, value := range values {
		if shell == "fish" && fishMajor >= 4 && value == "SUGGEST_SIMULATE_OLD_FISH=1" {
			const detector = "(string split . -- $version)"
			require.Equal(t, 1, strings.Count(script, detector))
			script = strings.Replace(script, detector, "(string split . -- 3.7.1)", 1)
		}
	}
	hook := filepath.Join(directory, "suggestions"+extension)
	require.NoError(t, os.WriteFile(hook, []byte(script), 0600))
	probe := filepath.Join(directory, "probe"+extension)
	probeScript := setup + "\n" + body
	if shell == "pwsh" {
		// PowerShell adds known-folder module paths before script startup.
		// Reset them before setup while preserving native module autoload.
		probeScript = "$env:PSModulePath = [IO.Path]::Combine($PSHOME, 'Modules')\n" + probeScript
	}
	require.NoError(t, os.WriteFile(probe, []byte(probeScript), 0600))
	fishBinary := binary
	if useFishPTY {
		binary, err = exec.LookPath("expect")
		if err != nil {
			for _, required := range strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",") {
				if required == "fish" {
					t.Fatal("required native Fish cancellation test needs expect")
				}
			}
			t.Skip("expect is required for native Fish cancellation")
		}
		driver := filepath.Join(directory, "cancellation.exp")
		require.NoError(t, os.WriteFile(driver, []byte(fishSuggestionCancellationDriver), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(directory, "continue.fish"), []byte(input), 0600))
		args = []string{"-N", "-n", driver}
		input = ""
	} else {
		args = append(args, probe)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.WaitDelay = time.Second
	if useFishPTY {
		command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	}
	command.Dir = directory
	command.Env = []string{
		"HOME=" + directory, "USERPROFILE=" + directory,
		"XDG_CONFIG_HOME=" + filepath.Join(directory, "config"),
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"LANG=C", "TERM=dumb", "NO_COLOR=1",
		"POWERSHELL_TELEMETRY_OPTOUT=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1", "POWERSHELL_UPDATECHECK=Off",
		"SUGGEST_HOOK=" + hook, "SUGGEST_PROBE=" + probe, "SUGGEST_EXEC_LOG=" + filepath.Join(directory, "launched"),
		"SUGGEST_EXECUTABLE=" + filepath.Join(bin, name),
	}
	if runtime.GOOS == "windows" {
		command.Env = append(command.Env, "SystemRoot="+os.Getenv("SystemRoot"), "PATHEXT="+os.Getenv("PATHEXT"))
	}
	command.Env = append(command.Env, values...)
	if shell == "pwsh" {
		command.Env = append(command.Env, "PSModuleAnalysisCachePath="+filepath.Join(directory, "ModuleAnalysisCache"))
	}
	if useFishPTY {
		command.Env = append(command.Env,
			"SUGGEST_FISH_BINARY="+fishBinary,
			"SUGGEST_CONTINUATION="+filepath.Join(directory, "continue.fish"),
			"SUGGEST_STDOUT="+filepath.Join(directory, "continuation.stdout"),
			"SUGGEST_STDERR="+filepath.Join(directory, "continuation.stderr"),
			"SUGGEST_TRANSCRIPT="+filepath.Join(directory, "terminal.log"))
	}
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("%s adapter stdout=%q stderr=%q", shell, stdout.String(), stderr.String())
			if useFishPTY {
				if transcript, readErr := os.ReadFile(filepath.Join(directory, "terminal.log")); readErr == nil {
					t.Logf("Fish cancellation transcript=%q", transcript)
				}
			}
		}
	})
	started := time.Now()
	runErr := command.Run()
	contextErr := ctx.Err()
	elapsed := time.Since(started).Round(time.Millisecond)
	processState := command.ProcessState
	for _, value := range values {
		if value == "SUGGEST_PHASE_RUN=baseline" || value == "SUGGEST_PHASE_RUN=hooked" {
			t.Logf("PowerShell %s start=%s elapsed=%s context=%v process=%v",
				strings.TrimPrefix(value, "SUGGEST_PHASE_RUN="), started.UTC().Format(time.RFC3339Nano), elapsed, contextErr, processState)
		}
	}
	require.NoError(t, runErr, "%s adapter ended after %s (context=%v, process=%v): stdout=%s\nstderr=%s",
		shell, elapsed, contextErr, processState, stdout.String(), stderr.String())
	require.NoError(t, contextErr)
	if useFishPTY {
		transcript, readErr := os.ReadFile(filepath.Join(directory, "terminal.log"))
		require.NoError(t, readErr)
		require.NotContains(t, string(transcript), "UNEXPECTED_AFTER_INTERRUPT", "delegation continued after SIGINT")
	}
	_, err = os.Stat(filepath.Join(directory, "launched"))
	require.ErrorIs(t, err, os.ErrNotExist, "the suggestion hook launched the bound executable")
	return strings.ReplaceAll(stdout.String(), "\r\n", "\n"), strings.ReplaceAll(stderr.String(), "\r\n", "\n")
}

// The PTY keeps interactive Fish alive after SIGINT. Each fixture owns one Fish child.
// The driver reaps that child on success, timeout, or a caught termination signal.
const fishSuggestionCancellationDriver = `
set timeout 5
set child_pid 0
proc owned_child {} {
    global child_pid
    if {$child_pid == 0} { return 0 }
    if {[catch {exec /bin/ps -p $child_pid -o ppid=} parent details]} {
        set errorcode [dict get $details -errorcode]
        if {[lindex $errorcode 0] eq "CHILDSTATUS" && [lindex $errorcode 2] == 1 && [string trim $parent] eq ""} {
            return 0
        }
        puts stderr "Cannot verify Fish child ownership: $parent"
        return -1
    }
    if {![string is integer -strict [string trim $parent]]} {
        puts stderr "Invalid Fish child ownership response: $parent"
        return -1
    }
    return [expr {[string trim $parent] eq [pid]}]
}
proc finish {code} {
    global child_pid
    if {$code != 0} {
        if {[owned_child] == 1} { catch {exec /bin/kill -TERM $child_pid} }
        after 150
        if {[owned_child] == 1} { catch {exec /bin/kill -KILL $child_pid} }
        catch {close}
        catch {wait}
    }
    exit $code
}
trap {finish 130} {SIGINT SIGTERM}
after 10000 {puts stderr "Fish cancellation deadline expired"; finish 124}
if {[catch {
log_user 0
log_file -a -noappend $env(SUGGEST_TRANSCRIPT)
spawn -noecho $env(SUGGEST_FISH_BINARY) --no-config --interactive --init-command {set -g fish_greeting ''; function fish_prompt; printf 'SUGGESTION_TEST> '; end; function fish_right_prompt; end}
set child_pid [exp_pid]
expect_after {
    timeout {puts stderr "Fish cancellation stage timed out"; finish 124}
    eof {puts stderr "Fish exited before continuation"; finish 1}
}
expect -exact "SUGGESTION_TEST> "
send -- {source "$SUGGEST_PROBE"}
send -- "\r"
expect -exact "__SUGGESTION_READY__\r\n"
expect -exact "SUGGESTION_TEST> "
send -- {source "$SUGGEST_CONTINUATION" >"$SUGGEST_STDOUT" 2>"$SUGGEST_STDERR"; printf '__SUGGESTION_DONE__\n'}
send -- "\r"
expect -exact "__SUGGESTION_DONE__\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "exit 0\r"
expect eof
set result [wait]
if {[lindex $result 2] != 0 || [lindex $result 3] != 0} {
    puts stderr "Fish cancellation child exited unsuccessfully: $result"
    finish 1
}
set output [open $env(SUGGEST_STDOUT) r]
puts -nonewline stdout [read $output]
close $output
set errors [open $env(SUGGEST_STDERR) r]
puts -nonewline stderr [read $errors]
close $errors
} failure]} {
    puts stderr "Fish cancellation driver failed: $failure"
    finish 1
}
finish 0
`

const suggestionAdvice = "Command not found. Did you mean: openai?\nKeep the remaining arguments unchanged.\n"

func TestShellSuggestionsFishMatcherAndPrivacy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		match bool
	}{
		{"opneai", true}, {"opena", true}, {"xopenai", true}, {"openxai", true},
		{"openaii", true}, {"opxnai", true}, {"Openai", true}, {"open-ai", true},
		{"openai", false}, {"unrelated", false}, {"./opneai", false}, {"opneai\n", false},
		{"opneai\x1b", false}, {"openai;", false}, {"", false}, {strings.Repeat("a", 1000), false},
	} {
		t.Run(fmt.Sprintf("%q", tc.name), func(t *testing.T) {
			out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'PRIOR\n' >&2
    return 42
end`, `
source "$SUGGEST_HOOK"
fish_command_not_found "$SUGGEST_NAME" 'FAKE_PRIVATE_MARKER' 'quote"value' '$(never-run)' ''
set -l result $status
read --local remaining
printf 'STATUS:%s\nSTDIN:%s\n' $result "$remaining"
`, "untouched\n", "SUGGEST_NAME="+tc.name)
			wantStatus, wantErr := 42, "PRIOR\n"
			if tc.match {
				wantErr += suggestionAdvice
			}
			require.Equal(t, fmt.Sprintf("STATUS:%d\nSTDIN:untouched\n", wantStatus), out)
			require.Equal(t, wantErr, errOut)
		})
	}
}

func TestShellSuggestionsFishPublicLookupAndLifecycle(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'PRIOR\n' >&2
    return 42
end
function probe_missing
    opneai images generate 'FAKE_PRIVATE_MARKER'
end`, `
for phase in baseline enabled
    if test "$phase" = enabled
        source "$SUGGEST_HOOK"
        source "$SUGGEST_HOOK"
    end
    printf 'BEGIN:%s\n' "$phase" >&2
    probe_missing
    printf 'PUBLIC:%s:%s\n' "$phase" $status
    printf 'END:%s\n' "$phase" >&2
end
openai_suggestions_disable
openai_suggestions_disable
fish_command_not_found unrelated
printf 'RESTORED:%s\n' $status
source "$SUGGEST_HOOK"
function opneai
    printf 'COLLISION\n'
    return 19
end
opneai images generate
printf 'COLLISION_STATUS:%s\n' $status
functions --copy fish_command_not_found retained_wrapper
function fish_command_not_found
    retained_wrapper $argv
end
source "$SUGGEST_HOOK"
openai_suggestions_disable
fish_command_not_found opneai 'FAKE_PRIVATE_MARKER'
printf 'REPLACED:%s\n' $status
source "$SUGGEST_HOOK"
fish_command_not_found opneai
printf 'REENABLED:%s\n' $status
`, "")
	require.Equal(t, "PUBLIC:baseline:127\nPUBLIC:enabled:127\nRESTORED:42\nCOLLISION\nCOLLISION_STATUS:19\nREPLACED:42\nREENABLED:42\n", out)
	segments := make(map[string]string)
	for _, phase := range []string{"baseline", "enabled"} {
		begin, end := "BEGIN:"+phase+"\n", "END:"+phase+"\n"
		require.Equal(t, 1, strings.Count(errOut, begin))
		require.Equal(t, 1, strings.Count(errOut, end))
		start, finish := strings.Index(errOut, begin)+len(begin), strings.Index(errOut, end)
		require.GreaterOrEqual(t, finish, start)
		segments[phase] = errOut[start:finish]
	}
	require.True(t, strings.HasPrefix(segments["baseline"], "PRIOR\n"))
	require.Contains(t, segments["baseline"], "opneai images generate 'FAKE_PRIVATE_MARKER'")
	require.Equal(t, "PRIOR\n"+suggestionAdvice+strings.TrimPrefix(segments["baseline"], "PRIOR\n"), segments["enabled"])
	require.Equal(t, "BEGIN:baseline\n"+segments["baseline"]+"END:baseline\n"+
		"BEGIN:enabled\n"+segments["enabled"]+"END:enabled\nPRIOR\nPRIOR\nPRIOR\n"+suggestionAdvice, errOut)
}

func TestShellSuggestionsFishDelegatesArgumentsAndStatus(t *testing.T) {
	for _, status := range []int{0, 42, 127} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf '<%s>\n' $argv
    printf 'PRIOR_ERROR\n' >&2
    return $SUGGEST_STATUS
end`, `
source "$SUGGEST_HOOK"
fish_command_not_found unrelated '' 'space value' 'quote"value' 'literal$(false)' "line
break"
printf 'STATUS:%s\n' $status
`, "", "SUGGEST_STATUS="+fmt.Sprint(status))
			require.Equal(t, "<unrelated>\n<>\n<space value>\n<quote\"value>\n<literal$(false)>\n<line\nbreak>\nSTATUS:"+fmt.Sprint(status)+"\n", out)
			require.Equal(t, "PRIOR_ERROR\n", errOut)
		})
	}
}

func TestShellSuggestionsFishRefusesLegacyEventWithoutMutation(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
# This is fish 3.2's shipped fallback contract: printf returns 0.
function __fish_default_command_not_found_handler
    printf 'DEFAULT:%s\n' "$argv[1]" >&2
end
function fish_command_not_found --on-event fish_command_not_found
    __fish_default_command_not_found_handler $argv
end`, `
set -l before (functions fish_command_not_found | string collect)
set -l events_before (functions --handlers | string collect)
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\n' $status
set -l after (functions fish_command_not_found | string collect)
test "$before" = "$after"
printf 'UNCHANGED:%s\n' $status
set -l events_after (functions --handlers | string collect)
test "$events_before" = "$events_after"
printf 'EVENTS:%s\n' $status
fish_command_not_found opneai
printf 'DIRECT:%s\n' $status
emit fish_command_not_found unrelated
functions --query openai_suggestions_disable
printf 'DISABLE:%s\n' $status
`, "")
	require.Equal(t, "SOURCE:1\nUNCHANGED:0\nEVENTS:0\nDIRECT:0\nDISABLE:1\n", out)
	require.Equal(t, "Executable suggestions cannot retain this custom event handler. Keep the existing handler and use CLI help.\n"+
		"DEFAULT:opneai\nDEFAULT:unrelated\n", errOut)
}

func TestShellSuggestionsFishOriginalNameAndSelfReplacement(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'NAME:%s\n' (status current-function)
    function fish_command_not_found
        printf 'REPLACEMENT\n'
        return 63
    end
    return 42
end`, `
source "$SUGGEST_HOOK"
fish_command_not_found unrelated
printf 'FIRST:%s\n' $status
fish_command_not_found unrelated
printf 'SECOND:%s\n' $status
openai_suggestions_disable
fish_command_not_found unrelated
printf 'DISABLED:%s\n' $status
`, "")
	require.Equal(t, "NAME:fish_command_not_found\nFIRST:42\nREPLACEMENT\nSECOND:63\nREPLACEMENT\nDISABLED:63\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsFishRejectsUnsupportedEventsWithoutMutation(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found --on-event custom_missing_event
    printf 'CUSTOM\n'
    return 42
end`, `
set -l before (functions fish_command_not_found | string collect)
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\n' $status
set -l after (functions fish_command_not_found | string collect)
test "$before" = "$after"
printf 'UNCHANGED:%s\n' $status
emit custom_missing_event
read --local remaining
printf 'STDIN:%s\n' "$remaining"
`, "untouched\n")
	require.Equal(t, "SOURCE:1\nUNCHANGED:0\nCUSTOM\nSTDIN:untouched\n", out)
	require.Equal(t, "Executable suggestions cannot retain this custom event handler. Keep the existing handler and use CLI help.\n", errOut)
}

func TestShellSuggestionsFishDescriptionValueIsNotEventMetadata(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found --description '--on-signal'
    printf 'PRIOR\n'
    return 42
end`, `
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\n' $status
fish_command_not_found unrelated
printf 'PRIOR_STATUS:%s\n' $status
openai_suggestions_disable
fish_command_not_found unrelated
printf 'RESTORED_STATUS:%s\n' $status
read --local remaining
printf 'STDIN:%s\n' "$remaining"
`, "untouched\n")
	require.Equal(t, "SOURCE:0\nPRIOR\nPRIOR_STATUS:42\nPRIOR\nRESTORED_STATUS:42\nSTDIN:untouched\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsFishRefusesSignalAndVariableEvents(t *testing.T) {
	for _, option := range []string{"--on-signal USR1", "--on-variable SUGGEST_EVENT_VARIABLE"} {
		t.Run(option, func(t *testing.T) {
			out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found `+option+`
    printf 'PRIOR\n'
    return 42
end`, `
set -l before (functions fish_command_not_found | string collect)
set -l events_before (functions --handlers | string collect)
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\n' $status
set -l after (functions fish_command_not_found | string collect)
set -l events_after (functions --handlers | string collect)
test "$before" = "$after"; and test "$events_before" = "$events_after"
printf 'UNCHANGED:%s\n' $status
fish_command_not_found unrelated
printf 'STATUS:%s\n' $status
functions --query __openai_suggestions_prior_1
printf 'COPY:%s\n' $status
`, "")
			require.Equal(t, "SOURCE:1\nUNCHANGED:0\nPRIOR\nSTATUS:42\nCOPY:1\n", out)
			require.Equal(t, "Executable suggestions cannot retain this custom event handler. Keep the existing handler and use CLI help.\n", errOut)
		})
	}
}

func TestShellSuggestionsFishNoninteractiveDoesNotActivate(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'PRIOR\n' >&2
    return 42
end`, `
set -l before (functions fish_command_not_found | string collect)
source "$SUGGEST_HOOK"
set -l after (functions fish_command_not_found | string collect)
test "$before" = "$after"
printf 'UNCHANGED:%s\n' $status
functions --query openai_suggestions_disable
printf 'DISABLE:%s\n' $status
`, "", "SUGGEST_NONINTERACTIVE=1", "SUGGEST_ALLOW_UNSUPPORTED_FISH=1")
	require.Equal(t, "UNCHANGED:0\nDISABLE:1\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsFishRefusesUnsupportedVersionBeforeMutation(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'PRIOR\n'
    return 42
end
set -l before (functions fish_command_not_found | string collect)
set -l functions_before (functions --names --all | string collect)
set -l events_before (functions --handlers | string collect)
`, `
source "$SUGGEST_HOOK"
printf 'ENABLE:%s\n' $status
set -l after (functions fish_command_not_found | string collect)
test "$before" = "$after"
printf 'BODY:%s\n' $status
set -l functions_after (functions --names --all | string collect)
test "$functions_before" = "$functions_after"
printf 'FUNCTIONS:%s\n' $status
set -l events_after (functions --handlers | string collect)
test "$events_before" = "$events_after"
printf 'EVENTS:%s\n' $status
set -q __openai_suggestions_serial
printf 'STATE:%s\n' $status
fish_command_not_found opneai
printf 'PRIOR_STATUS:%s\n' $status
`, "", "SUGGEST_ALLOW_UNSUPPORTED_FISH=1", "SUGGEST_SIMULATE_OLD_FISH=1")
	require.Equal(t, "ENABLE:1\nBODY:0\nFUNCTIONS:0\nEVENTS:0\nSTATE:1\nPRIOR\nPRIOR_STATUS:42\n", out)
	require.Equal(t, "Executable suggestions require fish 4.0 or newer.\n", errOut)
}

func TestShellSuggestionsFishRefusesCompletionWrappers(t *testing.T) {
	for _, mode := range []string{"initial", "later"} {
		t.Run(mode, func(t *testing.T) {
			out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    return 42
end
if test "$SUGGEST_WRAP_MODE" = later
    source "$SUGGEST_HOOK"
    functions --copy fish_command_not_found retained_wrapper
end
function fish_command_not_found --wraps=synthetic_command
    return 63
end
set -l before (functions fish_command_not_found | string collect)
set -l functions_before (functions --names --all | string collect)
set -l events_before (functions --handlers | string collect)
`, `
if test "$SUGGEST_WRAP_MODE" = later
    retained_wrapper opneai
    printf 'RESULT:%s\n' $status
else
    source "$SUGGEST_HOOK"
    printf 'RESULT:%s\n' $status
end
set -l after (functions fish_command_not_found | string collect)
test "$before" = "$after"
printf 'BODY:%s\n' $status
set -l functions_after (functions --names --all | string collect)
test "$functions_before" = "$functions_after"
printf 'FUNCTIONS:%s\n' $status
set -l events_after (functions --handlers | string collect)
test "$events_before" = "$events_after"
printf 'EVENTS:%s\n' $status
fish_command_not_found unrelated
printf 'PRESERVED:%s\n' $status
`, "", "SUGGEST_WRAP_MODE="+mode)
			status := 1
			if mode == "later" {
				status = 127
			}
			require.Equal(t, fmt.Sprintf("RESULT:%d\nBODY:0\nFUNCTIONS:0\nEVENTS:0\nPRESERVED:63\n", status), out)
			require.Equal(t, "Executable suggestions cannot retain a handler with completion wrappers. Keep the existing handler and use CLI help.\n", errOut)
		})
	}
}

func TestShellSuggestionsFishRefusesAmbiguousSourceMetadata(t *testing.T) {
	prior := filepath.Join(t.TempDir(), "prior\nsource.fish")
	require.NoError(t, os.WriteFile(prior, []byte(`
function fish_command_not_found --no-scope-shadowing
    return 42
end
`), 0600))
	out, errOut := runSuggestionAdapter(t, "fish", `
source "$SUGGEST_PRIOR"
set -l before (functions fish_command_not_found | string collect)
set -l functions_before (functions --names --all | string collect)
`, `
source "$SUGGEST_HOOK"
printf 'ENABLE:%s\n' $status
set -l after (functions fish_command_not_found | string collect)
test "$before" = "$after"
printf 'BODY:%s\n' $status
set -l functions_after (functions --names --all | string collect)
test "$functions_before" = "$functions_after"
printf 'FUNCTIONS:%s\n' $status
set -q __openai_suggestions_serial
printf 'STATE:%s\n' $status
fish_command_not_found unrelated
printf 'PRESERVED:%s\n' $status
`, "", "SUGGEST_PRIOR="+prior)
	require.Equal(t, "ENABLE:1\nBODY:0\nFUNCTIONS:0\nSTATE:1\nPRESERVED:42\n", out)
	require.Equal(t, "Executable suggestions cannot retain this handler metadata. Keep the existing handler and use CLI help.\n", errOut)
}

func TestShellSuggestionsFishRefusesForeignPendingStack(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function synthetic_foreign_cleanup
    printf 'UNEXPECTED_FOREIGN_EXECUTION\n'
end
function fish_command_not_found
    return 42
end
set -g __openai_suggestions_pending_restores synthetic_foreign_cleanup
set -l before (functions fish_command_not_found | string collect)
set -l events_before (functions --handlers | string collect)
set -l functions_before (functions --names --all | string collect)
`, `
source "$SUGGEST_HOOK"
printf 'ENABLE:%s\n' $status
emit fish_prompt
emit fish_postexec SYNTHETIC_PRIVATE_EVENT_ARGUMENT
set -l after (functions fish_command_not_found | string collect)
test "$before" = "$after"
printf 'BODY:%s\n' $status
set -l events_after (functions --handlers | string collect)
test "$events_before" = "$events_after"
printf 'EVENTS:%s\n' $status
set -l functions_after (functions --names --all | string collect)
test "$functions_before" = "$functions_after"
printf 'FUNCTIONS:%s\n' $status
printf 'FOREIGN:%s\n' "$__openai_suggestions_pending_restores"
`, "")
	require.Equal(t, "ENABLE:1\nBODY:0\nEVENTS:0\nFUNCTIONS:0\nFOREIGN:synthetic_foreign_cleanup\n", out)
	require.Equal(t, "Executable suggestions conflict with existing shell state. Keep the existing handler and use CLI help.\n", errOut)
}

func TestShellSuggestionsFishPreservesMultilineHandlerLiterals(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
set -l captured_values 'first inherited line
# inherited literal text
last inherited line' '' 'inherited space value'
function fish_command_not_found --description 'synthetic multiline handler' --inherit-variable captured_values
    printf 'LITERAL:<%s>\n' 'first line
# this remains literal text
last line'
    printf 'INHERITED:<%s>\n' $captured_values
    printf 'ARG:<%s>\n' $argv
    printf 'PRIOR_ERROR\n' >&2
    return 42
end
set captured_values 'changed after definition'`, `
printf 'BASELINE\n'
fish_command_not_found unrelated '' 'space value'
printf 'STATUS:%s\n' $status
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\nACTIVE\n' $status
fish_command_not_found unrelated '' 'space value'
printf 'STATUS:%s\n' $status
openai_suggestions_disable
printf 'DISABLE:%s\nRESTORED\n' $status
fish_command_not_found unrelated '' 'space value'
printf 'STATUS:%s\n' $status
read --local remaining
printf 'STDIN:%s\n' "$remaining"
`, "untouched\n")
	prior := "LITERAL:<first line\n# this remains literal text\nlast line>\n" +
		"INHERITED:<first inherited line\n# inherited literal text\nlast inherited line>\n" +
		"INHERITED:<>\nINHERITED:<inherited space value>\nARG:<unrelated>\nARG:<>\nARG:<space value>\nSTATUS:42\n"
	require.Equal(t, "BASELINE\n"+prior+"SOURCE:0\nACTIVE\n"+prior+"DISABLE:0\nRESTORED\n"+prior+"STDIN:untouched\n", out)
	require.Equal(t, strings.Repeat("PRIOR_ERROR\n", 3), errOut)
}

func TestShellSuggestionsFishPreservesEmptyHandlerAndInput(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
end`, `
fish_command_not_found unrelated
set -l baseline $status
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\n' $status
fish_command_not_found unrelated
set -l active $status
test "$active" = "$baseline"
printf 'ACTIVE_EQUAL:%s\n' $status
openai_suggestions_disable
printf 'DISABLE:%s\n' $status
fish_command_not_found unrelated
set -l restored $status
test "$restored" = "$baseline"
printf 'RESTORED_EQUAL:%s\n' $status
read --local remaining
printf 'STDIN:%s\n' "$remaining"
`, "untouched\n")
	require.Equal(t, "SOURCE:0\nACTIVE_EQUAL:0\nDISABLE:0\nRESTORED_EQUAL:0\nSTDIN:untouched\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsFishAbsentCurrentHandlerDoesNotReadInput(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'PRIOR\n'
    return 42
end`, `
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\n' $status
functions --copy fish_command_not_found retained_wrapper
functions --erase fish_command_not_found
retained_wrapper unrelated
printf 'STATUS:%s\n' $status
functions --query fish_command_not_found
printf 'CURRENT:%s\n' $status
openai_suggestions_disable
printf 'DISABLE:%s\n' $status
functions --query fish_command_not_found
printf 'DISABLED_CURRENT:%s\n' $status
read --local remaining
printf 'STDIN:%s\n' "$remaining"
`, "untouched\n")
	require.Equal(t, "SOURCE:0\nPRIOR\nSTATUS:42\nCURRENT:1\nDISABLE:0\nDISABLED_CURRENT:1\nSTDIN:untouched\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsFishPreservesPriorSelfRemoval(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function install_synthetic_prior
    function fish_command_not_found
        functions --erase fish_command_not_found
        printf 'REMOVED\n'
        return 42
    end
end
install_synthetic_prior`, `
printf 'BASELINE\n'
fish_command_not_found unrelated
printf 'STATUS:%s\n' $status
functions --query fish_command_not_found
printf 'CURRENT:%s\n' $status
install_synthetic_prior
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\nACTIVE\n' $status
fish_command_not_found unrelated
printf 'STATUS:%s\n' $status
functions --query fish_command_not_found
printf 'CURRENT:%s\n' $status
openai_suggestions_disable
printf 'DISABLE:%s\n' $status
functions --query fish_command_not_found
printf 'DISABLED_CURRENT:%s\n' $status
read --local remaining
printf 'STDIN:%s\n' "$remaining"
`, "untouched\n")
	removed := "REMOVED\nSTATUS:42\nCURRENT:1\n"
	require.Equal(t, "BASELINE\n"+removed+"SOURCE:0\nACTIVE\n"+removed+"DISABLE:0\nDISABLED_CURRENT:1\nSTDIN:untouched\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsFishAbsentPriorPreservesFallbackAndInput(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
functions --erase fish_command_not_found
function __fish_default_command_not_found_handler
    printf 'DEFAULT:<%s>\n' $argv
    return 42
end`, `
printf 'BASELINE\n'
__fish_default_command_not_found_handler unrelated '' 'space value'
printf 'STATUS:%s\n' $status
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\nACTIVE\n' $status
fish_command_not_found unrelated '' 'space value'
printf 'STATUS:%s\n' $status
openai_suggestions_disable
printf 'DISABLE:%s\n' $status
functions --query fish_command_not_found
printf 'CURRENT:%s\n' $status
printf 'RESTORED\n'
__fish_default_command_not_found_handler unrelated '' 'space value'
printf 'STATUS:%s\n' $status
read --local remaining
printf 'STDIN:%s\n' "$remaining"
`, "untouched\n")
	prior := "DEFAULT:<unrelated>\nDEFAULT:<>\nDEFAULT:<space value>\nSTATUS:42\n"
	require.Equal(t, "BASELINE\n"+prior+"SOURCE:0\nACTIVE\n"+prior+"DISABLE:0\nCURRENT:1\nRESTORED\n"+prior+"STDIN:untouched\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsFishTraceDoesNotDiscloseUnusedHandlerLiterals(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    if false
        printf 'FAKE_UNUSED_HANDLER_SECRET\n' >&2
    end
    printf 'PRIOR_TRACE_SENTINEL\n' >&2
    return 42
end
set -g fish_trace 1`, `
source "$SUGGEST_HOOK"
fish_command_not_found opneai
set -l result $status
openai_suggestions_disable
printf 'STATUS:%s\nTRACE:%s\n' $result "$fish_trace"
`, "")
	require.Equal(t, "STATUS:42\nTRACE:1\n", out)
	require.NotContains(t, errOut, "FAKE_UNUSED_HANDLER_SECRET")
	require.GreaterOrEqual(t, strings.Count(errOut, "PRIOR_TRACE_SENTINEL"), 2, "the prior handler must retain tracing")
	require.Contains(t, errOut, "PRIOR_TRACE_SENTINEL\n")
	require.Contains(t, errOut, suggestionAdvice)
}

func TestShellSuggestionsFishPreservesSourceAndSiblingResource(t *testing.T) {
	directory := t.TempDir()
	prior := filepath.Join(directory, "prior.fish")
	require.NoError(t, os.WriteFile(filepath.Join(directory, "sibling.txt"), []byte("SIBLING_RESOURCE\n"), 0600))
	require.NoError(t, os.WriteFile(prior, []byte(`function fish_command_not_found
    printf 'SOURCE:%s\n' (status filename)
    printf 'DIRECTORY:%s\n' (status dirname)
    printf 'FUNCTION:%s\n' (status current-function)
    read --local resource < (status dirname)/sibling.txt
    printf 'RESOURCE:%s\n' "$resource"
    printf 'ARG:%s\n' $argv
    printf 'PRIOR_ERROR\n' >&2
    return 42
end
`), 0600))
	out, errOut := runSuggestionAdapter(t, "fish", `
source "$SUGGEST_PRIOR"
set -l baseline_details (functions --details --verbose fish_command_not_found)
function probe_missing
    opneai synthetic_argument
end`, `
for phase in baseline enabled disabled
    if test "$phase" = enabled
        source "$SUGGEST_HOOK"
        printf 'SOURCE_STATUS:%s\n' $status
    else if test "$phase" = disabled
        openai_suggestions_disable
        printf 'DISABLE_STATUS:%s\n' $status
    end
    printf 'BEGIN:%s\n' "$phase" >&2
    probe_missing
    printf 'LOOKUP:%s:%s\n' "$phase" $status
    printf 'END:%s\n' "$phase" >&2
    read --local remaining
    printf 'STDIN:%s:%s\n' "$phase" "$remaining"
    functions --query __openai_suggestions_current_1
    printf 'TEMP:%s:%s\n' "$phase" $status
end
set -l restored_details (functions --details --verbose fish_command_not_found)
set -l restored_origin "$restored_details[1]"
if not contains -- "$restored_details[2]" autoloaded not-autoloaded n/a
    set restored_origin "$restored_details[2]"
end
test "$baseline_details[1]" = "$restored_origin"
and test "$baseline_details[3]" = "$restored_details[3]"
and test "$baseline_details[4]" = "$restored_details[4]"
and test "$baseline_details[5]" = "$restored_details[5]"
printf 'DETAILS:%s\n' $status
`, "baseline-unread\nenabled-unread\ndisabled-unread\n", "SUGGEST_PRIOR="+prior)
	require.Equal(t, "LOOKUP:baseline:127\nSTDIN:baseline:baseline-unread\nTEMP:baseline:1\n"+
		"SOURCE_STATUS:0\nLOOKUP:enabled:127\nSTDIN:enabled:enabled-unread\nTEMP:enabled:1\n"+
		"DISABLE_STATUS:0\nLOOKUP:disabled:127\nSTDIN:disabled:disabled-unread\nTEMP:disabled:1\nDETAILS:0\n", out)
	prefix := "SOURCE:" + prior + "\nDIRECTORY:" + directory + "\nFUNCTION:fish_command_not_found\n" +
		"RESOURCE:SIBLING_RESOURCE\nARG:opneai\nARG:synthetic_argument\nPRIOR_ERROR\n"
	segments := make(map[string]string)
	for _, phase := range []string{"baseline", "enabled", "disabled"} {
		begin, end := "BEGIN:"+phase+"\n", "END:"+phase+"\n"
		require.Equal(t, 1, strings.Count(errOut, begin))
		require.Equal(t, 1, strings.Count(errOut, end))
		start := strings.Index(errOut, begin) + len(begin)
		finish := strings.Index(errOut, end)
		require.GreaterOrEqual(t, finish, start)
		segments[phase] = errOut[start:finish]
		require.True(t, strings.HasPrefix(segments[phase], prefix), "%s: %q", phase, segments[phase])
	}
	require.Equal(t, prefix+suggestionAdvice+strings.TrimPrefix(segments["baseline"], prefix), segments["enabled"])
	require.Equal(t, segments["baseline"], segments["disabled"])
}

func TestShellSuggestionsFishRefusesCallerScopeWithoutMutation(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found --no-scope-shadowing
    printf 'SCOPE:%s\n' "$result"
    return 42
end
function call_prior
    set -l result CALLER_LOCAL
    fish_command_not_found unrelated
end`, `
set -l before (functions fish_command_not_found | string collect)
set -l details_before (functions --details --verbose fish_command_not_found | string collect)
call_prior
printf 'BASELINE:%s\n' $status
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\n' $status
set -l after (functions fish_command_not_found | string collect)
set -l details_after (functions --details --verbose fish_command_not_found | string collect)
test "$before" = "$after"; and test "$details_before" = "$details_after"
printf 'UNCHANGED:%s\n' $status
call_prior
printf 'PRESERVED:%s\n' $status
functions --query openai_suggestions_disable
printf 'DISABLE:%s\n' $status
functions --query __openai_suggestions_prior_1
printf 'COPY:%s\n' $status
set -q __openai_suggestions_app
printf 'ACTIVE:%s\n' $status
read --local remaining
printf 'STDIN:%s\n' "$remaining"
`, "untouched\n")
	require.Equal(t, "SCOPE:CALLER_LOCAL\nBASELINE:42\nSOURCE:1\nUNCHANGED:0\nSCOPE:CALLER_LOCAL\nPRESERVED:42\n"+
		"DISABLE:1\nCOPY:1\nACTIVE:1\nSTDIN:untouched\n", out)
	require.Equal(t, "Executable suggestions cannot retain a handler that reads caller variables. Keep the existing handler and use CLI help.\n", errOut)
}

func TestShellSuggestionsFishRejectsPrivateCopyCollisions(t *testing.T) {
	for _, name := range []string{"__openai_suggestions_prior_1", "__openai_suggestions_current_1"} {
		t.Run(name, func(t *testing.T) {
			out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'PRIOR\n'
    return 42
end
function `+name+`
    printf 'FOREIGN\n'
end`, `
set -l before (functions fish_command_not_found | string collect)
source "$SUGGEST_HOOK"
printf 'SOURCE:%s\n' $status
set -l after (functions fish_command_not_found | string collect)
test "$before" = "$after"
printf 'UNCHANGED:%s\n' $status
`+name+`
fish_command_not_found unrelated
printf 'STATUS:%s\n' $status
set -q __openai_suggestions_app
printf 'ACTIVE:%s\n' $status
`, "")
			require.Equal(t, "SOURCE:1\nUNCHANGED:0\nFOREIGN\nPRIOR\nSTATUS:42\nACTIVE:1\n", out)
			require.Equal(t, "Executable suggestions conflict with existing shell functions. Keep the existing handler and use CLI help.\n", errOut)
		})
	}
}

func TestShellSuggestionsFishRetainedWrapperAfterOwnedDisable(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'PRIOR\n'
    return 42
end`, `
set -l before (functions --details --verbose fish_command_not_found)
set -l body_before (functions --no-details fish_command_not_found | string collect)
source "$SUGGEST_HOOK"
functions --copy fish_command_not_found retained_wrapper
openai_suggestions_disable
retained_wrapper opneai
printf 'RETAINED:%s\n' $status
fish_command_not_found unrelated
printf 'RESTORED:%s\n' $status
set -l after (functions --details --verbose fish_command_not_found)
set -l after_origin "$after[1]"
if not contains -- "$after[2]" autoloaded not-autoloaded n/a
    set after_origin "$after[2]"
end
test "$before[1]" = "$after_origin"
and test "$before[3]" = "$after[3]"
and test "$before[4]" = "$after[4]"
and test "$before[5]" = "$after[5]"
printf 'DETAILS:%s\n' $status
set -l body_after (functions --no-details fish_command_not_found | string collect)
test "$body_before" = "$body_after"
printf 'BODY:%s\n' $status
functions --query __openai_suggestions_current_1
printf 'TEMP:%s\n' $status
read --local remaining
printf 'STDIN:%s\n' "$remaining"
`, "untouched\n")
	require.Equal(t, "PRIOR\nRETAINED:42\nPRIOR\nRESTORED:42\nDETAILS:0\nBODY:0\nTEMP:1\nSTDIN:untouched\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsFishDefersEmittedEventsAndRestoresAfterRepeatedCalls(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function 'Not a function'
    emit fish_prompt
    emit fish_postexec SYNTHETIC_PRIVATE_EVENT_ARGUMENT
end
function fish_command_not_found
    # This meaningful comment must survive native copies.
    set -l before (functions fish_command_not_found | string collect)
    emit fish_prompt
    emit fish_postexec SYNTHETIC_PRIVATE_EVENT_ARGUMENT
    'Not a function'
    set -l after (functions fish_command_not_found | string collect)
    test "$before" = "$after"
    printf 'STILL_PRIOR:%s\n' $status
    return 42
end`, `
set -l original (functions --no-details fish_command_not_found | string collect)
source "$SUGGEST_HOOK"
set -l events_before (functions --handlers | string collect)
set -l functions_before (functions --names --all | string collect)
for attempt in 1 2 3
    fish_command_not_found opneai
    printf 'STATUS:%s\n' $status
end
set -l events_after (functions --handlers | string collect)
set -l functions_after (functions --names --all | string collect)
test "$events_before" = "$events_after"
printf 'EVENTS:%s\n' $status
test "$functions_before" = "$functions_after"
printf 'FUNCTIONS:%s\n' $status
openai_suggestions_disable
set -l restored (functions --no-details fish_command_not_found | string collect)
test "$original" = "$restored"
printf 'RESTORED:%s\n' $status
`, "")
	require.Equal(t, strings.Repeat("STILL_PRIOR:0\nSTATUS:42\n", 3)+"EVENTS:0\nFUNCTIONS:0\nRESTORED:0\n", out)
	require.Equal(t, strings.Repeat(suggestionAdvice, 3), errOut)
}

func TestShellSuggestionsFishRestoresAfterInterruptedDelegation(t *testing.T) {
	for _, mode := range []string{"owned", "retained", "disabled-retained", "nested-retained"} {
		t.Run(mode, func(t *testing.T) {
			out, errOut := runSuggestionAdapter(t, "fish", `
set -g fish_greeting ''
function fish_command_not_found
    if not set -q interrupted_once
        set -g interrupted_once 1
        printf '__SUGGESTION_READY__\n'
        command kill -INT $fish_pid
        return 86
    end
    printf 'PRIOR\n'
    return 42
end`, `
source "$SUGGEST_HOOK"
set -g probe_call fish_command_not_found
if test "$SUGGEST_CANCEL_MODE" != owned
    functions --copy fish_command_not_found retained_wrapper
    if test "$SUGGEST_CANCEL_MODE" = nested-retained
        openai_suggestions_disable
        function fish_command_not_found
            retained_wrapper $argv
        end
        source "$SUGGEST_HOOK"
    else if test "$SUGGEST_CANCEL_MODE" = retained
        function fish_command_not_found
            retained_wrapper $argv
        end
    else
        openai_suggestions_disable
        set -g probe_call retained_wrapper
    end
end
set -g expected_body (functions --no-details fish_command_not_found | string collect)
set -g expected_events (functions --handlers | string collect)
set -g expected_functions (functions --names --all | string collect)
$probe_call opneai
printf 'UNEXPECTED_AFTER_INTERRUPT\n'
`, `printf 'CONTINUED\n'
functions --query __openai_suggestions_current_1
printf 'TEMP:%s\n' $status
set -q __openai_suggestions_pending_1
printf 'PENDING:%s\n' $status
set -q __openai_suggestions_pending_restores[1]
printf 'STACK:%s\n' $status
if test "$SUGGEST_CANCEL_MODE" = nested-retained
    functions --query __openai_suggestions_current_2
    printf 'TEMP2:%s\n' $status
    set -q __openai_suggestions_pending_2
    printf 'PENDING2:%s\n' $status
end
set -g actual_body (functions --no-details fish_command_not_found | string collect)
test "$expected_body" = "$actual_body"
printf 'BODY:%s\n' $status
set -g actual_events (functions --handlers | string collect)
test "$expected_events" = "$actual_events"
printf 'EVENTS:%s\n' $status
set -g actual_functions (functions --names --all | string collect)
test "$expected_functions" = "$actual_functions"
printf 'FUNCTIONS:%s\n' $status
$probe_call opneai
printf 'REPEATED:%s\n' $status
openai_suggestions_disable
$probe_call unrelated
printf 'DISABLED:%s\n' $status
`, "SUGGEST_FISH_CANCELLATION_PTY=1", "SUGGEST_CANCEL_MODE="+mode)
			expected := "CONTINUED\nTEMP:1\nPENDING:1\nSTACK:1\n"
			if mode == "nested-retained" {
				expected += "TEMP2:1\nPENDING2:1\n"
			}
			require.Equal(t, expected+"BODY:0\nEVENTS:0\nFUNCTIONS:0\nPRIOR\nREPEATED:42\nPRIOR\nDISABLED:42\n", out)
			if mode == "disabled-retained" {
				require.Empty(t, errOut)
			} else {
				require.Equal(t, suggestionAdvice, errOut)
			}
		})
	}
}

func TestShellSuggestionsFishPreservesLatePrivateCopyCollision(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'PRIOR\n'
    return 42
end`, `
source "$SUGGEST_HOOK"
function __openai_suggestions_current_1
    printf 'FOREIGN\n'
end
set -l before (functions __openai_suggestions_current_1 | string collect)
fish_command_not_found unrelated
printf 'BLOCKED:%s\n' $status
set -l after (functions __openai_suggestions_current_1 | string collect)
test "$before" = "$after"
printf 'UNCHANGED:%s\n' $status
openai_suggestions_disable
fish_command_not_found unrelated
printf 'RESTORED:%s\n' $status
__openai_suggestions_current_1
read --local remaining
printf 'STDIN:%s\n' "$remaining"
`, "untouched\n")
	require.Equal(t, "BLOCKED:127\nUNCHANGED:0\nPRIOR\nRESTORED:42\nFOREIGN\nSTDIN:untouched\n", out)
	require.Equal(t, "Executable suggestions could not reserve its handler copy. Keep the existing handler and use CLI help.\n", errOut)
}

func TestShellSuggestionsPowerShellPublicLookupAndPrivacy(t *testing.T) {
	phaseLog := filepath.Join(t.TempDir(), "lookup-phases.log")
	t.Cleanup(func() {
		phases, err := os.ReadFile(phaseLog)
		if err != nil {
			t.Logf("PowerShell lookup phase log unavailable (%T)", err)
			return
		}
		t.Logf("PowerShell lookup phases:\n%s", phases)
	})
	phase := func(name string) string {
		return "[IO.File]::AppendAllText($env:SUGGEST_PHASE_LOG, $env:SUGGEST_PHASE_RUN + '|" + name + "|' + " +
			"[DateTime]::UtcNow.ToString('O') + '|' + [Diagnostics.Stopwatch]::GetTimestamp() + '|' + " +
			"[Diagnostics.Stopwatch]::Frequency + [Environment]::NewLine)\n"
	}
	setup := phase("script-entry") + `
$ErrorActionPreference = 'Stop'
$global:LASTEXITCODE = 73
$global:PriorNames = [System.Collections.Generic.List[string]]::new()
$ExecutionContext.InvokeCommand.CommandNotFoundAction = {
    param($sender, $eventArgs)
` + phase("handler-entry") + `
    $global:PriorNames.Add($eventArgs.CommandName)
}`
	probe := phase("before-lookup") + `
try { opneai images generate 'FAKE_PRIVATE_MARKER' } catch {
` + phase("catch-entry") + `
    [Console]::Out.WriteLine('MISSING:' + $_.FullyQualifiedErrorId)
}
[Console]::Out.WriteLine('LAST:' + $global:LASTEXITCODE)
[Console]::Out.WriteLine('PRIOR:' + [string]::Join(',', $global:PriorNames))
[Console]::Out.WriteLine('STDIN:' + [Console]::ReadLine())
`
	baselineOut, baselineErr := runSuggestionAdapter(t, "pwsh", setup, probe, "untouched\n",
		"SUGGEST_PHASE_LOG="+phaseLog, "SUGGEST_PHASE_RUN=baseline")
	out, errOut := runSuggestionAdapter(t, "pwsh", setup, ". $env:SUGGEST_HOOK\n. $env:SUGGEST_HOOK\n"+probe, "untouched\n",
		"SUGGEST_PHASE_LOG="+phaseLog, "SUGGEST_PHASE_RUN=hooked")
	require.Empty(t, baselineErr)
	require.Equal(t, baselineOut, out)
	require.Contains(t, out, "MISSING:CommandNotFoundException\nLAST:73\n")
	require.Contains(t, out, "STDIN:untouched\n")
	require.Equal(t, suggestionAdvice, errOut)
}

func TestShellSuggestionsPowerShellModuleIsolation(t *testing.T) {
	modules := t.TempDir()
	module := filepath.Join(modules, "SyntheticSuggestionModule")
	require.NoError(t, os.Mkdir(module, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(module, "SyntheticSuggestionModule.psd1"), []byte(`@{
    RootModule = 'SyntheticSuggestionModule.psm1'
    ModuleVersion = '1.0.0'
    FunctionsToExport = @('Get-opneai')
    CmdletsToExport = @()
    VariablesToExport = @()
    AliasesToExport = @()
}
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(module, "SyntheticSuggestionModule.psm1"), []byte(`
[IO.File]::WriteAllText($env:SUGGEST_MODULE_IMPORT_LOG, 'imported')
function Get-opneai { [Console]::Out.WriteLine('AUTOLOAD:' + $args.Count) }
`), 0600))
	for _, tc := range []struct {
		name    string
		include bool
	}{
		{"isolated", false},
		{"included", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := `
$ErrorActionPreference = 'Stop'
$global:LASTEXITCODE = 73
$global:PriorNames = [System.Collections.Generic.List[string]]::new()
$ExecutionContext.InvokeCommand.CommandNotFoundAction = {
    param($sender, $eventArgs)
    $global:PriorNames.Add($eventArgs.CommandName)
}
`
			if tc.include {
				// Only this positive control opts its owned module into native discovery.
				setup += "$env:PSModulePath = $env:SUGGEST_TEST_MODULES + [IO.Path]::PathSeparator + $env:PSModulePath\n"
			}
			probe := `
try { opneai 'FAKE_PRIVATE_MARKER' '' 'space value' } catch {
    [Console]::Out.WriteLine('MISSING:' + $_.FullyQualifiedErrorId)
}
[Console]::Out.WriteLine('LAST:' + $global:LASTEXITCODE)
[Console]::Out.WriteLine('PRIOR:' + [string]::Join(',', $global:PriorNames))
[Console]::Out.WriteLine('STDIN:' + [Console]::ReadLine())
`
			run := func(hook string) (string, string) {
				marker := filepath.Join(t.TempDir(), "module-imported")
				out, errOut := runSuggestionAdapter(t, "pwsh", setup, hook+probe, "untouched\n",
					"PSModulePath="+modules, "SUGGEST_TEST_MODULES="+modules, "SUGGEST_MODULE_IMPORT_LOG="+marker)
				if tc.include {
					imported, err := os.ReadFile(marker)
					require.NoError(t, err, "public lookup must import the synthetic module")
					require.Equal(t, "imported", string(imported))
					require.Equal(t, "AUTOLOAD:3\nLAST:73\nPRIOR:\nSTDIN:untouched\n", out)
					require.Empty(t, errOut)
				} else {
					_, err := os.Stat(marker)
					require.ErrorIs(t, err, os.ErrNotExist, "isolated lookup imported the ambient module")
					require.Contains(t, out, "MISSING:CommandNotFoundException\nLAST:73\n")
					require.Contains(t, out, "STDIN:untouched\n")
					require.NotContains(t, out, "AUTOLOAD:")
					require.NotContains(t, out+errOut, "FAKE_PRIVATE_MARKER")
				}
				return out, errOut
			}
			baselineOut, baselineErr := run("")
			out, errOut := run(". $env:SUGGEST_HOOK\n. $env:SUGGEST_HOOK\n")
			require.Equal(t, baselineOut, out)
			require.Empty(t, baselineErr)
			if tc.include {
				require.Empty(t, errOut)
			} else {
				require.Equal(t, suggestionAdvice, errOut)
			}
		})
	}
}

func TestShellSuggestionsPowerShellMatchesAndWindowsExtension(t *testing.T) {
	for _, tc := range []struct {
		name  string
		match bool
	}{
		{"opneai", true}, {"opena", true}, {"xopenai", true}, {"openxai", true},
		{"openaii", true}, {"opxnai", true},
		{"open-ai", true}, {"opneai.exe", runtime.GOOS == "windows"}, {"opneai.EXE", runtime.GOOS == "windows"},
		{"Opneai.exe", runtime.GOOS == "windows"}, {"OPNEAI.EXE", runtime.GOOS == "windows"},
		{"openai.exe", false}, {"OpenAI.exe", false}, {"OPENAI.EXE", false},
		{"opneai.exe.exe", false}, {"./opneai.exe", false}, {"./Opneai.exe", false},
		{"C:\\opneai.exe", false}, {"opneai.ps1", false}, {"unrelated", false},
		{"opneai\n", false}, {"opneai\x1b", false}, {"openai;", false},
		{"opneaı.exe", false}, {"opneaİ.exe", false}, {"ＯＰＮＥＡＩ.EXE", false},
		{strings.Repeat("a", 1000), false},
	} {
		t.Run(fmt.Sprintf("%q", tc.name), func(t *testing.T) {
			out, errOut := runSuggestionAdapter(t, "pwsh", "$ErrorActionPreference = 'Stop'", `
. $env:SUGGEST_HOOK
try { & $env:SUGGEST_NAME 'FAKE_PRIVATE_MARKER' } catch {}
[Console]::Out.WriteLine('DONE')
`, "", "SUGGEST_NAME="+tc.name)
			require.Equal(t, "DONE\n", out)
			if tc.match {
				require.Equal(t, suggestionAdvice, errOut)
			} else {
				require.Empty(t, errOut)
			}
		})
	}
}

func TestShellSuggestionsPowerShellWindowsMatchingIgnoresCurrentCulture(t *testing.T) {
	setup := `
$ErrorActionPreference = 'Stop'
[Threading.Thread]::CurrentThread.CurrentCulture = [Globalization.CultureInfo]::GetCultureInfo('tr-TR')
`
	for _, tc := range []struct {
		name  string
		match bool
	}{
		{"Opneai.exe", runtime.GOOS == "windows"}, {"OPNEAI.EXE", runtime.GOOS == "windows"},
		{"OpenAI.exe", false}, {"OPENAI.EXE", false}, {"opneaı.exe", false},
		{"opneaİ.exe", false}, {"./Opneai.exe", false}, {"C:\\Opneai.exe", false},
	} {
		t.Run(fmt.Sprintf("%q", tc.name), func(t *testing.T) {
			out, errOut := runSuggestionAdapter(t, "pwsh", setup, `
. $env:SUGGEST_HOOK
try { & $env:SUGGEST_NAME 'FAKE_PRIVATE_MARKER' } catch {}
[Console]::Out.WriteLine('DONE')
`, "", "SUGGEST_NAME="+tc.name)
			require.Equal(t, "DONE\n", out)
			if tc.match {
				require.Equal(t, suggestionAdvice, errOut)
			} else {
				require.Empty(t, errOut)
			}
		})
	}
}

func TestShellSuggestionsPowerShellUppercaseBinding(t *testing.T) {
	missing := "OPNEAI"
	if runtime.GOOS == "windows" {
		missing = "opneai.exe"
	}
	out, errOut := runSuggestionAdapter(t, "pwsh", `
$ErrorActionPreference = 'Stop'
[Threading.Thread]::CurrentThread.CurrentCulture = [Globalization.CultureInfo]::GetCultureInfo('tr-TR')
`, `
. $env:SUGGEST_HOOK
try { & $env:SUGGEST_NAME 'FAKE_PRIVATE_MARKER' } catch {}
[Console]::Out.WriteLine('DONE')
`, "", "SUGGEST_APP_NAME=OPENAI", "SUGGEST_NAME="+missing)
	require.Equal(t, "DONE\n", out)
	require.Equal(t, strings.ReplaceAll(suggestionAdvice, "openai", "OPENAI"), errOut)
}

func TestShellSuggestionsPowerShellCanonicalCaseUsesExistingCommand(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "pwsh", "$ErrorActionPreference = 'Stop'", `
. $env:SUGGEST_HOOK
# Existing commands bypass command-not-found advice and retain their behavior.
function global:OPENAI { [Console]::Out.WriteLine('CANONICAL') }
OPENAI 'FAKE_PRIVATE_MARKER'
`, "")
	require.Equal(t, "CANONICAL\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsPowerShellPreservesResolutionAndDisable(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "pwsh", `
$ErrorActionPreference = 'Stop'
$global:LASTEXITCODE = 73
$prior = [System.EventHandler[System.Management.Automation.CommandLookupEventArgs]] {
    param($sender, $eventArgs)
    if ($eventArgs.CommandName -ceq 'virtual_command') {
        $eventArgs.CommandScriptBlock = { [Console]::Out.WriteLine('VIRTUAL') }
    }
}
$ExecutionContext.InvokeCommand.CommandNotFoundAction = $prior
`, `
. $env:SUGGEST_HOOK
virtual_command
openai_suggestions_disable
openai_suggestions_disable
[Console]::Out.WriteLine('RESTORED:' + [object]::ReferenceEquals($ExecutionContext.InvokeCommand.CommandNotFoundAction, $prior))
. $env:SUGGEST_HOOK
$retained = $ExecutionContext.InvokeCommand.CommandNotFoundAction
$later = [System.EventHandler[System.Management.Automation.CommandLookupEventArgs]] {
    param($sender, $eventArgs)
    $retained.Invoke($sender, $eventArgs)
}.GetNewClosure()
$ExecutionContext.InvokeCommand.CommandNotFoundAction = $later
. $env:SUGGEST_HOOK
openai_suggestions_disable
[Console]::Out.WriteLine('LATER:' + [object]::ReferenceEquals($ExecutionContext.InvokeCommand.CommandNotFoundAction, $later))
try { opneai 'FAKE_PRIVATE_MARKER' } catch {}
virtual_command
. $env:SUGGEST_HOOK
try { opneai 'FAKE_PRIVATE_MARKER' } catch {}
[Console]::Out.WriteLine('LAST:' + $global:LASTEXITCODE)
`, "")
	require.Equal(t, "VIRTUAL\nRESTORED:True\nLATER:True\nVIRTUAL\nLAST:73\n", out)
	require.Equal(t, suggestionAdvice, errOut)
}

func TestShellSuggestionsPowerShellPreservesPriorExceptions(t *testing.T) {
	setup := `
$ErrorActionPreference = 'Stop'
$global:LASTEXITCODE = 73
$ExecutionContext.InvokeCommand.CommandNotFoundAction = {
    param($sender, $eventArgs)
    [Console]::Error.WriteLine('PRIOR_ERROR')
    throw 'FAKE_PRIOR_EXCEPTION'
}
`
	probe := `
try { unrelated_missing 'FAKE_PRIVATE_MARKER' } catch {
    [Console]::Out.WriteLine($_.FullyQualifiedErrorId)
}
[Console]::Out.WriteLine('LAST:' + $global:LASTEXITCODE)
`
	baselineOut, baselineErr := runSuggestionAdapter(t, "pwsh", setup, probe, "")
	out, errOut := runSuggestionAdapter(t, "pwsh", setup, ". $env:SUGGEST_HOOK\n"+probe, "")
	require.Equal(t, baselineOut, out)
	require.Equal(t, baselineErr, errOut)
	require.Contains(t, errOut, "PRIOR_ERROR\n")
}

func TestShellSuggestionsPowerShellPriorResolutionWinsNearMiss(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "pwsh", `
$ErrorActionPreference = 'Stop'
$global:LASTEXITCODE = 73
$ExecutionContext.InvokeCommand.CommandNotFoundAction = {
    param($sender, $eventArgs)
    if ($eventArgs.CommandName -ceq 'opneai') {
        $eventArgs.CommandScriptBlock = {
            [Console]::Out.WriteLine('VIRTUAL:' + $args.Count)
        }
        $global:LASTEXITCODE = 87
    }
}`, `
. $env:SUGGEST_HOOK
opneai 'FAKE_PRIVATE_MARKER' '' 'space value'
[Console]::Out.WriteLine('LAST:' + $global:LASTEXITCODE)
`, "")
	require.Equal(t, "VIRTUAL:3\nLAST:87\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsPowerShellStopSearchRemainsUnchanged(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "pwsh", `
$ErrorActionPreference = 'Stop'
$ExecutionContext.InvokeCommand.CommandNotFoundAction = {
    param($sender, $eventArgs)
    $eventArgs.StopSearch = $true
}`, `
. $env:SUGGEST_HOOK
try { opneai } catch { [Console]::Out.WriteLine('MISSING') }
`, "")
	require.Equal(t, "MISSING\n", out)
	require.Empty(t, errOut)
}

func TestShellSuggestionsPowerShellDisableAliasConflict(t *testing.T) {
	out, errOut := runSuggestionAdapter(t, "pwsh", `
function original_disable { [Console]::Out.WriteLine('ORIGINAL_ALIAS') }
Set-Alias -Name openai_suggestions_disable -Value original_disable
$prior = $ExecutionContext.InvokeCommand.CommandNotFoundAction
`, `
. $env:SUGGEST_HOOK
openai_suggestions_disable
[Console]::Out.WriteLine('UNCHANGED:' + [object]::ReferenceEquals($prior, $ExecutionContext.InvokeCommand.CommandNotFoundAction))
`, "")
	require.Equal(t, "ORIGINAL_ALIAS\nUNCHANGED:True\n", out)
	require.Equal(t, "Executable suggestions conflict with an existing shell command. Keep the existing handler and use CLI help.\n", errOut)
}

func TestShellSuggestionsAdaptersUnavailableExecutable(t *testing.T) {
	for _, shell := range []string{"fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			body := "set -gx PATH\nsource \"$SUGGEST_HOOK\"\nfunctions --query openai_suggestions_disable\nprintf 'DISABLE:%s\\n' $status\n"
			if shell == "pwsh" {
				body = "$env:PATH = ''\n. $env:SUGGEST_HOOK\n[Console]::Out.WriteLine('DISABLE:' + @($ExecutionContext.InvokeCommand.GetCommands('openai_suggestions_disable', [System.Management.Automation.CommandTypes]::Function, $false)).Count)\n"
			}
			out, errOut := runSuggestionAdapter(t, shell, "", body, "")
			if shell == "fish" {
				require.Equal(t, "DISABLE:1\n", out)
			} else {
				require.Equal(t, "DISABLE:0\n", out)
			}
			require.Equal(t, "Executable suggestions require openai on PATH. Install the CLI before enabling suggestions.\n", errOut)
		})
	}
}

func TestShellSuggestionsAdaptersRefuseShadowedExecutable(t *testing.T) {
	for _, shell := range []string{"fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			setup := "function openai; printf 'SHADOW_LAUNCHED\\n'; end\n"
			body := "source \"$SUGGEST_HOOK\"\nprintf 'DONE\\n'\n"
			if shell == "pwsh" {
				setup = "function global:openai { [Console]::Out.WriteLine('SHADOW_LAUNCHED') }\n"
				body = ". $env:SUGGEST_HOOK\n[Console]::Out.WriteLine('DONE')\n"
			}
			out, errOut := runSuggestionAdapter(t, shell, setup, body, "")
			require.Equal(t, "DONE\n", out)
			require.Equal(t, "Executable suggestions require openai on PATH. Install the CLI before enabling suggestions.\n", errOut)
		})
	}
}

func TestShellSuggestionsAdaptersRecheckExecutablePrecedence(t *testing.T) {
	t.Run("fish", func(t *testing.T) {
		out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'PRIOR\n' >&2
    return 42
end`, `
source "$SUGGEST_HOOK"
function openai
    printf 'SHADOW_LAUNCHED\n'
end
fish_command_not_found opneai
printf 'STATUS:%s\n' $status
`, "")
		require.Equal(t, "STATUS:42\n", out)
		require.Equal(t, "PRIOR\n", errOut)
	})
	t.Run("pwsh", func(t *testing.T) {
		out, errOut := runSuggestionAdapter(t, "pwsh", "$ErrorActionPreference = 'Stop'", `
. $env:SUGGEST_HOOK
function global:openai { [Console]::Out.WriteLine('SHADOW_LAUNCHED') }
try { opneai 'FAKE_PRIVATE_MARKER' } catch {}
[Console]::Out.WriteLine('DONE')
`, "")
		require.Equal(t, "DONE\n", out)
		require.Empty(t, errOut)
	})
}

func TestShellSuggestionsAdaptersRecheckRemovedExecutable(t *testing.T) {
	t.Run("fish", func(t *testing.T) {
		out, errOut := runSuggestionAdapter(t, "fish", `
function fish_command_not_found
    printf 'PRIOR\n' >&2
    return 42
end`, `
set -gx PATH (string replace --regex '/[^/]+$' '' -- "$SUGGEST_EXECUTABLE")
source "$SUGGEST_HOOK"
fish_command_not_found opneai
printf 'FIRST:%s\n' $status
command /bin/rm -- "$SUGGEST_EXECUTABLE"
fish_command_not_found opneai
printf 'REMOVED:%s\n' $status
`, "")
		require.Equal(t, "FIRST:42\nREMOVED:42\n", out)
		require.Equal(t, "PRIOR\n"+suggestionAdvice+"PRIOR\n", errOut)
	})
	t.Run("pwsh", func(t *testing.T) {
		out, errOut := runSuggestionAdapter(t, "pwsh", `
$ErrorActionPreference = 'Stop'
$global:PriorLookups = 0
$ExecutionContext.InvokeCommand.CommandNotFoundAction = {
    param($sender, $eventArgs)
    if ($eventArgs.CommandName -ceq 'opneai') { $global:PriorLookups++ }
}`, `
$env:PATH = [IO.Path]::GetDirectoryName($env:SUGGEST_EXECUTABLE)
. $env:SUGGEST_HOOK
try { opneai } catch { [Console]::Out.WriteLine('FIRST:MISSING') }
Microsoft.PowerShell.Management\Remove-Item -LiteralPath $env:SUGGEST_EXECUTABLE
try { opneai } catch { [Console]::Out.WriteLine('REMOVED:MISSING') }
[Console]::Out.WriteLine('PRIOR:' + $global:PriorLookups)
`, "")
		require.Equal(t, "FIRST:MISSING\nREMOVED:MISSING\nPRIOR:2\n", out)
		require.Equal(t, suggestionAdvice, errOut)
	})
}
