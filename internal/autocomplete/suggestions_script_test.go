package autocomplete

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestSuggestionCompletionOptInAndErrors(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, flags := range [][]string{nil, {"--suggestions=false"}, {"--suggestions"}, {"--suggestions-double-tab"}, {"--suggestions", "--suggestions-double-tab"}} {
			t.Run(shell+strings.Join(flags, " "), func(t *testing.T) {
				var output bytes.Buffer
				root := &cli.Command{Name: "openai", Writer: &output, Commands: []*cli.Command{{
					Name: "@completion", Action: OutputCompletionScript, Flags: []cli.Flag{
						&cli.BoolFlag{Name: "suggestions"}, &cli.BoolFlag{Name: "suggestions-double-tab"},
					},
				}}, ExitErrHandler: func(context.Context, *cli.Command, error) {}}
				args := append([]string{"openai", "@completion", shell}, flags...)
				err := root.Run(t.Context(), args)
				enabled := len(flags) > 0 && flags[0] == "--suggestions"
				if len(flags) == 1 && flags[0] == "--suggestions-double-tab" || shell == "pwsh" && enabled {
					require.Error(t, err)
					require.Empty(t, output.String(), "failed setup must not emit a partial script")
					return
				}
				require.NoError(t, err)
				standard, err := shellCompletions[CompletionStyle(shell)](root, "openai")
				require.NoError(t, err)
				if !enabled {
					require.Equal(t, standard, output.String())
				} else {
					require.True(t, strings.HasPrefix(output.String(), standard+"\n"))
					require.Contains(t, output.String(), "openai_suggestion_next")
					require.NotContains(t, output.String(), "__APPNAME__")
					require.NotContains(t, output.String(), "__DOUBLE_TAB__")
				}
			})
		}
	}
	for _, name := range []string{"", "../openai", "openai.exe", "openai;id", "x'", "$(id)", "1name"} {
		_, err := renderSuggestionCompletion(CompletionStyleZsh, name, false)
		require.Error(t, err)
	}
}

func TestSuggestionNativeSelectionPreservesDraftAndRequiresEnter(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			dir := runSuggestionShellPTY(t, shell, "", "", false, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "draft tail\002\002\002\002\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
set choice [open "$env(SUGGESTION_TEST_DIR)/choice" w]; puts $choice 2; close $choice
send -- "\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
set choice [open "$env(SUGGESTION_TEST_DIR)/choice" w]; puts $choice 3; close $choice
send -- "\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\030\022\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
file delete "$env(SUGGESTION_TEST_DIR)/choice"
send -- "\005\025openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "\030\011\r"
expect -exact "EXECUTED_SELECTED_COMMAND\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "exit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<openai models list>|18\n<openai files list>|17\n<openai responses create --model demo --input 'Hi'>|49\n<draft tail>|6\n", string(buffers))
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			require.NoError(t, err)
			require.Equal(t, "<examples>\n<examples>\n<models>\n<list>\n", string(calls), "insertion must not run a selected command")
			files, err := filepath.Glob(filepath.Join(dir, "openai-suggestions.*"))
			require.NoError(t, err)
			require.Empty(t, files, "private capture files must not persist")
		})
	}
}

