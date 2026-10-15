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
	"github.com/urfave/cli/v3"
)

func TestBashFileCompletionPreservesAssignments(t *testing.T) {
	bash, err := exec.LookPath(pickerTestShell("bash"))
	if err != nil {
		t.Skip("Bash is not available")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	script, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	require.NoError(t, err)
	dir := t.TempDir()
	for _, name := range []string{
		"assets/logo.png", "assets/long name.png", "assets/a=b.png", "assets/雪-file.txt", "--file=logo.png", "=literal.wav",
		"@week:notes.txt", "@week::notes.txt", "@week:=notes.txt", ":notes.txt",
		"quote's recording.wav", "quote's second.wav", `say"hello.txt`, `back\slash.txt`,
		"dir's/quote's recording.wav", "glob[dir]/file[1].txt", "glob[dir]/file1.txt",
		"literal*.txt", "literal-other.txt", "question?.txt", "questionX.txt", "雪's file.txt",
		"$(touch sentinel).txt", "`touch sentinel`.txt",
	} {
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
		{"root command", "openai mo", "mo", []string{"mo"}, []string{"models"}},
		{"nested command", "openai models l", "l", []string{"models", "l"}, []string{"list"}},
		{"no matching command", "openai models missing", "missing", []string{"models", "missing"}, nil},
		{"bash3 assignment", "openai --file=assets/lo", "assets/lo", []string{"--file=assets/lo"}, []string{"assets/logo.png", "assets/long name.png"}},
		{"bash5 assignment", "openai --file=assets/lo", "assets/lo", []string{"--file", "=", "assets/lo"}, []string{"assets/logo.png", "assets/long name.png"}},
		{"bash3 quoted", `openai --file="assets/long `, "assets/long ", []string{`--file="assets/long `}, []string{"assets/long name.png"}},
		{"bash5 quoted", `openai --file="assets/long `, "assets/long ", []string{"--file", "=", `"assets/long `}, []string{"assets/long name.png"}},
		{"bash3 equals filename", "openai --file=assets/a=b", "b", []string{"--file=assets/a=b"}, []string{"b.png"}},
		{"bash5 closed quote", `openai --file="assets/lo`, "assets/lo", []string{"--file", "=", `"assets/lo"`}, []string{"assets/logo.png", "assets/long name.png"}},
		{"balanced double quoted apostrophe", `openai --file="quote's"`, `"quote's"`, []string{"--file", "=", `"quote's"`}, []string{"quote's recording.wav", "quote's second.wav"}},
		{"bash3 balanced apostrophe", `openai --file="quote's"`, `"quote's"`, []string{`--file="quote's"`}, []string{"quote's recording.wav", "quote's second.wav"}},
		{"open double quoted apostrophe", `openai --file="quote's`, "quote's", []string{"--file", "=", `"quote's`}, []string{"quote's recording.wav", "quote's second.wav"}},
		{"bash3 open double quoted apostrophe", `openai --file="quote's`, "quote's", []string{`--file="quote's`}, []string{"quote's recording.wav", "quote's second.wav"}},
		{"single quoted double quote", `openai --file='say"'`, `'say"'`, []string{"--file", "=", `'say"'`}, []string{`say"hello.txt`}},
		{"escaped backslash", `openai --file=back\\s`, `back\\s`, []string{"--file", "=", `back\\s`}, []string{`back\slash.txt`}},
		{"quoted directory", `openai --file="dir's/quote's"`, `"dir's/quote's"`, []string{"--file", "=", `"dir's/quote's"`}, []string{"dir's/quote's recording.wav"}},
		{"literal brackets", `openai --file='glob[dir]/file['`, `'glob[dir]/file['`, []string{"--file", "=", `'glob[dir]/file['`}, []string{"glob[dir]/file[1].txt"}},
		{"literal star", `openai --file='literal*'`, `'literal*'`, []string{"--file", "=", `'literal*'`}, []string{"literal*.txt"}},
		{"literal question", `openai --file='question?'`, `'question?'`, []string{"--file", "=", `'question?'`}, []string{"question?.txt"}},
		{"unicode apostrophe", `openai --file="雪's"`, `"雪's"`, []string{"--file", "=", `"雪's"`}, []string{"雪's file.txt"}},
		{"inert dollar substitution", `openai --file='$(touch sentinel)'`, `'$(touch sentinel)'`, []string{"--file", "=", `'$(touch sentinel)'`}, []string{"$(touch sentinel).txt"}},
		{"inert backticks", "openai --file='`touch sentinel`'", "'`touch sentinel`'", []string{"--file", "=", "'`touch sentinel`'"}, []string{"`touch sentinel`.txt"}},
		{"repeated earlier filename", "openai --format assets/logo.png --file=assets/lo", "assets/lo", []string{"--format", "assets/logo.png", "--file", "=", "assets/logo.png"}, []string{"assets/logo.png", "assets/long name.png"}},
		{"repeated earlier quoted filename", `openai --format "assets/logo.png" --file="assets/lo`, "assets/lo", []string{"--format", `"assets/logo.png"`, "--file", "=", `"assets/logo.png"`}, []string{"assets/logo.png", "assets/long name.png"}},
		{"bash5 repeated equals", "openai --file==", "", []string{"--file", "=="}, []string{"literal.wav"}},
		{"bash5 equals filename", "openai --file=assets/a=b", "b", []string{"--file", "=", "assets/a", "=", "b"}, []string{"b.png"}},
		{"separated colon filename", "openai --file @week:no", "no", []string{"--file", "@week", ":", "no"}, []string{"notes.txt"}},
		{"bash3 assigned colon filename", "openai --file=@week:no", "no", []string{"--file=@week", ":", "no"}, []string{"notes.txt"}},
		{"bash5 assigned colon filename", "openai --file=@week:no", "no", []string{"--file", "=", "@week", ":", "no"}, []string{"notes.txt"}},
		{"repeated colon filename", "openai --file @week::no", "no", []string{"--file", "@week", "::", "no"}, []string{"notes.txt"}},
		{"colon and equals filename", "openai --file @week:=no", "no", []string{"--file", "@week", ":", "=", "no"}, []string{"notes.txt"}},
		{"leading colon filename", "openai --file :no", "no", []string{"--file", ":", "no"}, []string{"notes.txt"}},
		{"empty colon suffix", "openai --file @week:", "", []string{"--file", "@week", ":"}, []string{"notes.txt", ":notes.txt", "=notes.txt"}},
		{"spaces around colon", "openai --file @week : no", "no", []string{"--file", "@week", ":", "no"}, nil},
		{"separate flag-like value", "openai --file --file=lo", "lo", []string{"--file", "--file", "=", "lo"}, []string{"logo.png"}},
		{"separate equals value", "openai --file =", "", []string{"--file", "="}, []string{"literal.wav"}},
		{"spaces around equals", "openai --file = assets/lo", "assets/lo", []string{"--file", "=", "assets/lo"}, nil},
		{"nonfile assignment", "openai --format=assets/lo", "assets/lo", []string{"--format", "=", "assets/lo"}, nil},
		{"unknown assignment", "openai --unknown=assets/lo", "assets/lo", []string{"--unknown", "=", "assets/lo"}, nil},
		{"end of options", "openai -- --file=assets/lo", "assets/lo", []string{"--", "--file", "=", "assets/lo"}, nil},
	} {
		for _, mode := range []string{"default", "nounset"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				probe := `
test_binary=$1
COMP_LINE=$2
callback=$3
point_bytes=$4
shift 4
openai() { "$test_binary" '-test.run=^TestShellCompletionProtocolHelper$' -- openai "$@"; }
` + script + `
COMP_POINT=${#COMP_LINE}
# Native COMP_POINT counts bytes before Bash 4.3 and characters in later versions.
if [[ ${BASH_VERSINFO[0]} -lt 4 ||
      ( ${BASH_VERSINFO[0]} -eq 4 && ${BASH_VERSINFO[1]} -lt 3 ) ]]; then COMP_POINT=$point_bytes; fi
COMP_WORDS=(openai "$@")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
__openai_bash_autocomplete openai "$callback" ""
if [[ ${#COMPREPLY[@]} -gt 0 ]]; then
  for item in "${COMPREPLY[@]}"; do printf '%s\n' "$item"; done
fi
`
				args := append([]string{"-c", probe, "completion-probe", binary, tc.line, tc.callback, strconv.Itoa(len(tc.line))}, tc.words...)
				if mode == "nounset" {
					args = append([]string{"-u"}, args...)
				}
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
				require.NoFileExists(t, filepath.Join(dir, "sentinel"))
			})
		}
	}
	localeBinary, localeErr := exec.LookPath("locale")
	utf8Locale := ""
	if localeErr == nil {
		localeContext, cancelLocale := context.WithTimeout(t.Context(), 5*time.Second)
		command := exec.CommandContext(localeContext, localeBinary, "-a")
		command.Env = append(os.Environ(), "LC_ALL=C")
		available, err := command.Output()
		cancelLocale()
		require.NoError(t, err, "list installed locales")
		for _, value := range strings.Fields(string(available)) {
			normalized := strings.ToLower(strings.ReplaceAll(value, "-", ""))
			if !strings.HasSuffix(normalized, "utf8") {
				continue
			}
			if utf8Locale == "" {
				utf8Locale = value
			}
			if normalized == "c.utf8" || normalized == "en_us.utf8" {
				utf8Locale = value
				break
			}
		}
	}
	for _, mode := range []string{"default", "nounset"} {
		t.Run("UTF-8 cursor before later flag/"+mode, func(t *testing.T) {
			if localeErr != nil {
				t.Skipf("UTF-8 locale discovery is unavailable: %v", localeErr)
			}
			if utf8Locale == "" {
				t.Skip("An installed UTF-8 locale is required for the cursor control")
			}
			t.Logf("UTF-8 locale: %s", utf8Locale)
			const prefix = "openai --file=assets/雪-fi"
			records := t.TempDir()
			probe := `
test_binary=$1
COMP_LINE=$2
cursor_prefix=$3
point_bytes=$4
records=$5
readonly LC_ALL LC_CTYPE LANG
character=雪
[[ ${#character} -eq 1 ]] || exit 90
openai() {
  printf '%s\0' "$@" > "$records/backend.argv"
  printf '%s\0' "$LC_ALL" "$LC_CTYPE" "$LANG" > "$records/backend.locale"
  "$test_binary" '-test.run=^TestShellCompletionProtocolHelper$' -- openai "$@"
  local status=$?
  printf '%s\n' "$status" > "$records/backend.status"
  return "$status"
}
` + script + `
COMP_POINT=${#cursor_prefix}
if [[ ${BASH_VERSINFO[0]} -lt 4 ||
      ( ${BASH_VERSINFO[0]} -eq 4 && ${BASH_VERSINFO[1]} -lt 3 ) ]]; then COMP_POINT=$point_bytes; fi
# Preserve the word layouts observed in native Bash 3.2 and Bash 5.3 callbacks.
if [[ ${BASH_VERSINFO[0]} -eq 3 ]]; then
  COMP_WORDS=(openai --file=assets/雪-fi --format=json)
  COMP_CWORD=1
  previous=openai
else
  COMP_WORDS=(openai --file = assets/雪-fi --format = json)
  COMP_CWORD=3
  previous==
fi
__openai_bash_autocomplete openai assets/雪-fi "$previous"
printf '%s\0' "$LC_ALL" "$LC_CTYPE" "$LANG" > "$records/caller.locale"
if [[ ${#COMPREPLY[@]} -gt 0 ]]; then printf '%s\0' "${COMPREPLY[@]}"; fi
`
			args := []string{"--noprofile", "--norc"}
			if mode == "nounset" {
				args = append(args, "-u")
			}
			args = append(args, "-c", probe, "cursor-probe", binary,
				prefix+" --format=json", prefix, strconv.Itoa(len(prefix)), records)
			command := exec.Command(bash, args...)
			command.Dir = dir
			command.Env = append(os.Environ(), "OPENAI_CLI_COMPLETION_HELPER=1",
				"LC_ALL="+utf8Locale, "LC_CTYPE="+utf8Locale, "LANG="+utf8Locale)
			out, err := command.CombinedOutput()
			require.NoError(t, err, string(out))
			require.Equal(t, "assets/雪-file.txt\x00", string(out))
			for name, want := range map[string]string{
				"backend.argv":   "__complete\x00--\x00--file=assets/雪-fi\x00",
				"backend.status": "10\n",
				"backend.locale": strings.Repeat(utf8Locale+"\x00", 3),
				"caller.locale":  strings.Repeat(utf8Locale+"\x00", 3),
			} {
				got, err := os.ReadFile(filepath.Join(records, name))
				require.NoError(t, err)
				require.Equal(t, want, string(got), name)
			}
		})
	}
}

