package autocomplete

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type suggestionStartupFixture struct {
	options SuggestionInstallation
	home    string
	path    string
}

func newSuggestionStartupFixture(t *testing.T, shell CompletionStyle, doubleTab, customBinding bool) suggestionStartupFixture {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin ' __DOUBLE_TAB_ARGUMENT__ space")
	require.NoError(t, os.MkdirAll(bin, 0700))
	fixture := suggestionStartupFixture{
		home: home, path: bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		options: SuggestionInstallation{Shell: shell, Directory: filepath.Join(home, "config", "openai", "shell"),
			Executable: filepath.Join(bin, "openai"), DoubleTab: doubleTab},
	}
	startup := "PS1='INSTALL_TEST> '; PS2='MORE> '\n"
	switch shell {
	case CompletionStyleBash:
		fixture.options.Profile = filepath.Join(home, ".bashrc")
		startup += `capture_buffer() { printf '<%s>|%s\n' "$READLINE_LINE" "$READLINE_POINT" >>"$INSTALL_TEST_HOME/buffers"; printf '\nCAPTURED_BUFFER\n'; }
bind -x '"\C-x\C-g": capture_buffer'
bind -x '"\C-x\C-r": openai_suggestion_restore'
`
		if customBinding {
			startup += `custom_insert() { READLINE_LINE=CUSTOM; READLINE_POINT=6; }
bind -x '"\C-x\C-i": custom_insert'
`
		}
	case CompletionStyleZsh:
		fixture.options.Profile = filepath.Join(home, ".zshrc")
		startup += `bindkey -e
capture_buffer() { printf '<%s>|%s\n' "$BUFFER" "$CURSOR" >>"$INSTALL_TEST_HOME/buffers"; printf '\nCAPTURED_BUFFER\n'; }
zle -N capture_buffer
bindkey '^X^G' capture_buffer
bindkey '^X^R' openai_suggestion_restore
`
		if customBinding {
			startup += `custom_insert() { BUFFER=CUSTOM; CURSOR=6; }
zle -N custom_insert
bindkey '^X^I' custom_insert
`
		}
	case CompletionStyleFish:
		fixture.options.Profile = filepath.Join(home, "config", "fish", "conf.d", "openai-suggestions.fish")
		startup = `fish_default_key_bindings
function fish_prompt; printf 'INSTALL_TEST> '; end
function capture_buffer
    printf '<%s>|%s\n' (commandline --current-buffer | string collect) (commandline --cursor) >>"$INSTALL_TEST_HOME/buffers"
    printf '\nCAPTURED_BUFFER\n'
end
bind \cx\cg capture_buffer
bind \cx\cr openai_suggestion_restore
`
		if customBinding {
			startup += `function custom_insert; commandline --replace CUSTOM; commandline --cursor 6; end
bind \cx\ci custom_insert
`
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(fixture.options.Profile), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(home, "config", "fish", "config.fish"), []byte(startup), 0600))
		startup = "# dedicated synthetic suggestions startup\n"
	}
	require.NoError(t, os.WriteFile(fixture.options.Profile, []byte(startup), 0600))
	return fixture
}