// This fixture tests shell acceptance and state. Real UI input is tested separately.
func TestSuggestionNativePickerFailurePreservesEditedDraft(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, failure := range []string{"picker-failure", "picker-invalid"} {
			t.Run(shell+"/"+failure, func(t *testing.T) {
				dir := runSuggestionShellPTY(t, shell, "", "", false, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
set failure [open "$env(SUGGESTION_TEST_DIR)/`+failure+`" w]; close $failure
send -- "draft tail\002\002\002\002\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\005\025exit\r"
expect eof
`)
				buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
				require.NoError(t, err)
				require.Equal(t, "<draft tail>|6\n", string(buffers))
				calls, err := os.ReadFile(filepath.Join(dir, "calls"))
				require.NoError(t, err)
				require.Equal(t, "<examples>\n", string(calls))
			})
		}
	}
}

func TestSuggestionNativeExploreStagesWithoutExecution(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			insert := ""
			if shell == "bash" {
				insert = `send -- "\030\011"`
			}
			dir := runSuggestionShellPTY(t, shell, "", "", false, `
send -- "openai explore\r"
expect -exact "SUGGESTION_TEST> "
`+insert+`
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
if {[file exists "$env(SUGGESTION_TEST_DIR)/calls"]} {error "explore ran selected command"}
send -- "\r"
expect -exact "EXECUTED_SELECTED_COMMAND\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "exit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<openai models list>|18\n", string(buffers))
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			require.NoError(t, err)
			require.Equal(t, "<models>\n<list>\n", string(calls))
		})
	}
}

func TestSuggestionNativeBashCaseMatchingRemainsExact(t *testing.T) {
	dir := runSuggestionShellPTY(t, "bash", "shopt -s nocasematch", "", false, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
set bad [open "$env(SUGGESTION_TEST_DIR)/picker-case" w]; close $bad
send -- "draft\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\025openai EXPLORE\r"
expect -exact "EXECUTED_SELECTED_COMMAND\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "exit\r"
expect eof
`)
	buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
	require.NoError(t, err)
	require.Equal(t, "<draft>|5\n", string(buffers))
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	require.NoError(t, err)
	require.Equal(t, "<examples>\n<EXPLORE>\n", string(calls))
}

func TestSuggestionNativeRealPickerSearchAndCancel(t *testing.T) {
	binary := os.Getenv("OPENAI_CLI_TEST_SUGGESTIONS_BINARY")
	if binary == "" {
		t.Skip("set OPENAI_CLI_TEST_SUGGESTIONS_BINARY to a built candidate for the real picker")
	}
	t.Setenv("SUGGESTION_TEST_REAL_PICKER", binary)
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			dir := runSuggestionShellPTY(t, shell, "", "", false, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "draft tail\002\002\002\002\030\011"
expect -exact "Search"
send -- "files"
after 150
send -- "\r"
expect -re {REAL_PICKER_CLOSED\r?\n}
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
if {[file exists "$env(SUGGESTION_TEST_DIR)/unsafe"]} {error "selection executed"}
send -- "\030\011"
expect -exact "Search"
send -- "\033"
expect -re {REAL_PICKER_CLOSED\r?\n}
send -- "\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\030\022\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\005\025exit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<openai files list>|17\n<openai files list>|17\n<draft tail>|6\n", string(buffers))
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			require.NoError(t, err)
			require.Equal(t, "<examples>\n", string(calls), "picker selection cannot execute a command")
		})
	}
}

func TestSuggestionNativeDoubleTabAndStaleClearing(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			dir := runSuggestionShellPTY(t, shell, "", "", true, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "\t\t\030\007\025openai silent\r"
expect -exact "SUGGESTION_TEST> "
send -- "draft\030\011\030\007\025exit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<openai models list>|18\n<draft>|5\n", string(buffers))
		})
	}
}

func TestSuggestionNativeWrapperPreservesStatusStreamsAndArguments(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			probe := `
export LC_ALL=POSIX
openai fail 'space $value' 'quote"' >"$SUGGESTION_TEST_DIR/stdout" 2>"$SUGGESTION_TEST_DIR/stderr"
printf '%s\n' "$?" >"$SUGGESTION_TEST_DIR/status"
`
			if shell == "fish" {
				probe = `
set -gx LC_ALL POSIX
openai fail 'space $value' 'quote"' >"$SUGGESTION_TEST_DIR/stdout" 2>"$SUGGESTION_TEST_DIR/stderr"
printf '%s\n' "$status" >"$SUGGESTION_TEST_DIR/status"
`
			}
			dir := runSuggestionShellPTY(t, shell, "", probe, false, `send -- "exit\r"; expect eof`)
			for file, expected := range map[string]string{
				"status": "23\n", "stdout": "STDOUT\n", "stderr": "STDERR\n", "locale": "POSIX\n",
				"calls": "<fail>\n<space $value>\n<quote\">\n",
			} {
				value, err := os.ReadFile(filepath.Join(dir, file))
				require.NoError(t, err)
				require.Equal(t, expected, string(value), file)
			}
		})
	}
}

func TestSuggestionNativeSingleTabStillCompletes(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			dir := runSuggestionShellPTY(t, shell, "", "", true, `
send -- "openai mo\t"
after 1200
send -- "\r"
expect -exact "EXECUTED_SELECTED_COMMAND\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "exit\r"
expect eof
`)
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			require.NoError(t, err)
			require.Equal(t, "<models>\n", string(calls))
		})
	}
}

func TestSuggestionNativePreservesBindingsAndDisable(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			setup := `custom_key() { printf 'CUSTOM\n' >>"$SUGGESTION_TEST_DIR/custom"; }
bind -x '"\C-x\C-i": custom_key'
`
			if shell == "zsh" {
				setup = `custom_key() { printf 'CUSTOM\n' >>"$SUGGESTION_TEST_DIR/custom"; }
zle -N custom_key
bindkey '^X^I' custom_key
`
			} else if shell == "fish" {
				setup = `function custom_key; printf 'CUSTOM\n' >>"$SUGGESTION_TEST_DIR/custom"; end
bind \cx\ci custom_key
`
			}
			dir := runSuggestionShellPTY(t, shell, setup, `source "$SUGGESTION_TEST_DIR/hook"`, false, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "draft\030\011\030\007\025openai_command_insertion_disable\r"
expect -exact "SUGGESTION_TEST> "
send -- "draft\030\011\030\007\025exit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<draft>|5\n<draft>|5\n", string(buffers))
			custom, err := os.ReadFile(filepath.Join(dir, "custom"))
			require.NoError(t, err)
			require.Equal(t, "CUSTOM\nCUSTOM\n", string(custom))
		})
	}
}

func TestSuggestionNativeDirectoryChangeClearsCommands(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			dir := runSuggestionShellPTY(t, shell, "", "", false, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "cd ..\r"
expect -exact "SUGGESTION_TEST> "
send -- "cd \"\$SUGGESTION_TEST_DIR\"\r"
expect -exact "SUGGESTION_TEST> "
send -- "draft\030\011\030\007\025exit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<draft>|5\n", string(buffers))
		})
	}
}

func TestSuggestionNativeCapturesTTYErrorsWithRedirectedStdout(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			dir := runSuggestionShellPTY(t, shell, "", "", false, `
send -- "openai recovery >/dev/null\r"
expect -exact "RECOVERY_ERROR\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "\030\011\030\007\025exit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<openai models list>|18\n", string(buffers))
		})
	}
}

