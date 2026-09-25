package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func streamingTextArgs(command string, prefix ...string) []string {
	args := append(prefix, command, "create", "--model", "fake-model", "--stream=true")
	switch command {
	case "chat:completions":
		return append(args, "--message", `{"role":"user","content":"synthetic input"}`)
	case "completions":
		return append(args, "--prompt", "synthetic input")
	default:
		return append(args, "--input", "synthetic input")
	}
}

// Use the same production-entrypoint subprocess as the ordinary CLI tests,
// with a pipe so a withheld event cannot be hidden by a buffered test writer.
func startStreamingTextCommand(t *testing.T, server *httptest.Server, args ...string) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, context.Context) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, args...)...)
	child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=sk-fake-streaming-test", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
	stderr := new(bytes.Buffer)
	child.Stderr = stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		stdout.Close()
		if child.ProcessState == nil {
			child.Wait()
		}
	})
	return child, stdout, stderr, ctx
}

func readStreamingTextPrefix(t *testing.T, ctx context.Context, stdout io.Reader, want string) {
	t.Helper()
	got := make([]byte, len(want))
	read := make(chan error, 1)
	go func() { _, err := io.ReadFull(stdout, got); read <- err }()
	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("first streamed text did not reach stdout: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("first streamed text was buffered while later events were withheld")
	}
	if string(got) != want {
		t.Fatalf("first streamed bytes = %q, want %q", got, want)
	}
}

func writeStreamingTextEvent(w io.Writer, event string) {
	fmt.Fprintf(w, "data: %s\n\n", event)
}

func TestMainStreamingTextArrivesBeforeCompletion(t *testing.T) {
	for _, tc := range []struct {
		command, path, first, last string
	}{
		{
			"responses", "/responses",
			`{"type":"response.output_text.delta","item_id":"msg_live","output_index":0,"content_index":0,"delta":"Hello ☀","sequence_number":0}`,
			`{"type":"response.completed","response":{"id":"resp_live","status":"completed","output":[{"id":"msg_live","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello ☀ world"}]}]}}`,
		},
		{
			"chat:completions", "/chat/completions",
			`{"id":"chat_live","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello ☀"},"finish_reason":null}]}`,
			`{"id":"chat_live","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`,
		},
		{
			"completions", "/completions",
			`{"id":"cmpl_live","object":"text_completion","choices":[{"index":0,"text":"Hello ☀","finish_reason":null}]}`,
			`{"id":"cmpl_live","object":"text_completion","choices":[{"index":0,"text":" world","finish_reason":"stop"}]}`,
		},
	} {
		for _, format := range []string{"", "text"} {
			t.Run(tc.command+"/"+format, func(t *testing.T) {
				release := make(chan struct{}, 1)
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["model"] != "fake-model" || body["stream"] != true {
						t.Errorf("stream request body = %v, decode error = %v", body, err)
					}
					if r.Method != http.MethodPost || r.URL.Path != tc.path {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					switch tc.command {
					case "responses":
						if body["input"] != "synthetic input" {
							t.Errorf("response input changed: %v", body["input"])
						}
					case "completions":
						if body["prompt"] != "synthetic input" {
							t.Errorf("completion prompt changed: %v", body["prompt"])
						}
					case "chat:completions":
						want := []any{map[string]any{"role": "user", "content": "synthetic input"}}
						if !reflect.DeepEqual(body["messages"], want) {
							t.Errorf("chat messages changed: %v", body["messages"])
						}
					}
					w.Header().Set("Content-Type", "text/event-stream")
					writeStreamingTextEvent(w, tc.first)
					w.(http.Flusher).Flush()
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					writeStreamingTextEvent(w, tc.last)
					io.WriteString(w, "data: [DONE]\n\n")
				}))
				t.Cleanup(server.Close)
				t.Cleanup(func() { close(release) })
				var prefix []string
				if format != "" {
					prefix = []string{"--format", format}
				}
				child, stdout, stderr, ctx := startStreamingTextCommand(t, server, streamingTextArgs(tc.command, prefix...)...)
				readStreamingTextPrefix(t, ctx, stdout, "Hello ☀")
				release <- struct{}{}
				rest, err := io.ReadAll(stdout)
				if err != nil {
					t.Fatal(err)
				}
				if err := child.Wait(); err != nil || stderr.Len() != 0 {
					t.Fatalf("stream failed: %v; stderr=%q", err, stderr.String())
				}
				if string(rest) != " world\n" || requests.Load() != 1 {
					t.Fatalf("remaining text = %q, requests = %d", rest, requests.Load())
				}
			})
		}
	}
}

