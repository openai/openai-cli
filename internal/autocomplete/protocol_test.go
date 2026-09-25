package autocomplete

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestShellCompletionProtocolHelper(t *testing.T) {
	if os.Getenv("OPENAI_CLI_COMPLETION_HELPER") != "1" {
		return
	}
	root := &cli.Command{
		Name: "openai", SkipFlagParsing: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format"},
			&cli.StringFlag{Name: "file", TakesFile: true},
			&cli.StringFlag{Name: "header"},
		},
		Commands: []*cli.Command{
			{Name: "models", Commands: []*cli.Command{
				{Name: "list", Flags: []cli.Flag{&cli.IntFlag{Name: "max-items"}}},
				{Name: "retrieve", Flags: []cli.Flag{&cli.StringFlag{Name: "model"}}},
			}},
			{Name: "__complete", Hidden: true, SkipFlagParsing: true, Action: ExecuteShellCompletion},
		},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
	}
	err := root.Run(context.Background(), os.Args[slices.Index(os.Args, "--")+1:])
	if exit, ok := err.(cli.ExitCoder); ok {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestShellCompletionProtocol(t *testing.T) {
	t.Parallel()

	bash, bashErr := exec.LookPath("bash")
	binary, err := os.Executable()
	require.NoError(t, err)
	script, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	require.NoError(t, err)

	for _, test := range []struct {
		name       string
		args       []string
		bashArgs   []string
		code       int
		output     string
		candidates string
	}{
		{name: "root value", args: []string{"--format", "candidate-"}, code: 11},
		{name: "nested local value", args: []string{"models", "list", "--max-items", "candidate-"}, code: 11},
		{name: "file value", args: []string{"--file", "candidate-"}, code: 10, candidates: "candidate-fixture.txt\n"},
		{
			name:       "colon file value",
			args:       []string{"--file", "cert:models"},
			bashArgs:   []string{"--file", "cert", ":", "models"},
			code:       10,
			candidates: "models-fixture.txt\n",
		},
		{name: "spaced preceding value", args: []string{"--format", "two words", "--file", "candidate-"}, code: 10, candidates: "candidate-fixture.txt\n"},
		{name: "empty preceding value", args: []string{"--format", "", "--file", "candidate-"}, code: 10, candidates: "candidate-fixture.txt\n"},
		{name: "explicit file prefix", args: []string{"--format", "@candidate-"}, code: 11, candidates: "@candidate-fixture.txt\n"},
		{name: "command prefix", args: []string{"mo"}, code: 0, output: "models\n", candidates: "models\n"},
		{
			name:       "bash colon-split header value",
			args:       []string{"--header", "X:completions", "models", "retrieve", "--mo"},
			bashArgs:   []string{"--header", "X", ":", "completions", "models", "retrieve", "--mo"},
			code:       0,
			output:     "--model\n",
			candidates: "--model\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := append(os.Environ(), "OPENAI_CLI_COMPLETION_HELPER=1", "COMPLETION_STYLE=bash")
			args := append([]string{"-test.run=^TestShellCompletionProtocolHelper$", "--", "openai", "__complete", "--"}, test.args...)
			command := exec.Command(binary, args...)
			command.Env = env
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			code := 0
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, stderr.String())
			assert.Equal(t, test.code, code)
			require.Equal(t, test.output, stdout.String())

			t.Run("rendered bash", func(t *testing.T) {
				if bashErr != nil {
					t.Skip("bash is not available")
				}
				if runtime.GOOS == "windows" && test.name == "colon file value" {
					t.Skip("Windows filenames cannot contain colons")
				}
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "candidate-fixture.txt"), nil, 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "cert:models-fixture.txt"), nil, 0o600))
				probe := `
test_binary=$1
shift
# Match the existing Bash tests' support for platforms with Bash 3.
if ! type mapfile >/dev/null 2>&1; then
  mapfile() {
    COMPREPLY=()
    local line
    while IFS= read -r line; do COMPREPLY+=("$line"); done
  }
fi
openai() {
  printf '%s\0' "$@" > helper.argv
  "$test_binary" -test.run='^TestShellCompletionProtocolHelper$' -- openai "$@"
}
` + script + `
COMP_WORDS=(openai "$@")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
__openai_bash_autocomplete
for candidate in "${COMPREPLY[@]}"; do
  printf '%s\n' "$candidate"
done
`
				bashArgs := test.args
				if test.bashArgs != nil {
					bashArgs = test.bashArgs
				}
				args := append([]string{"-c", probe, "completion-probe", binary}, bashArgs...)
				command := exec.Command(bash, args...)
				command.Dir, command.Env = dir, env
				stdout.Reset()
				stderr.Reset()
				command.Stdout, command.Stderr = &stdout, &stderr
				require.NoError(t, command.Run())
				require.Empty(t, stderr.String())
				require.Equal(t, test.candidates, stdout.String())
				argv, err := os.ReadFile(filepath.Join(dir, "helper.argv"))
				require.NoError(t, err)
				wantArgs := append([]string{"__complete", "--"}, bashArgs...)
				require.Equal(t, strings.Join(wantArgs, "\x00")+"\x00", string(argv))
			})
		})
	}
}