func TestSuggestionNativePreservesInheritedDescriptor(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			probe := `printf 'ORIGINAL_FD8\n' >"$SUGGESTION_TEST_DIR/input"
openai readfd 8<"$SUGGESTION_TEST_DIR/input" >"$SUGGESTION_TEST_DIR/output"`
			dir := runSuggestionShellPTY(t, shell, "", probe, false, `send -- "exit\r"; expect eof`)
			out, err := os.ReadFile(filepath.Join(dir, "output"))
			require.NoError(t, err)
			require.Equal(t, "ORIGINAL_FD8\n", string(out))
		})
	}
}

func TestSuggestionNativeRejectsPartialRecordsAndExpiresAfterPipeline(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			dir := runSuggestionShellPTY(t, shell, "", "", false, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "openai silent | cat\r"
expect -exact "SUGGESTION_TEST> "
send -- "draft\030\011\030\007\025openai partial\r"
expect -exact "SUGGESTION_TEST> "
send -- "draft\030\011\030\007\025exit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<draft>|5\n<draft>|5\n", string(buffers))
		})
	}
}

func TestSuggestionNativeCancellationCleansPrivateCapture(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			dir := runSuggestionShellPTY(t, shell, "", "", false, `
send -- "openai wait\r"
expect -exact "WAIT_READY\r\n"
send -- "\003"
expect -exact "SUGGESTION_TEST> "
send -- "printf 'AFTER_CANCEL\\n'\r"
expect -exact "AFTER_CANCEL\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "exit\r"
expect eof
`)
			files, err := filepath.Glob(filepath.Join(dir, "openai-suggestions.*"))
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}

func TestSuggestionNativeUnicodeLiteralAndMultilineDraft(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			setup := "export LC_ALL=en_US.UTF-8\n"
			probe := `seed_draft() { READLINE_LINE=$'  first\n\n'; READLINE_POINT=3; }
bind -x '"\e[99;7~": seed_draft'
`
			if shell == "zsh" {
				probe = `seed_draft() { BUFFER=$'  first\n\n'; CURSOR=3; }
zle -N seed_draft
bindkey '^[[99;7~' seed_draft
`
			} else if shell == "fish" {
				setup = "set -gx LC_ALL en_US.UTF-8\n"
				probe = `function seed_draft
    commandline --replace -- "  first

"
    commandline --cursor 3
end
bind \e'[99;7~' seed_draft
`
			}
			interaction := `
send -- "openai literal\r"
expect -exact "SUGGESTION_TEST> "
send -- "\030\011?\030\007\025openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "SEED_KEYS\030\011\030\022\030\007"
expect -exact "BUFFER_CAPTURED\r\n"
send -- "\030\011\025exit\r"
expect eof
`
			seed := `\033\13399;7~`
			if shell == "fish" {
				probe = strings.ReplaceAll(probe, `bind \e'[99;7~' seed_draft`, `bind \cx\cd seed_draft`)
				seed = `\030\004`
			}
			dir := runSuggestionShellPTY(t, shell, setup, probe, false, strings.ReplaceAll(interaction, "SEED_KEYS", seed))
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Contains(t, string(buffers), "<openai responses create --input '雪 $HOME $(touch unsafe); end'?>|")
			require.Contains(t, string(buffers), "<  first\n\n>|3\n")
			require.NoFileExists(t, filepath.Join(dir, "unsafe"))
		})
	}
}

