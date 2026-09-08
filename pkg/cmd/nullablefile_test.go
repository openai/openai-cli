package cmd

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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNullableStringEmbeddingReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic upload"), 0600))
	for _, prefix := range []string{"@", "@file://", "@data://"} {
		t.Run(prefix, func(t *testing.T) {
			value := prefix + path
			got, err := embedFiles(&value, EmbedIOReader, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, closeFileUploads(got)) })
			reader, ok := got.(io.Reader)
			require.True(t, ok, "expected upload reader, got %T", got)
			data, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, "synthetic upload", string(data))
			require.Equal(t, prefix+path, value, "input pointer must not be mutated")
		})
	}
	for _, style := range []FileEmbedStyle{EmbedText, EmbedIOReader} {
		var null *string
		got, err := embedFiles(null, style, nil)
		require.NoError(t, err)
		require.Equal(t, null, got)
		number := 1
		got, err = embedFiles(&number, style, nil)
		require.NoError(t, err)
		require.Same(t, &number, got, "non-string pointers remain untouched")
	}
}

func TestNullableStringFileReferencesCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.txt")
	const content = "SYNTHETIC_NULLABLE_CONTENT"
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	binaryPath := filepath.Join(t.TempDir(), "synthetic.bin")
	binaryContent := []byte{0xff, 0x00, 0xfe}
	require.NoError(t, os.WriteFile(binaryPath, binaryContent, 0600))
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../../cmd/openai")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	requests := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"vs_synthetic","object":"vector_store","output":[]}`)
	}))
	defer server.Close()
	update := []string{"vector-stores", "update", "--vector-store-id", "vs_synthetic"}
	named := func(value string) []string {
		return append(append([]string{}, update...), "--name", value)
	}
	piped, err := json.Marshal(map[string]string{"name": "@" + path})
	require.NoError(t, err)
	tests := []struct {
		name, stdin, mode string
		args              []string
		want              map[string]any
		wantError         bool
	}{
		{name: "ordinary string file", args: []string{"vector-stores", "create", "--name", "@" + path}, want: map[string]any{"name": content}},
		{name: "nullable file", args: named("@" + path), want: map[string]any{"name": content}},
		{name: "file URI", args: named("@file://" + path), want: map[string]any{"name": content}},
		{name: "data URI", args: named("@data://" + path), want: map[string]any{"name": base64.StdEncoding.EncodeToString([]byte(content))}},
		{name: "binary sniffing", args: named("@" + binaryPath), want: map[string]any{"name": base64.StdEncoding.EncodeToString(binaryContent)}},
		{name: "null", args: named("null"), want: map[string]any{"name": nil}},
		{name: "unset", args: update, want: map[string]any{}},
		{name: "ordinary value", args: named("ordinary"), want: map[string]any{"name": "ordinary"}},
		{name: "escaped", args: named("\\@" + path), want: map[string]any{"name": "@" + path}},
		{name: "username fallback", args: named("@synthetic_absent_username"), want: map[string]any{"name": "@synthetic_absent_username"}},
		{name: "missing file", args: named("@" + filepath.Join(t.TempDir(), "missing.txt")), wantError: true},
		{name: "untrusted explicit", mode: "true", args: named("@" + path), want: map[string]any{"name": content}},
		{name: "untrusted pipe", mode: "true", stdin: string(piped), args: update, want: map[string]any{"name": "@" + path}},
		{name: "explicit override", mode: "true", stdin: string(piped), args: named("@data://" + path), want: map[string]any{"name": base64.StdEncoding.EncodeToString([]byte(content))}},
		{name: "nullable inner explicit", mode: "true", stdin: `{"prompt":{"version":"PIPE_VALUE"}}`, args: []string{"responses", "create", "--prompt.version", "@" + path}, want: map[string]any{"prompt": map[string]any{"version": content}}},
		{name: "nullable inner pipe", mode: "true", stdin: "prompt:\n  version: '@" + filepath.ToSlash(path) + "'\n", args: []string{"responses", "create"}, want: map[string]any{"prompt": map[string]any{"version": "@" + filepath.ToSlash(path)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			args := append([]string{"--base-url", server.URL, "--api-key", "synthetic-key"}, tt.args...)
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = t.TempDir()
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "OPENAI_") {
					command.Env = append(command.Env, entry)
				}
			}
			if tt.mode != "" {
				command.Env = append(command.Env, untrustedStdinEnv+"="+tt.mode)
			}
			command.Stdin = strings.NewReader(tt.stdin)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			stdout, err := command.Output()
			var body []byte
			select {
			case body = <-requests:
			default:
			}
			if tt.wantError {
				require.Error(t, err)
				require.Contains(t, stderr.String(), "missing.txt")
				require.Nil(t, body, "unexpected request: %s", body)
				return
			}
			require.NoError(t, err, "stdout=%s stderr=%s", stdout, stderr.String())
			require.Empty(t, stderr.String())
			require.NotNil(t, body, "CLI sent no request")
			t.Logf("exit=0 stderr=empty body=%s", body)
			var got map[string]any
			require.NoError(t, json.Unmarshal(body, &got))
			require.Equal(t, tt.want, got)
		})
	}
}