func TestMainStreamingTextKeepsPartIdentityAndSnapshots(t *testing.T) {
	events := []string{
		`{"type":"response.output_text.delta","item_id":"msg_left","output_index":0,"content_index":0,"delta":"LEFT-"}`,
		`{"type":"response.output_text.delta","item_id":"msg_right","output_index":1,"content_index":0,"delta":"RIGHT-"}`,
		`{"type":"response.output_text.delta","item_id":"msg_left","output_index":0,"content_index":1,"delta":"LEFT-"}`,
		`{"type":"response.output_text.done","item_id":"msg_left","output_index":0,"content_index":0,"text":"LEFT-DONE"}`,
		`{"type":"response.content_part.done","item_id":"msg_left","output_index":0,"content_index":0,"part":{"type":"output_text","text":"LEFT-DONE"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_left","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"LEFT-DONE"},{"type":"output_text","text":"LEFT-"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_parts","status":"completed","output":[{"id":"msg_left","type":"message","role":"assistant","content":[{"type":"output_text","text":"LEFT-DONE"},{"type":"output_text","text":"LEFT-"}]},{"id":"msg_right","type":"message","role":"assistant","content":[{"type":"output_text","text":"RIGHT-"}]}],"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}`,
	}
	server := streamingTextServer(t, events)
	got := runReadableCommand(t, server, streamingTextArgs("responses")...)
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("stream failed: %+v", got)
	}
	for text, count := range map[string]int{"LEFT-": 2, "RIGHT-": 1, "DONE": 1} {
		if strings.Count(got.stdout, text) != count {
			t.Fatalf("text part %q count != %d: %q", text, count, got.stdout)
		}
	}
	remaining := got.stdout
	for _, text := range []string{"LEFT-", "RIGHT-", "LEFT-", "DONE"} {
		_, rest, found := strings.Cut(remaining, text)
		if !found {
			t.Fatalf("interleaved text moved out of arrival order: %q", got.stdout)
		}
		remaining = rest
	}
	for _, field := range []string{"Input tokens: 3", "Output tokens: 4", "Total tokens: 7"} {
		if !strings.Contains(got.stdout, field) {
			t.Fatalf("missing terminal usage %q: %q", field, got.stdout)
		}
	}
}

func streamingTextServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			writeStreamingTextEvent(w, event)
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server
}

func TestMainStreamingTextRetainsNontextDetails(t *testing.T) {
	events := []string{
		`{"type":"response.refusal.delta","item_id":"msg_refusal","output_index":0,"content_index":0,"delta":"Cannot assist"}`,
		`{"type":"response.refusal.done","item_id":"msg_refusal","output_index":0,"content_index":0,"refusal":"Cannot assist"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_synthetic","output_index":1,"delta":"{\"city\":\"Paris\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"fc_synthetic","type":"function_call","name":"synthetic_weather","call_id":"call_synthetic","arguments":"{\"city\":\"Paris\"}"}}`,
		`{"type":"response.future_event","future":{"note":"future marker"}}`,
		`{"type":"response.completed","response":{"id":"resp_details","status":"completed","output":[{"id":"msg_refusal","type":"message","role":"assistant","content":[{"type":"refusal","refusal":"Cannot assist"}]},{"id":"fc_synthetic","type":"function_call","name":"synthetic_weather","call_id":"call_synthetic","arguments":"{\"city\":\"Paris\"}"}],"usage":{"input_tokens":9,"output_tokens":2,"total_tokens":11}}}`,
	}
	server := streamingTextServer(t, events)
	got := runReadableCommand(t, server, streamingTextArgs("responses")...)
	if got.code != 0 || got.stderr != "" || strings.Count(got.stdout, "Cannot assist") != 1 {
		t.Fatalf("refusal was lost or repeated: %+v", got)
	}
	for _, content := range []string{"synthetic_weather", "call_synthetic", "Paris", "response.future_event", "future marker", "Input tokens: 9", "Output tokens: 2", "Total tokens: 11"} {
		if !strings.Contains(got.stdout, content) {
			t.Fatalf("missing stream detail %q: %q", content, got.stdout)
		}
	}
}

