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
	"github.com/urfave/cli/v3"
)

// These tests run real line editors. The synthetic executable records the
// argv/terminal boundary; the picker process suite exercises the real UI.
func TestPickerBashZshTabAndLifecycle(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			requirePickerBash(t, shell)
			directory := t.TempDir()
			setup := ""
			if shell == "zsh" {
				setup = `
fallback_emacs() { print -r -- fallback-emacs >>"$PICKER_TEST_RESULT"; }
fallback_viins() { print -r -- fallback-viins >>"$PICKER_TEST_RESULT"; }
zle -N fallback_emacs
zle -N fallback_viins
bindkey -M emacs '^I' fallback_emacs
bindkey -M viins '^I' fallback_viins
bindkey -e
`
			}
			body := `
send -- "openai images generate\t"
expect -exact "PICKER_LAUNCHED"
send -- "cancel\n"
expect -exact "PICKER_CANCELED"
expect -exact "PICKER_TEST> "
# The same command line remains editable after cancellation. Ctrl+U discards it.
send -- "\025printf 'AFTER_CANCEL\\n'\r"
expect -exact "AFTER_CANCEL\r\n"
expect -exact "PICKER_TEST> "
send -- "openai images gen\t"
`
			if shell == "bash" {
				body += `
expect -exact "generate "
send -- "\025openai_picker_disable; complete -p openai >\"\$PICKER_TEST_BINDING\"\r"
`
			} else {
				body += `
send -- "\025bindkey -v\r"
expect -exact "PICKER_TEST> "
send -- "openai images generate\t"
expect -exact "PICKER_LAUNCHED"
send -- "cancel\n"
expect -exact "PICKER_CANCELED"
expect -exact "PICKER_TEST> "
send -- "\025openai images gen\t"
send -- "\025openai_picker_disable; bindkey -M emacs '^I' >\"\$PICKER_TEST_BINDING\"; bindkey -M viins '^I' >>\"\$PICKER_TEST_BINDING\"\r"
`
			}
			body += `
expect -exact "PICKER_TEST> "
send -- "exit\r"
expect eof
`
			runPickerShellPTY(t, shell, directory, setup, "", body)
			result, err := os.ReadFile(filepath.Join(directory, "result"))
			require.NoError(t, err)
			text := string(result)
			require.Contains(t, text, "launch "+shell+" 2\n<images>\n<generate>\ntty:111\n")
			binding, err := os.ReadFile(filepath.Join(directory, "binding"))
			require.NoError(t, err)
			if shell == "bash" {
				require.Equal(t, 1, strings.Count(text, "launch "))
				require.Equal(t, "complete -o filenames -F __openai_bash_autocomplete openai\n", string(binding))
			} else {
				require.Equal(t, 2, strings.Count(text, "launch "))
				require.Contains(t, text, "fallback-emacs\n")
				require.Contains(t, text, "fallback-viins\n")
				require.Equal(t, "\"^I\" fallback_emacs\n\"^I\" fallback_viins\n", string(binding))
			}
		})
	}
}

func TestPickerBashZshExactLines(t *testing.T) {
	lines := []struct {
		line string
		want string
	}{
		{"openai images generate", "yes"},
		{" \topenai\timages  generate \t", "yes"},
		{"OPENAI IMAGES GENERATE", "no"},
		{"openai images generate --help", "no"},
		{"openai images generate >out", "no"},
		{"openai images generate;", "no"},
		{"openai images generate | cat", "no"},
		{"openai images generate && true", "no"},
		{"openai images generate\n", "no"},
		{"openai\nimages generate", "no"},
		{"'openai' images generate", "no"},
		{"command openai images generate", "no"},
		{"openai images generat", "no"},
		{"openai images generate $(touch unsafe)", "no"},
		{"openai\rimages generate", "no"},
	}
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			requirePickerBash(t, shell)
			var probe strings.Builder
			if shell == "bash" {
				probe.WriteString("shopt -s nocasematch\n")
			} else {
				probe.WriteString("setopt nocasematch\n")
			}
			var expected strings.Builder
			for _, line := range lines {
				variable, cursor := "COMP_LINE", "COMP_POINT"
				if shell == "zsh" {
					variable, cursor = "BUFFER", "CURSOR"
				}
				fmt.Fprintf(&probe, "%s=%s; %s=${#%s}\n", variable, pickerShellQuote(line.line), cursor, variable)
				probe.WriteString("if __openai_picker_matches; then printf 'yes\\n'; else printf 'no\\n'; fi >>\"$PICKER_TEST_RESULT\"\n")
				expected.WriteString(line.want + "\n")
			}
			probe.WriteString("COMP_LINE='openai images generate'; COMP_POINT=5; BUFFER=$COMP_LINE; CURSOR=5\n")
			probe.WriteString("if __openai_picker_matches; then printf 'yes\\n'; else printf 'no\\n'; fi >>\"$PICKER_TEST_RESULT\"\n")
			expected.WriteString("no\n")
			directory := t.TempDir()
			runPickerShellPTY(t, shell, directory, "", probe.String(), `send -- "exit\r"; expect eof`)
			result, err := os.ReadFile(filepath.Join(directory, "result"))
			require.NoError(t, err)
			require.Equal(t, expected.String(), string(result))
		})
	}
}

