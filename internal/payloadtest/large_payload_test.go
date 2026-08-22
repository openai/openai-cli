package payloadtest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestLargeResponsePayloads(t *testing.T) {
	// High memory use is intentional. Do not shrink these synthetic payloads or
	// raise client limits to make arbitrary caps pass. This probes compatibility,
	// not an API maximum. Keep the cases sequential to bound peak memory.
	payload := strings.Repeat("x", (32<<20)+1)
	binary := filepath.Join(t.TempDir(), "openai.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/openai")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	for _, tc := range []struct {
		name, command, path   string
		contentType           string
		prefix, suffix        string
		textPath              string
		finalPath, finalValue string
		stream                bool
	}{
		{
			name: "responses JSON", command: "responses", path: "/responses",
			contentType: "application/json",
			prefix:      `{"id":"resp_large","object":"response","status":"completed","output":[{"id":"msg_large","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"`,
			suffix:      `","annotations":[]}]}]}`,
			textPath:    "output.0.content.0.text",
		},
		{
			name: "responses SSE", command: "responses", path: "/responses", stream: true,
			contentType: "text/event-stream",
			prefix:      "event: response.output_text.delta\ndata: " + `{"type":"response.output_text.delta","item_id":"msg_large","output_index":0,"content_index":0,"sequence_number":0,"delta":"`,
			suffix: `"}` + "\n\nevent: response.completed\ndata: " +
				`{"type":"response.completed","sequence_number":1,"response":{"id":"resp_large","object":"response","status":"completed","output":[]}}` + "\n\n",
			textPath: "delta", finalPath: "type", finalValue: "response.completed",
		},
		{
			name: "chat completions SSE", command: "chat:completions", path: "/chat/completions", stream: true,
			contentType: "text/event-stream",
			prefix:      `data: {"id":"chat_large","object":"chat.completion.chunk","created":0,"model":"fake-model","choices":[{"index":0,"delta":{"role":"assistant","content":"`,
			suffix: `"},"finish_reason":null}]}` + "\n\ndata: " +
				`{"id":"chat_large","object":"chat.completion.chunk","created":0,"model":"fake-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n",
			textPath: "choices.0.delta.content", finalPath: "choices.0.finish_reason", finalValue: "stop",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tc.path {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", tc.contentType)
				io.WriteString(w, tc.prefix)
				io.WriteString(w, payload)
				io.WriteString(w, tc.suffix)
			}))
			defer server.Close()

			args := []string{"--base-url", server.URL, "--format", "jsonl", tc.command, "create", "--model", "fake-model"}
			if tc.command == "chat:completions" {
				args = append(args, "--message", `{"role":"user","content":"synthetic input"}`)
			} else {
				args = append(args, "--input", "synthetic input")
			}
			if tc.stream {
				args = append(args, "--stream=true")
			}
			command := exec.CommandContext(t.Context(), binary, args...)
			// Do not inherit real credentials, endpoint overrides, or mTLS settings.
			command.Env = []string{"OPENAI_API_KEY=sk-fake-payload-test", "FORCE_COLOR=0"}
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatalf("CLI failed: %v\n%s", err, &stderr)
			}

			decoder := json.NewDecoder(bytes.NewReader(output))
			var item json.RawMessage
			if err := decoder.Decode(&item); err != nil {
				t.Fatalf("decode CLI output: %v", err)
			}
			if got := gjson.GetBytes(item, tc.textPath).String(); got != payload {
				t.Fatalf("output text was not preserved: got %d bytes, want %d", len(got), len(payload))
			}
			if tc.stream {
				if err := decoder.Decode(&item); err != nil {
					t.Fatalf("decode event after large event: %v", err)
				}
				if got := gjson.GetBytes(item, tc.finalPath).String(); got != tc.finalValue {
					t.Fatalf("final event: got %q, want %q", got, tc.finalValue)
				}
			}
			if err := decoder.Decode(&item); err != io.EOF {
				t.Fatalf("expected end of output, got %v", err)
			}
		})
	}
}
