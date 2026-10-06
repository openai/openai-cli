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

func TestBashFileCompletionPreservesAssignments(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is not available")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	script, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	require.NoError(t, err)
	dir := t.TempDir()
	for _, name := range []string{"assets/logo.png", "assets/long name.png", "assets/a=b.png", "--file=logo.png", "=literal.wav"} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, nil, 0600))
	}
	// These shapes come from native Readline on Bash 3.2 and Bash 5.3.
	// Keep COMP_LINE and callback words to verify replacement boundaries.
	for _, tc := range []struct {
		name, line, callback string
		words, want          []string
	}{
		{"bash3 assignment", "openai --file=assets/lo", "assets/lo", []string{"--file=assets/lo"}, []string{"assets/logo.png", "assets/long name.png"}},
		{"bash5 assignment", "openai --file=assets/lo", "assets/lo", []string{"--file", "=", "assets/lo"}, []string{"assets/logo.png", "assets/long name.png"}},
		{"bash3 quoted", `openai --file="assets/long `, "assets/long ", []string{`--file="assets/long `}, []string{"assets/long name.png"}},
		{"bash5 quoted", `openai --file="assets/long `, "assets/long ", []string{"--file", "=", `"assets/long `}, []string{"assets/long name.png"}},
		{"bash3 equals filename", "openai --file=assets/a=b", "b", []string{"--file=assets/a=b"}, []string{"b.png"}},
		{"bash5 closed quote", `openai --file="assets/lo`, "assets/lo", []string{"--file", "=", `"assets/lo"`}, []string{"assets/logo.png", "assets/long name.png"}},
		{"repeated earlier filename", "openai --format assets/logo.png --file=assets/lo", "assets/lo", []string{"--format", "assets/logo.png", "--file", "=", "assets/logo.png"}, []string{"assets/logo.png", "assets/long name.png"}},
		{"repeated earlier quoted filename", `openai --format "assets/logo.png" --file="assets/lo`, "assets/lo", []string{"--format", `"assets/logo.png"`, "--file", "=", `"assets/logo.png"`}, []string{"assets/logo.png", "assets/long name.png"}},
		{"bash5 repeated equals", "openai --file==", "", []string{"--file", "=="}, []string{"literal.wav"}},
		{"bash5 equals filename", "openai --file=assets/a=b", "b", []string{"--file", "=", "assets/a", "=", "b"}, []string{"b.png"}},
		{"separate flag-like value", "openai --file --file=lo", "lo", []string{"--file", "--file", "=", "lo"}, []string{"logo.png"}},
		{"separate equals value", "openai --file =", "", []string{"--file", "="}, []string{"literal.wav"}},
		{"spaces around equals", "openai --file = assets/lo", "assets/lo", []string{"--file", "=", "assets/lo"}, nil},
		{"nonfile assignment", "openai --format=assets/lo", "assets/lo", []string{"--format", "=", "assets/lo"}, nil},
		{"unknown assignment", "openai --unknown=assets/lo", "assets/lo", []string{"--unknown", "=", "assets/lo"}, nil},
		{"end of options", "openai -- --file=assets/lo", "assets/lo", []string{"--", "--file", "=", "assets/lo"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := `
test_binary=$1
COMP_LINE=$2
callback=$3
shift 3
openai() { "$test_binary" '-test.run=^TestShellCompletionProtocolHelper$' -- openai "$@"; }
` + script + `
COMP_POINT=${#COMP_LINE}
COMP_WORDS=(openai "$@")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
__openai_bash_autocomplete openai "$callback" ""
for item in "${COMPREPLY[@]}"; do printf '%s\n' "$item"; done
`
			args := append([]string{"-c", probe, "completion-probe", binary, tc.line, tc.callback}, tc.words...)
			command := exec.Command(bash, args...)
			command.Dir = dir
			command.Env = append(os.Environ(), "OPENAI_CLI_COMPLETION_HELPER=1")
			out, err := command.CombinedOutput()
			require.NoError(t, err, string(out))
			var got []string
			if len(out) > 0 {
				got = strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
			}
			require.ElementsMatch(t, tc.want, got)
		})
	}
}
