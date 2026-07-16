package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNullableStreamFlags(t *testing.T) {
	cliPath := buildTestCLI(t)
	inputFile := filepath.Join(t.TempDir(), "input.txt")
	require.NoError(t, os.WriteFile(inputFile, []byte("input"), 0o600))

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "completions create",
			args: []string{"completions", "create", "--model", "model", "--prompt", "prompt"},
		},
		{
			name: "responses create",
			args: []string{"responses", "create", "--model", "model", "--input", "input"},
		},
		{
			name: "beta responses create",
			args: []string{"beta:responses", "create", "--model", "model", "--input", "input"},
		},
		{
			name: "chat completions create",
			args: []string{"chat:completions", "create", "--model", "model", "--message", "{content: input, role: user}"},
		},
		{
			name: "audio transcriptions create",
			args: []string{"audio:transcriptions", "create", "--file", inputFile, "--model", "model"},
		},
		{
			name: "beta thread runs create",
			args: []string{"beta:threads:runs", "create", "--thread-id", "thread", "--assistant-id", "assistant"},
		},
		{
			name: "beta thread runs submit tool outputs",
			args: []string{"beta:threads:runs", "submit-tool-outputs", "--thread-id", "thread", "--run-id", "run", "--tool-output", "{output: output, tool_call_id: call}"},
		},
		{
			name: "beta threads create and run",
			args: []string{"beta:threads", "create-and-run", "--assistant-id", "assistant"},
		},
		{
			name: "images edit",
			args: []string{"images", "edit", "--image", inputFile, "--prompt", "prompt"},
		},
		{
			name: "images generate",
			args: []string{"images", "generate", "--prompt", "prompt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()

			args := append(baseTestCLIArgs(server.URL), tt.args...)
			args = append(args, "--stream", "true", "--max-items", "1")
			output, err := runTestCLI(t, cliPath, args...)
			require.NoError(t, err, "output: %s", output)
			require.Contains(t, string(output), "{}")
		})
	}

	t.Run("null selects the non-streaming handler", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"completion"}`)
		}))
		defer server.Close()

		args := append(baseTestCLIArgs(server.URL), "completions", "create", "--model", "model", "--prompt", "prompt", "--stream", "null")
		output, err := runTestCLI(t, cliPath, args...)
		require.NoError(t, err, "output: %s", output)
		require.Contains(t, string(output), `"id":"completion"`)
	})

	t.Run("writes SSE output before the response completes", func(t *testing.T) {
		eventsSent := make(chan struct{})
		releaseResponse := make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(releaseResponse)
			}
		}()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming unsupported", http.StatusInternalServerError)
				return
			}
			for i := range 40 {
				fmt.Fprintf(w, "data: {\"id\":\"event-%d\"}\n\n", i)
				flusher.Flush()
			}
			close(eventsSent)
			<-releaseResponse
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		args := append(baseTestCLIArgs(server.URL), "completions", "create", "--model", "model", "--prompt", "prompt", "--stream", "true")
		command := exec.CommandContext(ctx, cliPath, args...)
		command.Env = append(os.Environ(), "PAGER=cat")
		stdout, err := command.StdoutPipe()
		require.NoError(t, err)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		require.NoError(t, command.Start())

		select {
		case <-eventsSent:
		case <-ctx.Done():
			require.FailNow(t, "server did not send events")
		}

		outputStarted := make(chan error, 1)
		go func() {
			buffer := make([]byte, 1)
			_, readErr := io.ReadFull(stdout, buffer)
			outputStarted <- readErr
		}()

		select {
		case readErr := <-outputStarted:
			require.NoError(t, readErr)
		case <-ctx.Done():
			require.FailNow(t, "command did not emit streamed output before the response completed")
		}

		close(releaseResponse)
		released = true
		require.NoError(t, command.Wait(), "stderr: %s", stderr.String())
	})
}

func buildTestCLI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openai")
	command := exec.Command("go", "build", "-o", path, "../../cmd/openai")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "output: %s", output)
	return path
}

func baseTestCLIArgs(baseURL string) []string {
	return []string{
		"--base-url", baseURL,
		"--format", "raw",
		"--api-key", "key",
	}
}

func runTestCLI(t *testing.T, path string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	command.Env = append(os.Environ(), "PAGER=cat")
	return command.CombinedOutput()
}
