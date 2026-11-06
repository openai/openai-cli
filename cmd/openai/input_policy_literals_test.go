package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func inputPolicyServer(t *testing.T) (*httptest.Server, <-chan map[string]any) {
	t.Helper()
	requests := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		select {
		case requests <- body:
		default:
			t.Error("unexpected extra request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_synthetic","object":"response","status":"completed","output":[]}`)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func inputPolicyBody(t *testing.T, requests <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case body := <-requests:
		return body
	default:
		t.Fatal("command did not send a request")
		return nil
	}
}

func inputPolicyNoRequest(t *testing.T, requests <-chan map[string]any) {
	t.Helper()
	select {
	case <-requests:
		t.Error("local validation sent a request")
	default:
	}
}

func inputPolicyValue(t *testing.T, body map[string]any, key string, want any) {
	t.Helper()
	got, present := body[key]
	if !present || !reflect.DeepEqual(got, want) {
		t.Fatalf("request %s = %#v, present %t; want %#v", key, got, present, want)
	}
}

func TestMainInputPolicyAmbiguousText(t *testing.T) {
	server, requests := inputPolicyServer(t)
	for _, value := range []string{"", " \t\n ", `"quoted text"`, "true", "~", "[]", "{}", "[unfinished", "line one\r\nline two\n"} {
		t.Run(value, func(t *testing.T) {
			got := runShellFileCommand(t, server, nil, nil, "responses", "create", "--model", "model_synthetic", "--input", value)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("command: %+v", got)
			}
			inputPolicyValue(t, inputPolicyBody(t, requests), "input", value)
		})
	}
}

func TestMainInputPolicyNullableText(t *testing.T) {
	server, requests := inputPolicyServer(t)
	for _, flag := range []string{"instructions", "previous-response-id"} {
		for _, value := range []string{"null", "", `"quoted text"`, "  text: # unchanged  "} {
			t.Run(flag+"/"+value, func(t *testing.T) {
				got := runShellFileCommand(t, server, nil, nil, "responses", "create", "--model", "model_synthetic", "--input", "synthetic", "--"+flag, value)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("command: %+v", got)
				}
				inputPolicyValue(t, inputPolicyBody(t, requests), strings.ReplaceAll(flag, "-", "_"), value)
			})
		}
		t.Run(flag+"/explicit null", func(t *testing.T) {
			got := runShellFileCommand(t, server, nil, nil, "responses", "create", "--model", "model_synthetic", "--input", "synthetic", "--"+flag+"-structured", "null")
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("command: %+v", got)
			}
			inputPolicyValue(t, inputPolicyBody(t, requests), strings.ReplaceAll(flag, "-", "_"), nil)
		})
	}
}

func TestMainInputPolicyTypedOperands(t *testing.T) {
	server, requests := inputPolicyServer(t)
	for _, tc := range []struct {
		name, flag, value, key string
		want                   any
	}{
		{"numeric", "max-output-tokens", "1024", "max_output_tokens", float64(1024)},
		{"numeric null", "max-output-tokens", "null", "max_output_tokens", nil},
		{"enum", "tool-choice", "auto", "tool_choice", "auto"},
		{"enum object alternative", "tool-choice", `{"type":"function","name":"synthetic"}`, "tool_choice", map[string]any{"type": "function", "name": "synthetic"}},
		{"object", "metadata", `{"count":"42"}`, "metadata", map[string]any{"count": "42"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runShellFileCommand(t, server, nil, nil, "responses", "create", "--model", "model_synthetic", "--input", "synthetic", "--"+tc.flag, tc.value)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("command: %+v", got)
			}
			inputPolicyValue(t, inputPolicyBody(t, requests), tc.key, tc.want)
		})
	}
}

func TestMainInputPolicyProcessMode(t *testing.T) {
	server, requests := inputPolicyServer(t)
	path := filepath.Join(t.TempDir(), "source.json")
	const source = `["file contents"]`
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, flag, value string
		env               []string
		want              any
	}{
		{"legacy mapping", "input", "Translate: hello", []string{"OPENAI_CLI_INPUT_MODE=legacy"}, map[string]any{"Translate": "hello"}},
		{"legacy array", "input", `["synthetic"]`, []string{"OPENAI_CLI_INPUT_MODE=legacy"}, []any{"synthetic"}},
		{"legacy null", "instructions", "null", []string{"OPENAI_CLI_INPUT_MODE=legacy"}, nil},
		{"legacy quoted source", "input", `"@` + filepath.ToSlash(path) + `"`, []string{"OPENAI_CLI_INPUT_MODE=legacy"}, source},
		{"legacy source remains text", "input", "@" + path, []string{"OPENAI_CLI_INPUT_MODE=legacy"}, source},
		{"structured overrides legacy", "input-structured", "@" + path, []string{"OPENAI_CLI_INPUT_MODE=legacy"}, []any{"file contents"}},
		{"explicit literal", "input", "null", []string{"OPENAI_CLI_INPUT_MODE=literal"}, "null"},
		{"literal overrides inherited legacy", "input", "null", []string{"OPENAI_CLI_INPUT_MODE=legacy", "OPENAI_CLI_INPUT_MODE=literal"}, "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runShellFileCommand(t, server, nil, tc.env, "responses", "create", "--model", "model_synthetic", "--"+tc.flag, tc.value)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("command: %+v", got)
			}
			key := strings.TrimSuffix(tc.flag, "-structured")
			inputPolicyValue(t, inputPolicyBody(t, requests), key, tc.want)
		})
	}
}

func TestMainInputPolicyInvalidModeFailsLocally(t *testing.T) {
	server, requests := inputPolicyServer(t)
	for _, mode := range []string{"automatic", "LITERAL", "legacy ", "synthetic-private-mode\x1b[31m"} {
		t.Run(mode, func(t *testing.T) {
			got := runShellFileCommand(t, server, nil, []string{"OPENAI_CLI_INPUT_MODE=" + mode}, "responses", "create", "--model", "model_synthetic", "--input", "synthetic")
			if got.code == 0 || got.stdout != "" || !strings.Contains(got.stderr, "OPENAI_CLI_INPUT_MODE") {
				t.Fatalf("invalid mode did not fail locally: %+v", got)
			}
			if strings.Contains(got.stderr, "synthetic-private-mode") || strings.Contains(got.stderr, "\x1b") {
				t.Fatalf("mode diagnostic disclosed its value: %q", got.stderr)
			}
			inputPolicyNoRequest(t, requests)
		})
	}
}

func TestMainInputPolicyRepeatedAndNestedOperands(t *testing.T) {
	server, requests := inputPolicyServer(t)
	for _, tc := range []struct {
		name string
		args []string
		key  string
		want any
	}{
		{"last literal", []string{"responses", "create", "--model", "model_synthetic", "--input", "first", "--input", "null"}, "input", "null"},
		{"last nullable literal", []string{"responses", "create", "--model", "model_synthetic", "--instructions", "first", "--instructions", "null"}, "instructions", "null"},
		{"nested literal", []string{"beta", "threads", "runs", "create", "--thread-id", "thread_synthetic", "--assistant-id", "asst_synthetic", "--additional-message.role", "user", "--additional-message.content", "Translate: hello"}, "additional_messages", []any{map[string]any{"role": "user", "content": "Translate: hello"}}},
		{"nested structured", []string{"beta:threads:runs", "create", "--thread-id", "thread_synthetic", "--assistant-id", "asst_synthetic", "--additional-message.role", "user", "--additional-message.content-structured", `[{"type":"text","text":"synthetic"}]`}, "additional_messages", []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "synthetic"}}}}},
		{"object-only operand", []string{"beta", "threads", "runs", "create", "--thread-id", "thread_synthetic", "--assistant-id", "asst_synthetic", "--additional-message", `[{"role":"user","content":"synthetic"}]`}, "additional_messages", []any{map[string]any{"role": "user", "content": "synthetic"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runShellFileCommand(t, server, nil, nil, tc.args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("command: %+v", got)
			}
			inputPolicyValue(t, inputPolicyBody(t, requests), tc.key, tc.want)
		})
	}
}