func TestMainStreamingExplicitFormatsPreserveEveryEvent(t *testing.T) {
	responseEvents := []string{
		`{"type":"response.output_text.delta","item_id":"msg_machine","output_index":0,"content_index":0,"delta":"same text","future":{"count":9007199254740993}}`,
		`{"type":"response.output_text.done","item_id":"msg_machine","output_index":0,"content_index":0,"text":"same text"}`,
		`{"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7},"extra":"kept"}}`,
	}
	for _, tc := range []struct {
		command string
		events  []string
	}{
		{"responses", responseEvents},
		{"chat:completions", []string{
			`{"id":"chat_machine","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"same text","tool_calls":[{"index":0,"id":"call_machine","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":null}],"future":{"count":9007199254740993}}`,
			`{"id":"chat_machine","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
		}},
		{"completions", []string{
			`{"id":"cmpl_machine","object":"text_completion","choices":[{"index":0,"text":"same text","finish_reason":null,"logprobs":{"tokens":["same","text"]}}],"future":{"count":9007199254740993}}`,
			`{"id":"cmpl_machine","object":"text_completion","choices":[{"index":0,"text":"","finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
		}},
	} {
		server := streamingTextServer(t, tc.events)
		for _, format := range []string{"raw", "json", "jsonl"} {
			t.Run(tc.command+"/"+format, func(t *testing.T) {
				got := runReadableCommand(t, server, streamingTextArgs(tc.command, "--format", format)...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("machine stream failed: %+v", got)
				}
				decoder := json.NewDecoder(strings.NewReader(got.stdout))
				decoder.UseNumber()
				for i, event := range tc.events {
					var actual, want any
					if err := decoder.Decode(&actual); err != nil {
						t.Fatalf("decode event %d: %v", i, err)
					}
					original := json.NewDecoder(strings.NewReader(event))
					original.UseNumber()
					if err := original.Decode(&want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(actual, want) {
						t.Fatalf("event %d changed: got %v, want %v", i, actual, want)
					}
				}
				var extra any
				if err := decoder.Decode(&extra); err != io.EOF {
					t.Fatalf("unexpected trailing output: %v, %v", extra, err)
				}
				if format == "raw" && got.stdout != strings.Join(tc.events, "\n")+"\n" {
					t.Fatalf("raw event bytes changed: %q", got.stdout)
				}
			})
		}
	}
	server := streamingTextServer(t, responseEvents)
	for _, prefix := range [][]string{
		{"--transform", "delta", "--raw-output"},
		{"--format", "text", "--transform", "delta", "--raw-output"},
	} {
		got := runReadableCommand(t, server, streamingTextArgs("responses", prefix...)...)
		if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, "same text\n") {
			t.Fatalf("explicit extraction changed: %+v", got)
		}
	}
}

func TestMainStreamingTextInterleavedChoicesKeepDetails(t *testing.T) {
	for _, tc := range []struct {
		command string
		events  []string
		details []string
	}{
		{"chat:completions", []string{
			`{"id":"chat_choices","object":"chat.completion.chunk","choices":[{"index":1,"delta":{"content":"Second choice"},"finish_reason":null},{"index":0,"delta":{"content":"First choice"},"finish_reason":null}]}`,
			`{"id":"chat_choices","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"refusal":"Cannot continue","tool_calls":[{"index":0,"id":"call_chat","function":{"name":"synthetic_lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"},{"index":1,"delta":{"content":" ending"},"finish_reason":"stop"}],"future":"chat marker"}`,
			`{"id":"chat_choices","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`,
		}, []string{"Cannot continue", "synthetic_lookup", "call_chat", "chat marker", "Prompt tokens: 12", "Completion tokens: 5", "Total tokens: 17"}},
		{"completions", []string{
			`{"id":"cmpl_choices","object":"text_completion","choices":[{"index":1,"text":"Second choice","finish_reason":null},{"index":0,"text":"First choice","finish_reason":null}]}`,
			`{"id":"cmpl_choices","object":"text_completion","choices":[{"index":0,"text":"","finish_reason":"stop","logprobs":{"tokens":["synthetic-token"]}},{"index":1,"text":" ending","finish_reason":"stop"}],"future":"legacy marker","usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`,
		}, []string{"synthetic-token", "legacy marker", "Prompt tokens: 12", "Completion tokens: 5", "Total tokens: 17"}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			server := streamingTextServer(t, tc.events)
			got := runReadableCommand(t, server, streamingTextArgs(tc.command)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("interleaved choices failed: %+v", got)
			}
			for _, text := range []string{"First choice", "Second choice", " ending"} {
				if strings.Count(got.stdout, text) != 1 {
					t.Fatalf("choice text %q lost or repeated: %q", text, got.stdout)
				}
			}
			for _, detail := range tc.details {
				if !strings.Contains(got.stdout, detail) {
					t.Fatalf("choice detail %q lost: %q", detail, got.stdout)
				}
			}
		})
	}
}

func TestMainStreamingFailuresPreservePartialOutput(t *testing.T) {
	for _, tc := range []struct {
		name, last, detail string
	}{
		{"ordinary failed event", `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"synthetic failure"}}}`, "synthetic failure"},
		{"ordinary incomplete event", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`, "max_output_tokens"},
		{"SDK error event", `{"error":{"message":"synthetic stream error","type":"server_error"}}`, ""},
		{"malformed event", `{"type":"response.output_text.delta","delta":`, ""},
	} {
		for _, format := range []string{"text", "jsonl"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				server := streamingTextServer(t, []string{`{"type":"response.output_text.delta","item_id":"msg_partial","output_index":0,"content_index":0,"delta":"Partial answer"}`, tc.last})
				got := runReadableCommand(t, server, streamingTextArgs("responses", "--format", format)...)
				if got.code == 0 || got.stderr == "" || !strings.Contains(got.stdout, "Partial answer") {
					t.Fatalf("failure lost partial output or exit status: %+v", got)
				}
				if tc.detail != "" && !strings.Contains(got.stdout, tc.detail) {
					t.Fatalf("ordinary failure details lost: %+v", got)
				}
			})
		}
	}
}

func TestMainStreamingInterruptedTransportPreservesPartialOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamingTextEvent(w, `{"type":"response.output_text.delta","item_id":"msg_interrupted","output_index":0,"content_index":0,"delta":"Partial before disconnect"}`)
		w.(http.Flusher).Flush()
		// Close an unfinished chunked HTTP response. This produces a transport
		// error rather than a successful EOF or a fabricated API failure event.
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	t.Cleanup(server.Close)
	got := runReadableCommand(t, server, streamingTextArgs("responses")...)
	if got.code == 0 || got.stderr == "" || !strings.Contains(got.stdout, "Partial before disconnect") {
		t.Fatalf("interrupted stream reported success or lost partial output: %+v", got)
	}
}

func TestMainStreamingTextDistinguishesEarlyEOFAndEmptyCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		events        []string
		failed        bool
	}{
		{"responses early EOF", "responses", []string{`{"type":"response.output_text.delta","item_id":"msg_eof","output_index":0,"content_index":0,"delta":"Unfinished answer"}`}, true},
		{"chat early EOF", "chat:completions", []string{`{"id":"chat_eof","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Unfinished answer"},"finish_reason":null}]}`}, true},
		{"completion early EOF", "completions", []string{`{"id":"cmpl_eof","object":"text_completion","choices":[{"index":0,"text":"Unfinished answer","finish_reason":null}]}`}, true},
		{"empty transport", "responses", nil, false},
		{"completed empty response", "responses", []string{`{"type":"response.completed","response":{"status":"completed","output":[]}}`}, false},
		{"completed empty chat", "chat:completions", []string{`{"id":"chat_empty","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range tc.events {
					writeStreamingTextEvent(w, event)
				}
			}))
			t.Cleanup(server.Close)
			got := runReadableCommand(t, server, streamingTextArgs(tc.command)...)
			if tc.failed {
				if got.code == 0 || got.stderr == "" || !strings.Contains(got.stdout, "Unfinished answer") {
					t.Fatalf("unfinished stream reported success or lost partial output: %+v", got)
				}
			} else if got.code != 0 || got.stderr != "" {
				t.Fatalf("empty stream failed: %+v", got)
			}
		})
	}
}