func writeSuggestionStartupCLI(t *testing.T, fixture suggestionStartupFixture, path, version, command string, fail bool) {
	t.Helper()
	suggestions, err := renderSuggestionCompletion(fixture.options.Shell, "openai", fixture.options.DoubleTab)
	require.NoError(t, err)
	integration := filepath.Join(fixture.home, "integration-"+version)
	generated := `printf '%s\n' evaluated >"$INSTALL_TEST_HOME/evaluated"` + "\n" + suggestions + "\n# openai-cli-suggestions-complete v1\n"
	require.NoError(t, os.WriteFile(integration, []byte(generated), 0600))
	exit := "0"
	if fail {
		exit = "7"
	}
	source := `#!/bin/sh
if [ "$1" = explore ]; then
    sed -n '1p'
    exit 0
fi
if [ "$1" = @completion ]; then
    if [ -f "$INSTALL_TEST_HOME/read-stdin" ]; then
        read -r consumed
        printf '%s\n' "$?" >"$INSTALL_TEST_HOME/stdin-status"
    fi
    printf '%s|%s\n' ` + quotePickerPath(CompletionStyleBash, version) + ` "$*" >>"$INSTALL_TEST_HOME/generation-calls"
    cat ` + quotePickerPath(CompletionStyleBash, integration) + `
    exit ` + exit + `
fi
if [ "$1" = examples ]; then
    printf '%s\n' ` + quotePickerPath(CompletionStyleBash, command) + `
    if [ "$OPENAI_CLI_COMMANDS_FD" = 9 ]; then printf '%s\n' ` + quotePickerPath(CompletionStyleBash, command) + ` >&9; fi
    printf 'DISPLAYED_COMMAND_END\n'
    exit 0
fi
if [ "$1" = __complete ]; then
    for word do :; done
    case "$word" in gen*) printf 'generate\n';; *) printf 'future-feature\n';; esac
    exit 0
fi
if [ "$1" = images ]; then
    original_tty=$(stty -g)
    stty -inlcr icrnl
    printf 'STARTUP_PICKER_OPEN\n'
    read -r ignored
    stty "$original_tty"
    printf 'STARTUP_PICKER_CLOSED\n'
    exit 130
fi
printf '%s\n' "$*" >>"$INSTALL_TEST_HOME/executed"
printf 'SELECTED_COMMAND_EXECUTED\n'
`
	require.NoError(t, os.WriteFile(path, []byte(source), 0700))
}

func runSuggestionInstalledShell(t *testing.T, fixture suggestionStartupFixture, interaction string, beforePrompt ...string) {
	t.Helper()
	shell := string(fixture.options.Shell)
	required := false
	for _, requested := range strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",") {
		required = required || strings.TrimSpace(requested) == shell
	}
	unavailable := func(reason string) {
		t.Helper()
		if required {
			t.Fatal(reason + "; native " + shell + " checks are required")
		}
		t.Skip(reason)
	}
	name := shell
	if override := os.Getenv("OPENAI_CLI_TEST_" + strings.ToUpper(shell)); override != "" {
		name = override
	}
	binary, err := exec.LookPath(name)
	if err != nil {
		unavailable(shell + " is unavailable")
	}
	if shell == "bash" && exec.Command(binary, "-c", `(( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 3) ))`).Run() != nil {
		unavailable("command insertion requires Bash 4.3+")
	}
	expect, err := exec.LookPath("expect")
	if err != nil {
		unavailable("expect is required for native startup tests")
	}
	arguments := map[string]string{"bash": "--noprofile --rcfile $env(INSTALL_TEST_PROFILE) -i", "zsh": "-d -i", "fish": "--interactive"}[shell]
	driver := `set timeout 8
match_max 100000
spawn -noecho $env(INSTALL_TEST_SHELL) ` + arguments + `
expect_before {
  -exact "\033\1330c" {send -- "\033\133?1;2c"; exp_continue}
  -exact "\033\1336n" {send -- "\033\1331;1R"; exp_continue}
}
` + strings.Join(beforePrompt, "\n") + `
expect_after {
  timeout {puts stderr "installed suggestions probe timed out"; exit 1}
  eof {puts stderr "unexpected shell exit: [wait]"; exit 1}
}
expect -exact "INSTALL_TEST> "
` + interaction + `
send -- "exit\r"
expect eof
`
	path := filepath.Join(fixture.home, "driver.exp")
	require.NoError(t, os.WriteFile(path, []byte(driver), 0600))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, expect, path)
	process.Dir = fixture.home
	process.Env = []string{
		"HOME=" + fixture.home, "ZDOTDIR=" + fixture.home, "XDG_CONFIG_HOME=" + filepath.Join(fixture.home, "config"),
		"PATH=" + fixture.path, "TERM=xterm-256color", "LC_ALL=C", "TMPDIR=" + fixture.home,
		"INSTALL_TEST_HOME=" + fixture.home, "INSTALL_TEST_SHELL=" + binary, "INSTALL_TEST_PROFILE=" + fixture.options.Profile,
		"INSTALL_TEST_EXECUTABLE=" + fixture.options.Executable,
		"BASH_SILENCE_DEPRECATION_WARNING=1",
	}
	output, err := process.CombinedOutput()
	require.NoError(t, err, string(output))
}

