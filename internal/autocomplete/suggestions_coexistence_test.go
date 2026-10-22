package autocomplete

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Both integrations are opt-in. Their disable functions and command ownership
// must remain separate, regardless of the order in which users enable them.
func TestSuggestionNativeExecutableAdviceCoexistsWithInsertion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, adviceFirst := range []bool{false, true} {
			t.Run(shell+"/advice-first="+strconv.FormatBool(adviceFirst), func(t *testing.T) {
				if shell == "fish" {
					name := "fish"
					if override := os.Getenv("OPENAI_CLI_TEST_FISH"); override != "" {
						name = override
					}
					unavailable := func(message string) {
						t.Helper()
						if strings.Contains(","+os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS")+",", ",fish,") {
							t.Fatal(message)
						}
						t.Skip(message)
					}
					binary, err := exec.LookPath(name)
					if err != nil {
						unavailable("fish is unavailable")
					}
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					version, err := exec.CommandContext(ctx, binary, "--version").Output()
					require.NoError(t, err)
					fields := strings.Fields(string(version))
					require.NotEmpty(t, fields)
					major, err := strconv.Atoi(strings.Split(fields[len(fields)-1], ".")[0])
					require.NoError(t, err)
					if major < 4 {
						unavailable("executable advice requires fish 4 or newer")
					}
				}
				advice, err := RenderShellSuggestions(CompletionStyle(shell), "openai")
				require.NoError(t, err)
				setup, probe := "", ""
				if adviceFirst {
					setup = advice
				} else {
					probe = advice + "\n"
				}
				probe += `opneai --api-key=synthetic-private >"$SUGGESTION_TEST_DIR/advice.out" 2>"$SUGGESTION_TEST_DIR/advice.err"
printf '%s' "$?" >"$SUGGESTION_TEST_DIR/advice.status"
openai_command_insertion_disable
opneai >"$SUGGESTION_TEST_DIR/after-disable.out" 2>"$SUGGESTION_TEST_DIR/after-disable.err"
printf '%s' "$?" >"$SUGGESTION_TEST_DIR/after-disable.status"
source "$SUGGESTION_TEST_DIR/hook"
openai() { printf replaced >"$SUGGESTION_TEST_DIR/replaced-called"; }
opneai >"$SUGGESTION_TEST_DIR/replaced.out" 2>"$SUGGESTION_TEST_DIR/replaced.err"
printf '%s' "$?" >"$SUGGESTION_TEST_DIR/replaced.status"
`
				if shell == "fish" {
					probe = strings.ReplaceAll(probe, `"$?"`, `"$status"`)
					probe = strings.ReplaceAll(probe, `openai() { printf replaced >"$SUGGESTION_TEST_DIR/replaced-called"; }`, "function openai\n    printf replaced >\"$SUGGESTION_TEST_DIR/replaced-called\"\nend")
					// Fish performs missing-command lookup before applying that
					// command's redirections. Capture the known function instead,
					// while retaining a real failed lookup and its public status.
					probe = "\n" + probe
					probe = strings.ReplaceAll(probe, "\nopneai ", "\nsuggestion_probe_missing ")
					probe = "function suggestion_probe_missing\n    opneai $argv\nend\n" + probe
				}
				dir := runSuggestionShellPTY(t, shell, setup, probe, false, "send -- \"exit\\r\"\nexpect eof\n")
				read := func(name string) string {
					data, err := os.ReadFile(filepath.Join(dir, name))
					require.NoError(t, err)
					return string(data)
				}
				for _, stage := range []string{"advice", "after-disable"} {
					require.Empty(t, read(stage+".out"))
					require.Equal(t, "127", read(stage+".status"))
					require.Contains(t, read(stage+".err"), "Command not found. Did you mean: openai?")
					require.NotContains(t, read(stage+".err"), "synthetic-private")
				}
				require.Equal(t, "127", read("replaced.status"))
				require.NotContains(t, read("replaced.err"), "Did you mean: openai?")
				require.NoFileExists(t, filepath.Join(dir, "calls"), "advice must never execute the CLI")
				require.NoFileExists(t, filepath.Join(dir, "replaced-called"), "ownership checks must not execute user functions")
			})
		}
	}
}

