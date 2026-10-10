package autocomplete

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestBashColonCompletionHelper(t *testing.T) {
	if os.Getenv("OPENAI_CLI_COLON_HELPER") != "1" {
		return
	}
	root := &cli.Command{
		Name: "openai", SkipFlagParsing: true,
		Flags: []cli.Flag{&cli.StringFlag{Name: "header"}, &cli.StringFlag{Name: "file", TakesFile: true}},
		Commands: []*cli.Command{
			{Name: "chat", Commands: []*cli.Command{{Name: "completions"}}},
			{Name: "chat:completions", Hidden: true, Metadata: map[string]any{"command-compatibility-alias": true},
				Commands: []*cli.Command{{Name: "create", Flags: []cli.Flag{&cli.StringFlag{Name: "model"}}}}},
			{Name: "chat:completions:messages", Hidden: true, Metadata: map[string]any{"command-compatibility-alias": true}},
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

func TestBashColonCompletionCandidates(t *testing.T) {
	bash, err := exec.LookPath(pickerTestShell("bash"))
	if err != nil {
		t.Skip("Bash is not available")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	completion, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	require.NoError(t, err)
	suffixes := []string{"completions", "completions:messages"}
	commands := []string{"chat:completions", "chat:completions:messages"}
	// Captured native Bash word/callback shapes also run when expect is absent.
	for _, tc := range []struct {
		name, line, callback string
		words, want          []string
	}{
		{"ordinary", "openai chat:comple", "comple", []string{"chat", ":", "comple"}, suffixes},
		{"trailing colon", "openai chat:", "", []string{"chat", ":"}, suffixes},
		{"quoted", "openai 'chat:comple'", "'chat:comple'", []string{"'chat:comple'"}, commands},
		{"escaped", `openai chat\:comple`, `chat\:comple`, []string{`chat\:comple`}, commands},
		{"partially quoted", "openai 'chat:'comple", "'chat:'comple", []string{"'chat:'comple"}, commands},
		{"colon not a word break", "openai chat:comple", "chat:comple", []string{"chat:comple"}, commands},
		{"trailing colon not a word break", "openai chat:", "chat:", []string{"chat:"}, commands},
		{"multiple colons", "openai chat:completions:mess", "mess", []string{"chat", ":", "completions", ":", "mess"}, []string{"messages"}},
		{"partially escaped colons", `openai chat:completions\:mess`, `completions\:mess`, []string{"chat", ":", `completions\:mess`}, []string{"completions:messages"}},
		{"nested command", "openai chat comple", "comple", []string{"chat", "comple"}, []string{"completions"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := `
COMP_LINE=$1
callback=$2
shift 2
openai() { OPENAI_CLI_COLON_HELPER=1 "$COLON_BINARY" -test.run='^TestBashColonCompletionHelper$' -- openai "$@"; }
` + completion + `
COMP_POINT=${#COMP_LINE}
COMP_WORDS=(openai "$@")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
__openai_bash_autocomplete openai "$callback" ""
if [[ ${#COMPREPLY[@]} -gt 0 ]]; then printf '%s\0' "${COMPREPLY[@]}"; fi
`
			args := append([]string{"-c", probe, "completion-probe", tc.line, tc.callback}, tc.words...)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bash, args...)
			cmd.Dir = t.TempDir()
			cmd.Env = []string{"HOME=" + cmd.Dir, "PATH=" + os.Getenv("PATH"), "LC_ALL=C", "COLON_BINARY=" + binary}
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			require.Equal(t, strings.Join(tc.want, "\x00")+"\x00", string(out))
		})
	}
}

// Exercise Readline's actual replacement span. Candidate-only tests cannot
// detect a missing or duplicated prefix in the command accepted after Tab.
func TestBashColonCompletionReadline(t *testing.T) {
	bash, err := exec.LookPath(pickerTestShell("bash"))
	if err != nil {
		t.Skip("Bash is not available")
	}
	expect, err := exec.LookPath("expect")
	if err != nil {
		t.Skip("expect is required for real shell line editor tests")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	completion, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	require.NoError(t, err)
	completion = strings.Replace(completion, "__openai_bash_autocomplete()", "__colon_original_completion()", 1)
	command := []string{"chat:completions"}
	messages := []string{"chat:completions:messages"}
	for _, tc := range []struct {
		name, typed, setup, before, after string
		want                              []string
	}{
		{name: "ordinary", typed: "openai chat:comple", want: command},
		{name: "trailing colon", typed: "openai chat:", want: command},
		{name: "escaped", typed: `openai chat\:comple`, want: command},
		{name: "single quoted", typed: "openai 'chat:comple'", want: command},
		{name: "double quoted", typed: `openai "chat:comple"`, want: command},
		{name: "partially quoted", typed: "openai 'chat:'comple", want: command},
		{name: "escaped trailing colon", typed: `openai chat\:`, want: command},
		{name: "quoted trailing colon", typed: "openai 'chat:'", want: command},
		{name: "open single quote", typed: "openai 'chat:comple", after: "'", want: command},
		{name: "open double quote", typed: `openai "chat:comple`, after: `"`, want: command},
		{name: "before closing quote", typed: "openai 'chat:comple'", before: "\x02", after: "\x05", want: command},
		{name: "colon not a word break", typed: "openai chat:comple", setup: "COMP_WORDBREAKS=${COMP_WORDBREAKS//:}", want: command},
		{name: "trailing colon not a word break", typed: "openai chat:", setup: "COMP_WORDBREAKS=${COMP_WORDBREAKS//:}", want: command},
		{name: "multiple colons", typed: "openai chat:completions:mess", want: messages},
		{name: "multiple quoted colons", typed: "openai 'chat:completions:mess'", want: messages},
		{name: "partially escaped colons", typed: `openai chat:completions\:mess`, want: messages},
		{name: "first colon escaped", typed: `openai chat\:completions:mess`, want: messages},
		{name: "multiple colons not word breaks", typed: "openai chat:completions:mess", setup: "COMP_WORDBREAKS=${COMP_WORDBREAKS//:}", want: messages},
		{name: "nested command", typed: "openai chat comple", want: []string{"chat", "completions"}},
		{name: "flag after alias", typed: "openai chat:completions create --mo", want: []string{"chat:completions", "create", "--model"}},
		{name: "quoted header control", typed: "openai --header 'chat:' chat comple", want: []string{"--header", "chat:", "chat", "completions"}},
		{name: "file control", typed: "openai --file assets/fi", want: []string{"--file", "assets/fixture name.json"}},
		{name: "assigned file control", typed: "openai --file=assets/fi", want: []string{"--file=assets/fixture name.json"}},
		{name: "no matching command", typed: "openai chat:unknown", want: []string{"chat:unknown"}},
		{name: "nounset", typed: `openai chat\:comple`, setup: "set -u", want: command},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(directory, "assets"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(directory, "assets", "fixture name.json"), nil, 0600))
			startup := `
PS1='COLON_TEST> '; PS2='COLON_MORE> '
set +o history
bind 'set bell-style none'
openai() {
  if [[ "${1-}" == __complete ]]; then
    OPENAI_CLI_COLON_HELPER=1 "$COLON_BINARY" -test.run='^TestBashColonCompletionHelper$' -- openai "$@"
  else
    printf '%s\0' "$@" > "$COLON_RESULT"
    printf '\nCOLON_ENTERED\n'
  fi
}
` + completion + `
__openai_bash_autocomplete() {
  __colon_original_completion "$@"
  printf '\nCOLON_COMPLETED\n'
}
` + tc.setup + "\nprintf '\\nCOLON_READY\\n'\n"
			require.NoError(t, os.WriteFile(filepath.Join(directory, "startup"), []byte(startup), 0600))
			driver := `
set timeout 10
match_max 100000
spawn -noecho $env(COLON_BASH) --noprofile --norc -i
expect_after timeout { puts stderr "Bash completion probe timed out"; exit 1 }
send -- "source \"\$COLON_STARTUP\"\r"
expect -exact "\r\nCOLON_READY\r\n"
expect -exact "COLON_TEST> "
send -- $env(COLON_TYPED)
send -- $env(COLON_BEFORE)
send -- "\t"
expect -exact "\r\nCOLON_COMPLETED\r\n"
send -- $env(COLON_AFTER)
send -- "\r"
expect -exact "\r\nCOLON_ENTERED\r\n"
expect -exact "COLON_TEST> "
send -- "exit\r"
expect eof
`
			require.NoError(t, os.WriteFile(filepath.Join(directory, "driver.exp"), []byte(driver), 0600))
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, expect, filepath.Join(directory, "driver.exp"))
			cmd.Dir = directory
			cmd.Env = []string{
				"HOME=" + directory, "PATH=" + os.Getenv("PATH"), "LC_ALL=C", "TERM=xterm",
				"BASH_SILENCE_DEPRECATION_WARNING=1", "COLON_BASH=" + bash, "COLON_BINARY=" + binary,
				"COLON_STARTUP=" + filepath.Join(directory, "startup"), "COLON_RESULT=" + filepath.Join(directory, "argv"),
				"COLON_TYPED=" + tc.typed, "COLON_BEFORE=" + tc.before, "COLON_AFTER=" + tc.after,
			}
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			accepted, err := os.ReadFile(filepath.Join(directory, "argv"))
			require.NoError(t, err, string(out))
			require.Equal(t, strings.Join(tc.want, "\x00")+"\x00", string(accepted), string(out))
		})
	}
}
