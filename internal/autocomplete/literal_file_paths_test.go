package autocomplete

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestBashLiteralFileCompletionKeepsLeadingAt(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	script, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	require.NoError(t, err)
	for _, words := range [][]string{
		{"openai", "files", "upload", "@"},
		{"openai", "files", "upload", "--file", "@"},
		{"openai", "files", "upload", "--file=@"},
	} {
		directory := t.TempDir()
		for _, name := range []string{"plain.txt", "@literal.txt", "@literal space.txt"} {
			require.NoError(t, os.WriteFile(filepath.Join(directory, name), nil, 0o600))
		}
		probe := `
cd "$1" || exit 1
shift
openai() {
  local argument
  for argument in "$@"; do
    case "$argument" in --file=*) printf '%s\n' '--file=';; esac
  done
  return 10
}
` + script + `
COMP_WORDS=("$@")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
__openai_bash_autocomplete
printf '%s\n' "${COMPREPLY[@]}"
`
		out, err := exec.Command(bash, append([]string{"--noprofile", "--norc", "-c", probe, "literal-file-probe", directory}, words...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		prefix := ""
		if strings.HasPrefix(words[len(words)-1], "--file=") {
			prefix = "--file="
		}
		require.ElementsMatch(t, []string{prefix + "@literal.txt", prefix + "@literal space.txt"}, strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"))
		require.NotContains(t, string(out), "@plain.txt")
	}
}
