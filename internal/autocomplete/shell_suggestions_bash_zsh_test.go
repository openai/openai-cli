package autocomplete

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func shellSuggestionsTestShell(t *testing.T, style CompletionStyle) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Bash/zsh executable fixture requires a Unix host")
	}
	name := string(style)
	if style == CompletionStyleBash && os.Getenv("OPENAI_CLI_TEST_BASH") != "" {
		name = os.Getenv("OPENAI_CLI_TEST_BASH")
	}
	path, err := exec.LookPath(name)
	if err != nil {
		if strings.Contains(","+os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS")+",", ","+string(style)+",") {
			t.Fatalf("required shell unavailable: %s", style)
		}
		t.Skipf("shell unavailable: %s", style)
	}
	return path
}

func shellSuggestionsRun(t *testing.T, shell string, style CompletionStyle, directory, program string) {
	t.Helper()
	args := []string{"--noprofile", "--norc", "-i", "-c", program}
	if style == CompletionStyleZsh {
		args = []string{"-f", "-i", "-c", program}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, shell, args...)
	command.WaitDelay = time.Second
	command.Dir = directory
	command.Env = []string{"HOME=" + directory, "ZDOTDIR=" + directory, "HISTFILE=/dev/null",
		"PATH=" + directory + ":/usr/bin:/bin", "TERM=dumb", "LC_ALL=C", "TMPDIR=" + directory}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	require.NoError(t, err, "shell stdout=%q stderr=%q", stdout.String(), stderr.String())
	require.NoError(t, ctx.Err(), "shell exceeded its deadline")
}

func shellSuggestionsFixture(t *testing.T, style CompletionStyle) (string, string) {
	t.Helper()
	shell := shellSuggestionsTestShell(t, style)
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "openai"), []byte("#!/bin/sh\n: > backend-called\n"), 0700))
	script, err := RenderShellSuggestions(style, "openai")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "suggestions"), []byte(script), 0600))
	if style == CompletionStyleBash {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		probe := exec.CommandContext(ctx, shell, "--noprofile", "--norc", "-c", "(( BASH_VERSINFO[0] >= 4 ))")
		probe.Env = []string{"HOME=" + directory, "PATH=/usr/bin:/bin"}
		if err := probe.Run(); err != nil {
			require.NoError(t, ctx.Err(), "Bash capability probe exceeded its deadline")
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			require.Equal(t, 1, exit.ExitCode())
			if strings.Contains(","+os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS")+",", ",bash,") {
				t.Fatal("required Bash 4+ unavailable; set OPENAI_CLI_TEST_BASH")
			}
			t.Skip("Bash 4+ unavailable; set OPENAI_CLI_TEST_BASH")
		}
	}
	return shell, directory
}

func shellSuggestionsRead(t *testing.T, directory, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, name))
	require.NoError(t, err)
	return string(data)
}