const suggestionStartupCapture = `
send -- "openai examples\r"
expect -exact "DISPLAYED_COMMAND_END\r\n"
expect -exact "INSTALL_TEST> "
send -- "\030\011\030\007"
expect -re {CAPTURED_BUFFER\r?\n}
send -- "\005\025"
`

func TestSuggestionInstallNativeTracksExecutableUpgrades(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, symlink := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/symlink=%t", shell, symlink), func(t *testing.T) {
				fixture := newSuggestionStartupFixture(t, shell, false, false)
				firstBinary := fixture.options.Executable
				if symlink {
					firstBinary = filepath.Join(fixture.home, "version-one")
					if err := os.Symlink(firstBinary, fixture.options.Executable); err != nil {
						t.Skipf("symbolic links unavailable: %v", err)
					}
				}
				writeSuggestionStartupCLI(t, fixture, firstBinary, "one", "openai models list", false)
				installed, err := InstallSuggestions(t.Context(), fixture.options)
				require.NoError(t, err)
				profile, err := os.ReadFile(fixture.options.Profile)
				require.NoError(t, err)
				runSuggestionInstalledShell(t, fixture, suggestionStartupCapture)
				secondBinary := fixture.options.Executable
				if symlink {
					secondBinary = filepath.Join(fixture.home, "version-two")
					require.NoError(t, os.Remove(fixture.options.Executable))
					require.NoError(t, os.Symlink(secondBinary, fixture.options.Executable))
				}
				writeSuggestionStartupCLI(t, fixture, secondBinary, "two", "openai future-feature run", false)
				runSuggestionInstalledShell(t, fixture, suggestionStartupCapture)
				buffers, err := os.ReadFile(filepath.Join(fixture.home, "buffers"))
				require.NoError(t, err)
				require.Equal(t, "<openai models list>|18\n<openai future-feature run>|25\n", string(buffers))
				calls, err := os.ReadFile(filepath.Join(fixture.home, "generation-calls"))
				require.NoError(t, err)
				require.Equal(t, "one|@completion "+string(shell)+" --suggestions --suggestions-only\ntwo|@completion "+string(shell)+" --suggestions --suggestions-only\n", string(calls))
				_, err = os.Stat(filepath.Join(fixture.home, "executed"))
				require.ErrorIs(t, err, os.ErrNotExist)
				after, err := os.ReadFile(fixture.options.Profile)
				require.NoError(t, err)
				require.Equal(t, profile, after, "startup must not rewrite installed profile state")
				status, err := InspectSuggestions(t.Context(), fixture.options)
				require.NoError(t, err)
				require.Equal(t, SuggestionStatus{Installed: true, Executable: fixture.options.Executable, ScriptPath: installed.ScriptPath}, status)
			})
		}
	}
}

