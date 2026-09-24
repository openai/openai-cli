package custom

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const prettyColorInput = `{"id":"abc","count":3,"ok":true,"none":null,"items":[1,2]}`

// clearColorEnv unsets the variables that affect color detection so a test
// starts from a known environment. t.Setenv restores the old values.
func clearColorEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"NO_COLOR", "FORCE_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "TTY_FORCE"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
}

func TestPrettyOutputIsPlainWhenNotATerminal(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { readPipe.Close() })
	t.Cleanup(func() { writePipe.Close() })

	file, err := os.CreateTemp(t.TempDir(), "pretty")
	require.NoError(t, err)
	t.Cleanup(func() { file.Close() })

	destinations := []struct {
		name   string
		stdout *os.File
		dest   io.Writer
	}{
		{name: "buffer", stdout: writePipe, dest: &bytes.Buffer{}},
		{name: "pipe", stdout: writePipe, dest: writePipe},
		{name: "file", stdout: file, dest: file},
	}

	for _, noColor := range []bool{false, true} {
		for _, d := range destinations {
			name := d.name
			if noColor {
				name += "/NO_COLOR=1"
			}
			t.Run(name, func(t *testing.T) {
				clearColorEnv(t)
				t.Setenv("TERM", "xterm-256color")
				if noColor {
					t.Setenv("NO_COLOR", "1")
				}

				formatted, err := formatJSONForOutput(gjson.Parse(prettyColorInput), ShowJSONOpts{
					Format: "pretty",
					Title:  "Result",
					Stdout: d.stdout,
				}, d.dest)
				require.NoError(t, err)
				require.NotContains(t, string(formatted), "\x1b[")
				require.Contains(t, string(formatted), "count")
			})
		}
	}
}

func TestPrettyOutputHonorsForcedColor(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { readPipe.Close() })
	t.Cleanup(func() { writePipe.Close() })

	tests := []struct {
		name      string
		env       map[string]string
		wantColor bool
	}{
		{name: "FORCE_COLOR=1", env: map[string]string{"FORCE_COLOR": "1"}, wantColor: true},
		{name: "CLICOLOR_FORCE=1", env: map[string]string{"CLICOLOR_FORCE": "1"}, wantColor: true},
		{name: "FORCE_COLOR=0 with CLICOLOR_FORCE=1", env: map[string]string{"FORCE_COLOR": "0", "CLICOLOR_FORCE": "1"}, wantColor: false},
		{name: "NO_COLOR=1 with CLICOLOR_FORCE=1", env: map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"}, wantColor: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearColorEnv(t)
			t.Setenv("TERM", "xterm-256color")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			formatted, err := formatJSONForOutput(gjson.Parse(prettyColorInput), ShowJSONOpts{
				Format: "pretty",
				Title:  "Result",
				Stdout: writePipe,
			}, &bytes.Buffer{})
			require.NoError(t, err)
			require.Equal(t, tt.wantColor, bytes.Contains(formatted, []byte("\x1b[")))
		})
	}
}

func TestPrettyOutputKeepsColorOnTerminal(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("requires an interactive stdout terminal")
	}
	clearColorEnv(t)

	formatted, err := formatJSONForOutput(gjson.Parse(prettyColorInput), ShowJSONOpts{
		Format: "pretty",
		Title:  "Result",
		Stdout: os.Stdout,
	}, os.Stdout)
	require.NoError(t, err)
	require.Contains(t, string(formatted), "\x1b[")
}
