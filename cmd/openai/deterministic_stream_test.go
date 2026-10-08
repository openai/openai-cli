package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The server cannot send its terminal event until the consumer sees output.
// This tests the real stdout path, including its process-level pipe handling.
func TestMainDispatchOutputStructuredStreamEmitsBeforeNextEvent(t *testing.T) {
	for _, format := range []string{"json", "jsonl", "yaml", "raw", "pretty", "explore"} {
		t.Run(format, func(t *testing.T) {
			release := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamingTextEvent(w, `{"type":"response.output_text.delta","delta":"synthetic first event","sequence_number":0}`)
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				writeStreamingTextEvent(w, `{"type":"response.completed","response":{"id":"resp_synthetic","status":"completed","output":[]}}`)
				io.WriteString(w, "data: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(release) })
			child, stdout, stderr, ctx := startStreamingTextCommand(t, server, streamingTextArgs("responses", "--format", format)...)
			prefix := make([]byte, 1)
			read := make(chan error, 1)
			go func() { _, err := io.ReadFull(stdout, prefix); read <- err }()
			select {
			case err := <-read:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("first event stayed buffered while the server withheld the next event")
			}
			release <- struct{}{}
			rest, err := io.ReadAll(stdout)
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err != nil {
				t.Fatalf("stream failed: %v; stderr=%q", err, stderr.String())
			}
			if format != "explore" && stderr.Len() != 0 {
				t.Fatalf("unexpected diagnostics: %q", stderr.String())
			}
			output := string(prefix) + string(rest)
			if !strings.Contains(output, "synthetic first event") || !strings.Contains(output, "resp_synthetic") {
				t.Fatalf("missing event data: %q", output)
			}
			if format == "json" || format == "jsonl" || format == "raw" || format == "explore" {
				decoder := json.NewDecoder(strings.NewReader(output))
				for range 2 {
					var event map[string]any
					if err := decoder.Decode(&event); err != nil {
						t.Fatalf("invalid JSON record: %v", err)
					}
				}
				var extra any
				if err := decoder.Decode(&extra); err != io.EOF {
					t.Fatalf("unexpected trailing data: %v", err)
				}
			}
			if format == "jsonl" && strings.Count(output, "\n") != 2 {
				t.Fatalf("JSONL must emit exactly one line per event: %q", output)
			}
		})
	}
}
