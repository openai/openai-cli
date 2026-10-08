package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const fileReceiptFixture = `{"object":"file","id":"file-example","filename":"upload space.txt","purpose":"user_data","bytes":13}`

func TestFileResultPreservesNonterminalOutput(t *testing.T) {
	for _, route := range []string{"", fileUploadCommand, fileGetCommand, "download"} {
		for _, format := range []string{"", "auto", "text", "json", "jsonl", "raw", "yaml", "pretty", "explore"} {
			t.Run(route+"/"+format, func(t *testing.T) {
				for _, extraction := range []string{"", "id"} {
					for _, raw := range []bool{false, true} {
						var stdout, stderr bytes.Buffer
						opts := ShowJSONOpts{
							Context:   context.WithValue(t.Context(), fileCommandKey{}, route),
							Operation: "(resource) files > (method) create", OutputKind: OutputResponse,
							Format: format, ExplicitFormat: format != "", Transform: extraction, RawOutput: raw,
							Stdout: &stdout, Stderr: &stderr,
						}
						handled, err := showFileResult(gjson.Parse(fileReceiptFixture), opts)
						require.NoError(t, err)
						require.False(t, handled)
						require.Empty(t, stdout.String())
						require.Empty(t, stderr.String())
					}
				}
			})
		}
	}
}

func TestFileReceiptRequiresSuccessfulFileShape(t *testing.T) {
	require.True(t, validFileReceipt(gjson.Parse(fileReceiptFixture)))
	for _, raw := range []string{
		`null`, `[]`, `{"object":"batch","id":"x","filename":"x","purpose":"x"}`,
		`{"object":"file","id":"","filename":"x","purpose":"x"}`,
		`{"object":"file","id":123,"filename":"x","purpose":"x"}`,
		`{"object":"file","id":"x","filename":null,"purpose":"x"}`,
		`{"object":"file","id":"x","filename":"x"}`,
		`{"object":"file","id":"x","filename":"x","purpose":"x"`,
	} {
		require.False(t, validFileReceipt(gjson.Parse(raw)), raw)
	}
}

func TestFileReceiptValuesAndUnknownShell(t *testing.T) {
	for _, tc := range []struct{ bytes, suffix string }{{"13", " (13 B)"}, {"0", " (0 B)"}, {"null", ""}, {"9007199254740993", " (9007199254740993 B)"}} {
		value := gjson.Parse(strings.Replace(fileReceiptFixture, `"bytes":13`, `"bytes":`+tc.bytes, 1))
		for _, shell := range []string{"", "unknown-shell"} {
			var out bytes.Buffer
			require.NoError(t, writeFileReceipt(&out, value, shell))
			require.Equal(t, "Uploaded upload space.txt"+tc.suffix+"\nID: file-example\nPurpose: user_data\n", out.String())
		}
	}
}

func TestFileReceiptFallsBackForInvalidByteCounts(t *testing.T) {
	for _, count := range []string{`-1`, `1.5`, `"13"`, `1e3`, `true`, `{}`, `[]`} {
		value := gjson.Parse(strings.Replace(fileReceiptFixture, `"bytes":13`, `"bytes":`+count, 1))
		require.False(t, validFileReceipt(value), count)
	}
	for _, count := range []string{`0`, `13`, `9007199254740993`, `null`} {
		value := gjson.Parse(strings.Replace(fileReceiptFixture, `"bytes":13`, `"bytes":`+count, 1))
		require.True(t, validFileReceipt(value), count)
	}
}