func TestMainStreamingTextExplicitLimitStopsWithoutCompletionError(t *testing.T) {
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamingTextEvent(w, `{"type":"response.output_text.delta","item_id":"msg_limited","output_index":0,"content_index":0,"delta":"First"}`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(disconnected)
	}))
	t.Cleanup(server.Close)
	args := append(streamingTextArgs("responses"), "--max-items", "1")
	got := runReadableCommand(t, server, args...)
	if got.code != 0 || got.stderr != "" || got.stdout != "First\n" {
		t.Fatalf("explicit event limit changed: %+v", got)
	}
	select {
	case <-disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("limited CLI left its HTTP stream open")
	}
}

func TestMainStreamingTextLargeEvent(t *testing.T) {
	// High memory use is intentional. Do not reduce the probe to accommodate
	// new output caps. Leave envelope space below the SDK's existing 32 MiB
	// SSE line limit, as in internal/payloadtest. Keep large cases sequential.
	text := strings.Repeat("x", (32<<20)-1024)
	for _, tc := range []struct{ command, first, last, completed string }{
		{"responses", `{"type":"response.output_text.delta","item_id":"msg_large","output_index":0,"content_index":0,"delta":"`, `"}`, `{"type":"response.completed","response":{"status":"completed","output":[]}}`},
		{"chat:completions", `{"id":"chat_large","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"`, `"},"finish_reason":null}]}`, `{"id":"chat_large","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
	} {
		t.Run(tc.command, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: "+tc.first)
				io.WriteString(w, text)
				io.WriteString(w, tc.last+"\n\n")
				writeStreamingTextEvent(w, tc.completed)
				io.WriteString(w, "data: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			got := runReadableCommand(t, server, streamingTextArgs(tc.command)...)
			if got.code != 0 || got.stderr != "" || len(got.stdout) != len(text)+1 || strings.TrimSuffix(got.stdout, "\n") != text {
				t.Fatalf("large streamed text changed: code=%d, output bytes=%d, want=%d, stderr=%q", got.code, len(got.stdout), len(text)+1, got.stderr)
			}
		})
	}
}