func TestBashLiteralFileCompletionPreservesExpansionFallback(t *testing.T) {
	bash, err := exec.LookPath(pickerTestShell("bash"))
	if err != nil {
		t.Skip("Bash is not available")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	script, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	require.NoError(t, err)
	for _, mode := range []string{"logical", "physical"} {
		for _, prefix := range []string{"alias/../pick", "assets/$PART/fi", "assets/${PART}/fi", "~/assets/$PART/fi"} {
			t.Run(mode+"/"+prefix, func(t *testing.T) {
				dir := t.TempDir()
				for _, name := range []string{"physical/child/unused.txt", "physical/pick-physical.txt", "pick-logical.txt", "assets/child/file.txt"} {
					path := filepath.Join(dir, name)
					require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
					require.NoError(t, os.WriteFile(path, nil, 0600))
				}
				if err := os.Symlink(filepath.Join("physical", "child"), filepath.Join(dir, "alias")); err != nil && prefix == "alias/../pick" {
					t.Skipf("Symlink fixture is unavailable: %v", err)
				}
				probe := `
prefix=$1
PART=child
export PART
if [[ $2 == physical ]]; then set -P; else set +P; fi
openai() { OPENAI_CLI_COMPLETION_HELPER=1 "$COMPLETION_BINARY" '-test.run=^TestShellCompletionProtocolHelper$' -- openai "$@"; }
` + script + `
while IFS= read -r file; do printf '%s\0' "$file"; done < <(builtin compgen -f -- "$prefix") > expected
COMP_LINE="openai --file $prefix"
COMP_POINT=${#COMP_LINE}
COMP_WORDS=(openai --file "$prefix")
COMP_CWORD=2
__openai_bash_autocomplete openai "$prefix" --file
if [[ ${#COMPREPLY[@]} -gt 0 ]]; then printf '%s\0' "${COMPREPLY[@]}"; fi
`
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", probe, "fallback-probe", prefix, mode)
				command.Dir = dir
				command.Env = []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "LC_ALL=C", "COMPLETION_BINARY=" + binary}
				out, err := command.CombinedOutput()
				require.NoError(t, ctx.Err())
				require.NoError(t, err, string(out))
				want, err := os.ReadFile(filepath.Join(dir, "expected"))
				require.NoError(t, err)
				require.Equal(t, string(want), string(out), "preserve this Bash's existing directory lookup and spelling")
				if len(want) == 0 {
					t.Log("The legacy lookup produced no candidate in this shell context")
				}
			})
		}
	}
}
