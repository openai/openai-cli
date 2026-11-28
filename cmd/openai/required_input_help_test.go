package main

import (
	"encoding/json"
	"fmt"
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
		{"empty Responses request", "Add --input to create a response with the default model.", []string{"responses", "create"}},
		{"empty beta Responses request", "Missing required options: --model.", []string{"beta:responses", "create"}},
		{"Responses stream only", "Add --input to create a response with the default model.", []string{"responses", "create", "--stream=true"}},
		{"beta Responses stream false only", "Missing required options: --model.", []string{"beta:responses", "create", "--stream=false"}},
		{"Responses stream controls only", "Add --input to create a response with the default model.", []string{"responses", "create", "--stream=true", "--stream-options.include-obfuscation=false"}},
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
			if tc.args[0] == "responses" {
				structured := runMainDispatchWithEnv(t, "bash", env, append(args, "--format-error", "json")...)
				value := decodeMainStructuredError(t, "json", structured.stderr)
				message, _ := value["message"].(string)
				if structured.code != 1 || structured.stdout != "" || !strings.HasPrefix(message, "Missing required options: --model.") || requests.Load() != 0 {
					t.Fatalf("structured missing argument changed: %+v; API calls: %d", structured, requests.Load())
				}
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
	home := t.TempDir()
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-required-help",
		"OPENAI_ADMIN_KEY=sk-fake-admin-required-help", "NO_COLOR=1", "ZDOTDIR=",
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home}
	structuredMessages := map[string]string{
		"automatic suggestion shell": "Choose one command insertion setup action. Picker and mouse setup are separate.",
		"click adapter shell":        "click insertion currently supports zsh with a local Kitty adapter",
		"picker zsh profile":         "Could not choose a supported shell startup file.",
		"picker with no shell":       "Could not choose a supported shell startup file.",
	}

	for _, tc := range []struct {
		name, diagnostic string
		command, options []string
		exit             int
	}{
		{"audio history ID", "Specify one audio history ID.", []string{"audio", "export"}, []string{"--output=-"}, 1},
		{"legacy audio browser without live", "Use audio translate --live to open the browser panel.", []string{"audio", "translate"}, []string{"--browser"}, 1},
		{"batch file", "Choose a local JSONL file", []string{"batches", "submit"}, nil, 1},
		{"batch wait option", "Add --wait", []string{"batches", "submit"}, []string{"fixture.jsonl", "--wait-timeout", "5s"}, 1},
		{"batch retrieve without wait", "Use --wait", []string{"batches", "retrieve"}, []string{"batch_fixture", "--wait-timeout", "1s"}, 1},
		{"batch list without watch", "Use --watch", []string{"batches", "list"}, []string{"--watch-count", "1"}, 1},
		{"debug capture without debug", "--debug-body requires --debug", []string{"models", "list"}, []string{"--debug-body"}, 1},
		{"image API output without stream", "--partial-images needs --stream true", []string{"images", "generate"}, []string{"--format", "json", "--prompt", "fixture", "--partial-images", "1"}, 1},
		{"saved image", "Provide one saved image", []string{"images", "preview"}, nil, 1},
		{"Codex destination", "Opening a Codex destination requires --destination", []string{"codex"}, []string{"--open"}, 1},
		{"completion shell", "Choose a shell", []string{"@completion"}, nil, 1},
		{"suggestion shell", "Choose one shell", []string{"@completion"}, []string{"--suggest-executable"}, 2},
		{"automatic suggestion shell", "Specify Bash, zsh, or Fish", []string{"@completion"}, []string{"--auto-suggestions"}, 2},
		{"automatic picker action", "Use --install-picker or --uninstall-picker", []string{"@completion"}, []string{"--automatic"}, 2},
		{"explicit profile without action", "Use --install-picker or --uninstall-picker", []string{"@completion"}, []string{"zsh", "--profile=fixture"}, 2},
		{"picker zsh profile", "Add --profile", []string{"@completion"}, []string{"zsh", "--install-picker"}, 1},
		{"picker with no shell", "Specify Bash, zsh, or Fish", []string{"@completion"}, []string{"--install-picker"}, 1},
		{"suggestions zsh profile", "For zsh, specify the current startup file", []string{"@completion"}, []string{"zsh", "--install-suggestions"}, 2},
		{"suggestion status without detectable shell", "Choose Bash 4.3+", []string{"@completion"}, []string{"--suggestions-status"}, 2},
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
			args := append(slices.Clone(command), tc.options...)
			got := runMainDispatchWithEnv(t, "bash", env, append(args, "--format-error", "text")...)
			if got.code != tc.exit || got.stdout != "" || !strings.HasPrefix(got.stderr, tc.diagnostic) ||
				help.code != 0 || help.stdout == "" || help.stderr != "" || !strings.HasSuffix(got.stderr, "\n\n"+help.stdout) {
				t.Fatalf("missing input: %+v; explicit help: %+v", got, help)
			}
			structured := runMainDispatchWithEnv(t, "bash", env, append(args, "--format-error", "json")...)
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

func TestMainMissingMTLSConfigurationShowsCommandHelp(t *testing.T) {
	const incomplete = "mTLS client certificate and key files must be configured together"
	const noURL = "mTLS requires an explicit HTTPS base URL"
	var requests atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer trap.Close()
	env := adminCredentialsProxyEnv(trap.URL, []string{"OPENAI_BASE_URL=", "NO_COLOR=1"})
	for _, tc := range []struct {
		name, diagnostic, flag string
		options, sources       []string
	}{
		{"certificate only", incomplete, "--mtls-client-key-file", []string{"--mtls-client-cert-file=synthetic-cert.pem"}, nil},
		{"key from environment", incomplete, "--mtls-client-cert-file", nil, []string{"OPENAI_MTLS_CLIENT_KEY_FILE=synthetic-key.pem"}},
		{"pair without URL", noURL, "--base-url", []string{"--mtls-client-cert-file=synthetic-cert.pem", "--mtls-client-key-file=synthetic-key.pem"}, nil},
		{"explicit empty override", incomplete, "", []string{"--mtls-client-key-file="},
			[]string{"OPENAI_MTLS_CLIENT_CERT_FILE=synthetic-cert.pem", "OPENAI_MTLS_CLIENT_KEY_FILE=synthetic-key.pem"}},
		{"explicit HTTP URL", noURL, "", []string{"--base-url=" + trap.URL, "--mtls-client-cert-file=synthetic-cert.pem", "--mtls-client-key-file=synthetic-key.pem"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testEnv := append(slices.Clone(env), tc.sources...)
			command := []string{"openai", "files", "list"}
			help := runMainDispatchWithEnv(t, "bash", testEnv, append(slices.Clone(command), "--help")...)
			args := append(slices.Clone(command), tc.options...)
			got := runMainDispatchWithEnv(t, "bash", testEnv, args...)
			if got.code != 1 || got.stdout != "" || !strings.HasPrefix(got.stderr, tc.diagnostic) ||
				strings.Contains(got.stderr, "synthetic-") || help.code != 0 || help.stdout == "" || help.stderr != "" {
				t.Fatalf("unexpected mTLS validation: %+v", got)
			}
			if tc.flag != "" {
				if !strings.Contains(got.stderr, "Add "+tc.flag+".\n\n") || !strings.HasSuffix(got.stderr, "\n\n"+help.stdout) {
					t.Fatalf("mTLS omission did not append exact command help: %+v", got)
				}
			} else if got.stderr != tc.diagnostic+"\n" {
				t.Fatalf("mTLS invalid value replaced its diagnostic: %+v", got)
			}
			structured := runMainDispatchWithEnv(t, "bash", testEnv, append(args, "--format-error", "json")...)
			value := decodeMainStructuredError(t, "json", structured.stderr)
			if structured.code != 1 || structured.stdout != "" || value["message"] != tc.diagnostic {
				t.Fatalf("mTLS machine diagnostic changed: %+v", structured)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("sent %d requests after mTLS configuration failed", requests.Load())
	}
}

func TestMainBatchSubmitMissingCustomEndpointShowsCommandHelp(t *testing.T) {
	const endpoint = "/v1/custom_operation_extra"
	const payload = `{"custom_id":"fixture","method":"POST","url":"` + endpoint + `","body":{}}` + "\n"
	fixture := newSimpleBatchServer(t, payload, endpoint, "completed")
	path := simpleBatchPath(t, payload)
	env := []string{"OPENAI_BASE_URL=" + fixture.server.URL, "OPENAI_API_KEY=sk-fake-readable-test", "NO_COLOR=1"}
	command := []string{"openai", "batches", "submit"}
	help := runMainDispatchWithEnv(t, "bash", env, append(slices.Clone(command), "--help")...)
	var escaped strings.Builder
	for _, char := range endpoint {
		fmt.Fprintf(&escaped, `\u%04x`, char)
	}
	escapedPath := simpleBatchPath(t, strings.Replace(payload, endpoint, escaped.String(), 1))
	for _, tc := range []struct{ name, path string }{{"plain URL", path}, {"escaped URL", escapedPath}} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(slices.Clone(command), tc.path)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "provide --endpoint explicitly") ||
				help.code != 0 || help.stdout == "" || !strings.HasSuffix(got.stderr, "\n\n"+help.stdout) {
				t.Fatalf("missing custom endpoint did not append exact help: %+v", got)
			}
			structured := runMainDispatchWithEnv(t, "bash", env, append(args, "--format-error", "json")...)
			value := decodeMainStructuredError(t, "json", structured.stderr)
			if structured.code != 1 || structured.stdout != "" ||
				value["message"] != "Cannot determine the batch endpoint: line 1 has an unsupported url. Check the request endpoint, or provide --endpoint explicitly for a custom endpoint." {
				t.Fatalf("machine diagnostic changed: %+v", structured)
			}
		})
	}
	invalid := simpleBatchPath(t, strings.Replace(payload, endpoint, "https://fixture.invalid/path", 1))
	malformed := runMainDispatchWithEnv(t, "bash", env, append(slices.Clone(command), invalid)...)
	if malformed.code != 1 || malformed.stdout != "" || strings.HasSuffix(malformed.stderr, "\n\n"+help.stdout) {
		t.Fatalf("malformed request URL triggered omission help: %+v", malformed)
	}
	fixture.mu.Lock()
	requests := len(fixture.requests)
	fixture.mu.Unlock()
	if requests != 0 {
		t.Fatalf("sent %d requests before local validation finished", requests)
	}
	accepted := runSimpleBatchCommand(t, fixture, nil, path, "--endpoint", endpoint)
	if accepted.code != 0 || accepted.stderr != "" || !json.Valid([]byte(accepted.stdout)) {
		t.Fatalf("explicit custom endpoint did not work: %+v", accepted)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !slices.Equal(fixture.requests, []string{"POST /files", "POST /batches"}) {
		t.Fatalf("explicit custom endpoint changed request sequence: %v", fixture.requests)
	}
}

func TestMainStreamingMissingInputShowsCommandHelp(t *testing.T) {
	const partial = `{"type":"response.output_text.delta","item_id":"msg_fixture","output_index":0,"content_index":0,"delta":"Partial answer"}`
	for _, tc := range []struct {
		name, event, diagnostic string
	}{
		{"ordinary error event", `{"type":"error","code":"missing_required_parameter","param":"model","message":"private-fixture"}`, "Add --model with a value."},
		{"failed response", `{"type":"response.failed","response":{"status":"failed","error":{"code":"missing_required_argument","param":"model","message":"private-fixture"}}}`, "Add --model with a value."},
		{"SDK error envelope", `{"error":{"code":"missing_required_parameter","param":"private-fixture\u001b[2J","message":"private-fixture"}}`, "A required value is missing from the request."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := streamingTextServer(t, []string{partial, tc.event})
			help := runReadableCommand(t, server, "responses", "create", "--help")
			got := runReadableCommand(t, server, streamingTextArgs("responses")...)
			if got.code != 1 || !strings.Contains(got.stdout, "Partial answer") || help.code != 0 ||
				!strings.HasPrefix(got.stderr, tc.diagnostic) || !strings.Contains(got.stderr, "Output may be incomplete.") ||
				!strings.HasSuffix(got.stderr, "\n\n"+help.stdout) || strings.Contains(got.stderr, "private-fixture") {
				t.Fatalf("streamed missing input: %+v; explicit help: %+v", got, help)
			}
			structured := runReadableCommand(t, server, streamingTextArgs("responses", "--format-error", "json")...)
			value := decodeMainErrorObject(t, "json", structured.stderr)
			if structured.code != 1 || structured.stdout != got.stdout {
				t.Fatalf("structured error changed stream exit or output: %+v", structured)
			}
			if tc.name == "SDK error envelope" {
				event, _ := value["error"].(map[string]any)
				if event["code"] != "missing_required_parameter" || event["message"] != "private-fixture" {
					t.Fatalf("structured SDK event changed: %+v", value)
				}
			} else if message, _ := value["message"].(string); !strings.HasSuffix(message, "\nOutput may be incomplete.") {
				t.Fatalf("structured result error changed: %+v", value)
			}
		})
	}
}

func TestMainBatchMissingInputShowsCommandHelpAfterUpload(t *testing.T) {
	var uploads, creations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.Copy(io.Discard, r.Body)
		switch r.Method + " " + r.URL.Path {
		case "POST /files":
			uploads.Add(1)
			_, _ = io.WriteString(w, `{"id":"file_fixture","purpose":"batch"}`)
		case "POST /batches":
			creations.Add(1)
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"error":{"code":"missing_required_parameter","param":"input_file_id","message":"private-fixture"}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	path := simpleBatchPath(t, simpleBatchInput)
	help := runReadableCommand(t, server, "batches", "submit", "--help")
	got := runReadableCommand(t, server, "batches", "submit", path)
	if got.code != 1 || got.stdout != "" || help.code != 0 || !strings.HasSuffix(got.stderr, "\n\n"+help.stdout) ||
		!strings.Contains(got.stderr, "Uploaded input file:") || !strings.Contains(got.stderr, "HTTP 422:") ||
		strings.Contains(got.stderr, "private-fixture") || uploads.Load() != 1 || creations.Load() != 1 {
		t.Fatalf("batch missing input lost help or recovery: %+v; explicit help: %+v", got, help)
	}
	structured := runReadableCommand(t, server, "--format-error", "json", "batches", "submit", path)
	value := decodeMainErrorObject(t, "json", structured.stderr)
	apiError, _ := value["error"].(map[string]any)
	if structured.code != 1 || structured.stdout != "" || apiError["code"] != "missing_required_parameter" ||
		apiError["message"] != "private-fixture" || value["batch_submission"] == nil || uploads.Load() != 2 || creations.Load() != 2 {
		t.Fatalf("structured batch recovery changed: %+v", structured)
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