func TestSuggestionNativeBashUTF8CursorAndRestore(t *testing.T) {
	unavailable := func(message string) {
		t.Helper()
		for _, shell := range strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",") {
			if strings.TrimSpace(shell) == "bash" {
				t.Fatal(message + "; native bash checks are required")
			}
		}
		t.Skip(message)
	}
	bash := pickerTestShell("bash")
	for _, dependency := range []string{bash, "expect", "locale"} {
		if _, err := exec.LookPath(dependency); err != nil {
			unavailable(dependency + " is unavailable")
		}
	}
	if err := exec.Command(bash, "--noprofile", "--norc", "-c", `(( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 3) ))`).Run(); err != nil {
		unavailable("command insertion requires Bash 4.3+")
	}
	locales, err := exec.Command("locale", "-a").Output()
	if err != nil {
		unavailable("cannot list installed locales: " + err.Error())
	}
	var utf8Locale string
	for _, name := range strings.Fields(string(locales)) {
		normalized := strings.ToLower(strings.ReplaceAll(name, "-", ""))
		if normalized == "en_us.utf8" || normalized == "c.utf8" {
			utf8Locale = name
			break
		}
	}
	if utf8Locale == "" {
		unavailable("native UTF-8 cursor checks require an installed UTF-8 locale")
	}
	t.Setenv("OPENAI_CLI_TEST_SUGGESTIONS_LOCALE", utf8Locale)
	const selected = "openai responses create --input '雪 café 🦊'"
	const draft = "draft 雪 café tail"
	for _, source := range []string{"recent", "explore"} {
		t.Run(source, func(t *testing.T) {
			setup := `suggestion_test_character=雪
printf '%s|%s\n' "$LC_ALL" "${#suggestion_test_character}" >"$SUGGESTION_TEST_DIR/locale.before"
printf '%s\n' "openai responses create --input '雪 café 🦊'" >"$SUGGESTION_TEST_DIR/selection"
`
			command := "openai unicode"
			if source == "explore" {
				command = "openai explore"
			}
			dir := runSuggestionShellPTY(t, "bash", setup, "", false, `
send -- "`+command+`\r"
expect -exact "SUGGESTION_TEST> "
send -- "`+draft+`\002\002\002\002\002\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\030\022\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "!\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\030\011?\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\005\025printf '%s' \"\$LC_ALL\" >\"\$SUGGESTION_TEST_DIR/locale.after\"\r"
expect -exact "SUGGESTION_TEST> "
send -- "exit\r"
expect eof
`)
			read := func(name string) string {
				data, err := os.ReadFile(filepath.Join(dir, name))
				require.NoError(t, err)
				return string(data)
			}
			locale := strings.TrimSpace(read("locale.before"))
			require.True(t, strings.HasSuffix(locale, "|1"), "native Bash must count a UTF-8 character as one character")
			require.Equal(t, strings.TrimSuffix(locale, "|1"), read("locale.after"), "insertion must preserve the caller's locale")
			buffers := strings.Split(strings.TrimSuffix(read("buffers"), "\n"), "\n")
			require.Len(t, buffers, 5)
			require.True(t, strings.HasPrefix(buffers[0], "<"+draft+">|"), buffers[0])
			require.True(t, strings.HasPrefix(buffers[1], "<"+selected+">|"), buffers[1])
			require.Equal(t, buffers[0], buffers[2], "restore must preserve Unicode draft text and its native cursor offset")
			require.True(t, strings.HasPrefix(buffers[3], "<draft 雪 café! tail>|"), buffers[3])
			require.True(t, strings.HasPrefix(buffers[4], "<"+selected+"?>|"), "typing must append after the selected command: %s", buffers[4])
			if source == "recent" {
				require.Equal(t, "<unicode>\n", read("calls"), "selection must not submit a command")
			} else {
				require.NoFileExists(t, filepath.Join(dir, "calls"), "explore selection must not submit a command")
			}
		})
	}
}

