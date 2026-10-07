package autocomplete

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestCompletionCapabilityEnvironmentIsScoped(t *testing.T) {
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish, CompletionStylePowershell} {
		t.Run(string(style), func(t *testing.T) {
			shell, err := exec.LookPath(string(style))
			if err != nil {
				t.Skipf("%s is unavailable", style)
			}
			script, err := shellCompletions[style](&cli.Command{}, "openai")
			require.NoError(t, err)
			for _, previous := range []string{"unset", "", "other-adapter"} {
				for _, failure := range []string{"0", "1"} {
					t.Run(previous+"/failure="+failure, func(t *testing.T) {
						dir := t.TempDir()
						var probe string
						var args []string
						switch style {
						case CompletionStyleBash, CompletionStyleZsh:
							probe = `
openai() {
  printf '%s\n%s\n' "$OPENAI_CLI_COMPLETION_FILE_VALUES" "$COMPLETION_STYLE" > observed
  if [ "$COMPLETION_PROBE_FAILURE" = 1 ]; then return 1; fi
  return 11
}
compdef() { :; }
` + script
							if style == CompletionStyleBash {
								probe += "\nCOMP_WORDS=(openai --image assets/lo)\nCOMP_CWORD=2\n__openai_bash_autocomplete\n"
							} else {
								probe += "\nwords=(openai --image assets/lo)\nCURRENT=3\n__openai_zsh_autocomplete\n"
							}
							probe += `printf '%s\n%s\n' "${OPENAI_CLI_COMPLETION_FILE_VALUES-unset}" "${COMPLETION_STYLE-unset}"`
							args = []string{"-c", probe}
							if style == CompletionStyleZsh {
								args = append([]string{"-f"}, args...)
							}
						case CompletionStyleFish:
							if runtime.GOOS == "windows" {
								t.Skip("fish probe requires a POSIX executable fixture")
							}
							fixture := "#!/bin/sh\nprintf '%s\\n%s\\n' \"$OPENAI_CLI_COMPLETION_FILE_VALUES\" \"$COMPLETION_STYLE\" > observed\nif [ \"$COMPLETION_PROBE_FAILURE\" = 1 ]; then exit 1; fi\nexit 11\n"
							require.NoError(t, os.WriteFile(filepath.Join(dir, "openai"), []byte(fixture), 0700))
							probe = script + `
complete -C 'openai --image assets/lo' >/dev/null
if set -q OPENAI_CLI_COMPLETION_FILE_VALUES
  printf '%s\n' "$OPENAI_CLI_COMPLETION_FILE_VALUES"
else
  printf 'unset\n'
end
if set -q COMPLETION_STYLE
  printf '%s\n' "$COMPLETION_STYLE"
else
  printf 'unset\n'
end
`
							args = []string{"--no-config", "-c", probe}
						case CompletionStylePowershell:
							probe = `
function openai {
  $global:observed = @($env:OPENAI_CLI_COMPLETION_FILE_VALUES, $env:COMPLETION_STYLE)
  if ($env:COMPLETION_PROBE_FAILURE -eq '1') { throw 'synthetic backend failure' }
  $global:LASTEXITCODE = 11
}
$before = @($env:OPENAI_CLI_COMPLETION_FILE_VALUES, $env:COMPLETION_STYLE)
` + script + `
$line = 'openai --image assets/lo'
TabExpansion2 $line $line.Length | Out-Null
[pscustomobject]@{ before=$before; after=@($env:OPENAI_CLI_COMPLETION_FILE_VALUES, $env:COMPLETION_STYLE); observed=$global:observed } | ConvertTo-Json -Compress
`
							probePath := filepath.Join(dir, "probe.ps1")
							require.NoError(t, os.WriteFile(probePath, []byte(probe), 0600))
							args = []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File", probePath}
						}
						ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
						defer cancel()
						command := exec.CommandContext(ctx, shell, args...)
						command.Dir = dir
						for _, entry := range os.Environ() {
							name, _, _ := strings.Cut(entry, "=")
							if name != "OPENAI_CLI_COMPLETION_FILE_VALUES" && name != "COMPLETION_STYLE" && name != "PATH" {
								command.Env = append(command.Env, entry)
							}
						}
						command.Env = append(command.Env, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "COMPLETION_PROBE_FAILURE="+failure)
						if previous != "unset" {
							command.Env = append(command.Env, "OPENAI_CLI_COMPLETION_FILE_VALUES="+previous, "COMPLETION_STYLE="+previous)
						}
						var stdout, stderr bytes.Buffer
						command.Stdout, command.Stderr = &stdout, &stderr
						require.NoError(t, command.Run(), stderr.String())
						require.Empty(t, stderr.String())
						if style == CompletionStylePowershell {
							var result struct{ Before, After, Observed []*string }
							require.NoError(t, json.Unmarshal(stdout.Bytes(), &result), stdout.String())
							require.Equal(t, result.Before, result.After, "completion leaked its environment")
							require.Len(t, result.Observed, 2)
							require.NotNil(t, result.Observed[0])
							require.NotNil(t, result.Observed[1])
							require.Equal(t, "1", *result.Observed[0])
							require.Equal(t, string(style), *result.Observed[1])
						} else {
							observed, err := os.ReadFile(filepath.Join(dir, "observed"))
							require.NoError(t, err)
							require.Equal(t, "1\n"+string(style)+"\n", string(observed))
							require.Equal(t, previous+"\n"+previous+"\n", stdout.String(), "completion leaked its environment")
						}
					})
				}
			}
		})
	}
}