func TestSuggestionInstallNativeFailsClosedWithoutPATHFallback(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, failure := range []string{"missing", "mismatched-path", "failed-generation", "truncated-success", "missing-marker"} {
			t.Run(string(shell)+"/"+failure, func(t *testing.T) {
				fixture := newSuggestionStartupFixture(t, shell, false, false)
				writeSuggestionStartupCLI(t, fixture, fixture.options.Executable, "one", "openai models list", failure == "failed-generation")
				_, err := InstallSuggestions(t.Context(), fixture.options)
				require.NoError(t, err)
				if failure == "truncated-success" || failure == "missing-marker" {
					integration := filepath.Join(fixture.home, "integration-one")
					content, err := os.ReadFile(integration)
					require.NoError(t, err)
					content = []byte(strings.TrimSuffix(string(content), "\n# openai-cli-suggestions-complete v1\n"))
					if failure == "truncated-success" {
						content = content[:len(content)/2]
					}
					require.NoError(t, os.WriteFile(integration, content, 0600))
				} else if failure != "failed-generation" {
					otherDirectory := filepath.Join(fixture.home, "other")
					require.NoError(t, os.Mkdir(otherDirectory, 0700))
					writeSuggestionStartupCLI(t, fixture, filepath.Join(otherDirectory, "openai"), "wrong", "openai wrong command", false)
					fixture.path = otherDirectory + string(os.PathListSeparator) + fixture.path
					if failure == "missing" {
						require.NoError(t, os.Remove(fixture.options.Executable))
					}
				}
				runSuggestionInstalledShell(t, fixture, `
send -- "draft\030\007"
expect -re {CAPTURED_BUFFER\r?\n}
send -- "\005\025"
`)
				buffers, err := os.ReadFile(filepath.Join(fixture.home, "buffers"))
				require.NoError(t, err)
				require.Equal(t, "<draft>|5\n", string(buffers))
				_, err = os.Stat(filepath.Join(fixture.home, "evaluated"))
				require.ErrorIs(t, err, os.ErrNotExist, "rejected output must never be evaluated")
				calls, err := os.ReadFile(filepath.Join(fixture.home, "generation-calls"))
				if failure == "failed-generation" || failure == "truncated-success" || failure == "missing-marker" {
					require.NoError(t, err)
					require.Equal(t, "one|@completion "+string(shell)+" --suggestions --suggestions-only\n", string(calls))
				} else {
					require.ErrorIs(t, err, os.ErrNotExist, "neither the pinned nor PATH binary should run")
				}
			})
		}
	}
}

func TestSuggestionInstallNativePreservesBindingsAndDoubleTabOptIn(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, customBinding := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/custom=%t", shell, customBinding), func(t *testing.T) {
				fixture := newSuggestionStartupFixture(t, shell, !customBinding, customBinding)
				writeSuggestionStartupCLI(t, fixture, fixture.options.Executable, "one", "openai models list", false)
				_, err := InstallSuggestions(t.Context(), fixture.options)
				require.NoError(t, err)
				interaction := suggestionStartupCapture
				if !customBinding {
					interaction = strings.Replace(interaction, `\030\011\030\007`, `\t\t\030\007`, 1)
				}
				runSuggestionInstalledShell(t, fixture, interaction)
				buffers, err := os.ReadFile(filepath.Join(fixture.home, "buffers"))
				require.NoError(t, err)
				want := "<openai models list>|18\n"
				if customBinding {
					want = "<CUSTOM>|6\n"
				}
				require.Equal(t, want, string(buffers))
				calls, err := os.ReadFile(filepath.Join(fixture.home, "generation-calls"))
				require.NoError(t, err)
				require.Equal(t, !customBinding, strings.Contains(string(calls), " --suggestions-double-tab"))
			})
		}
	}
}

func TestSuggestionInstallNativeDoesNotReactivateAfterSessionDisable(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		t.Run(string(shell), func(t *testing.T) {
			fixture := newSuggestionStartupFixture(t, shell, false, false)
			writeSuggestionStartupCLI(t, fixture, fixture.options.Executable, "one", "openai models list", false)
			_, err := InstallSuggestions(t.Context(), fixture.options)
			require.NoError(t, err)
			runSuggestionInstalledShell(t, fixture, `
send -- "openai_command_insertion_disable; source \"\$INSTALL_TEST_PROFILE\"; printf 'RELOADED_OK\\n'\r"
expect -exact "RELOADED_OK\r\n"
expect -exact "INSTALL_TEST> "
`)
			calls, err := os.ReadFile(filepath.Join(fixture.home, "generation-calls"))
			require.NoError(t, err)
			require.Equal(t, "one|@completion "+string(shell)+" --suggestions --suggestions-only\n", string(calls))
		})
	}
}