func TestSuggestionNativePickerBridgeCapturesPrintedCommand(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			picker, err := renderPickerCompletion(CompletionStyle(shell), "openai")
			require.NoError(t, err)
			dir := runSuggestionShellPTY(t, shell, picker, "", false, `
send -- "openai images generate\t"
expect -re {PICKER_BRIDGE_READY\r?\n}
send -- "cancel\n"
expect -re {PICKER_BRIDGE_DONE\r?\n}
expect -exact "SUGGESTION_TEST> "
send -- "\030\011\030\007\025exit\r"
expect eof
`)
			buffers, err := os.ReadFile(filepath.Join(dir, "buffers"))
			require.NoError(t, err)
			require.Equal(t, "<openai models list>|18\n", string(buffers))
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			require.NoError(t, err)
			require.Equal(t, "<images>\n<generate>\n", string(calls), "insertion must not execute the printed command")
		})
	}
}

func TestSuggestionBashPreservesPromptCallbacks(t *testing.T) {
	for _, declaration := range []string{
		`PROMPT_COMMAND='printf original >>"$SUGGESTION_TEST_DIR/prompt"'`,
		`PROMPT_COMMAND=('printf first >>"$SUGGESTION_TEST_DIR/prompt"' 'printf second >>"$SUGGESTION_TEST_DIR/prompt"')`,
		`readonly PROMPT_COMMAND='printf readonly >>"$SUGGESTION_TEST_DIR/prompt"'`,
		`readonly -a PROMPT_COMMAND=([3]='printf readonly >>"$SUGGESTION_TEST_DIR/prompt"')`,
	} {
		t.Run(declaration, func(t *testing.T) {
			setup := declaration + "\ndeclare -p PROMPT_COMMAND >\"$SUGGESTION_TEST_DIR/before\"\n"
			probe := "openai_command_insertion_disable\ndeclare -p PROMPT_COMMAND >\"$SUGGESTION_TEST_DIR/after\""
			dir := runSuggestionShellPTY(t, "bash", setup, probe, false, `send -- "exit\r"; expect eof`)
			before, err := os.ReadFile(filepath.Join(dir, "before"))
			require.NoError(t, err)
			after, err := os.ReadFile(filepath.Join(dir, "after"))
			require.NoError(t, err)
			require.Equal(t, string(before), string(after))
		})
	}
}

func TestSuggestionNativeBashPromptArrayFreshness(t *testing.T) {
	for _, tc := range []struct {
		name, declaration string
		first, second     bool
	}{
		{"dense", `PROMPT_COMMAND=(record_first record_second)`, true, true},
		{"sparse", `PROMPT_COMMAND=([0]=record_first [4]=record_second)`, true, true},
		{"without zero", `PROMPT_COMMAND=([4]=record_second)`, false, true},
		{"empty", `PROMPT_COMMAND=()`, false, false},
	} {
		for _, legacy := range []bool{false, true} {
			t.Run(tc.name+"/legacy="+strconv.FormatBool(legacy), func(t *testing.T) {
				setup := `record_first() { printf 'first:%s\n' "$?" >>"$SUGGESTION_TEST_DIR/prompt"; }
record_second() { printf 'second:%s\n' "$?" >>"$SUGGESTION_TEST_DIR/prompt"; }
` + tc.declaration + `
declare -p PROMPT_COMMAND >"$SUGGESTION_TEST_DIR/before"
if (( BASH_VERSINFO[0] > 5 || (BASH_VERSINFO[0] == 5 && BASH_VERSINFO[1] >= 1) )); then
  printf modern >"$SUGGESTION_TEST_DIR/prompt-array-support"
fi
`
				if legacy {
					// An older sourced hook saves values and appends its callback, without index metadata.
					setup += `__openai_suggestions_prompt_kind=array
declare -a __openai_suggestions_prompt_previous=("${PROMPT_COMMAND[@]}")
PROMPT_COMMAND+=(__openai_suggestions_prompt)
__openai_suggestions_prompt_installed=$(declare -p PROMPT_COMMAND)
`
				}
				dir := runSuggestionShellPTY(t, "bash", setup, `source "$SUGGESTION_TEST_DIR/hook"`, false, `
send -- "openai examples\r"
expect -exact "EXAMPLES_FINISHED\r\n"
expect -exact "SUGGESTION_TEST> "
send -- "\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\025false\r"
expect -exact "SUGGESTION_TEST> "
send -- "draft\030\011\030\007"
expect -re {BUFFER_CAPTURED\r?\n}
send -- "\025openai_command_insertion_disable; declare -p PROMPT_COMMAND >\"\$SUGGESTION_TEST_DIR/after\"\r"
expect -exact "SUGGESTION_TEST> "
send -- "false\r"
expect -exact "SUGGESTION_TEST> "
send -- "exit\r"
expect eof
`)
				read := func(name string) string {
					data, err := os.ReadFile(filepath.Join(dir, name))
					require.NoError(t, err)
					return string(data)
				}
				require.Equal(t, "<openai models list>|18\n<draft>|5\n", read("buffers"))
				require.Equal(t, "<examples>\n", read("calls"), "selection must not submit a command")
				require.Equal(t, read("before"), read("after"), "disable must restore sparse indices and empty arrays")
				_, modern := os.Stat(filepath.Join(dir, "prompt-array-support"))
				var expected strings.Builder
				for _, status := range []string{"0", "0", "1", "0", "1"} {
					if tc.first {
						expected.WriteString("first:" + status + "\n")
					}
					if tc.second && modern == nil {
						expected.WriteString("second:" + status + "\n")
					}
				}
				if expected.Len() == 0 {
					require.NoFileExists(t, filepath.Join(dir, "prompt"))
				} else {
					require.Equal(t, expected.String(), read("prompt"), "existing prompt callbacks keep their native order and status")
				}
			})
		}
	}
}

