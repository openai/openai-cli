//go:build darwin || linux

package autocomplete

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// Wait for an actual interactive prompt before sending Tab through the PTY.
// Cmd uses one copy goroutine because stdout and stderr share this writer.
type readlineTranscript struct {
	buffer bytes.Buffer
	ready  chan struct{}
	once   sync.Once
}

func (output *readlineTranscript) Write(data []byte) (int, error) {
	n, err := output.buffer.Write(data)
	if bytes.Contains(output.buffer.Bytes(), []byte("__COMPLETION_READY__ ")) {
		output.once.Do(func() { close(output.ready) })
	}
	return n, err
}

func TestBashReadlineInsertsColonFilenameOnce(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("the script PTY utility is not available")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	rendered, err := shellCompletions[CompletionStyleBash](&cli.Command{}, "openai")
	require.NoError(t, err)

	for _, test := range []struct {
		name, flag, filename, input, want string
		keepColonInWord, keepAtInWord     bool
	}{
		{"review example", "--file", "cert:models-fixture.txt", "cert:models", "cert:models-fixture.txt", false, false},
		{"multiple colons", "--file", "cert:part:models-fixture.txt", "cert:part:models", "cert:part:models-fixture.txt", false, false},
		{"space in filename", "--file", "cert:models fixture.txt", "cert:models", "cert:models fixture.txt", false, false},
		{"literal pattern prefix", "--file", "[cert]:models-fixture.txt", "[cert]:models", "[cert]:models-fixture.txt", false, false},
		{"colon in directory", "--file", "cert:directory/models.txt", "cert:directory/mod", "cert:directory/models.txt", false, false},
		{"ordinary filename", "--file", "candidate-fixture.txt", "candidate-", "candidate-fixture.txt", false, false},
		{"colon removed from word breaks", "--file", "cert:models-fixture.txt", "cert:models", "cert:models-fixture.txt", true, false},
		{"at prefix retained", "--format", "candidate-fixture.txt", "@candidate-", "@candidate-fixture.txt", false, true},
		{"file URL prefix retained", "--format", "candidate-fixture.txt", "@file://candidate-", "@file://candidate-fixture.txt", false, true},
		{"data URL prefix retained", "--format", "candidate-fixture.txt", "@data://candidate-", "@data://candidate-fixture.txt", false, true},
		{"embedded at prefix retained", "--format", "candidate-fixture.txt", "field@candidate-", "field@candidate-fixture.txt", false, true},
		{"embedded file URL prefix retained", "--format", "candidate-fixture.txt", "field@file://candidate-", "field@file://candidate-fixture.txt", false, true},
		{"embedded data URL prefix retained", "--format", "candidate-fixture.txt", "field@data://candidate-", "field@data://candidate-fixture.txt", false, true},
		{"file URL with colon retained", "--format", "cert:models-fixture.txt", "@file://cert:models", "@file://cert:models-fixture.txt", false, true},
		{"data URL with colon retained", "--format", "cert:models-fixture.txt", "@data://cert:models", "@data://cert:models-fixture.txt", false, true},
		{"file URL with colon kept in word", "--format", "cert:models-fixture.txt", "@file://cert:models", "@file://cert:models-fixture.txt", true, true},
		{"empty inline value keeps following flag", "--header=", "unused.txt", "--debug models retrieve --mo", "--debug\x00models\x00retrieve\x00--model", false, false},
		{"literal equals value keeps following flag", "--header", "unused.txt", "= --debug models retrieve --mo", "=\x00--debug\x00models\x00retrieve\x00--model", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			fixture := filepath.Join(directory, test.filename)
			require.NoError(t, os.MkdirAll(filepath.Dir(fixture), 0o700))
			require.NoError(t, os.WriteFile(fixture, []byte("synthetic input"), 0o600))
			completionFile := filepath.Join(directory, "completion.bash")
			require.NoError(t, os.WriteFile(completionFile, []byte(rendered), 0o600))
			resultFile := filepath.Join(directory, "inserted.argv")
			rcFile := filepath.Join(directory, "bashrc")
			rc := `
unset HISTFILE
PS1='__COMPLETION_READY__ '
PS2='__COMPLETION_CONTINUE__ '
bind 'set enable-bracketed-paste off'
bind 'set bell-style none'
if ! type mapfile >/dev/null 2>&1; then
  mapfile() {
    COMPREPLY=()
    local line
    while IFS= read -r line; do COMPREPLY+=("$line"); done
  }
fi
openai() {
  if [[ "$1" == "__complete" ]]; then
    "$OPENAI_CLI_TEST_BINARY" -test.run='^TestShellCompletionProtocolHelper$' -- openai "$@"
  else
    # Record what Readline actually inserted; never send an API request.
    printf '%s\0' "$@" > "$OPENAI_CLI_TEST_RESULT"
    exit 0
  fi
}
source "$OPENAI_CLI_TEST_COMPLETION"
`
			if test.keepColonInWord {
				rc += "COMP_WORDBREAKS=${COMP_WORDBREAKS//:}\n"
			}
			if test.keepAtInWord {
				rc += "COMP_WORDBREAKS=${COMP_WORDBREAKS//@}\n"
			}
			require.NoError(t, os.WriteFile(rcFile, []byte(rc), 0o600))
			args := []string{"-q", "/dev/null", bash, "--noprofile", "--rcfile", rcFile, "-i"}
			if runtime.GOOS == "linux" {
				quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
				args = []string{"-q", "-e", "-c", quote(bash) + " --noprofile --rcfile " + quote(rcFile) + " -i", "/dev/null"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, script, args...)
			command.Dir = directory
			command.Env = append(os.Environ(), "TERM=dumb", "INPUTRC=/dev/null", "OPENAI_CLI_COMPLETION_HELPER=1",
				"OPENAI_CLI_TEST_BINARY="+binary, "OPENAI_CLI_TEST_COMPLETION="+completionFile,
				"OPENAI_CLI_TEST_RESULT="+resultFile)
			command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
			command.WaitDelay = 2 * time.Second
			input, err := command.StdinPipe()
			require.NoError(t, err)
			defer input.Close()
			output := &readlineTranscript{ready: make(chan struct{})}
			command.Stdout, command.Stderr = output, output
			require.NoError(t, command.Start())
			select {
			case <-output.ready:
				_, err = fmt.Fprintf(input, "openai %s %s\t\n", test.flag, test.input)
				if err != nil {
					cancel()
				}
			case <-ctx.Done():
				err = ctx.Err()
			}
			waitErr := command.Wait()
			require.NoError(t, err, output.buffer.String())
			require.NoError(t, waitErr, output.buffer.String())
			inserted, err := os.ReadFile(resultFile)
			require.NoError(t, err, output.buffer.String())
			require.Equal(t, test.flag+"\x00"+test.want+"\x00", string(inserted), output.buffer.String())
		})
	}
}