func TestSuggestionInstallNativeUpdatesCommandsInRunningShell(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		t.Run(string(shell), func(t *testing.T) {
			fixture := newSuggestionStartupFixture(t, shell, false, false)
			writeSuggestionStartupCLI(t, fixture, fixture.options.Executable, "one", "openai models list", false)
			upgradedCommand := "openai future-feature run --fresh-flag yes"
			writeSuggestionStartupCLI(t, fixture, filepath.Join(fixture.home, "upgrade"), "two", upgradedCommand, false)
			_, err := InstallSuggestions(t.Context(), fixture.options)
			require.NoError(t, err)
			runSuggestionInstalledShell(t, fixture, `
send -- "openai examples\r"
expect -exact "DISPLAYED_COMMAND_END\r\n"
expect -exact "INSTALL_TEST> "
send -- "draft tail\002\002\002\002\030\011\030\007"
expect -re {CAPTURED_BUFFER\r?\n}
send -- "\030\022\030\007"
expect -re {CAPTURED_BUFFER\r?\n}
file rename -force "$env(INSTALL_TEST_HOME)/upgrade" $env(INSTALL_TEST_EXECUTABLE)
send -- "\005\025openai examples\r"
expect -exact "DISPLAYED_COMMAND_END\r\n"
expect -exact "INSTALL_TEST> "
send -- "draft tail\002\002\002\002\030\011\030\007"
expect -re {CAPTURED_BUFFER\r?\n}
send -- "\030\022\030\007"
expect -re {CAPTURED_BUFFER\r?\n}
if {[file exists "$env(INSTALL_TEST_HOME)/executed"]} {error "insertion submitted a command"}
send -- "\030\011\r"
expect -exact "SELECTED_COMMAND_EXECUTED\r\n"
expect -exact "INSTALL_TEST> "
`)
			buffers, err := os.ReadFile(filepath.Join(fixture.home, "buffers"))
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("<openai models list>|18\n<draft tail>|6\n<%s>|%d\n<draft tail>|6\n", upgradedCommand, len(upgradedCommand)), string(buffers))
			calls, err := os.ReadFile(filepath.Join(fixture.home, "generation-calls"))
			require.NoError(t, err)
			require.Equal(t, "one|@completion "+string(shell)+" --suggestions --suggestions-only\n", string(calls), "the running shell must not reload integration code")
			executed, err := os.ReadFile(filepath.Join(fixture.home, "executed"))
			require.NoError(t, err)
			require.Equal(t, "future-feature run --fresh-flag yes\n", string(executed))
		})
	}
}

func TestSuggestionInstallNativePickerCoexistence(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, pickerFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/picker-first=%t", shell, pickerFirst), func(t *testing.T) {
				fixture := newSuggestionStartupFixture(t, shell, false, false)
				writeSuggestionStartupCLI(t, fixture, fixture.options.Executable, "one", "openai models list", false)
				picker := PickerInstallation{Shell: shell, Directory: fixture.options.Directory, Profile: fixture.options.Profile}
				if pickerFirst {
					_, err := InstallPicker(t.Context(), picker)
					require.NoError(t, err)
				}
				_, err := InstallSuggestions(t.Context(), fixture.options)
				require.NoError(t, err)
				if !pickerFirst {
					_, err := InstallPicker(t.Context(), picker)
					require.NoError(t, err)
				}
				runSuggestionInstalledShell(t, fixture, `
send -- "openai images generate\t"
expect -re {STARTUP_PICKER_OPEN\r?\n}
send -- "cancel\n"
expect -re {STARTUP_PICKER_CLOSED\r?\n}
expect -exact "INSTALL_TEST> "
send -- "\005\025"
`+suggestionStartupCapture)
				buffers, err := os.ReadFile(filepath.Join(fixture.home, "buffers"))
				require.NoError(t, err)
				require.Equal(t, "<openai models list>|18\n", string(buffers))
			})
		}
	}
}