// Disabling insertion must preserve replacements even when Bash ignores case.
func TestSuggestionNativeBashDisablePreservesCaseOnlyReplacements(t *testing.T) {
	for _, arrayPrompt := range []bool{false, true} {
		t.Run("array-prompt="+strconv.FormatBool(arrayPrompt), func(t *testing.T) {
			setup := `PROMPT_COMMAND='builtin :'
`
			replacement := `PROMPT_COMMAND=${PROMPT_COMMAND/__openai_suggestions_prompt/__OPENAI_suggestions_prompt}
`
			if arrayPrompt {
				setup = "PROMPT_COMMAND=('builtin :')\n"
				replacement = `PROMPT_COMMAND=("${PROMPT_COMMAND[@]/__openai_suggestions_prompt/__OPENAI_suggestions_prompt}")
`
			}
			probe := `builtin shopt -s nocasematch
__OPENAI_suggestions_prompt() { builtin :; }
OPENAI_suggestion_next() { builtin printf called >"$SUGGESTION_TEST_DIR/replaced-called"; }
for keymap in emacs-standard vi-insert; do
    builtin bind -m "$keymap" -x '"\C-x\C-i": OPENAI_suggestion_next'
    builtin bind -m "$keymap" -x '"\C-i\C-i": OPENAI_suggestion_next'
    builtin bind -m "$keymap" -X >"$SUGGESTION_TEST_DIR/$keymap.before"
done
` + replacement + `builtin declare -p PROMPT_COMMAND >"$SUGGESTION_TEST_DIR/prompt.before"
builtin test "$(builtin declare -p PROMPT_COMMAND)" != "$__openai_suggestions_prompt_installed"
builtin printf '%s' "$?" >"$SUGGESTION_TEST_DIR/exact-difference.status"
[[ $(builtin declare -p PROMPT_COMMAND) == "$__openai_suggestions_prompt_installed" ]]
builtin printf '%s' "$?" >"$SUGGESTION_TEST_DIR/case-insensitive-match.status"
openai_command_insertion_disable
builtin declare -p PROMPT_COMMAND >"$SUGGESTION_TEST_DIR/prompt.after"
for keymap in emacs-standard vi-insert; do
    builtin bind -m "$keymap" -X >"$SUGGESTION_TEST_DIR/$keymap.after"
done
builtin shopt -q nocasematch
builtin printf '%s' "$?" >"$SUGGESTION_TEST_DIR/nocasematch.status"
`
			dir := runSuggestionShellPTY(t, "bash", setup, probe, true, `send -- "exit\r"; expect eof`)
			read := func(name string) string {
				data, err := os.ReadFile(filepath.Join(dir, name))
				require.NoError(t, err)
				return string(data)
			}
			require.Equal(t, "0", read("exact-difference.status"))
			require.Equal(t, "0", read("case-insensitive-match.status"))
			require.Equal(t, read("prompt.before"), read("prompt.after"))
			for _, keymap := range []string{"emacs-standard", "vi-insert"} {
				require.Contains(t, read(keymap+".before"), "OPENAI_suggestion_next")
				require.Equal(t, read(keymap+".before"), read(keymap+".after"))
			}
			require.Equal(t, "0", read("nocasematch.status"))
			require.NoFileExists(t, filepath.Join(dir, "calls"))
			require.NoFileExists(t, filepath.Join(dir, "replaced-called"))
		})
	}
}