func TestFileReceiptEscapesFieldsAndRetainsProcessingFailure(t *testing.T) {
	value := gjson.Parse(`{"object":"file","id":"file-\nID: spoof\u001b","filename":"a\r\n\t\u202e.txt","purpose":"user_data\u0007","bytes":0,"status":"error","status_details":"failed\nPurpose: spoof"}`)
	var out bytes.Buffer
	require.NoError(t, writeFileReceipt(&out, value, ""))
	require.Equal(t, "Uploaded a\\r\\n\\t\\u202e.txt (0 B)\nID: file-\\nID: spoof\\u001b\nPurpose: user_data\\u0007\nStatus: error\nStatus details: failed\\nPurpose: spoof\n", out.String())
	for _, control := range []string{"\r", "\t", "\x1b", "\x07", "\u202e"} {
		require.NotContains(t, out.String(), control)
	}
}

func TestFileReceiptCommandsPreserveShellArguments(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is unavailable", shell)
			}
			for _, tc := range []struct{ id, filename string }{
				{"file-example", "upload space.txt"},
				{"-file-example", "-notes.txt"},
				{"file-example", "-"},
				{"file-'quote", "an owner's $HOME `literal` $(printf BAD) file.txt"},
				{"file-example", "@literal.txt"},
				{"file-example", "line\nwith\tcontrols\x1b.txt"},
			} {
				data, err := json.Marshal(map[string]any{"object": "file", "id": tc.id, "filename": tc.filename, "purpose": "user_data", "bytes": 0})
				require.NoError(t, err)
				var out bytes.Buffer
				require.NoError(t, writeFileReceipt(&out, gjson.ParseBytes(data), shell))
				_, command, found := strings.Cut(out.String(), "\nDownload it: ")
				require.True(t, found)
				command = strings.TrimSuffix(command, "\n")
				require.NotContains(t, command, "\n")
				require.NotContains(t, command, "\x1b")
				args := []string{"-f", "-c"}
				if shell == "bash" {
					args = []string{"--noprofile", "--norc", "-c"}
				}
				process := exec.Command(binary, append(args, "openai() { printf '%s\\0' \"$@\"; }\n"+command)...)
				process.Dir = t.TempDir()
				process.Env = []string{"LC_ALL=C"}
				got, err := process.CombinedOutput()
				require.NoError(t, err, string(got))
				id, filename := tc.id, tc.filename
				if strings.HasPrefix(id, "-") {
					id = "--file-id=" + id
				}
				if filename == "-" {
					filename = "./-"
				}
				want := []string{"files", "download", id, "--output", filename}
				require.Equal(t, strings.Join(want, "\x00")+"\x00", string(got), command)
			}
		})
	}
}

func TestFileReceiptPropagatesOutputFailuresAndCancellation(t *testing.T) {
	value := gjson.Parse(fileReceiptFixture)
	failure := errors.New("synthetic output failure")
	for _, tc := range []struct{ returned, want error }{{failure, failure}, {nil, io.ErrShortWrite}} {
		out := outputWriter{ctx: t.Context(), out: failOutputWriter{tc.returned}}
		require.ErrorIs(t, writeFileReceipt(out, value, "bash"), tc.want)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out bytes.Buffer
	require.ErrorIs(t, writeFileReceipt(outputWriter{ctx: ctx, out: &out}, value, "bash"), context.Canceled)
	require.Empty(t, out.String())
}

func TestFileReceiptOmitsAmbiguousDestinations(t *testing.T) {
	for _, tc := range []struct{ id, filename string }{
		{"file-example", "../outside.txt"},
		{"file-example", "/dev/stdout"},
		{"file-example", `folder\file.txt`},
		{"file-example", "."},
		{"file-example", ".."},
		{"file-example", "nul\x00.txt"},
		{"file-\x00example", "safe.txt"},
	} {
		data, err := json.Marshal(map[string]any{"object": "file", "id": tc.id, "filename": tc.filename, "purpose": "user_data"})
		require.NoError(t, err)
		var out bytes.Buffer
		require.NoError(t, writeFileReceipt(&out, gjson.ParseBytes(data), "bash"))
		require.Contains(t, out.String(), "Uploaded ")
		require.NotContains(t, out.String(), "Download it:")
		require.NotContains(t, out.String(), "\x00")
	}
}