func TestSuggestionInstallNativeStartupInputAndCancellation(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, cancellation := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cancel=%t", shell, cancellation), func(t *testing.T) {
				fixture := newSuggestionStartupFixture(t, shell, false, false)
				writeSuggestionStartupCLI(t, fixture, fixture.options.Executable, "one", "openai models list", false)
				_, err := InstallSuggestions(t.Context(), fixture.options)
				require.NoError(t, err)
				if !cancellation {
					require.NoError(t, os.WriteFile(filepath.Join(fixture.home, "read-stdin"), nil, 0600))
					runSuggestionInstalledShell(t, fixture, suggestionStartupCapture)
					input, err := os.ReadFile(filepath.Join(fixture.home, "stdin-status"))
					require.NoError(t, err)
					require.Equal(t, "1\n", string(input), "startup generation receives EOF instead of terminal input")
					return
				}
				program := `#!/bin/sh
trap 'exit 130' INT TERM
printf 'ready\n' >"$INSTALL_TEST_HOME/generation-ready"
printf '%s\n' 'printf evaluated >"$INSTALL_TEST_HOME/evaluated"'
while :; do sleep 1; done
`
				require.NoError(t, os.WriteFile(fixture.options.Executable, []byte(program), 0700))
				runSuggestionInstalledShell(t, fixture, "", `
for {set attempt 0} {$attempt < 300 && ![file exists "$env(INSTALL_TEST_HOME)/generation-ready"]} {incr attempt} {
    expect -timeout 0 {
        timeout {}
        -re {.+} {}
    }
    after 20
}

if {![file exists "$env(INSTALL_TEST_HOME)/generation-ready"]} {error "startup generation never began"}
send -- "\003"
`)
				_, err = os.Stat(filepath.Join(fixture.home, "evaluated"))
				require.ErrorIs(t, err, os.ErrNotExist)
			})
		}
	}
}

func TestSuggestionInstallNativePreservesUserCompletion(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		t.Run(string(shell), func(t *testing.T) {
			fixture := newSuggestionStartupFixture(t, shell, false, false)
			profile := fixture.options.Profile
			custom := `__openai_bash_autocomplete() { COMPREPLY=(custom-entry); }
complete -F __openai_bash_autocomplete openai
`
			if shell == CompletionStyleZsh {
				custom = `autoload -Uz compinit; compinit -D
__openai_zsh_autocomplete() { compadd custom-entry; }
compdef __openai_zsh_autocomplete openai
`
			} else if shell == CompletionStyleFish {
				profile = filepath.Join(fixture.home, "config", "fish", "config.fish")
				custom = `function __openai_fish_autocomplete; printf 'custom-entry\n'; end
complete -c openai -f -a '(__openai_fish_autocomplete)'
`
			}
			content, err := os.ReadFile(profile)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(profile, append(content, []byte(custom)...), 0600))
			writeSuggestionStartupCLI(t, fixture, fixture.options.Executable, "one", "openai models list", false)
			_, err = InstallSuggestions(t.Context(), fixture.options)
			require.NoError(t, err)
			runSuggestionInstalledShell(t, fixture, `
send -- "openai cu\t\030\007"
expect -re {CAPTURED_BUFFER\r?\n}
send -- "\005\025"
`+suggestionStartupCapture)
			buffers, err := os.ReadFile(filepath.Join(fixture.home, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<openai custom-entry >|20\n<openai models list>|18\n", string(buffers))
		})
	}
}