func runSuggestionShellPTY(t *testing.T, shell, setup, probe string, doubleTab bool, interaction string) string {
	t.Helper()
	required := false
	for _, name := range strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",") {
		if strings.TrimSpace(name) == shell {
			required = true
		}
	}
	unavailable := func(message string) {
		t.Helper()
		if required {
			t.Fatal(message + "; native " + shell + " checks are required")
		}
		t.Skip(message)
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
		unavailable("expect is required for native line-editor tests")
	}
	dir := t.TempDir()
	script, err := renderSuggestionCompletion(CompletionStyle(shell), "openai", doubleTab)
	require.NoError(t, err)
	completion, err := shellCompletions[CompletionStyle(shell)](&cli.Command{}, "openai")
	require.NoError(t, err)
	startup := "PS1='SUGGESTION_TEST> '; PS2='MORE> '\n"
	capture := `
capture_buffer() { printf '<%s>|%s\n' "$READLINE_LINE" "$READLINE_POINT" >>"$SUGGESTION_TEST_DIR/buffers"; printf '\nBUFFER_CAPTURED\n'; }
bind -x '"\C-x\C-g": capture_buffer'
bind -x '"\C-x\C-r": openai_suggestion_restore'
`
	arguments := "--noprofile --norc -i"
	if shell == "zsh" {
		arguments = "-df -i"
		startup += "autoload -Uz compinit; compinit -D\nbindkey -e\n"
		capture = `
capture_buffer() { printf '<%s>|%s\n' "$BUFFER" "$CURSOR" >>"$SUGGESTION_TEST_DIR/buffers"; printf '\nBUFFER_CAPTURED\n'; }
zle -N capture_buffer
bindkey '^X^G' capture_buffer
bindkey '^X^R' openai_suggestion_restore
`
	} else if shell == "fish" {
		arguments = "--no-config --interactive"
		startup = "function fish_prompt; printf 'SUGGESTION_TEST> '; end\nfish_default_key_bindings\n"
		capture = `
function capture_buffer
    set -l size (commandline --current-buffer | command wc -c)
    printf '<' >>"$SUGGESTION_TEST_DIR/buffers"
    commandline --current-buffer | command head -c (math $size - 1) >>"$SUGGESTION_TEST_DIR/buffers"
    printf '>|%s\n' (commandline --cursor) >>"$SUGGESTION_TEST_DIR/buffers"
    printf '\nBUFFER_CAPTURED\n'
end
bind \cx\cg capture_buffer
bind \cx\cr openai_suggestion_restore
`
	}
	startup += completion + "\n" + setup + "\n" + script + "\n" + capture + "\n" + probe + "\nprintf '\\nSUGGESTION_SHELL_READY\\n'\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "startup"), []byte(startup), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hook"), []byte(script), 0o600))
	fixture := `#!/bin/sh
if [ "$1" = explore ]; then
  if [ -n "$SUGGESTION_TEST_REAL_PICKER" ]; then
    "$SUGGESTION_TEST_REAL_PICKER" "$@"
    picker_status=$?
    printf '\nREAL_PICKER_CLOSED\n' >&2
    exit "$picker_status"
  fi
  if [ -f "$SUGGESTION_TEST_DIR/picker-case" ]; then
    printf 'OPENAI MODELS LIST\n'
    exit 0
  fi
  if [ -f "$SUGGESTION_TEST_DIR/picker-failure" ]; then
    printf 'openai models list\n'
    exit 130
  fi
  if [ -f "$SUGGESTION_TEST_DIR/picker-invalid" ]; then
    printf 'not a captured command\n'
    exit 0
  fi
  printf 'selected\n' >>"$SUGGESTION_TEST_DIR/picker-calls"
  if [ "$2" = --suggestions ]; then
    choice=1
    if [ -f "$SUGGESTION_TEST_DIR/choice" ]; then read -r choice <"$SUGGESTION_TEST_DIR/choice"; fi
    sed -n "${choice}p"
  elif [ -f "$SUGGESTION_TEST_DIR/selection" ]; then
    cat "$SUGGESTION_TEST_DIR/selection"
  else
    printf 'openai models list\n'
  fi
  exit 0
fi
if [ "$1" = __complete ]; then
  for completion_word do :; done
  case "$completion_word" in gen*) printf 'generate\n';; mo*) printf 'models\n';; esac
  exit 0
fi
printf '<%s>\n' "$@" >>"$SUGGESTION_TEST_DIR/calls"
printf '%s\n' "$LC_ALL" >"$SUGGESTION_TEST_DIR/locale"
case "$1" in
images)
  printf 'PICKER_BRIDGE_READY\n'
  read -r ignored
  printf 'openai models list\n'
  if [ "$OPENAI_CLI_COMMANDS_FD" = 9 ]; then printf 'openai models list\n' >&9; fi
  printf 'PICKER_BRIDGE_DONE\n'
  exit 130
  ;;
examples)
  printf "openai models list\nopenai files list\nopenai responses create --model demo --input 'Hi'\n"
  if [ "$OPENAI_CLI_COMMANDS_FD" = 9 ]; then
    printf "openai models list\nopenai files list\nopenai responses create --model demo --input 'Hi'\n" >&9
  fi
  printf 'EXAMPLES_FINISHED\n'
  ;;
silent) ;;
partial) if [ "$OPENAI_CLI_COMMANDS_FD" = 9 ]; then printf 'openai models li' >&9; fi ;;
literal) if [ "$OPENAI_CLI_COMMANDS_FD" = 9 ]; then printf '%s\n' "openai responses create --input '雪 \$HOME \$(touch unsafe); end'" >&9; fi ;;
unicode) if [ "$OPENAI_CLI_COMMANDS_FD" = 9 ]; then cat "$SUGGESTION_TEST_DIR/selection" >&9; fi ;;
readfd) cat <&8 ;;
wait) printf 'WAIT_READY\n'; read -r ignored ;;
recovery)
  printf 'RECOVERY_ERROR\n' >&2
  if [ "$OPENAI_CLI_COMMANDS_FD" = 9 ] && [ -t 2 ]; then printf 'openai models list\n' >&9; fi
  exit 17
  ;;
fail) printf 'STDOUT\n'; printf 'STDERR\n' >&2; exit 23 ;;
*) printf 'EXECUTED_SELECTED_COMMAND\n' ;;
esac
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "openai"), []byte(fixture), 0o700))
	driver := `
