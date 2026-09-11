package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise the public CLI entrypoint: nested flags and piped input must survive
// the CLI request overlay and the Go SDK serializer with the same GA wire shape.
func TestLiveSIPRequestContract(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "openai")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "../../cmd/openai").CombinedOutput()
	require.NoError(t, err, "building CLI: %s", output)

	tests := []struct {
		name   string
		action string
		flags  []string
		stdin  string
		body   string
	}{
		{name: "accept nested flags", action: "accept", flags: []string{
			"--session.type", "live", "--session.model", "gpt-live-1",
			"--session.instructions", "Help the caller.",
		}, body: `{"session":{"type":"live","model":"gpt-live-1","instructions":"Help the caller."}}`},
		{name: "accept piped history", action: "accept",
			stdin: `{"session":{"type":"live","model":"gpt-live-1","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Help with my order."}]}]}}`,
			body:  `{"session":{"type":"live","model":"gpt-live-1","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Help with my order."}]}]}}`},
		{name: "refer", action: "refer", flags: []string{"--target-uri", "sip:agent@example.com"}, body: `{"target_uri":"sip:agent@example.com"}`},
		{name: "reject explicit status", action: "reject", flags: []string{"--status-code", "486"}, body: `{"status_code":486}`},
		{name: "hangup", action: "hangup"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type request struct {
				method, path, body string
				header             http.Header
			}
			received := make(chan request, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, readErr := io.ReadAll(r.Body)
				if readErr != nil {
					http.Error(w, "cannot read request", 500)
					return
				}
				received <- request{r.Method, r.URL.Path, string(body), r.Header.Clone()}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			args := []string{"--base-url", server.URL, "--api-key", "synthetic-test-key", "live:sessions", test.action, "--session-id", "session_test"}
			args = append(args, test.flags...)
			command := exec.CommandContext(t.Context(), binary, args...)
			command.Stdin = strings.NewReader(test.stdin)
			// Keep local credentials, endpoint overrides, and mTLS settings out.
			command.Env = []string{"FORCE_COLOR=0"}
			output, err := command.CombinedOutput()
			require.NoError(t, err, "CLI: %s", output)
			select {
			case got := <-received:
				require.Equal(t, "POST", got.method)
				require.Equal(t, "/live/sessions/session_test/"+test.action, got.path)
				require.Empty(t, got.header.Get("OpenAI-Alpha"))
				require.Empty(t, got.header.Get("OpenAI-Beta"))
				if test.body == "" {
					require.Empty(t, got.body)
				} else {
					require.JSONEq(t, test.body, got.body)
				}
			default:
				t.Fatal("CLI did not send a request")
			}
		})
	}
}
