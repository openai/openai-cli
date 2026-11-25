package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMainRequiredInputShowsCommandHelp(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(proxy.Close)
	env := adminCredentialsProxyEnv(proxy.URL, []string{"OPENAI_API_KEY=sk-fake-required-help", "NO_COLOR=1"})
	for _, tc := range []struct {
		name, diagnostic string
		args             []string
	}{
		{"empty Responses request", "Missing required options: --model.", []string{"responses", "create"}},
		{"required request flags", "Missing required options: --input, --model.", []string{"embeddings", "create"}},
		{"missing resource ID", "Missing required options: --file-id.", []string{"files", "get"}},
		{"nested command", "Missing required options:", []string{"chat", "completions", "create"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"openai", "--base-url", "https://api.openai.com/v1"}, tc.args...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			help := runMainDispatchWithEnv(t, "bash", env, append(slices.Clone(args), "--help")...)
			if got.code != 1 || got.stdout != "" || !strings.HasPrefix(got.stderr, tc.diagnostic) ||
				help.code != 0 || help.stdout == "" || help.stderr != "" ||
				!strings.HasSuffix(got.stderr, "\n\n"+help.stdout) || requests.Load() != 0 {
				t.Fatalf("missing argument: %+v; explicit help: %+v; API calls: %d", got, help, requests.Load())
			}
		})
	}
}