func TestZshCompletionRespectsCursor(t *testing.T) {
	t.Parallel()

	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not available")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	script, err := shellCompletions[CompletionStyleZsh](&cli.Command{}, "openai")
	require.NoError(t, err)
	// An empty prefix also matches the help command installed by the CLI library.
	const allModelCommands = "list\nretrieve\nhelp:Shows a list of commands or help for one command\nh:Shows a list of commands or help for one command\n"

	for _, test := range []struct {
		name       string
		args       []string
		current    int // Zsh's one-based index, including the executable.
		code       int
		candidates string
	}{
		{"mid-line command", []string{"models", "li", "--format", "json"}, 3, 0, "list\n"},
		{"mid-line flag", []string{"models", "list", "--max", "--format", "json"}, 4, 0, "--max-items\n"},
		{"end-of-line command", []string{"models", "li"}, 3, 0, "list\n"},
		{"empty mid-line word", []string{"models", "", "--format", "json"}, 3, 0, allModelCommands},
		{"empty end-of-line word", []string{"models", ""}, 3, 0, allModelCommands},
		{"spaced preceding value", []string{"--format", "two words", "models", "li", "--file", "unused"}, 5, 0, "list\n"},
		{"empty preceding value", []string{"--format", "", "models", "li", "--file", "unused"}, 5, 0, "list\n"},
		{"mid-line file value", []string{"--file", "candidate-", "models", "list"}, 3, 0, "files\n"},
		{"end-of-line file value", []string{"--file", "candidate-"}, 3, 0, "files\n"},
		{"spaced file value", []string{"--file", "two words", "models", "list"}, 3, 0, "files\n"},
		{"explicit file prefix", []string{"--format", "@candidate-", "models", "list"}, 3, 0, "prefix:*@\nfiles\n"},
		{"file URL prefix", []string{"--format", "@file://candidate-", "models", "list"}, 3, 0, "prefix:*file://\nfiles\n"},
		{"data URL prefix", []string{"--format", "@data://candidate-", "models", "list"}, 3, 0, "prefix:*data://\nfiles\n"},
		{"mid-line non-file value", []string{"--format", "json", "models", "list"}, 3, 1, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Stub Zsh's completion UI, but use the real script and Go backend.
			probe := `
test_binary=$1
cursor_word=$2
shift 2
compdef() { :; }
_describe() { printf '%s\n' "${opts[@]}"; }
_files() { printf 'files\n'; }
compset() { printf 'prefix:%s\n' "$2"; }
openai() {
  printf '%s\0' "$@" > helper.argv
  "$test_binary" -test.run='^TestShellCompletionProtocolHelper$' -- openai "$@"
}
` + script + `
words=(openai "$@")
CURRENT=$cursor_word
__openai_zsh_autocomplete
`
			dir := t.TempDir()
			args := append([]string{"-f", "-c", probe, "zsh-completion-probe", binary, fmt.Sprint(test.current)}, test.args...)
			command := exec.Command(zsh, args...)
			command.Dir = dir
			command.Env = append(os.Environ(), "OPENAI_CLI_COMPLETION_HELPER=1", "COMPLETION_STYLE=zsh")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			code := 0
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, stderr.String())
			assert.Equal(t, test.code, code)
			assert.Equal(t, test.candidates, stdout.String())
			argv, err := os.ReadFile(filepath.Join(dir, "helper.argv"))
			require.NoError(t, err)
			wantArgs := append([]string{"__complete"}, test.args[:test.current-1]...)
			assert.Equal(t, strings.Join(wantArgs, "\x00")+"\x00", string(argv))
		})
	}
}
