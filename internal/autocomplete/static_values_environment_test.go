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
	"github.com/urfave/cli/v3"
)

func TestStaticCompletionCapabilityEnvironmentIsScoped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("These callback probes require POSIX physical paths; native Windows completion needs separate validation.")
	}
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh} {
		t.Run(string(style), func(t *testing.T) {
			shell, err := exec.LookPath(pickerTestShell(string(style)))
			if err != nil {
				t.Skipf("native %s is unavailable", style)
			}
			adapter, err := shellCompletions[style](&cli.Command{}, "openai")
			require.NoError(t, err)
			for _, previous := range []struct{ name, present, value string }{
				{"unset", "", ""}, {"empty", "x", ""}, {"sentinel", "x", "previous-adapter\n"},
			} {
				for _, backendStatus := range []string{"0", "1"} {
					for _, scenario := range []string{"separated", "assignment", "closed empty", "failed pwd"} {
						if style != CompletionStyleBash && scenario == "failed pwd" {
							continue
						}
						t.Run(previous.name+"/status="+backendStatus+"/"+scenario, func(t *testing.T) {
							directory := t.TempDir()
							physicalDirectory, err := filepath.EvalSymlinks(directory)
							require.NoError(t, err)
							// The backend records call-local values without generating candidates.
							probe := `
completion_static_snapshot() {
  printf '%s\0' "${OPENAI_CLI_COMPLETION_STATIC_VALUES+x}" "${OPENAI_CLI_COMPLETION_STATIC_VALUES-}" \
    "${OPENAI_CLI_COMPLETION_BASH_VALUE_PREFIX+x}" "${OPENAI_CLI_COMPLETION_BASH_VALUE_PREFIX-}" \
    "${OPENAI_CLI_COMPLETION_BASH_CWD+x}" "${OPENAI_CLI_COMPLETION_BASH_CWD-}"
}
openai() {
  completion_static_snapshot > observed
  return "$COMPLETION_PROBE_STATUS"
}
compdef() { :; }
_describe() { :; }
completion_static_snapshot > before
` + adapter + "\n"
							staticValue, valuePrefix, observedDirectory := "1", "", physicalDirectory
							if scenario == "closed empty" || scenario == "failed pwd" {
								staticValue = "0"
							}
							if style == CompletionStyleBash {
								switch scenario {
								case "assignment":
									valuePrefix = "--format="
									probe += "COMP_LINE='openai --format=y'\nCOMP_WORDS=(openai --format=y)\n"
								case "closed empty":
									probe += "COMP_LINE=\"openai --format ''\"\nCOMP_WORDS=(openai --format \"''\")\n"
								default:
									probe += "COMP_LINE='openai --format y'\nCOMP_WORDS=(openai --format y)\n"
								}
								if scenario == "failed pwd" {
									observedDirectory = ""
									probe += "pwd() { return 1; }\n"
								}
								probe += `COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
COMP_POINT=${#COMP_LINE}
__openai_bash_autocomplete openai "${COMP_WORDS[COMP_CWORD]}" ""
`
							} else {
								switch scenario {
								case "assignment":
									probe += "words=(openai --format=y)\n"
								case "closed empty":
									probe += "words=(openai --format \"''\")\n"
								default:
									probe += "words=(openai --format y)\n"
								}
								probe += "CURRENT=${#words[@]}\n__openai_zsh_autocomplete\n"
							}
							probe += "completion_static_snapshot > after\n"
							args := []string{"--noprofile", "--norc", "-c", probe}
							if style == CompletionStyleZsh {
								args = []string{"-f", "-c", probe}
							}
							ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
							defer cancel()
							command := exec.CommandContext(ctx, shell, args...)
							command.Dir = directory
							command.WaitDelay = 2 * time.Second
							command.Env = []string{"HOME=" + directory, "PATH=/usr/bin:/bin", "LC_ALL=C", "COMPLETION_PROBE_STATUS=" + backendStatus}
							if previous.present != "" {
								for _, name := range []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES", "OPENAI_CLI_COMPLETION_BASH_VALUE_PREFIX", "OPENAI_CLI_COMPLETION_BASH_CWD"} {
									command.Env = append(command.Env, name+"="+previous.value)
								}
							}
							var stdout, stderr bytes.Buffer
							command.Stdout, command.Stderr = &stdout, &stderr
							require.NoError(t, command.Run(), stderr.String())
							require.NoError(t, ctx.Err())
							require.Empty(t, stdout.String())
							require.Empty(t, stderr.String())
							prior := strings.Repeat(previous.present+"\x00"+previous.value+"\x00", 3)
							observed := "x\x00" + staticValue + "\x00"
							if style == CompletionStyleBash {
								observed += "x\x00" + valuePrefix + "\x00x\x00" + observedDirectory + "\x00"
							} else {
								observed += strings.Repeat(previous.present+"\x00"+previous.value+"\x00", 2)
							}
							for name, want := range map[string]string{"before": prior, "after": prior, "observed": observed} {
								got, err := os.ReadFile(filepath.Join(directory, name))
								require.NoError(t, err)
								require.Equal(t, want, string(got), "%s snapshot", name)
							}
						})
					}
				}
			}
		})
	}
}