func TestShellSuggestionsBashZshAdvisoryAndPrivacy(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh} {
		t.Run(string(style), func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, style)
			shellSuggestionsRun(t, shell, style, directory, `
source ./suggestions
source ./suggestions
opneai --api-key=synthetic-private '$(touch replayed)' 'private prompt' > result.out 2> result.err
builtin printf '%s' "$?" > result.status
builtin printf 'still-unread' > input
exec 3< input
opneai responses create <&3 > second.out 2> second.err
builtin printf '%s' "$?" > second.status
IFS= read -r remaining <&3
builtin printf '%s' "$remaining" > remaining.input
exit 0
`)
			require.Equal(t, "", shellSuggestionsRead(t, directory, "result.out"))
			require.Equal(t, "127", shellSuggestionsRead(t, directory, "result.status"))
			require.Equal(t, "Command not found. Did you mean: openai?\nKeep the remaining arguments unchanged.\n",
				shellSuggestionsRead(t, directory, "result.err"))
			require.Equal(t, "127", shellSuggestionsRead(t, directory, "second.status"))
			require.Equal(t, "still-unread", shellSuggestionsRead(t, directory, "remaining.input"))
			for _, marker := range []string{"backend-called", "replayed"} {
				_, err := os.Stat(filepath.Join(directory, marker))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

const shellSuggestionsZshConflict = "Executable suggestions conflict with an existing zsh handler.\n" +
	"Keep that handler and use openai for CLI commands.\n"

const shellSuggestionsBashConflict = "Executable suggestions conflict with an existing Bash handler.\n" +
	"Keep that handler and use openai for CLI commands.\n"

func TestShellSuggestionsBashRefusesPriorHandlerWithoutChangingNativeIdentity(t *testing.T) {
	for _, status := range []string{"0", "42", "127"} {
		t.Run(status, func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
			shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `command_not_found_handle() {
    builtin printf '%s\000' "$@" > "$stage.args"
    builtin printf '%s' "${FUNCNAME[0]}" > "$stage.name"
    builtin printf 'prior stdout\n'
    builtin printf 'prior stderr\n' >&2
    return `+status+`
}
builtin declare -f command_not_found_handle > before.definition
builtin printf 'still-unread\n' > input
exec 3< input
stage=baseline
opneai 'space value' 'quote"value' 'line
value' '$(touch replayed)' '' <&3 > baseline.out 2> baseline.err
builtin printf '%s' "$?" > baseline.status
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
stage=attempted
opneai 'space value' 'quote"value' 'line
value' '$(touch replayed)' '' <&3 > attempted.out 2> attempted.err
builtin printf '%s' "$?" > attempted.status
source ./suggestions > repeated.out 2> repeated.err
builtin printf '%s' "$?" > repeated.status
stage=after
opneai 'space value' 'quote"value' 'line
value' '$(touch replayed)' '' <&3 > after.out 2> after.err
builtin printf '%s' "$?" > after.status
IFS= read -r remaining <&3
builtin printf '%s' "$remaining" > remaining.input
builtin declare -f command_not_found_handle > after.definition
builtin declare -F openai_suggestions_disable __openai_suggestions_bash_match > residue.functions
builtin printf '%s|%s' "${__openai_suggestions_bash_state+x}" "${__openai_suggestions_bash_source_trace+x}" > residue.variables
exit 0
`)
			for _, stage := range []string{"setup", "repeated"} {
				require.Equal(t, "2", shellSuggestionsRead(t, directory, stage+".status"))
				require.Empty(t, shellSuggestionsRead(t, directory, stage+".out"))
				require.Equal(t, shellSuggestionsBashConflict, shellSuggestionsRead(t, directory, stage+".err"))
			}
			for _, stage := range []string{"baseline", "attempted", "after"} {
				require.Equal(t, status, shellSuggestionsRead(t, directory, stage+".status"))
				require.Equal(t, "prior stdout\n", shellSuggestionsRead(t, directory, stage+".out"))
				require.Equal(t, "prior stderr\n", shellSuggestionsRead(t, directory, stage+".err"))
				require.Equal(t, "opneai\x00space value\x00quote\"value\x00line\nvalue\x00$(touch replayed)\x00\x00", shellSuggestionsRead(t, directory, stage+".args"))
				require.Equal(t, "command_not_found_handle", shellSuggestionsRead(t, directory, stage+".name"))
			}
			require.Equal(t, "still-unread", shellSuggestionsRead(t, directory, "remaining.input"))
			require.Equal(t, shellSuggestionsRead(t, directory, "before.definition"), shellSuggestionsRead(t, directory, "after.definition"))
			require.Empty(t, shellSuggestionsRead(t, directory, "residue.functions"))
			require.Equal(t, "|", shellSuggestionsRead(t, directory, "residue.variables"))
			for _, marker := range []string{"backend-called", "replayed"} {
				_, err := os.Stat(filepath.Join(directory, marker))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestShellSuggestionsBashRefusalPreservesSourceProvenanceAndSiblingFiles(t *testing.T) {
	shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
	priorDirectory := filepath.Join(directory, "prior handler")
	require.NoError(t, os.Mkdir(priorDirectory, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(priorDirectory, "sibling.txt"), []byte("sibling resource\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(priorDirectory, "handler.bash"), []byte(`function command_not_found_handle {
    builtin printf '%s' "${BASH_SOURCE[0]}" > "$stage.source"
    local sibling
    IFS= read -r sibling < "${BASH_SOURCE[0]%/*}/sibling.txt"
    builtin printf '%s\n' "$sibling"
    return 42
}
`), 0600))
	shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `
source './prior handler/handler.bash'
builtin shopt -s extdebug
builtin declare -F command_not_found_handle > before.location
builtin declare -f command_not_found_handle > before.definition
stage=baseline
opneai > baseline.out 2> baseline.err
builtin printf '%s' "$?" > baseline.status
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
stage=attempted
opneai > attempted.out 2> attempted.err
builtin printf '%s' "$?" > attempted.status
source ./suggestions > repeated.out 2> repeated.err
builtin printf '%s' "$?" > repeated.status
stage=after
opneai > after.out 2> after.err
builtin printf '%s' "$?" > after.status
builtin declare -F command_not_found_handle > after.location
builtin declare -f command_not_found_handle > after.definition
exit 0
`)
	for _, stage := range []string{"setup", "repeated"} {
		require.Equal(t, "2", shellSuggestionsRead(t, directory, stage+".status"))
		require.Equal(t, shellSuggestionsBashConflict, shellSuggestionsRead(t, directory, stage+".err"))
	}
	for _, stage := range []string{"baseline", "attempted", "after"} {
		require.Equal(t, "./prior handler/handler.bash", shellSuggestionsRead(t, directory, stage+".source"))
		require.Equal(t, "sibling resource\n", shellSuggestionsRead(t, directory, stage+".out"))
		require.Empty(t, shellSuggestionsRead(t, directory, stage+".err"))
		require.Equal(t, "42", shellSuggestionsRead(t, directory, stage+".status"))
	}
	for _, field := range []string{"location", "definition"} {
		require.Equal(t, shellSuggestionsRead(t, directory, "before."+field), shellSuggestionsRead(t, directory, "after."+field))
	}
}

func TestShellSuggestionsZshRefusesPriorHandlerWithoutChangingNativeIdentity(t *testing.T) {
	for _, option := range []string{"setopt", "unsetopt"} {
		for _, status := range []string{"0", "42", "127"} {
			t.Run(option+"/"+status, func(t *testing.T) {
				shell, directory := shellSuggestionsFixture(t, CompletionStyleZsh)
				shellSuggestionsRun(t, shell, CompletionStyleZsh, directory, option+` FUNCTION_ARGZERO
builtin printf '%s' "$0" > caller.name
function command_not_found_handler {
    builtin printf '%s' "$0" > "$stage.name"
    builtin printf '%s' "${funcstack[1]}" > "$stage.function"
    builtin printf '%s\000' "$@" > "$stage.args"
    builtin setopt > "$stage.options"
    builtin printf 'prior stdout\n'
    builtin printf 'prior stderr\n' >&2
    return `+status+`
}
builtin functions command_not_found_handler > before.definition
builtin printf 'still-unread\n' > input
exec 3< input
stage=baseline
opneai 'space value' 'quote"value' 'line
value' '$(touch replayed)' <&3 > baseline.out 2> baseline.err
builtin printf '%s' "$?" > baseline.status
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
stage=attempted
opneai 'space value' 'quote"value' 'line
value' '$(touch replayed)' <&3 > attempted.out 2> attempted.err
builtin printf '%s' "$?" > attempted.status
source ./suggestions > repeated.out 2> repeated.err
builtin printf '%s' "$?" > repeated.status
stage=after
opneai 'space value' 'quote"value' 'line
value' '$(touch replayed)' <&3 > after.out 2> after.err
builtin printf '%s' "$?" > after.status
IFS= read -r remaining <&3
builtin printf '%s' "$remaining" > remaining.input
builtin functions command_not_found_handler > after.definition
builtin printf '%s %s %s %s' "$+functions[openai_suggestions_disable]" "$+functions[__openai_suggestions_zsh_match]" "$+parameters[__openai_suggestions_zsh_state]" "$+parameters[__openai_suggestions_zsh_source_trace]" > residue
exit 0
`)
				for _, prefix := range []string{"setup", "repeated"} {
					require.Equal(t, "2", shellSuggestionsRead(t, directory, prefix+".status"))
					require.Empty(t, shellSuggestionsRead(t, directory, prefix+".out"))
					require.Equal(t, shellSuggestionsZshConflict, shellSuggestionsRead(t, directory, prefix+".err"))
				}
				for _, stage := range []string{"attempted", "after"} {
					for _, field := range []string{"name", "function", "args", "options", "out", "err", "status"} {
						require.Equal(t, shellSuggestionsRead(t, directory, "baseline."+field),
							shellSuggestionsRead(t, directory, stage+"."+field), stage+" "+field)
					}
				}
				name := "opneai"
				if option == "unsetopt" {
					name = shellSuggestionsRead(t, directory, "caller.name")
				}
				require.Equal(t, name, shellSuggestionsRead(t, directory, "baseline.name"))
				require.Equal(t, "command_not_found_handler", shellSuggestionsRead(t, directory, "baseline.function"))
				require.Equal(t, "opneai\x00space value\x00quote\"value\x00line\nvalue\x00$(touch replayed)\x00",
					shellSuggestionsRead(t, directory, "baseline.args"))
				require.Equal(t, status, shellSuggestionsRead(t, directory, "baseline.status"))
				require.Equal(t, "prior stdout\n", shellSuggestionsRead(t, directory, "baseline.out"))
				require.Equal(t, "prior stderr\n", shellSuggestionsRead(t, directory, "baseline.err"))
				require.Equal(t, shellSuggestionsRead(t, directory, "before.definition"), shellSuggestionsRead(t, directory, "after.definition"))
				require.Equal(t, "0 0 0 0", shellSuggestionsRead(t, directory, "residue"))
				require.Equal(t, "still-unread", shellSuggestionsRead(t, directory, "remaining.input"))
				for _, marker := range []string{"backend-called", "replayed"} {
					_, err := os.Stat(filepath.Join(directory, marker))
					require.ErrorIs(t, err, os.ErrNotExist)
				}
			})
		}
	}
}

func TestShellSuggestionsBashZshPreserveLaterReplacement(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh} {
		t.Run(string(style), func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, style)
			handler := "command_not_found_handle"
			if style == CompletionStyleZsh {
				handler = "command_not_found_handler"
			}
			shellSuggestionsRun(t, shell, style, directory, `source ./suggestions
`+handler+`() { builtin printf 'replacement\n' >&2; return 42; }
source ./suggestions
openai_suggestions_disable
opneai > result.out 2> result.err
builtin printf '%s' "$?" > result.status
exit 0
`)
			require.Equal(t, "", shellSuggestionsRead(t, directory, "result.out"))
			require.Equal(t, "replacement\n", shellSuggestionsRead(t, directory, "result.err"))
			require.Equal(t, "42", shellSuggestionsRead(t, directory, "result.status"))
		})
	}
}

func TestShellSuggestionsBashZshFallbackDoesNotEchoCommandText(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh} {
		t.Run(string(style), func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, style)
			shellSuggestionsRun(t, shell, style, directory, `source ./suggestions
missing=$'missing\033]0;synthetic-title\007'
"$missing" > control.out 2> control.err
builtin printf '%s' "$?" > control.status
'missing command' > space.out 2> space.err
builtin printf '%s' "$?" > space.status
exit 0
`)
			for _, prefix := range []string{"control", "space"} {
				require.Equal(t, "", shellSuggestionsRead(t, directory, prefix+".out"))
				require.Equal(t, string(style)+": command not found\n", shellSuggestionsRead(t, directory, prefix+".err"))
				require.Equal(t, "127", shellSuggestionsRead(t, directory, prefix+".status"))
			}
		})
	}
}

func TestShellSuggestionsBashZshRefuseShadowedExecutable(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh} {
		t.Run(string(style), func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, style)
			shellSuggestionsRun(t, shell, style, directory, `function openai { builtin printf 'must not run\n'; }
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
exit 0
`)
			require.Equal(t, "", shellSuggestionsRead(t, directory, "setup.out"))
			require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
			require.Contains(t, shellSuggestionsRead(t, directory, "setup.err"), "require openai on PATH")
		})
	}
}

func TestShellSuggestionsBashZshRejectRemovedCachedExecutable(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh} {
		for _, removeBeforeSetup := range []bool{false, true} {
			stage := "after-setup"
			if removeBeforeSetup {
				stage = "before-setup"
			}
			t.Run(string(style)+"/"+stage, func(t *testing.T) {
				shell, directory := shellSuggestionsFixture(t, style)
				handler := "command_not_found_handle"
				prime := `builtin hash -p "$PWD/openai" openai`
				inspect := `builtin hash -t openai > cached.path`
				if style == CompletionStyleZsh {
					handler = "command_not_found_handler"
					prime = `builtin hash "openai=$PWD/openai"`
					inspect = `builtin hash -m openai > cached.path`
				}
				setup := "source ./suggestions > setup.out 2> setup.err\nbuiltin printf '%s' \"$?\" > setup.status\n"
				remove := "/bin/rm -f -- \"$PWD/openai\"\n" + inspect + "\n"
				sequence := setup + remove
				if removeBeforeSetup {
					sequence = remove + setup
				}
				prior := `function ` + handler + ` {
    builtin printf 'prior stdout\n'
    builtin printf 'prior stderr\n' >&2
    builtin printf 'called\n' >> prior.calls
    return 42
}
`
				after := ""
				if removeBeforeSetup {
					after = prior
				}
				shellSuggestionsRun(t, shell, style, directory, prime+"\n"+sequence+after+`
opneai --api-key=synthetic-private > result.out 2> result.err
builtin printf '%s' "$?" > result.status
builtin typeset -f openai_suggestions_disable > disable.exists
exit 0
`)
				require.Contains(t, shellSuggestionsRead(t, directory, "cached.path"), filepath.Join(directory, "openai"))
				if !removeBeforeSetup {
					require.Empty(t, shellSuggestionsRead(t, directory, "result.out"))
					require.Equal(t, string(style)+": command not found\n", shellSuggestionsRead(t, directory, "result.err"))
					require.Equal(t, "127", shellSuggestionsRead(t, directory, "result.status"))
				} else {
					require.Equal(t, "prior stdout\n", shellSuggestionsRead(t, directory, "result.out"))
					require.Equal(t, "prior stderr\n", shellSuggestionsRead(t, directory, "result.err"))
					require.Equal(t, "42", shellSuggestionsRead(t, directory, "result.status"))
					require.Equal(t, "called\n", shellSuggestionsRead(t, directory, "prior.calls"))
				}
				if removeBeforeSetup {
					require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
					require.Empty(t, shellSuggestionsRead(t, directory, "disable.exists"))
				} else {
					require.Equal(t, "0", shellSuggestionsRead(t, directory, "setup.status"))
				}
				_, err := os.Stat(filepath.Join(directory, "backend-called"))
				require.ErrorIs(t, err, os.ErrNotExist, "lookup must not launch the sentinel executable")
			})
		}
	}
}

func TestShellSuggestionsBashRefusesExportedOrReadonlyHandler(t *testing.T) {
	for _, attribute := range []string{"export", "readonly", "declare -t"} {
		t.Run(attribute, func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
			shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `
command_not_found_handle() { builtin printf 'original\n' >&2; return 42; }
`+attribute+` -f command_not_found_handle
builtin declare -f command_not_found_handle > before.definition
{ builtin declare -Fr; builtin declare -Fx; builtin declare -Ft; } > before.attributes
builtin printf 'still-unread\n' > input
exec 3< input
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
builtin declare -F openai_suggestions_disable > disable.exists
opneai 'private value' <&3 > result.out 2> result.err
builtin printf '%s' "$?" > result.status
IFS= read -r remaining <&3
builtin printf '%s' "$remaining" > remaining.input
builtin declare -f command_not_found_handle > after.definition
{ builtin declare -Fr; builtin declare -Fx; builtin declare -Ft; } > after.attributes
exit 0
`)
			require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
			require.Equal(t, shellSuggestionsBashConflict, shellSuggestionsRead(t, directory, "setup.err"))
			require.Empty(t, shellSuggestionsRead(t, directory, "setup.out"))
			require.Empty(t, shellSuggestionsRead(t, directory, "disable.exists"))
			require.Empty(t, shellSuggestionsRead(t, directory, "result.out"))
			require.Equal(t, "original\n", shellSuggestionsRead(t, directory, "result.err"))
			require.Equal(t, "42", shellSuggestionsRead(t, directory, "result.status"))
			require.Equal(t, "still-unread", shellSuggestionsRead(t, directory, "remaining.input"))
			for _, field := range []string{"definition", "attributes"} {
				require.Equal(t, shellSuggestionsRead(t, directory, "before."+field), shellSuggestionsRead(t, directory, "after."+field))
			}
		})
	}
}

func TestShellSuggestionsBashNocasematchPreservesExactMatchingAndOptions(t *testing.T) {
	for _, setting := range []string{"on", "off"} {
		t.Run(setting, func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
			option, optionStatus := "-s", "0"
			if setting == "off" {
				option, optionStatus = "-u", "1"
			}
			shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `
function COMMAND_NOT_FOUND_HANDLE { :; }
readonly -f COMMAND_NOT_FOUND_HANDLE
builtin shopt `+option+` nocasematch
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
for candidate in Openai OPENAI openai opneai ''; do
    "__openai_suggestions_bash_match" "$candidate"
    builtin printf '%s\n' "$?" >> match.statuses
    builtin shopt -q nocasematch
    builtin printf '%s\n' "$?" >> caller.options
done
# Execution subshells also work on filesystems that resolve Openai as openai.
( "command_not_found_handle" Openai ) > near.out 2> near.err
builtin printf '%s' "$?" > near.status
( "command_not_found_handle" OPENAI ) > distant.out 2> distant.err
builtin printf '%s' "$?" > distant.status
builtin shopt -q nocasematch
builtin printf '%s' "$?" > final.option
exit 0
`)
			require.Equal(t, "0", shellSuggestionsRead(t, directory, "setup.status"))
			require.Empty(t, shellSuggestionsRead(t, directory, "setup.err"))
			require.Equal(t, "0\n1\n1\n0\n1\n", shellSuggestionsRead(t, directory, "match.statuses"))
			require.Equal(t, strings.Repeat(optionStatus+"\n", 5), shellSuggestionsRead(t, directory, "caller.options"))
			require.Equal(t, optionStatus, shellSuggestionsRead(t, directory, "final.option"))
			require.Equal(t, "Command not found. Did you mean: openai?\nKeep the remaining arguments unchanged.\n",
				shellSuggestionsRead(t, directory, "near.err"))
			require.Equal(t, "bash: command not found\n", shellSuggestionsRead(t, directory, "distant.err"))
			require.Equal(t, "127", shellSuggestionsRead(t, directory, "near.status"))
			require.Equal(t, "127", shellSuggestionsRead(t, directory, "distant.status"))
		})
	}
}

func TestShellSuggestionsBashNocasematchPreservesCaseOnlyReplacement(t *testing.T) {
	for _, option := range []string{"-s", "-u"} {
		t.Run(option, func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
			shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `
builtin shopt `+option+` nocasematch
source ./suggestions
replacement=${__openai_suggestions_bash_owner/Keep the remaining arguments unchanged./KEEP THE REMAINING ARGUMENTS UNCHANGED.}
builtin eval -- "$replacement"
before=$(builtin declare -f command_not_found_handle)
openai_suggestions_disable
builtin test "$(builtin declare -f command_not_found_handle)" = "$before"
builtin printf '%s' "$?" > preserved.status
exit 0
`)
			require.Equal(t, "0", shellSuggestionsRead(t, directory, "preserved.status"))
		})
	}
}

func TestShellSuggestionsBashNocasematchRejectsCaseOnlyBindingChange(t *testing.T) {
	for _, option := range []string{"-s", "-u"} {
		t.Run(option, func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
			require.NoError(t, os.WriteFile(filepath.Join(directory, "Openai"), []byte("#!/bin/sh\n: > backend-called\n"), 0700))
			script, err := RenderShellSuggestions(CompletionStyleBash, "Openai")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(directory, "other"), []byte(script), 0600))
			shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `
builtin shopt `+option+` nocasematch
source ./suggestions
source ./other > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
builtin printf '%s' "$__openai_suggestions_bash_command" > binding
exit 0
`)
			require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
			require.Equal(t, "openai", shellSuggestionsRead(t, directory, "binding"))
		})
	}
}

func TestShellSuggestionsBashRefusalPreservesTracingPrivacy(t *testing.T) {
	for _, initialTrace := range []string{"", "set -x"} {
		t.Run(initialTrace, func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
			shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `function command_not_found_handle {
    if false; then builtin printf 'SYNTHETIC_UNEXECUTED_SECRET_731'; fi
    return 42
}
`+initialTrace+`
source ./suggestions > source.out 2> source.err
builtin printf '%s' "$?" > source.status
case $- in *x*) builtin printf 'on' > source.trace ;; *) builtin printf 'off' > source.trace ;; esac
set -x
opneai > lookup.out 2> lookup.err
builtin printf '%s' "$?" > lookup.status
case $- in *x*) builtin printf 'on' > lookup.trace ;; esac
source ./suggestions > repeated.out 2> repeated.err
builtin printf '%s' "$?" > repeated.status
case $- in *x*) builtin printf 'on' > repeated.trace ;; esac
builtin declare -F openai_suggestions_disable __openai_suggestions_bash_match > residue.functions
builtin printf '%s' "${__openai_suggestions_bash_state+x}" > residue.variables
set +x
exit 0
`)
			for _, prefix := range []string{"source", "lookup", "repeated"} {
				require.NotContains(t, shellSuggestionsRead(t, directory, prefix+".err"), "SYNTHETIC_UNEXECUTED_SECRET_731")
				require.Empty(t, shellSuggestionsRead(t, directory, prefix+".out"))
			}
			for _, prefix := range []string{"source", "repeated"} {
				require.Equal(t, "2", shellSuggestionsRead(t, directory, prefix+".status"))
				require.Contains(t, shellSuggestionsRead(t, directory, prefix+".err"), shellSuggestionsBashConflict)
			}
			trace := "off"
			if initialTrace != "" {
				trace = "on"
			}
			require.Equal(t, trace, shellSuggestionsRead(t, directory, "source.trace"))
			require.Equal(t, "42", shellSuggestionsRead(t, directory, "lookup.status"))
			require.Equal(t, "on", shellSuggestionsRead(t, directory, "lookup.trace"))
			require.Equal(t, "on", shellSuggestionsRead(t, directory, "repeated.trace"))
			require.Empty(t, shellSuggestionsRead(t, directory, "residue.functions"))
			require.Empty(t, shellSuggestionsRead(t, directory, "residue.variables"))
		})
	}
}

func TestShellSuggestionsZshRefusalPreservesTracingPrivacy(t *testing.T) {
	for _, initialTrace := range []string{"", "set -x"} {
		t.Run(initialTrace, func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, CompletionStyleZsh)
			shellSuggestionsRun(t, shell, CompletionStyleZsh, directory, `function command_not_found_handler {
    if false; then builtin printf 'SYNTHETIC_UNEXECUTED_SECRET_731'; fi
    return 42
}
`+initialTrace+`
source ./suggestions > source.out 2> source.err
builtin printf '%s' "$?" > source.status
case $- in *x*) builtin printf 'on' > source.trace ;; *) builtin printf 'off' > source.trace ;; esac
set -x
opneai > lookup.out 2> lookup.err
builtin printf '%s' "$?" > lookup.status
source ./suggestions > repeated.out 2> repeated.err
builtin printf '%s' "$?" > repeated.status
case $- in *x*) builtin printf 'on' > repeated.trace ;; esac
builtin printf '%s %s %s' "$+functions[openai_suggestions_disable]" "$+functions[__openai_suggestions_zsh_match]" "$+parameters[__openai_suggestions_zsh_state]" > residue
set +x
exit 0
`)
			for _, prefix := range []string{"source", "lookup", "repeated"} {
				require.NotContains(t, shellSuggestionsRead(t, directory, prefix+".err"), "SYNTHETIC_UNEXECUTED_SECRET_731")
				require.Empty(t, shellSuggestionsRead(t, directory, prefix+".out"))
			}
			require.Equal(t, "2", shellSuggestionsRead(t, directory, "source.status"))
			require.Equal(t, "2", shellSuggestionsRead(t, directory, "repeated.status"))
			require.Contains(t, shellSuggestionsRead(t, directory, "source.err"), shellSuggestionsZshConflict)
			require.Equal(t, "42", shellSuggestionsRead(t, directory, "lookup.status"))
			trace := "off"
			if initialTrace != "" {
				trace = "on"
			}
			require.Equal(t, trace, shellSuggestionsRead(t, directory, "source.trace"))
			require.Equal(t, "on", shellSuggestionsRead(t, directory, "repeated.trace"))
			require.Equal(t, "0 0 0", shellSuggestionsRead(t, directory, "residue"))
		})
	}
}

func TestShellSuggestionsZshRefusalPreservesHandlerAttributes(t *testing.T) {
	for _, attribute := range []string{"trace", "trace-local", "warn-nested", "sticky-emulation"} {
		t.Run(attribute, func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, CompletionStyleZsh)
			definition := `function command_not_found_handler {
    builtin printf '%s\000' "$0" "${funcstack[1]}" "$@"
    builtin setopt
    return 42
}
`
			switch attribute {
			case "trace":
				definition += "builtin functions -t command_not_found_handler\n"
			case "trace-local":
				definition += "builtin functions -T command_not_found_handler\n"
			case "warn-nested":
				definition += "builtin functions -W command_not_found_handler\n"
			case "sticky-emulation":
				definition = "builtin emulate zsh -o no_function_argzero -c '" + strings.ReplaceAll(definition, "'", "'\\''") + "'\n"
			}
			require.NoError(t, os.WriteFile(filepath.Join(directory, "prior"), []byte(definition), 0600))
			shellSuggestionsRun(t, shell, CompletionStyleZsh, directory, `
source ./prior
builtin functions command_not_found_handler > before.definition
opneai 'synthetic argument' > baseline.out 2> baseline.err
builtin printf '%s' "$?" > baseline.status
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
opneai 'synthetic argument' > attempted.out 2> attempted.err
builtin printf '%s' "$?" > attempted.status
builtin functions command_not_found_handler > after.definition
builtin printf '%s %s %s' "$+functions[openai_suggestions_disable]" "$+functions[__openai_suggestions_zsh_match]" "$+parameters[__openai_suggestions_zsh_state]" > residue
exit 0
`)
			for _, field := range []string{"out", "err", "status"} {
				require.Equal(t, shellSuggestionsRead(t, directory, "baseline."+field), shellSuggestionsRead(t, directory, "attempted."+field))
			}
			require.Equal(t, "42", shellSuggestionsRead(t, directory, "baseline.status"))
			require.Contains(t, shellSuggestionsRead(t, directory, "baseline.out"), "command_not_found_handler\x00opneai\x00synthetic argument\x00")
			require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
			require.Empty(t, shellSuggestionsRead(t, directory, "setup.out"))
			require.Equal(t, shellSuggestionsZshConflict, shellSuggestionsRead(t, directory, "setup.err"))
			require.Equal(t, "0 0 0", shellSuggestionsRead(t, directory, "residue"))
			require.Equal(t, shellSuggestionsRead(t, directory, "before.definition"), shellSuggestionsRead(t, directory, "after.definition"))
		})
	}
}

func TestShellSuggestionsZshRefusesAutoloadWithoutLoadingHandler(t *testing.T) {
	shell, directory := shellSuggestionsFixture(t, CompletionStyleZsh)
	autoload := filepath.Join(directory, "autoload")
	require.NoError(t, os.Mkdir(autoload, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(autoload, "command_not_found_handler"),
		[]byte("builtin printf 'loaded' > autoload.called\nbuiltin printf '%s' \"$0\" > prior.name\nreturn 42\n"), 0600))
	shellSuggestionsRun(t, shell, CompletionStyleZsh, directory, `
fpath=("$PWD/autoload")
autoload -Uz command_not_found_handler
builtin functions command_not_found_handler > before.definition
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
builtin functions command_not_found_handler > after.definition
builtin test -e autoload.called
builtin printf '%s' "$?" > setup.loaded
builtin printf '%s %s %s' "$+functions[openai_suggestions_disable]" "$+functions[__openai_suggestions_zsh_match]" "$+parameters[__openai_suggestions_zsh_state]" > residue
opneai > result.out 2> result.err
builtin printf '%s' "$?" > result.status
exit 0
`)
	require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
	require.Empty(t, shellSuggestionsRead(t, directory, "setup.out"))
	require.Equal(t, shellSuggestionsZshConflict, shellSuggestionsRead(t, directory, "setup.err"))
	require.Equal(t, shellSuggestionsRead(t, directory, "before.definition"), shellSuggestionsRead(t, directory, "after.definition"))
	require.Equal(t, "1", shellSuggestionsRead(t, directory, "setup.loaded"))
	require.Equal(t, "0 0 0", shellSuggestionsRead(t, directory, "residue"))
	require.Equal(t, "42", shellSuggestionsRead(t, directory, "result.status"))
	require.Equal(t, "opneai", shellSuggestionsRead(t, directory, "prior.name"))
	require.Equal(t, "loaded", shellSuggestionsRead(t, directory, "autoload.called"))
	require.Empty(t, shellSuggestionsRead(t, directory, "result.out"))
	require.Empty(t, shellSuggestionsRead(t, directory, "result.err"))
}

func TestShellSuggestionsZshRejectsMissingAutoloadWithoutPartialActivation(t *testing.T) {
	shell, directory := shellSuggestionsFixture(t, CompletionStyleZsh)
	shellSuggestionsRun(t, shell, CompletionStyleZsh, directory, `
fpath=("$PWD/missing-autoload")
autoload -Uz command_not_found_handler
builtin functions command_not_found_handler > before.definition
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
builtin functions command_not_found_handler > after.definition
builtin printf '%s %s %s' "$+functions[openai_suggestions_disable]" "$+functions[__openai_suggestions_zsh_match]" "$+parameters[__openai_suggestions_zsh_state]" > residue
exit 0
`)
	require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
	require.Empty(t, shellSuggestionsRead(t, directory, "setup.out"))
	require.Equal(t, shellSuggestionsZshConflict, shellSuggestionsRead(t, directory, "setup.err"))
	require.Equal(t, "0 0 0", shellSuggestionsRead(t, directory, "residue"))
	require.Equal(t, shellSuggestionsRead(t, directory, "before.definition"), shellSuggestionsRead(t, directory, "after.definition"))
}

func TestShellSuggestionsZshRefusesEmptyPriorHandler(t *testing.T) {
	shell, directory := shellSuggestionsFixture(t, CompletionStyleZsh)
	shellSuggestionsRun(t, shell, CompletionStyleZsh, directory, `
functions[command_not_found_handler]=''
builtin functions command_not_found_handler > before.definition
opneai > baseline.out 2> baseline.err
builtin printf '%s' "$?" > baseline.status
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
opneai > attempted.out 2> attempted.err
builtin printf '%s' "$?" > attempted.status
builtin functions command_not_found_handler > after.definition
builtin printf '%s %s %s' "$+functions[openai_suggestions_disable]" "$+functions[__openai_suggestions_zsh_match]" "$+parameters[__openai_suggestions_zsh_state]" > residue
exit 0
`)
	for _, prefix := range []string{"baseline", "attempted"} {
		require.Empty(t, shellSuggestionsRead(t, directory, prefix+".out"))
		require.Empty(t, shellSuggestionsRead(t, directory, prefix+".err"))
		require.Equal(t, "0", shellSuggestionsRead(t, directory, prefix+".status"))
	}
	require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
	require.Empty(t, shellSuggestionsRead(t, directory, "setup.out"))
	require.Equal(t, shellSuggestionsZshConflict, shellSuggestionsRead(t, directory, "setup.err"))
	require.Equal(t, "0 0 0", shellSuggestionsRead(t, directory, "residue"))
	require.Equal(t, shellSuggestionsRead(t, directory, "before.definition"), shellSuggestionsRead(t, directory, "after.definition"))
}

func TestShellSuggestionsBashZshPriorExitPrecedesAdvice(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh} {
		t.Run(string(style), func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, style)
			handler := "command_not_found_handle"
			if style == CompletionStyleZsh {
				handler = "command_not_found_handler"
			}
			shellSuggestionsRun(t, shell, style, directory, handler+`() { builtin printf 'prior exit\n' >&2; exit 42; }
opneai > baseline.out 2> baseline.err
builtin printf '%s' "$?" > baseline.status
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
opneai > result.out 2> result.err
builtin printf '%s' "$?" > result.status
exit 0
`)
			wantError := "prior exit\n"
			if style == CompletionStyleBash {
				// Interactive Bash prints its own exit diagnostic from the native handler.
				wantError += "exit\n"
			}
			require.Equal(t, "", shellSuggestionsRead(t, directory, "baseline.out"))
			require.Equal(t, wantError, shellSuggestionsRead(t, directory, "baseline.err"))
			require.Equal(t, "42", shellSuggestionsRead(t, directory, "baseline.status"))
			for _, field := range []string{"out", "err", "status"} {
				require.Equal(t, shellSuggestionsRead(t, directory, "baseline."+field), shellSuggestionsRead(t, directory, "result."+field))
			}
			if style == CompletionStyleZsh {
				require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
				require.Equal(t, shellSuggestionsZshConflict, shellSuggestionsRead(t, directory, "setup.err"))
			} else {
				require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
				require.Equal(t, shellSuggestionsBashConflict, shellSuggestionsRead(t, directory, "setup.err"))
			}
		})
	}
}

func TestShellSuggestionsBashZshPreservePriorErrexit(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh} {
		t.Run(string(style), func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, style)
			handler := "command_not_found_handle"
			if style == CompletionStyleZsh {
				handler = "command_not_found_handler"
			}
			shellSuggestionsRun(t, shell, style, directory, `function `+handler+` {
    builtin printf 'prior stdout\n'
    builtin printf 'prior stderr\n' >&2
    set -e
    false
    builtin printf 'unreachable' > "$stage.unreachable"
    return 42
}
stage=baseline
opneai > baseline.out 2> baseline.err
builtin printf '%s' "$?" > baseline.status
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
stage=integrated
opneai > integrated.out 2> integrated.err
builtin printf '%s' "$?" > integrated.status
exit 0
`)
			for _, field := range []string{"out", "err", "status"} {
				require.Equal(t, shellSuggestionsRead(t, directory, "baseline."+field),
					shellSuggestionsRead(t, directory, "integrated."+field))
			}
			require.Equal(t, "1", shellSuggestionsRead(t, directory, "baseline.status"))
			if style == CompletionStyleZsh {
				require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
				require.Equal(t, shellSuggestionsZshConflict, shellSuggestionsRead(t, directory, "setup.err"))
			} else {
				require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
				require.Equal(t, shellSuggestionsBashConflict, shellSuggestionsRead(t, directory, "setup.err"))
			}
			for _, stage := range []string{"baseline", "integrated"} {
				_, err := os.Stat(filepath.Join(directory, stage+".unreachable"))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestShellSuggestionsBashZshRejectRebindingUntilDisabled(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh} {
		t.Run(string(style), func(t *testing.T) {
			shell, directory := shellSuggestionsFixture(t, style)
			require.NoError(t, os.WriteFile(filepath.Join(directory, "synthetic_cli"), []byte("#!/bin/sh\n: > backend-called\n"), 0700))
			script, err := RenderShellSuggestions(style, "synthetic_cli")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(directory, "other"), []byte(script), 0600))
			shellSuggestionsRun(t, shell, style, directory, `source ./suggestions
source ./other > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
openai_suggestions_disable
source ./other
synthetci_cli > result.out 2> result.err
builtin printf '%s' "$?" > result.status
exit 0
`)
			require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
			require.Contains(t, shellSuggestionsRead(t, directory, "setup.err"), "Disable executable suggestions")
			require.Contains(t, shellSuggestionsRead(t, directory, "result.err"), "Did you mean: synthetic_cli?")
			require.Equal(t, "127", shellSuggestionsRead(t, directory, "result.status"))
		})
	}
}

func TestShellSuggestionsZshRejectsForeignHandlerAfterDisable(t *testing.T) {
	shell, directory := shellSuggestionsFixture(t, CompletionStyleZsh)
	shellSuggestionsRun(t, shell, CompletionStyleZsh, directory, `
source ./suggestions
openai_suggestions_disable
command_not_found_handler() { builtin printf 'replacement\n' >&2; return 42; }
builtin functions command_not_found_handler > before.definition
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
openai_suggestions_disable
builtin functions command_not_found_handler > after.definition
opneai > result.out 2> result.err
builtin printf '%s' "$?" > result.status
builtin printf '%s' "$__openai_suggestions_zsh_state" > state
exit 0
`)
	require.Empty(t, shellSuggestionsRead(t, directory, "setup.out"))
	require.Equal(t, shellSuggestionsZshConflict, shellSuggestionsRead(t, directory, "setup.err"))
	require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
	require.Empty(t, shellSuggestionsRead(t, directory, "result.out"))
	require.Equal(t, "replacement\n", shellSuggestionsRead(t, directory, "result.err"))
	require.Equal(t, "42", shellSuggestionsRead(t, directory, "result.status"))
	require.Equal(t, "disabled", shellSuggestionsRead(t, directory, "state"))
	require.Equal(t, shellSuggestionsRead(t, directory, "before.definition"), shellSuggestionsRead(t, directory, "after.definition"))
}

func TestShellSuggestionsZshDisableAndReenablePreservesTracing(t *testing.T) {
	shell, directory := shellSuggestionsFixture(t, CompletionStyleZsh)
	shellSuggestionsRun(t, shell, CompletionStyleZsh, directory, `
set -x
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
source ./suggestions > repeated.out 2> repeated.err
builtin printf '%s' "$?" > repeated.status
opneai > initial.out 2> initial.err
builtin printf '%s' "$?" > initial.status
openai_suggestions_disable > disable.out 2> disable.err
builtin printf '%s' "$?" > disable.status
builtin printf '%s' "$+functions[command_not_found_handler]" > disabled.handler
source ./suggestions > reenabled.out 2> reenabled.err
builtin printf '%s' "$?" > reenabled.status
opneai > result.out 2> result.err
builtin printf '%s' "$?" > result.status
case $- in *x*) builtin printf 'on' > final.trace ;; esac
set +x
exit 0
`)
	for _, prefix := range []string{"setup", "repeated", "disable", "reenabled"} {
		require.Equal(t, "0", shellSuggestionsRead(t, directory, prefix+".status"))
		require.Empty(t, shellSuggestionsRead(t, directory, prefix+".out"))
	}
	for _, prefix := range []string{"initial", "result"} {
		require.Equal(t, "127", shellSuggestionsRead(t, directory, prefix+".status"))
		require.Empty(t, shellSuggestionsRead(t, directory, prefix+".out"))
		require.Contains(t, shellSuggestionsRead(t, directory, prefix+".err"),
			"Command not found. Did you mean: openai?\nKeep the remaining arguments unchanged.\n")
	}
	require.Equal(t, "0", shellSuggestionsRead(t, directory, "disabled.handler"))
	require.Equal(t, "on", shellSuggestionsRead(t, directory, "final.trace"))
	_, err := os.Stat(filepath.Join(directory, "backend-called"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestShellSuggestionsBashRejectsForeignHandlerAfterDisable(t *testing.T) {
	shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
	shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `
source ./suggestions
openai_suggestions_disable
command_not_found_handle() { builtin printf 'replacement\n' >&2; return 42; }
builtin declare -f command_not_found_handle > before.definition
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
openai_suggestions_disable
builtin declare -f command_not_found_handle > after.definition
opneai > result.out 2> result.err
builtin printf '%s' "$?" > result.status
builtin printf '%s' "$__openai_suggestions_bash_state" > state
exit 0
`)
	require.Empty(t, shellSuggestionsRead(t, directory, "setup.out"))
	require.Equal(t, shellSuggestionsBashConflict, shellSuggestionsRead(t, directory, "setup.err"))
	require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
	require.Empty(t, shellSuggestionsRead(t, directory, "result.out"))
	require.Equal(t, "replacement\n", shellSuggestionsRead(t, directory, "result.err"))
	require.Equal(t, "42", shellSuggestionsRead(t, directory, "result.status"))
	require.Equal(t, "disabled", shellSuggestionsRead(t, directory, "state"))
	require.Equal(t, shellSuggestionsRead(t, directory, "before.definition"), shellSuggestionsRead(t, directory, "after.definition"))
}

func TestShellSuggestionsBashDisableAndReenablePreservesTracing(t *testing.T) {
	shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
	shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `
set -x
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
source ./suggestions > repeated.out 2> repeated.err
builtin printf '%s' "$?" > repeated.status
opneai > initial.out 2> initial.err
builtin printf '%s' "$?" > initial.status
openai_suggestions_disable > disable.out 2> disable.err
builtin printf '%s' "$?" > disable.status
openai_suggestions_disable > disabled_again.out 2> disabled_again.err
builtin printf '%s' "$?" > disabled_again.status
builtin declare -F command_not_found_handle > disabled.handler
source ./suggestions > reenabled.out 2> reenabled.err
builtin printf '%s' "$?" > reenabled.status
opneai > result.out 2> result.err
builtin printf '%s' "$?" > result.status
case $- in *x*) builtin printf 'on' > final.trace ;; esac
set +x
exit 0
`)
	for _, prefix := range []string{"setup", "repeated", "disable", "disabled_again", "reenabled"} {
		require.Equal(t, "0", shellSuggestionsRead(t, directory, prefix+".status"))
		require.Empty(t, shellSuggestionsRead(t, directory, prefix+".out"))
	}
	for _, prefix := range []string{"initial", "result"} {
		require.Equal(t, "127", shellSuggestionsRead(t, directory, prefix+".status"))
		require.Empty(t, shellSuggestionsRead(t, directory, prefix+".out"))
		require.Contains(t, shellSuggestionsRead(t, directory, prefix+".err"),
			"Command not found. Did you mean: openai?\nKeep the remaining arguments unchanged.\n")
	}
	require.Empty(t, shellSuggestionsRead(t, directory, "disabled.handler"))
	require.Equal(t, "on", shellSuggestionsRead(t, directory, "final.trace"))
	_, err := os.Stat(filepath.Join(directory, "backend-called"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestShellSuggestionsBashRefusalPreservesParsedAliases(t *testing.T) {
	shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
	shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `
function original_action { builtin printf 'original\n' >&2; return 42; }
function replacement_action { builtin printf 'replacement must not run\n' >&2; return 0; }
function command_not_found_handle { original_action "$@"; }
alias original_action=replacement_action
alias command_not_found_handle=replacement_action
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
missing_synthetic_command > result.out 2> result.err
builtin printf '%s' "$?" > result.status
source ./suggestions > repeated.out 2> repeated.err
builtin printf '%s' "$?" > repeated.status
missing_synthetic_command > repeated_lookup.out 2> repeated_lookup.err
builtin printf '%s' "$?" > repeated_lookup.status
builtin shopt -q expand_aliases
builtin printf '%s' "$?" > alias.status
exit 0
`)
	for _, prefix := range []string{"setup", "repeated"} {
		require.Equal(t, "2", shellSuggestionsRead(t, directory, prefix+".status"))
		require.Equal(t, shellSuggestionsBashConflict, shellSuggestionsRead(t, directory, prefix+".err"))
	}
	for _, prefix := range []string{"result", "repeated_lookup"} {
		require.Equal(t, "", shellSuggestionsRead(t, directory, prefix+".out"))
		require.Equal(t, "original\n", shellSuggestionsRead(t, directory, prefix+".err"))
		require.Equal(t, "42", shellSuggestionsRead(t, directory, prefix+".status"))
	}
	require.Equal(t, "0", shellSuggestionsRead(t, directory, "alias.status"))
}

func TestShellSuggestionsBashRefusalPreservesExtglobOptions(t *testing.T) {
	shell, directory := shellSuggestionsFixture(t, CompletionStyleBash)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "prior"), []byte(`function command_not_found_handle {
    case "$1" in
        +(missing_*)) builtin printf 'extglob\n'; return 42 ;;
        *) builtin printf 'fallback\n'; return 43 ;;
    esac
}
`), 0600))
	shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `
builtin shopt -s extglob
source ./prior
builtin shopt -u extglob
missing_synthetic_command > baseline.out 2> baseline.err
builtin printf '%s' "$?" > baseline.status
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
missing_synthetic_command > result.out 2> result.err
builtin printf '%s' "$?" > result.status
builtin shopt -q extglob
builtin printf '%s' "$?" > lookup.extglob
source ./suggestions > repeated.out 2> repeated.err
builtin printf '%s' "$?" > repeated.status
missing_synthetic_command > repeated_lookup.out 2> repeated_lookup.err
builtin printf '%s' "$?" > repeated_lookup.status
builtin shopt -q extglob
builtin printf '%s' "$?" > repeated.extglob
exit 0
`)
	for _, prefix := range []string{"setup", "repeated"} {
		require.Equal(t, "2", shellSuggestionsRead(t, directory, prefix+".status"))
		require.Equal(t, shellSuggestionsBashConflict, shellSuggestionsRead(t, directory, prefix+".err"))
	}
	for _, prefix := range []string{"result", "repeated_lookup"} {
		for _, field := range []string{"out", "err", "status"} {
			require.Equal(t, shellSuggestionsRead(t, directory, "baseline."+field), shellSuggestionsRead(t, directory, prefix+"."+field))
		}
	}
	require.Equal(t, "1", shellSuggestionsRead(t, directory, "lookup.extglob"))
	require.Equal(t, "1", shellSuggestionsRead(t, directory, "repeated.extglob"))
}

func TestShellSuggestionsBash32RejectsWithoutPartialActivation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacy Bash fixture requires a Unix host")
	}
	shell, err := exec.LookPath("/bin/bash")
	if err != nil {
		t.Skip("/bin/bash is unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, shell, "--noprofile", "--norc", "-c", "(( BASH_VERSINFO[0] == 3 && BASH_VERSINFO[1] == 2 ))")
	probe.Env = []string{"PATH=/usr/bin:/bin"}
	if err := probe.Run(); err != nil {
		require.NoError(t, ctx.Err(), "Bash 3.2 capability probe exceeded its deadline")
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		require.Equal(t, 1, exit.ExitCode())
		t.Skip("native Bash 3.2 is unavailable")
	}
	directory := t.TempDir()
	script, err := RenderShellSuggestions(CompletionStyleBash, "openai")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "suggestions"), []byte(script), 0600))
	shellSuggestionsRun(t, shell, CompletionStyleBash, directory, `
source ./suggestions > setup.out 2> setup.err
builtin printf '%s' "$?" > setup.status
builtin declare -F command_not_found_handle openai_suggestions_disable > functions.out
exit 0
`)
	require.Equal(t, "2", shellSuggestionsRead(t, directory, "setup.status"))
	require.Contains(t, shellSuggestionsRead(t, directory, "setup.err"), "Bash 4 or later")
	require.Empty(t, shellSuggestionsRead(t, directory, "functions.out"))
}