func TestPickerZshHintAndReplacement(t *testing.T) {
	directory := t.TempDir()
	probe := `
BUFFER='openai images generate'; CURSOR=${#BUFFER}; POSTDISPLAY='existing suggestion'
__openai_picker_hint
printf '%s\n' "$POSTDISPLAY" >>"$PICKER_TEST_RESULT"
POSTDISPLAY=''; __openai_picker_hint
printf '%s\n' "$POSTDISPLAY" >>"$PICKER_TEST_RESULT"
BUFFER='openai images generate --help'; __openai_picker_hint
printf '<%s>\n' "$POSTDISPLAY" >>"$PICKER_TEST_RESULT"
POSTDISPLAY='new suggestion'; __openai_picker_hint
printf '%s\n' "$POSTDISPLAY" >>"$PICKER_TEST_RESULT"
new_tab() { :; }; zle -N new_tab
bindkey -M emacs '^I' new_tab
source "$PICKER_TEST_HOOK"
openai_picker_disable
bindkey -M emacs '^I' >>"$PICKER_TEST_RESULT"
openai_picker_disable
bindkey -M emacs '^I' >>"$PICKER_TEST_RESULT"
`
	runPickerShellPTY(t, "zsh", directory, "", probe, `send -- "exit\r"; expect eof`)
	result, err := os.ReadFile(filepath.Join(directory, "result"))
	require.NoError(t, err)
	require.Equal(t, "existing suggestion\n  [Tab: image options]\n<>\nnew suggestion\n\"^I\" new_tab\n\"^I\" new_tab\n", string(result))
}

func TestPickerBashReplacementAndDisable(t *testing.T) {
	requirePickerBash(t, "bash")
	directory := t.TempDir()
	probe := `
complete -F newer_completion openai
openai_picker_disable
complete -p openai >>"$PICKER_TEST_RESULT"
openai_picker_disable
complete -p openai >>"$PICKER_TEST_RESULT"
`
	runPickerShellPTY(t, "bash", directory, "", probe, `send -- "exit\r"; expect eof`)
	result, err := os.ReadFile(filepath.Join(directory, "result"))
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("complete -F newer_completion openai\n", 2), string(result))
}

func TestPickerBashPreservesOtherCompletionKey(t *testing.T) {
	requirePickerBash(t, "bash")
	for _, mode := range []string{"emacs", "vi"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			setup := "set -o " + mode + `
other_completion() {
  printf '%s %s\n' "$COMP_KEY" "$COMP_TYPE" >>"$PICKER_TEST_RESULT"
  COMPREPLY=()
  if [[ $COMP_KEY == 9 ]]; then COMPREPLY=(tab-result); fi
}
complete -F other_completion other
`
			runPickerShellPTY(t, "bash", directory, setup, "", `
send -- "other tab\t"
expect -exact "tab-result "
send -- "\025exit\r"; expect eof
`)
			result, err := os.ReadFile(filepath.Join(directory, "result"))
			require.NoError(t, err)
			require.Equal(t, "9 9\n", string(result))
		})
	}
}