// nocasematch must not make a replaced wrapper appear to be owned by insertion.
func TestSuggestionNativeBashAdviceRejectsCaseOnlyWrapperReplacement(t *testing.T) {
	advice, err := RenderShellSuggestions(CompletionStyleBash, "openai")
	require.NoError(t, err)
	advicePath := filepath.Join(t.TempDir(), "advice.bash")
	require.NoError(t, os.WriteFile(advicePath, []byte(advice), 0o600))
	sourceAdvice := "source '" + strings.ReplaceAll(advicePath, "'", "'\\''") + "'"
	for _, alreadyEnabled := range []bool{false, true} {
		t.Run("already-enabled="+strconv.FormatBool(alreadyEnabled), func(t *testing.T) {
			probe := ""
			if alreadyEnabled {
				probe = sourceAdvice + "\n"
			}
			probe += `builtin shopt -s nocasematch
__OPENAI_suggestions_run() { builtin printf called >"$SUGGESTION_TEST_DIR/replaced-called"; }
replacement=${__openai_suggestions_wrapper_body/__openai_suggestions_run/__OPENAI_suggestions_run}
builtin eval "$replacement"
builtin declare -f openai >"$SUGGESTION_TEST_DIR/replaced.before"
builtin test "$(builtin declare -f openai)" != "$__openai_suggestions_wrapper_body"
builtin printf '%s' "$?" >"$SUGGESTION_TEST_DIR/exact-difference.status"
[[ $(builtin declare -f openai) == "$__openai_suggestions_wrapper_body" ]]
builtin printf '%s' "$?" >"$SUGGESTION_TEST_DIR/case-insensitive-match.status"
`
			if alreadyEnabled {
				probe += `opneai >"$SUGGESTION_TEST_DIR/runtime.out" 2>"$SUGGESTION_TEST_DIR/runtime.err"
builtin printf '%s' "$?" >"$SUGGESTION_TEST_DIR/runtime.status"
`
			}
			probe += sourceAdvice + ` >"$SUGGESTION_TEST_DIR/source.out" 2>"$SUGGESTION_TEST_DIR/source.err"
builtin printf '%s' "$?" >"$SUGGESTION_TEST_DIR/source.status"
builtin declare -f openai >"$SUGGESTION_TEST_DIR/replaced.after"
builtin shopt -q nocasematch
builtin printf '%s' "$?" >"$SUGGESTION_TEST_DIR/nocasematch.status"
opneai >"$SUGGESTION_TEST_DIR/after-source.out" 2>"$SUGGESTION_TEST_DIR/after-source.err"
builtin printf '%s' "$?" >"$SUGGESTION_TEST_DIR/after-source.status"
`
			dir := runSuggestionShellPTY(t, "bash", "", probe, false, "send -- \"exit\\r\"\nexpect eof\n")
			read := func(name string) string {
				data, err := os.ReadFile(filepath.Join(dir, name))
				require.NoError(t, err)
				return string(data)
			}
			require.Equal(t, "0", read("exact-difference.status"))
			require.Equal(t, "0", read("case-insensitive-match.status"))
			require.Equal(t, "2", read("source.status"))
			require.Empty(t, read("source.out"))
			require.Contains(t, read("source.err"), "No suggestion hook was installed.")
			require.Equal(t, read("replaced.before"), read("replaced.after"))
			require.Equal(t, "0", read("nocasematch.status"), "user shell option changed")
			stages := []string{"after-source"}
			if alreadyEnabled {
				stages = append(stages, "runtime")
			}
			for _, stage := range stages {
				require.Equal(t, "127", read(stage+".status"))
				require.Empty(t, read(stage+".out"))
				require.NotContains(t, read(stage+".err"), "Did you mean: openai?")
			}
			require.NoFileExists(t, filepath.Join(dir, "calls"), "advice must never execute the CLI")
			require.NoFileExists(t, filepath.Join(dir, "replaced-called"), "ownership checks must not execute user functions")
		})
	}
}