func TestMainHandwrittenRequiredInputShowsCommandHelp(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-required-help",
		"OPENAI_ADMIN_KEY=sk-fake-admin-required-help", "NO_COLOR=1"}
	structuredMessages := map[string]string{
		"automatic suggestion shell": "Choose one command insertion setup action. Picker and mouse setup are separate.",
		"click adapter shell":        "click insertion currently supports zsh with a local Kitty adapter",
	}

	for _, tc := range []struct {
		name, diagnostic string
		command, options []string
		exit             int
	}{
		{"audio history ID", "Specify one audio history ID.", []string{"audio", "export"}, []string{"--output=-"}, 1},
		{"batch file", "Choose a local JSONL file", []string{"batches", "submit"}, nil, 1},
		{"batch wait option", "Add --wait", []string{"batches", "submit"}, []string{"fixture.jsonl", "--wait-timeout", "5s"}, 1},
		{"saved image", "Provide one saved image", []string{"images", "preview"}, nil, 1},
		{"Codex destination", "Opening a Codex destination requires --destination", []string{"codex"}, []string{"--open"}, 1},
		{"completion shell", "Choose a shell", []string{"@completion"}, nil, 1},
		{"suggestion shell", "Choose one shell", []string{"@completion"}, []string{"--suggest-executable"}, 2},
		{"automatic suggestion shell", "Specify Bash, zsh, or Fish", []string{"@completion"}, []string{"--auto-suggestions"}, 2},
		{"automatic picker action", "Use --install-picker or --uninstall-picker", []string{"@completion"}, []string{"--automatic"}, 2},
		{"suggestions prerequisite", "--suggestions-double-tab requires --suggestions", []string{"@completion"}, []string{"zsh", "--suggestions-double-tab"}, 1},
		{"click adapter shell", "Specify zsh for --suggestions-click.", []string{"@completion"}, []string{"--suggestions", "--suggestions-click"}, 1},
		{"API key update fields", "provide at least one of --scope or --expires-in-seconds",
			[]string{"admin", "organization", "projects", "api-keys", "update"}, []string{"proj_fixture", "key_fixture"}, 1},
		{"provider-specific key fields", "Required external-key fields not set: --external-id, --kms-arn, --role-arn",
			[]string{"admin", "organization", "external-keys", "create"}, []string{"--type", "aws", "--name", "fixture", "--purpose", "encryption"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := append([]string{"openai"}, tc.command...)
			help := runMainDispatchWithEnv(t, "bash", env, append(slices.Clone(command), "--help")...)
			got := runMainDispatchWithEnv(t, "bash", env, append(slices.Clone(command), tc.options...)...)
			if got.code != tc.exit || got.stdout != "" || !strings.HasPrefix(got.stderr, tc.diagnostic) ||
				help.code != 0 || help.stdout == "" || help.stderr != "" || !strings.HasSuffix(got.stderr, "\n\n"+help.stdout) {
				t.Fatalf("missing input: %+v; explicit help: %+v", got, help)
			}
			args := append([]string{"openai", "--format-error", "json"}, tc.command...)
			structured := runMainDispatchWithEnv(t, "bash", env, append(args, tc.options...)...)
			value := decodeMainStructuredError(t, "json", structured.stderr)
			message, _ := value["message"].(string)
			want := tc.diagnostic
			if original, ok := structuredMessages[tc.name]; ok {
				want = original
			}
			if structured.code != tc.exit || structured.stdout != "" || !strings.HasPrefix(message, want) ||
				strings.HasSuffix(message, help.stdout) {
				t.Fatalf("structured missing input: %+v", structured)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("sent %d HTTP requests for local missing inputs", requests.Load())
	}
}

func TestMainResponsesRequiredInputPreservesRequestSources(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Method != http.MethodPost || r.URL.Path != "/responses" {
			t.Errorf("unexpected request: %s %s (%v)", r.Method, r.URL.Path, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if body["input"] == "fixture-server-rejects" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":"missing_required_parameter","param":"model","message":"fixture-only detail"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"resp_fixture","object":"response","status":"completed"}`)
	}))
	t.Cleanup(server.Close)
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-required-help", "NO_COLOR=1"}
	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, []byte(`{"model":"file-model","input":"fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, stdin, key, value string
		args                    []string
	}{
		{"flags", "", "model", "flag-model", []string{"--model", "flag-model", "--input", "fixture"}},
		{"piped YAML", "model: pipe-model\ninput: fixture\n", "model", "pipe-model", nil},
		{"named request file", "", "model", "file-model", []string{"--request-file", path}},
		{"request file stdin", `{"model":"stdin-model"}`, "model", "stdin-model", []string{"--request-file", "-"}},
		{"saved prompt without model", "", "prompt", "fixture-prompt", []string{"--prompt.id", "fixture-prompt"}},
		{"continuation without model", "", "previous_response_id", "resp_fixture", []string{"--previous-response-id", "resp_fixture"}},
		{"custom endpoint accepts empty request", "", "", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdin *os.File
			if tc.stdin != "" {
				file := filepath.Join(t.TempDir(), "stdin")
				if err := os.WriteFile(file, []byte(tc.stdin), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				stdin, err = os.Open(file)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stdin.Close() })
			}
			mu.Lock()
			before := len(bodies)
			mu.Unlock()
			args := append([]string{"openai", "--format", "json", "responses", "create"}, tc.args...)
			got := runMainDispatchWithStdin(t, "bash", env, stdin, args...)
			mu.Lock()
			defer mu.Unlock()
			if got.code != 0 || !json.Valid([]byte(got.stdout)) || got.stderr != "" || len(bodies) != before+1 {
				t.Fatalf("request changed: %+v; request bodies: %#v", got, bodies[before:])
			}
			if tc.key == "" {
				if len(bodies[before]) != 0 {
					t.Fatalf("custom endpoint received a modified body: %#v", bodies[before])
				}
				return
			}
			field := bodies[before][tc.key]
			if prompt, ok := field.(map[string]any); ok {
				field = prompt["id"]
			}
			if field != tc.value {
				t.Fatalf("forwarded %s = %#v, want %q", tc.key, field, tc.value)
			}
		})
	}

	rejected := []string{"openai", "responses", "create", "--input", "fixture-server-rejects"}
	measured := runMainDispatchWithEnv(t, "bash", env, rejected...)
	help := runMainDispatchWithEnv(t, "bash", env, "openai", "responses", "create", "--help")
	if measured.code != 1 || measured.stdout != "" || !strings.HasPrefix(measured.stderr, "Add --model with a value.") ||
		!strings.Contains(measured.stderr, "HTTP 400: Bad Request.") ||
		strings.Contains(measured.stderr, "fixture-only detail") || !strings.HasSuffix(measured.stderr, "\n\n"+help.stdout) {
		t.Fatalf("server missing-input error: %+v", measured)
	}
	structured := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai", "--format-error", "json"}, rejected[1:]...)...)
	value := decodeMainStructuredError(t, "json", structured.stderr)
	if structured.code != 1 || structured.stdout != "" || value["code"] != "missing_required_parameter" ||
		value["message"] != "fixture-only detail" || len(value) != 3 {
		t.Fatalf("structured API error changed: %+v", structured)
	}
}