func TestPickerBashReenableAfterTemporaryReplacement(t *testing.T) {
	requirePickerBash(t, "bash")
	for _, mode := range []string{"emacs", "vi"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			probe := `
for keymap in emacs-standard vi-insert; do bind -m "$keymap" '"\C-i": "temporary"'; done
openai_picker_disable
for keymap in emacs-standard vi-insert; do
  __openai_picker_binding "$keymap" '\C-i' >>"$PICKER_TEST_BINDING"
  bind -m "$keymap" '"\C-i": complete'
done
source "$PICKER_TEST_HOOK"
`
			runPickerShellPTY(t, "bash", directory, "set -o "+mode, probe, `
send -- "openai images generate\t"
expect -exact "PICKER_LAUNCHED"
send -- "cancel\n"
expect -exact "PICKER_CANCELED"
expect -exact "PICKER_TEST> "
send -- "\025openai images gen\t"
expect -exact "generate "
send -- "\025openai_picker_disable\r"
expect -exact "PICKER_TEST> "
send -- "exit\r"; expect eof
`)
			binding, err := os.ReadFile(filepath.Join(directory, "binding"))
			require.NoError(t, err)
			require.Equal(t, strings.Repeat("\"\\C-i\": \"temporary\"\n", 2), string(binding))
			result, err := os.ReadFile(filepath.Join(directory, "result"))
			require.NoError(t, err)
			require.Equal(t, 1, strings.Count(string(result), "launch "))
		})
	}
}

func TestPickerBashBindingsAndResourcing(t *testing.T) {
	requirePickerBash(t, "bash")
	for _, scenario := range []struct {
		name, setup, probe, want string
	}{
		{
			name:  "custom macro",
			setup: `bind '"\C-i": "custom"'`,
			probe: `source "$PICKER_TEST_HOOK"; openai_picker_disable
__openai_picker_binding emacs-standard '\C-i' >>"$PICKER_TEST_RESULT"`,
			want: "\"\\C-i\": \"custom\"\n",
		},
		{
			name:  "custom shell callback",
			setup: `bind -x '"\C-i": printf custom'`,
			probe: `source "$PICKER_TEST_HOOK"; openai_picker_disable
__openai_picker_binding emacs-standard '\C-i' >>"$PICKER_TEST_RESULT"`,
			want: "\"\\C-i\": \"printf custom\"\n",
		},
		{
			name:  "occupied private key",
			setup: `bind -x '"\e[99;1\C-i": printf owned'`,
			probe: `__openai_picker_binding emacs-standard '\C-i' >>"$PICKER_TEST_RESULT"
openai_picker_disable
__openai_picker_binding emacs-standard '\e[99;1\C-i' >>"$PICKER_TEST_RESULT"`,
			want: "\"\\C-i\": complete\n\"\\e[99;1\\C-i\": \"printf owned\"\n",
		},
		{
			name: "replacement after install",
			probe: `bind '"\C-i": "new owner"'
source "$PICKER_TEST_HOOK"; openai_picker_disable
__openai_picker_binding emacs-standard '\C-i' >>"$PICKER_TEST_RESULT"`,
			want: "\"\\C-i\": \"new owner\"\n",
		},
		{
			name: "replacement private callback",
			probe: `bind -x '"\e[99;2~": printf newer'
COMP_LINE='openai images generate'; COMP_POINT=${#COMP_LINE}; COMP_TYPE=9; COMP_KEY=9
COMP_WORDS=(openai images generate); COMP_CWORD=2
__openai_picker_complete
openai_picker_disable
__openai_picker_binding emacs-standard '\e[99;2~' >>"$PICKER_TEST_RESULT"`,
			want: "\"\\e[99;2~\": \"printf newer\"\n",
		},
		{
			name: "repeated install disable enable",
			probe: `source "$PICKER_TEST_HOOK"; openai_picker_disable
__openai_picker_binding emacs-standard '\C-i' >>"$PICKER_TEST_RESULT"
__openai_picker_binding emacs-standard '\e[99;1\C-i' >>"$PICKER_TEST_RESULT"
__openai_picker_binding emacs-standard '\e[99;2~' >>"$PICKER_TEST_RESULT"
source "$PICKER_TEST_HOOK"; openai_picker_disable
__openai_picker_binding emacs-standard '\C-i' >>"$PICKER_TEST_RESULT"`,
			want: strings.Repeat("\"\\C-i\": complete\n", 2),
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := t.TempDir()
			runPickerShellPTY(t, "bash", directory, scenario.setup, scenario.probe, `send -- "exit\r"; expect eof`)
			result, err := os.ReadFile(filepath.Join(directory, "result"))
			require.NoError(t, err)
			require.Equal(t, scenario.want, string(result))
		})
	}
}

