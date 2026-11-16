package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainPipedValueErrorsRecoverRequests(t *testing.T) {
	for _, tc := range []struct {
		name, path, method, message, contentType, response string
		args                                               []string
		broken, repaired                                   []string
	}{
		{
			name: "Files query integer", path: "/files", method: http.MethodGet,
			args:        []string{"files", "list"},
			message:     "Invalid piped input for --limit. Expected an integer, such as 1.\nOptions and examples: openai files list --help",
			broken:      []string{`{"limit":"synthetic-private-value","purpose":"user_data","order":"asc"}`, "limit: synthetic-private-value\npurpose: user_data\norder: asc\n"},
			repaired:    []string{`{"limit":1,"purpose":"user_data","order":"asc"}`, "limit: 1\npurpose: user_data\norder: asc\n"},
			contentType: "application/json",
			response:    `{"object":"list","data":[{"id":"file_recovered","object":"file","bytes":1,"created_at":1,"filename":"fixture.txt","purpose":"user_data"}],"has_more":false}`,
		},
		{
			name: "Chat nested boolean", path: "/chat/completions", method: http.MethodPost,
			args:        []string{"chat", "completions", "create"},
			message:     "Invalid piped input for --stream-options.include-usage. Expected a boolean (true or false).\nOptions and examples: openai chat completions create --help",
			broken:      []string{`{"model":"synthetic-model","messages":[{"role":"user","content":"synthetic-private-prompt"}],"stream":true,"stream_options":{"include_usage":"synthetic-private-value","include_obfuscation":false},"temperature":0,"max_completion_tokens":null}`, "model: synthetic-model\nmessages:\n  - role: user\n    content: synthetic-private-prompt\nstream: true\nstream_options:\n  include_usage: synthetic-private-value\n  include_obfuscation: false\ntemperature: 0\nmax_completion_tokens: null\n"},
			repaired:    []string{`{"model":"synthetic-model","messages":[{"role":"user","content":"synthetic-private-prompt"}],"stream":true,"stream_options":{"include_usage":false,"include_obfuscation":false},"temperature":0,"max_completion_tokens":null}`, "model: synthetic-model\nmessages:\n  - role: user\n    content: synthetic-private-prompt\nstream: true\nstream_options:\n  include_usage: false\n  include_obfuscation: false\ntemperature: 0\nmax_completion_tokens: null\n"},
			contentType: "text/event-stream",
			response:    "data: {\"id\":\"chatcmpl_recovered\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"synthetic-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"recovered\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		},
	} {
		for inputIndex, inputKind := range []string{"JSON", "YAML"} {
			for _, mode := range []struct {
				name, format string
				flags        []string
				extract      bool
			}{
				{name: "text", format: "text", flags: []string{"--format-error", "text"}},
				{name: "JSON", format: "json", flags: []string{"--format-error", "json"}},
				{name: "JSONL", format: "jsonl", flags: []string{"--format-error", "jsonl"}},
				{name: "YAML", format: "yaml", flags: []string{"--format-error", "yaml"}},
				{name: "quiet", format: "text", flags: []string{"--format-error", "text", "--quiet"}},
				{name: "extraction", flags: []string{"--format-error", "json", "--transform-error", "message"}, extract: true},
			} {
				t.Run(tc.name+"/"+inputKind+"/"+mode.name, func(t *testing.T) {
					type request struct {
						method, path, project string
						query                 url.Values
						body                  map[string]any
					}
					var count atomic.Int32
					requests := make(chan request, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						count.Add(1)
						got := request{method: r.Method, path: r.URL.Path, project: r.Header.Get("OpenAI-Project"), query: r.URL.Query()}
						if r.Method == http.MethodPost {
							if err := json.NewDecoder(r.Body).Decode(&got.body); err != nil {
								t.Errorf("decode request: %v", err)
							}
						}
						select {
						case requests <- got:
						default:
							t.Error("unexpected repeated request")
						}
						w.Header().Set("Content-Type", tc.contentType)
						if r.URL.Path != tc.path || r.Method != tc.method {
							w.WriteHeader(http.StatusBadRequest)
						}
						if _, err := io.WriteString(w, tc.response); err != nil {
							t.Errorf("write response: %v", err)
						}
					}))
					defer server.Close()
					args := append([]string{"--project", "project_synthetic"}, mode.flags...)
					args = append(args, tc.args...)
					failed := runShellFileCommand(t, server, shellFileInput(t, []byte(tc.broken[inputIndex])), []string{"NO_COLOR=1"}, args...)
					if failed.code != 1 || failed.stdout != "" || count.Load() != 0 {
						t.Fatalf("piped validation must fail before requests: result=%+v requests=%d", failed, count.Load())
					}
					if strings.Contains(failed.stderr, "synthetic-private") || strings.ContainsAny(failed.stderr, "\x1b\a\r") {
						t.Fatalf("piped diagnostic exposed values or controls: %q", failed.stderr)
					}
					message := strings.TrimSpace(failed.stderr)
					if mode.extract {
						if err := json.Unmarshal([]byte(failed.stderr), &message); err != nil {
							t.Fatal(err)
						}
					} else if mode.format != "text" {
						payload := decodeMainStructuredError(t, mode.format, failed.stderr)
						if len(payload) != 1 {
							t.Fatalf("invented local error fields: %#v", payload)
						}
						message, _ = payload["message"].(string)
					}
					if message != tc.message {
						t.Fatalf("got %q, want %q", message, tc.message)
					}
					_, help, found := strings.Cut(message, "Options and examples: ")
					if !found {
						t.Fatal("missing copied help")
					}
					copied := runShellFileCommand(t, server, nil, nil, strings.Fields(help)[1:]...)
					if copied.code != 0 || copied.stderr != "" || !strings.Contains(copied.stdout, "openai "+strings.Join(tc.args, " ")) || count.Load() != 0 {
						t.Fatalf("copied help failed or made a request: %+v", copied)
					}
					recovered := runShellFileCommand(t, server, shellFileInput(t, []byte(tc.repaired[inputIndex])), []string{"NO_COLOR=1"}, args...)
					if recovered.code != 0 || recovered.stderr != "" || count.Load() != 1 || !json.Valid([]byte(recovered.stdout)) {
						t.Fatalf("explicit piped recovery failed: result=%+v requests=%d", recovered, count.Load())
					}
					var received request
					select {
					case received = <-requests:
					default:
						t.Fatal("missing recorded request")
					}
					if received.method != tc.method || received.path != tc.path || received.project != "project_synthetic" {
						t.Fatalf("changed request context: %#v", received)
					}
					if tc.path == "/files" {
						want := url.Values{"limit": {"1"}, "purpose": {"user_data"}, "order": {"asc"}}
						if !reflect.DeepEqual(received.query, want) || !strings.Contains(recovered.stdout, "file_recovered") {
							t.Fatalf("changed repaired Files request: %#v, %q", received, recovered.stdout)
						}
					} else {
						want := map[string]any{"model": "synthetic-model", "messages": []any{map[string]any{"role": "user", "content": "synthetic-private-prompt"}}, "stream": true, "stream_options": map[string]any{"include_usage": false, "include_obfuscation": false}, "temperature": float64(0), "max_completion_tokens": nil}
						if !reflect.DeepEqual(received.body, want) || !strings.Contains(recovered.stdout, "chatcmpl_recovered") {
							t.Fatalf("changed repaired Chat request: %#v, %q", received.body, recovered.stdout)
						}
					}
				})
			}
		}
	}
}

func TestMainPipedInvalidValueKeepsExplicitOverride(t *testing.T) {
	for _, input := range []string{`{"limit":"synthetic-private-invalid","purpose":"user_data"}`, "limit: synthetic-private-invalid\npurpose: user_data\n"} {
		t.Run(input, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/files" || !reflect.DeepEqual(r.URL.Query(), url.Values{"limit": {"1"}, "purpose": {"user_data"}}) {
					t.Errorf("unexpected explicit override request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if _, err := io.WriteString(w, `{"object":"list","data":[{"id":"file_override","object":"file"}],"has_more":false}`); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			got := runShellFileCommand(t, server, shellFileInput(t, []byte(input)), nil, "files", "list", "--limit", "1")
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 || !strings.Contains(got.stdout, "file_override") {
				t.Fatalf("explicit value lost precedence over piped invalid value: %+v", got)
			}
		})
	}
}
