package custom

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
)

func TestEmbedNullableStrings(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "synthetic.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic content"), 0600))
	for _, style := range []FileEmbedStyle{EmbedText, EmbedIOReader} {
		got, err := embedFiles(map[string]any{
			"reference": requestflag.Ptr("@" + path),
			"escaped":   requestflag.Ptr(`\@literal`),
			"null":      (*string)(nil),
			"number":    requestflag.Ptr[int64](42),
		}, style, nil)
		require.NoError(t, err)
		fields := got.(map[string]any)
		if style == EmbedIOReader {
			reader := fields["reference"].(io.ReadCloser)
			data, err := io.ReadAll(reader)
			require.NoError(t, reader.Close())
			require.NoError(t, err)
			fields["reference"] = string(data)
		}
		encoded, err := json.Marshal(fields)
		require.NoError(t, err)
		require.JSONEq(t, `{"reference":"synthetic content","escaped":"@literal","null":null,"number":42}`, string(encoded))
	}
}

func TestNullableFileReferencesEndToEnd(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai.exe")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/openai")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "synthetic.txt"))
	const content = "synthetic instructions"
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	requests := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_synthetic","object":"response","output":[]}`)
	}))
	defer server.Close()
	for _, tt := range []struct {
		name, stdin string
		flags       []string
		want        any
		present     bool
		wantErr     bool
	}{
		{name: "implicit file", flags: []string{"--instructions", "@" + path}, want: content, present: true},
		{name: "text file", flags: []string{"--instructions", "@file://" + path}, want: content, present: true},
		{name: "base64 file", flags: []string{"--instructions", "@data://" + path}, want: base64.StdEncoding.EncodeToString([]byte(content)), present: true},
		{name: "escaped", flags: []string{"--instructions", `\@literal`}, want: "@literal", present: true},
		{name: "null", flags: []string{"--instructions", "null"}, present: true},
		{name: "unset"},
		{name: "missing file", flags: []string{"--instructions", "@file://" + path + ".missing"}, wantErr: true},
		{name: "untrusted stdin", stdin: `{"instructions":"@file://` + path + `"}`, want: "@file://" + path, present: true},
		{name: "explicit override", stdin: `{"instructions":"@file://` + path + `"}`, flags: []string{"--instructions", "@" + path}, want: content, present: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			args := append([]string{"--base-url", server.URL, "responses", "create", "--model", "synthetic-model"}, tt.flags...)
			command := exec.CommandContext(ctx, binary, args...)
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "OPENAI_") {
					command.Env = append(command.Env, entry)
				}
			}
			command.Env = append(command.Env, "OPENAI_API_KEY=sk-fake-test", "OPENAI_UNTRUSTED_STDIN=true")
			command.Stdin = strings.NewReader(tt.stdin)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			_, err := command.Output()
			var body []byte
			select {
			case body = <-requests:
				// The handler queues the body before sending the response.
			default:
			}
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, stderr.String(), ".missing")
				require.Nil(t, body)
				return
			}
			require.NoError(t, err, "%s", stderr.String())
			require.Empty(t, stderr.String())
			require.NotNil(t, body, "CLI did not send a request")
			var fields map[string]any
			require.NoError(t, json.Unmarshal(body, &fields))
			value, exists := fields["instructions"]
			require.Equal(t, tt.present, exists)
			require.Equal(t, tt.want, value)
		})
	}
}