func TestPickerBashRejectsWholeLineContext(t *testing.T) {
	requirePickerBash(t, "bash")
	var interaction strings.Builder
	for index, line := range []string{
		"OPENAI_KEY=x openai images generate",
		"true; openai images generate",
		"true && openai images generate",
		"true | openai images generate",
		"(openai images generate",
	} {
		fmt.Fprintf(&interaction, "send -- {%s}\nsend -- \"\\t\\025printf 'CONTEXT_%d\\\\n'\\r\"\n", line, index)
		fmt.Fprintf(&interaction, "expect -exact \"CONTEXT_%d\\r\\n\"\nexpect -exact \"PICKER_TEST> \"\n", index)
	}
	interaction.WriteString(`send -- "exit\r"; expect eof`)
	directory := t.TempDir()
	runPickerShellPTY(t, "bash", directory, "", "", interaction.String())
	_, err := os.Stat(filepath.Join(directory, "result"))
	require.ErrorIs(t, err, os.ErrNotExist, "none of the completion attempts may run the picker")
}

func TestPickerBashKeepsRepeatedCompletionAfterCancel(t *testing.T) {
	requirePickerBash(t, "bash")
	directory := t.TempDir()
	runPickerShellPTY(t, "bash", directory, "", "", `
send -- "openai images generate\t"
expect -exact "PICKER_LAUNCHED"
send -- "cancel\n"
expect -exact "PICKER_CANCELED"
expect -exact "PICKER_TEST> "
send -- "\025__openai_bash_autocomplete() { COMPREPLY=(general generate); }\r"
expect -exact "PICKER_TEST> "
send -- "openai gen\t\t\t"
expect -re {general +generate}
send -- "\025exit\r"; expect eof
`)
}

func TestPickerBashPreservesWhitespaceAfterCancel(t *testing.T) {
	requirePickerBash(t, "bash")
	for _, line := range []string{"  openai images generate", "  openai\timages  generate  ", "openai images generate\t"} {
		t.Run(fmt.Sprintf("%q", line), func(t *testing.T) {
			directory := t.TempDir()
			probe := `
capture_buffer() { printf 'buffer:<%s>\n' "$READLINE_LINE" >>"$PICKER_TEST_RESULT"; }
bind -x '"\C-x\C-g": capture_buffer'
`
			interaction := fmt.Sprintf("send -- \"%s\\t\"\n", strings.ReplaceAll(line, "\t", `\026\t`)) + `
expect -exact "PICKER_LAUNCHED"
send -- "cancel\n"
expect -exact "PICKER_CANCELED"
expect -exact "PICKER_TEST> "
send -- "\030\007\025exit\r"; expect eof
`
			runPickerShellPTY(t, "bash", directory, "", probe, interaction)
			result, err := os.ReadFile(filepath.Join(directory, "result"))
			require.NoError(t, err)
			require.Contains(t, string(result), "buffer:<"+line+">\n")
		})
	}
}

func TestPickerBashLegacyKeepsNormalCompletion(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("system Bash is unavailable")
	}
	if err := exec.Command("/bin/bash", "-c", `(( BASH_VERSINFO[0] < 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] < 3) ))`).Run(); err != nil {
		t.Skip("system Bash is new enough for the Tab hook")
	}
	t.Setenv("OPENAI_CLI_TEST_BASH", "/bin/bash")
	directory := t.TempDir()
	probe := `
if type openai_picker_disable >/dev/null 2>&1; then printf 'installed\n'; else printf 'preserved\n'; fi >>"$PICKER_TEST_RESULT"
complete -p openai >>"$PICKER_TEST_RESULT"
`
	runPickerShellPTY(t, "bash", directory, "", probe, `
send -- "openai images gen\t"
expect -exact "generate "
send -- "\025exit\r"; expect eof
`)
	result, err := os.ReadFile(filepath.Join(directory, "result"))
	require.NoError(t, err)
	require.Equal(t, "preserved\ncomplete -o filenames -F __openai_bash_autocomplete openai\n", string(result))
}

