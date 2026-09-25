//go:build !windows

package main

import (
	"bytes"
	"context"
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
	for _, format := range []string{"text", "jsonl", "raw"} {
		t.Run(format, func(t *testing.T) {
			disconnected := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				// Large enough to reach the machine-format output path as well as
				// the live text writer while the remote stream remains open.
				writeStreamingTextEvent(w, `{"type":"response.output_text.delta","item_id":"msg_pipe","output_index":0,"content_index":0,"delta":"`+strings.Repeat("x", 8000)+`"}`)
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
			if err := child.Run(); err != nil || stderr.Len() != 0 {
				t.Fatalf("closed stdout must finish cleanly: %v; stderr=%q", err, stderr.String())
			}
			select {
			case <-disconnected:
			case <-ctx.Done():
				t.Fatal("closed stdout left the upstream stream open")
			}
		})
	}
}