set timeout 8
match_max 100000
spawn -noecho $env(SUGGESTION_TEST_SHELL) ` + arguments + `
stty rows 30 columns 100 < $spawn_out(slave,name)
expect_before {
  -exact "\033\1330c" {send -- "\033\133?1;2c"; exp_continue}
  -exact "\033\1336n" {send -- "\033\1331;1R"; exp_continue}
}
expect_after timeout { puts stderr "shell insertion probe timed out"; exit 1 }
send -- "source \"\$SUGGESTION_TEST_DIR/startup\"\r"
expect -exact "\r\nSUGGESTION_SHELL_READY\r\n"
expect -exact "SUGGESTION_TEST> "
` + interaction
	require.NoError(t, os.WriteFile(filepath.Join(dir, "driver.exp"), []byte(driver), 0o600))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, expect, filepath.Join(dir, "driver.exp"))
	cmd.Dir = dir
	locale := os.Getenv("OPENAI_CLI_TEST_SUGGESTIONS_LOCALE")
	if locale == "" {
		locale = "C"
	}
	cmd.Env = []string{
		"HOME=" + dir, "PATH=" + dir + ":" + os.Getenv("PATH"), "LC_ALL=" + locale, "TERM=xterm-256color",
		"TERMINFO=" + os.Getenv("TERMINFO"),
		"TMPDIR=" + dir, "SUGGESTION_TEST_DIR=" + dir, "SUGGESTION_TEST_SHELL=" + binary,
		"BASH_SILENCE_DEPRECATION_WARNING=1",
		"SUGGESTION_TEST_REAL_PICKER=" + os.Getenv("SUGGESTION_TEST_REAL_PICKER"),
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "transcript"), out, 0o600))
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Native %s transcript: %s", shell, out)
		}
	})
	require.NoError(t, err, string(out))
	return dir
}