func TestPickerBashZshRejectAliasesAndFunctions(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			requirePickerBash(t, shell)
			directory := t.TempDir()
			probe := `
COMP_LINE='openai images generate'; COMP_POINT=${#COMP_LINE}; BUFFER=$COMP_LINE; CURSOR=${#BUFFER}
alias openai='printf alias'
if __openai_picker_matches; then printf 'launched\n'; else printf 'preserved\n'; fi >>"$PICKER_TEST_RESULT"
unalias openai
openai() { :; }
if __openai_picker_matches; then printf 'launched\n'; else printf 'preserved\n'; fi >>"$PICKER_TEST_RESULT"
`
			runPickerShellPTY(t, shell, directory, "", probe, `send -- "exit\r"; expect eof`)
			result, err := os.ReadFile(filepath.Join(directory, "result"))
			require.NoError(t, err)
			require.Equal(t, "preserved\npreserved\n", string(result))
		})
	}
}

func TestPickerZshChainedReplacementAfterReenable(t *testing.T) {
	directory := t.TempDir()
	setup := `
old_tab() { print -r -- old >>"$PICKER_TEST_RESULT"; }
zle -N old_tab
bindkey -M emacs '^I' old_tab
bindkey -e
`
	probe := `
new_tab() { print -r -- new >>"$PICKER_TEST_RESULT"; zle __openai_picker_tab_emacs; }
zle -N new_tab
bindkey -M emacs '^I' new_tab
openai_picker_disable
source "$PICKER_TEST_HOOK"
`
	runPickerShellPTY(t, "zsh", directory, setup, probe, `
send -- "unrelated\t"
send -- "\025printf 'CHAINED_DONE\\n'\r"
expect -exact "CHAINED_DONE\r\n"
expect -exact "PICKER_TEST> "
send -- "exit\r"; expect eof
`)
	result, err := os.ReadFile(filepath.Join(directory, "result"))
	require.NoError(t, err)
	require.Equal(t, "new\nold\n", string(result))
}

func TestPickerBashZshDumbTerminalKeepsCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			directory := t.TempDir()
			probe := `
if type openai_picker_disable >/dev/null 2>&1; then printf 'installed\n'; else printf 'preserved\n'; fi >>"$PICKER_TEST_RESULT"
`
			runPickerShellPTY(t, shell, directory, "TERM=dumb", probe, `send -- "exit\r"; expect eof`)
			result, err := os.ReadFile(filepath.Join(directory, "result"))
			require.NoError(t, err)
			require.Equal(t, "preserved\n", string(result))
		})
	}
}

func TestPickerBashZshDoesNotInstallOutsideTerminal(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(shell + " is not available")
			}
			script, err := renderPickerCompletion(CompletionStyle(shell), "openai")
			require.NoError(t, err)
			cmd := exec.Command(binary, "-c", script+"\nif type openai_picker_disable >/dev/null 2>&1; then exit 1; fi")
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
		})
	}
}

func TestBashCompletionWithoutMapfile(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	script, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	require.NoError(t, err)
	for _, scenario := range []string{"values", "files", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(directory, "fixture one.txt"), nil, 0o600))
			probe := `
mapfile() { printf 'unexpected mapfile call' >&2; return 99; }
openai() { if [ "$PICKER_COMPLETION_CASE" = files ]; then return 10; fi; if [ "$PICKER_COMPLETION_CASE" != empty ]; then printf 'fixture one\nfixture two\n'; fi; }
` + script + `
file='caller shell value'
COMP_WORDS=(openai fix); COMP_CWORD=1
__openai_bash_autocomplete
if [[ "$file" != 'caller shell value' ]]; then
  printf 'completion changed caller variable file to <%s>\n' "$file" >&2
  exit 1
fi
for candidate in "${COMPREPLY[@]}"; do printf '<%s>\n' "$candidate"; done
`
			cmd := exec.Command(bash, "-c", probe)
			cmd.Dir = directory
			cmd.Env = append(os.Environ(), "PICKER_COMPLETION_CASE="+scenario)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			want := "<fixture one>\n<fixture two>\n"
			if scenario == "files" {
				want = "<fixture one.txt>\n"
			} else if scenario == "empty" {
				want = ""
			}
			require.Equal(t, want, string(out))
		})
	}
}

func pickerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func runPickerShellPTY(t *testing.T, shell, directory, setup, probe, interaction string) {
	t.Helper()
	binary, err := exec.LookPath(pickerTestShell(shell))
	if err != nil {
		t.Skip(shell + " is not available")
	}
	expect, err := exec.LookPath("expect")
	if err != nil {
		t.Skip("expect is required for real shell line editor tests")
	}
	completion, err := shellCompletions[CompletionStyle(shell)](&cli.Command{}, "openai")
	require.NoError(t, err)
	hook, err := renderPickerCompletion(CompletionStyle(shell), "openai")
	require.NoError(t, err)
	if shell == "zsh" {
		completion = "autoload -Uz compinit; compinit -D\n" + completion
	}
	require.NoError(t, os.WriteFile(filepath.Join(directory, "hook"), []byte(hook), 0o600))
	startup := "PS1='PICKER_TEST> '; PS2='PICKER_MORE> '\n" + completion + "\n" + setup + "\n" + hook + "\n" + probe + "\nprintf '\\nPICKER_SHELL_READY\\n'\n"
	if shell == "fish" {
		startup = "function fish_prompt; printf 'PICKER_TEST> '; end\n" + completion + "\n" + setup + "\n" + hook + "\n" + probe + "\nprintf '\\nPICKER_SHELL_READY\\n'\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(directory, "startup"), []byte(startup), 0o600))
	fixture := `#!/bin/sh
if [ "$1" = __complete ]; then
  for completion_word do :; done
  case "$completion_word" in gen*) printf 'generate\n';; esac
  exit 0
fi
printf 'launch %s %s\n' "$OPENAI_PICKER_SHELL" "$#" >>"$PICKER_TEST_RESULT"
printf '<%s>\n' "$@" >>"$PICKER_TEST_RESULT"
terminal_fds=''
for fd in 0 1 2; do if [ -t "$fd" ]; then terminal_fds="${terminal_fds}1"; else terminal_fds="${terminal_fds}0"; fi; done
printf 'tty:%s\n' "$terminal_fds" >>"$PICKER_TEST_RESULT"
printf '\nPICKER_LAUNCHED\n'
read -r response
printf '\nPICKER_CANCELED\n'
exit 130
`
	require.NoError(t, os.WriteFile(filepath.Join(directory, "openai"), []byte(fixture), 0o700))
	arguments := "--noprofile --norc -i"
	if shell == "zsh" {
		arguments = "-f -i"
	}
	if shell == "fish" {
		arguments = "--no-config --interactive"
	}
	driver := `
set timeout 8
match_max 100000
proc fail {message} { puts stderr $message; exit 1 }
spawn -noecho $env(PICKER_TEST_SHELL) ` + arguments + `
expect_before {
  -exact "\033\1330c" {send -- "\033\133?1;2c"; exp_continue}
  -exact "\033\1336n" {send -- "\033\1331;1R"; exp_continue}
}
expect_after timeout { fail "shell probe timed out" }
send -- "source \"\$PICKER_TEST_STARTUP\"\r"
expect -exact "\r\nPICKER_SHELL_READY\r\n"
expect -exact "PICKER_TEST> "
` + interaction
	require.NoError(t, os.WriteFile(filepath.Join(directory, "driver.exp"), []byte(driver), 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, expect, filepath.Join(directory, "driver.exp"))
	cmd.Dir = directory
	cmd.Env = []string{
		"HOME=" + directory, "PATH=" + directory + ":" + os.Getenv("PATH"), "LC_ALL=C", "TERM=xterm-256color",
		"BASH_SILENCE_DEPRECATION_WARNING=1", "PICKER_TEST_SHELL=" + binary,
		"PICKER_TEST_STARTUP=" + filepath.Join(directory, "startup"),
		"PICKER_TEST_HOOK=" + filepath.Join(directory, "hook"),
		"PICKER_TEST_RESULT=" + filepath.Join(directory, "result"),
		"PICKER_TEST_BINDING=" + filepath.Join(directory, "binding"),
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func pickerTestShell(shell string) string {
	if shell == "bash" && os.Getenv("OPENAI_CLI_TEST_BASH") != "" {
		return os.Getenv("OPENAI_CLI_TEST_BASH")
	}
	return shell
}

func requirePickerBash(t *testing.T, shell string) {
	t.Helper()
	if shell != "bash" {
		return
	}
	binary, err := exec.LookPath(pickerTestShell(shell))
	if err != nil {
		t.Skip("bash is not available")
	}
	if err := exec.Command(binary, "-c", `(( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 3) ))`).Run(); err != nil {
		t.Skip("Tab hook requires Bash 4.3+; set OPENAI_CLI_TEST_BASH to test a supported runtime")
	}
}
