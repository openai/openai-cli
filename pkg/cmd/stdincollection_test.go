package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStdinQueryCollectionsEndToEnd(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../../cmd/openai")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_synthetic","object":"response","output":[]}`)
	}))
	defer server.Close()
	run := func(t *testing.T, stdin, mode string, flags ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		args := append([]string{"--base-url", server.URL, "--api-key", "synthetic-key", "responses", "retrieve", "--response-id", "resp_synthetic"}, flags...)
		command := exec.CommandContext(ctx, binary, args...)
		for _, v := range os.Environ() {
			if !strings.HasPrefix(v, "OPENAI_") {
				command.Env = append(command.Env, v)
			}
		}
		command.Env = append(command.Env, "OPENAI_UNTRUSTED_STDIN="+mode)
		command.Stdin = strings.NewReader(stdin)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		stdout, err := command.Output()
		require.NoError(t, err, "stdout=%s stderr=%s", stdout, stderr.String())
		require.Empty(t, stderr.String())
		select {
		case query := <-requests:
			t.Logf("exit=0 stderr=empty query=%s", query)
			return query
		default:
			t.Fatal("CLI did not send a request")
			return ""
		}
	}
	for _, mode := range []string{"false", "true"} {
		t.Run(mode, func(t *testing.T) {
			explicit := run(t, "", mode, "--include", "first", "--include", "second")
			require.Equal(t, "include%5B%5D=first&include%5B%5D=second", explicit)
			for _, input := range []string{`{"include":["first","second"]}`, "include:\n  - first\n  - second\n"} {
				require.Equal(t, explicit, run(t, input, mode))
				require.Equal(t, "include%5B%5D=explicit", run(t, input, mode, "--include", "explicit"))
			}
			require.Empty(t, run(t, `{"include":[]}`, mode))
		})
	}
	t.Run("untrusted references stay literal and explicit references remain trusted", func(t *testing.T) {
		path := filepath.ToSlash(filepath.Join(t.TempDir(), "synthetic.txt"))
		require.NoError(t, os.WriteFile(path, []byte("SYNTHETIC_FILE_CONTENT"), 0600))
		values := []string{"@" + path, "@file://" + path, "@data://" + path, "\\@" + path}
		input, err := json.Marshal(map[string]any{"include": values})
		require.NoError(t, err)
		query := run(t, string(input), "true")
		require.NotContains(t, query, "SYNTHETIC_FILE_CONTENT")
		decoded, err := url.ParseQuery(query)
		require.NoError(t, err)
		require.Equal(t, values, decoded["include[]"])
		require.Equal(t, "include%5B%5D=SYNTHETIC_FILE_CONTENT", run(t, string(input), "true", "--include", "@"+path))
	})
}
