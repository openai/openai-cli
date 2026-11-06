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
		requiresMapCase                   bool
		hiddenSetting                     string
		wantBackendStatus                 string
		hasBash32QuoteLimit               bool
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
		{name: "literal colon value", typed: "openai --file : chat comple", want: []string{"--file", ":", "chat", "completions"}, wantBackendStatus: "0\n"},
		{name: "quoted colon value", typed: "openai --file ':' chat comple", want: []string{"--file", ":", "chat", "completions"}},
		{name: "file control", typed: "openai --file assets/fi", want: []string{"--file", "assets/fixture name.json"}},
		{name: "assigned file control", typed: "openai --file=assets/fi", want: []string{"--file=assets/fixture name.json"}},
		{name: "literal balanced apostrophe", typed: `openai --file="quote's"`, want: []string{"--file=quote's recording.wav"}, wantBackendStatus: "10\n"},
		{name: "literal open apostrophe", typed: `openai --file="quote's`, want: []string{"--file=quote's recording.wav"}, wantBackendStatus: "10\n"},
		{name: "literal before closing quote", typed: `openai --file="quote's"`, before: "\x02", after: "\x05", want: []string{"--file=quote's recording.wav"}, hasBash32QuoteLimit: true, wantBackendStatus: "10\n"},
		{name: "literal escaped apostrophe", typed: `openai --file=quote\'s`, want: []string{"--file=quote's recording.wav"}, wantBackendStatus: "10\n"},
		{name: "literal separated apostrophe", typed: `openai --file "quote's"`, want: []string{"--file", "quote's recording.wav"}, wantBackendStatus: "10\n"},
		{name: "literal quoted directory", typed: `openai --file="dir's/quote's"`, want: []string{"--file=dir's/quote's recording.wav"}, wantBackendStatus: "10\n"},
		{name: "literal quoted double quote", typed: `openai --file='say"'`, want: []string{`--file=say"hello.txt`}, wantBackendStatus: "10\n"},
		{name: "literal brackets", typed: `openai --file='glob[dir]/file['`, want: []string{"--file=glob[dir]/file[1].txt"}, wantBackendStatus: "10\n"},
		{name: "literal star", typed: `openai --file='literal*'`, want: []string{"--file=literal*.txt"}, wantBackendStatus: "10\n"},
		{name: "literal no match", typed: `openai --file="missing's"`, want: []string{"--file=missing's"}, wantBackendStatus: "10\n"},
		{name: "literal case sensitive", typed: `openai --file="case-fi"`, setup: "bind 'set completion-ignore-case off'", want: []string{"--file=case-fi"}, wantBackendStatus: "10\n"},
		{name: "literal ignore case", typed: `openai --file="case-fi"`, setup: "bind 'set completion-ignore-case on'", want: []string{"--file=case-File.txt"}, wantBackendStatus: "10\n"},
		{name: "literal map case", typed: `openai --file="map-under"`, setup: "bind 'set completion-ignore-case on'; bind 'set completion-map-case on'", want: []string{"--file=map_under.txt"}, requiresMapCase: true, wantBackendStatus: "10\n"},
		{name: "literal map case disabled", typed: `openai --file="map-under"`, setup: "bind 'set completion-ignore-case on'; bind 'set completion-map-case off'", want: []string{"--file=map-under"}, requiresMapCase: true, wantBackendStatus: "10\n"},
		{name: "literal current home", typed: "openai --file=~/assets/fi", want: []string{"--file=~/assets/fixture name.json"}, wantBackendStatus: "10\n"},
		{name: "literal empty hidden on", typed: "openai --file=", setup: "bind 'set match-hidden-files on'", want: []string{"--file="}, hiddenSetting: "on", wantBackendStatus: "10\n"},
		{name: "literal empty hidden off", typed: "openai --file=", setup: "bind 'set match-hidden-files off'", want: []string{"--file="}, hiddenSetting: "off", wantBackendStatus: "10\n"},
		{name: "literal explicit hidden", typed: "openai --file=.hid", setup: "bind 'set match-hidden-files off'", want: []string{"--file=.hidden.txt"}, wantBackendStatus: "10\n"},
		{name: "literal filename ignore", typed: `openai --file="ignore'"`, setup: "FIGNORE=.bak; shopt -s force_fignore", want: []string{"--file=ignore'keep.txt"}, wantBackendStatus: "10\n"},
		{name: "literal inherited glob settings", typed: `openai --file="quote's"`, setup: "GLOBIGNORE='*'; shopt -s failglob nullglob extglob; set -f", want: []string{"--file=quote's recording.wav"}, wantBackendStatus: "10\n"},
		{name: "literal inert substitution", typed: `openai --file='$(touch sentinel)'`, want: []string{"--file=$(touch sentinel).txt"}, wantBackendStatus: "10\n"},
		{name: "no matching command", typed: "openai chat:unknown", want: []string{"chat:unknown"}},
		{name: "nounset", typed: `openai chat\:comple`, setup: "set -u", want: command},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			for _, name := range []string{"assets/fixture name.json", "quote's recording.wav", "dir's/quote's recording.wav", `say"hello.txt`, "glob[dir]/file[1].txt", "literal*.txt", "literal-other.txt", "case-File.txt", "map_under.txt", ".hidden.txt", "ignore'keep.txt", "ignore'drop.bak", "$(touch sentinel).txt"} {
				path := filepath.Join(directory, name)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
				require.NoError(t, os.WriteFile(path, nil, 0600))
			}
			startup := `
PS1='COLON_TEST> '; PS2='COLON_MORE> '
set +o history
bind 'set bell-style none'
if [[ $COLON_KNOWN_QUOTE_LIMIT == true && $BASH_VERSION == '3.2.57(1)-release' && $OSTYPE == darwin* ]]; then
  printf '\nCOLON_BASH32_QUOTE_LIMIT\n'
  return 0
fi
if [[ $COLON_REQUIRES_MAP_CASE == true ]]; then
  if builtin bind -v > "$COLON_READLINE_SETTINGS"; then
    colon_readline_settings=$(<"$COLON_READLINE_SETTINGS")
    case $colon_readline_settings in
      *'set completion-map-case '*) ;;
      *) printf '\nCOLON_MAP_CASE_UNSUPPORTED\n'; return 0 ;;
    esac
  else
    printf '\nCOLON_CAPABILITY_FAILED\n'
    return 1
  fi
  unset colon_readline_settings
fi
openai() {
  if [[ "${1-}" == __complete ]]; then
    printf '%s\0' "$@" > "$COLON_BACKEND_ARGS"
    OPENAI_CLI_COLON_HELPER=1 "$COLON_BINARY" -test.run='^TestBashColonCompletionHelper$' -- openai "$@"
    local backend_status=$?
    printf '%s\n' "$backend_status" > "$COLON_BACKEND_STATUS"
    return "$backend_status"
  else
    printf '%s\0' "$@" > "$COLON_RESULT"
    printf '\nCOLON_ENTERED\n'
  fi
}
` + completion + `
__openai_bash_autocomplete() {
  local old_directory=$PWD old_options old_flags=$- old_glob_ignore=${GLOBIGNORE-} old_file_ignore=${FIGNORE-}
  old_options=$(shopt -p nocaseglob dotglob nullglob failglob extglob cdspell cdable_vars force_fignore)
  __colon_original_completion "$@"
  if [[ $PWD == "$old_directory" && $- == "$old_flags" && ${GLOBIGNORE-} == "$old_glob_ignore" && ${FIGNORE-} == "$old_file_ignore" && $(shopt -p nocaseglob dotglob nullglob failglob extglob cdspell cdable_vars force_fignore) == "$old_options" ]]; then
    printf 'unchanged\n' > "$COLON_SHELL_STATE"
  fi
  if [[ ${#COMPREPLY[@]} -gt 0 ]]; then printf '%s\0' "${COMPREPLY[@]}" > "$COLON_CANDIDATES"; fi
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
set startup_exit 0
expect {
  -exact "\r\nCOLON_READY\r\n" {}
  -exact "\r\nCOLON_MAP_CASE_UNSUPPORTED\r\n" { set startup_exit 77 }
  -exact "\r\nCOLON_BASH32_QUOTE_LIMIT\r\n" { set startup_exit 78 }
  -exact "\r\nCOLON_CAPABILITY_FAILED\r\n" { set startup_exit 1 }
  eof { puts stderr "Bash exited before the completion probe started"; exit 1 }
}
expect -exact "COLON_TEST> "
if {$startup_exit != 0} {
  send -- "exit 0\r"
  expect eof
  set child_status [wait]
  if {[llength $child_status] != 4 || [lindex $child_status 2] != 0 || [lindex $child_status 3] != 0} {
    puts stderr "Bash startup probe did not exit normally: $child_status"
    exit 1
  }
  exit $startup_exit
}
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
			// Skip Expect's personal startup file in these isolated shell tests.
			cmd := exec.CommandContext(ctx, expect, "-n", filepath.Join(directory, "driver.exp"))
			cmd.Dir = directory
			cmd.Env = []string{
				"HOME=" + directory, "PATH=" + os.Getenv("PATH"), "LC_ALL=C", "TERM=xterm",
				"BASH_SILENCE_DEPRECATION_WARNING=1", "COLON_BASH=" + bash, "COLON_BINARY=" + binary,
				"COLON_STARTUP=" + filepath.Join(directory, "startup"), "COLON_RESULT=" + filepath.Join(directory, "argv"),
				"COLON_BACKEND_ARGS=" + filepath.Join(directory, "backend-argv"), "COLON_BACKEND_STATUS=" + filepath.Join(directory, "backend-status"),
				"COLON_SHELL_STATE=" + filepath.Join(directory, "shell-state"), "COLON_CANDIDATES=" + filepath.Join(directory, "candidates"),
				"COLON_TYPED=" + tc.typed, "COLON_BEFORE=" + tc.before, "COLON_AFTER=" + tc.after,
				"COLON_REQUIRES_MAP_CASE=" + fmt.Sprint(tc.requiresMapCase),
				"COLON_READLINE_SETTINGS=" + filepath.Join(directory, "readline-settings"),
				"COLON_KNOWN_QUOTE_LIMIT=" + fmt.Sprint(tc.hasBash32QuoteLimit),
			}
			out, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
				require.True(t, tc.requiresMapCase, string(out))
				require.Contains(t, string(out), "\r\nCOLON_MAP_CASE_UNSUPPORTED\r\n")
				require.NoFileExists(t, filepath.Join(directory, "backend-argv"))
				require.NoFileExists(t, filepath.Join(directory, "argv"))
				t.Skip("This interactive Bash Readline does not expose completion-map-case")
			}
			if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 78 {
				require.True(t, tc.hasBash32QuoteLimit, string(out))
				require.Contains(t, string(out), "\r\nCOLON_BASH32_QUOTE_LIMIT\r\n")
				require.NoFileExists(t, filepath.Join(directory, "backend-argv"))
				require.NoFileExists(t, filepath.Join(directory, "argv"))
				t.Skip("Known Darwin Bash 3.2.57 filename completion duplicates an existing closing quote")
			}
			require.NoError(t, err, string(out))
			accepted, err := os.ReadFile(filepath.Join(directory, "argv"))
			require.NoError(t, err, string(out))
			want := strings.Join(tc.want, "\x00") + "\x00"
			require.Equal(t, want, string(accepted), string(out))
			require.NoFileExists(t, filepath.Join(directory, "sentinel"))
			state, err := os.ReadFile(filepath.Join(directory, "shell-state"))
			require.NoError(t, err)
			require.Equal(t, "unchanged\n", string(state))
			if tc.hiddenSetting != "" {
				candidates, err := os.ReadFile(filepath.Join(directory, "candidates"))
				require.NoError(t, err)
				require.Equal(t, tc.hiddenSetting == "on", slices.Contains(strings.Split(string(candidates), "\x00"), ".hidden.txt"))
			}
			if tc.wantBackendStatus != "" {
				status, err := os.ReadFile(filepath.Join(directory, "backend-status"))
				require.NoError(t, err)
				require.Equal(t, tc.wantBackendStatus, string(status), "completion must retain the expected backend protocol status")
			}
			if tc.name == "literal balanced apostrophe" {
				arguments, err := os.ReadFile(filepath.Join(directory, "backend-argv"))
				require.NoError(t, err)
				require.Equal(t, "__complete\x00--\x00--file=quote's\x00", string(arguments))
			}
		})
	}
}
