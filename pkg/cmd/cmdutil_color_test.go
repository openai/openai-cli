package cmd

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/pretty"
)

// An unset value is distinct from an explicitly empty environment variable.
func setColorEnvironment(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value)
	if value == "unset" {
		require.NoError(t, os.Unsetenv(key))
	}
}

func TestJSONColorEnvironment(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	defer file.Close()
	for _, destination := range []struct {
		name string
		file *os.File
	}{{"file", file}, {"stdout", os.Stdout}} {
		for _, format := range []string{"json", "jsonl", "auto"} {
			for _, noColor := range []string{"unset", "", "1", "0"} {
				for _, force := range []string{"unset", "", "0", "1", "auto"} {
					t.Run(fmt.Sprintf("%s/%s/no=%s/force=%s", destination.name, format, noColor, force), func(t *testing.T) {
						setColorEnvironment(t, "NO_COLOR", noColor)
						setColorEnvironment(t, "FORCE_COLOR", force)
						res := gjson.Parse(`{"data":{"text":"quote\" slash\\ newline\n escape\u001b[31m","n":2}}`)
						plain := []byte("{\n  \"text\": \"quote\\\" slash\\\\ newline\\n escape\\u001b[31m\",\n  \"n\": 2\n}\n")
						if format == "jsonl" {
							plain = []byte("{\"text\":\"quote\\\" slash\\\\ newline\\n escape\\u001b[31m\",\"n\":2}\n")
						}
						wantColor := force == "1" || (force != "0" && (noColor == "unset" || noColor == "") && isTerminal(destination.file))
						want := plain
						if wantColor {
							want = pretty.Color(plain, pretty.TerminalStyle)
						}
						actual, err := formatJSONForOutput(res, ShowJSONOpts{Format: format, Transform: "data", Stdout: file}, destination.file)
						require.NoError(t, err)
						require.Equal(t, string(want), string(actual))
					})
				}
			}
		}
	}
}

func TestJSONColorEnvironmentPagerIterator(t *testing.T) {
	for _, format := range []string{"json", "jsonl"} {
		for _, noColor := range []string{"1", "0"} {
			t.Run(format+"/no="+noColor, func(t *testing.T) {
				outputPath := configureCapturePager(t)
				setColorEnvironment(t, "FORCE_COLOR", "unset")
				t.Setenv("NO_COLOR", noColor)
				items := make([]map[string]any, 64)
				for i := range items {
					items[i] = map[string]any{"data": map[string]any{"message": "safe"}}
				}
				var stderr bytes.Buffer
				iter := &sliceIterator[map[string]any]{items: items}
				require.NoError(t, ShowJSONIterator(iter, -1, ShowJSONOpts{Format: format, Transform: "data", Stderr: &stderr}))
				output, err := os.ReadFile(outputPath)
				require.NoError(t, err)
				line := "{\n  \"message\": \"safe\"\n}\n"
				if format == "jsonl" {
					line = "{\"message\":\"safe\"}\n"
				}
				require.Equal(t, bytes.Repeat([]byte(line), len(items)), output)
				require.Empty(t, stderr.String())
			})
		}
	}
}
