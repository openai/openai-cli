//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMainStreamingTextInterruptPreservesVisibleOutput(t *testing.T) {
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamingTextEvent(w, `{"type":"response.output_text.delta","item_id":"msg_interrupt","output_index":0,"content_index":0,"delta":"Visible before interrupt"}`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(disconnected)
	}))
	t.Cleanup(server.Close)
	child, stdout, _, ctx := startStreamingTextCommand(t, server, streamingTextArgs("responses")...)
	readStreamingTextPrefix(t, ctx, stdout, "Visible before interrupt")
	if err := child.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("interrupted CLI reported success")
	}
	select {
	case <-disconnected:
	case <-ctx.Done():
		t.Fatal("interrupted CLI did not close the stalled HTTP stream")
	}
}

func TestMainStreamingTextClosedStdoutPipe(t *testing.T) {
	for _, tc := range []struct {
		name, event, message string
		formats              []string
		code                 int
	}{
		{
			name: "consumer closed",
			// Reach buffered machine output while the remote stream stays open.
			event:   `{"type":"response.output_text.delta","item_id":"msg_pipe","output_index":0,"content_index":0,"delta":"` + strings.Repeat("x", 8000) + `"}`,
			formats: []string{"text", "jsonl", "raw"},
		},
		{
			name:    "upstream failed",
			event:   `{"type":"response.failed","response":{"status":"failed","error":{"message":"synthetic-private-detail https://secret.invalid/?token=fake\u001b[2J"}}}`,
			message: "the streamed response failed\nOutput may be incomplete.",
			formats: []string{"text", "json", "jsonl", "raw"},
			code:    1,
		},
	} {
		for _, format := range tc.formats {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				disconnected := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					writeStreamingTextEvent(w, tc.event)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					close(disconnected)
				}))
				t.Cleanup(server.Close)
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				reader.Close()
				defer writer.Close()
				binary, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				args := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, streamingTextArgs("responses", "--format", format)...)
				child := exec.CommandContext(ctx, binary, args...)
				child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-streaming-pipe-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
				var stderr bytes.Buffer
				child.Stdout, child.Stderr = writer, &stderr
				if err := child.Run(); ctx.Err() != nil || child.ProcessState == nil || child.ProcessState.ExitCode() != tc.code {
					t.Fatalf("closed stdout must exit %d: %v; stderr=%q", tc.code, err, stderr.String())
				}
				message := strings.TrimSpace(stderr.String())
				if format != "text" && tc.message != "" {
					var diagnostic struct {
						Message string `json:"message"`
					}
					if err := json.Unmarshal(stderr.Bytes(), &diagnostic); err != nil {
						t.Fatalf("invalid structured diagnostic: %q: %v", stderr.String(), err)
					}
					message = diagnostic.Message
				}
				if message != tc.message {
					t.Fatalf("closed stdout diagnostic = %q, want %q", message, tc.message)
				}
				for _, private := range []string{"synthetic-private-detail", "secret.invalid", "token=", "\x1b"} {
					if strings.Contains(stderr.String(), private) {
						t.Fatalf("closed stdout diagnostic exposed API data: %q", stderr.String())
					}
				}
				select {
				case <-disconnected:
				case <-ctx.Done():
					t.Fatal("closed stdout left the upstream stream open")
				}
			})
		}
	}
}
