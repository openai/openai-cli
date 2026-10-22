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
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const webhookCreatedResult = `{"object":"webhook_endpoint","id":"whe_demo","name":"Synthetic receiver","url":"https://example.com/webhook","event_types":["response.completed","batch.completed"],"signing_secret":"whsec_fake_for_tests_only","future":{"sequence":9007199254740993}}`
const webhookEventCatalog = `{"object":"list","data":["response.completed","batch.completed","response.failed","future.synthetic_event"]}`

func TestMainWebhookWorkflowHelp(t *testing.T) {
	for _, full := range []bool{false, true} {
		for _, operation := range []string{"", "create", "retrieve", "update", "list", "test", "rotate-secret", "delete", "event-types list"} {
			t.Run(fmt.Sprintf("full=%t/%s", full, operation), func(t *testing.T) {
				args := append([]string{"openai", "webhooks"}, strings.Fields(operation)...)
				if full {
					args = append([]string{"openai", "help"}, args[1:]...)
				} else {
					args = append(args, "--help")
				}
				got := runMainDispatch(t, "bash", args...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("webhook help failed: %+v", got)
				}
				section := webhookHelpExamples(t, got.stdout)
				if strings.Count(section, "\n     openai ") != 1 {
					t.Fatalf("expected one runnable example: %q", section)
				}
				if operation == "" {
					for _, name := range []string{"create", "retrieve", "update", "list", "test", "rotate-secret", "delete", "event-types"} {
						if !strings.Contains(got.stdout, name) {
							t.Errorf("webhook discovery omitted %q: %s", name, got.stdout)
						}
					}
				}
				if operation == "create" && !strings.Contains(got.stdout, "guided creation") {
					t.Errorf("create help omitted guided entrypoint: %s", got.stdout)
				}
				if operation == "test" && full && !strings.Contains(got.stdout, "exit status 0") {
					t.Errorf("test help omitted receiver failure status: %s", got.stdout)
				}
			})
		}
	}
}

func webhookHelpExamples(t *testing.T, output string) string {
	t.Helper()
	_, section, ok := strings.Cut(output, "EXAMPLES:\n")
	if !ok {
		t.Fatalf("help omitted its example: %s", output)
	}
	for _, next := range []string{"\nOPTIONS:", "\nCOMMANDS:", "\nGLOBAL OPTIONS:"} {
		section, _, _ = strings.Cut(section, next)
	}
	return "\n" + section
}

func TestMainWebhookWorkflowStatusAdvice(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{200, "verified the signature and processed the sample event"},
		{202, "receipt, not completed application work"},
		{302, "final HTTPS destination"},
		{401, "receiver access rules and webhook signature verification"},
		{403, "signing secret to verify the original request bytes"},
		{404, "exact path is deployed"},
		{405, "HTTP POST"},
		{413, "payload's size"},
		{415, "application/json"},
		{429, "rate limits and capacity"},
		{500, "receiver and proxy logs"},
		{503, "Fix the server error before retrying"},
		{504, "receiver and proxy timeouts"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			server := webhookTestServer(t, strings.Replace(webhookTestResult, `"status_code":500`, fmt.Sprintf(`"status_code":%d`, tc.status), 1))
			got := runReadableCommand(t, server, webhookTestCommand()...)
			if got.code != 0 || !strings.Contains(got.stderr, tc.want) || strings.Contains(got.stdout, "Next:") || strings.Contains(got.stderr, "Webhook creation is unconfirmed.") {
				t.Fatalf("receiver guidance changed output or exit status: %+v, want %q on stderr", got, tc.want)
			}
			if tc.status >= 300 && !strings.Contains(got.stderr, "webhooks retrieve --webhook-endpoint-id=wh_demo") {
				t.Fatalf("receiver failure omitted inspection command: %q", got.stderr)
			}
		})
	}
}

func TestMainWebhookWorkflowPreservesCreateInputsAndResult(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		flags      []string
	}{
		{"flags", "", []string{"--name", "Synthetic receiver", "--url", "https://example.com/webhook", "--event-type", "response.completed", "--event-type", "batch.completed"}},
		{"JSON stdin", `{"name":"@literal_name","url":"https://example.com/webhook","event_types":["response.completed","batch.completed"],"future_option":true}`, nil},
		{"YAML stdin", "name: '@literal_name'\nurl: https://example.com/webhook\nevent_types:\n  - response.completed\n  - batch.completed\nfuture_option: true\n", nil},
	} {
		for _, mode := range []string{"default", "text", "quiet", "json", "jsonl", "raw", "secret"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != http.MethodPost || r.URL.Path != "/webhook_endpoints" {
						t.Errorf("explicit create made unexpected request: %s %s", r.Method, r.URL.Path)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					want := map[string]any{"name": "Synthetic receiver", "url": "https://example.com/webhook", "event_types": []any{"response.completed", "batch.completed"}}
					if tc.body != "" {
						want["name"], want["future_option"] = "@literal_name", true
					}
					if !reflect.DeepEqual(body, want) {
						t.Errorf("create request changed: got %#v, want %#v", body, want)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, webhookCreatedResult)
				}))
				defer server.Close()
				flags := webhookWorkflowOutputFlags(mode)
				args := append(append(flags, "webhooks", "create"), tc.flags...)
				got := runWebhookWorkflowInput(t, server, tc.body, args...)
				if got.code != 0 || calls.Load() != 1 {
					t.Fatalf("explicit create changed lifecycle: %+v, requests=%d", got, calls.Load())
				}
				if mode == "json" || mode == "jsonl" || mode == "raw" {
					assertWebhookMachineResult(t, mode, got, webhookCreatedResult)
				} else if mode == "secret" {
					if got.stdout != "whsec_fake_for_tests_only\n" || got.stderr != "" {
						t.Fatalf("secret extraction changed: %+v", got)
					}
				} else {
					for _, field := range []string{"ID: whe_demo", "Name: Synthetic receiver", "URL: https://example.com/webhook", "Signing secret: whsec_fake_for_tests_only", "Sequence: 9007199254740993", "response.completed", "batch.completed"} {
						if !strings.Contains(got.stdout, field) {
							t.Errorf("create result lost %q: %q", field, got.stdout)
						}
					}
					if mode == "quiet" && got.stderr != "" {
						t.Errorf("quiet create wrote advice: %q", got.stderr)
					} else if mode != "quiet" {
						for _, want := range []string{"save the signing secret", "verify signatures", "webhooks test", "--webhook-endpoint-id=whe_demo", "--event-type=response.completed"} {
							if !strings.Contains(got.stderr, want) {
								t.Errorf("create advice omitted %q: %q", want, got.stderr)
							}
						}
					}
					if strings.Contains(got.stderr, "whsec_fake_for_tests_only") || strings.Contains(got.stdout, "Next:") {
						t.Fatalf("create advice mixed result data and diagnostics: %+v", got)
					}
				}
			})
		}
	}
}

func webhookWorkflowOutputFlags(mode string) []string {
	switch mode {
	case "default":
		return nil
	case "quiet":
		return []string{"--quiet"}
	case "secret":
		return []string{"--transform", "signing_secret", "--raw-output"}
	default:
		return []string{"--format", mode}
	}
}

func runWebhookWorkflowInput(t *testing.T, server *httptest.Server, body string, args ...string) mainDispatchResult {
	t.Helper()
	var stdin *os.File
	if body != "" {
		path := filepath.Join(t.TempDir(), "request")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		var err error
		stdin, err = os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer stdin.Close()
	}
	return runMainDispatchWithStdin(t, "bash", []string{
		"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-webhook-workflow", "FORCE_COLOR=0",
	}, stdin, append([]string{"openai"}, args...)...)
}

func assertWebhookMachineResult(t *testing.T, format string, got mainDispatchResult, response string) {
	t.Helper()
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("machine result acquired diagnostics: %+v", got)
	}
	if format == "json" {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(got.stdout)); err != nil || compact.String() != response {
			t.Fatalf("JSON data changed: %q, error=%v", got.stdout, err)
		}
	} else if got.stdout != response+"\n" {
		t.Fatalf("%s bytes changed: %q", format, got.stdout)
	}
}

func TestMainWebhookWorkflowCatalog(t *testing.T) {
	for _, route := range [][]string{{"webhooks", "event-types", "list"}, {"webhooks:event-types", "list"}} {
		for _, mode := range []string{"default", "text", "quiet", "json", "jsonl", "raw", "extract"} {
			t.Run(strings.Join(route, "/")+"/"+mode, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/webhook_event_types" {
						t.Errorf("unexpected catalog request: %s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, webhookEventCatalog)
				}))
				defer server.Close()
				flags := webhookWorkflowOutputFlags(mode)
				if mode == "extract" {
					flags = []string{"--transform", "data.0", "--raw-output"}
				}
				got := runReadableCommand(t, server, append(flags, route...)...)
				if got.code != 0 || calls.Load() != 1 {
					t.Fatalf("catalog request changed: %+v, requests=%d", got, calls.Load())
				}
				switch mode {
				case "json", "jsonl", "raw":
					assertWebhookMachineResult(t, mode, got, webhookEventCatalog)
				case "extract":
					if got.stdout != "response.completed\n" || got.stderr != "" {
						t.Fatalf("catalog extraction changed: %+v", got)
					}
				default:
					for _, name := range []string{"Background responses:", "Batches:", "Other:", "response.completed", "response.failed", "batch.completed", "future.synthetic_event"} {
						if strings.Count(got.stdout, name) != 1 {
							t.Errorf("catalog omitted or duplicated %q: %q", name, got.stdout)
						}
					}
					if mode == "quiet" && got.stderr != "" || mode != "quiet" && !strings.Contains(got.stderr, "Repeat --event-type") {
						t.Fatalf("catalog advice policy changed: %+v", got)
					}
				}
			})
		}
	}
}

func TestMainWebhookWorkflowDoesNotGuideScripts(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected API request", http.StatusBadRequest)
	}))
	defer server.Close()
	for _, flags := range [][]string{nil, {"--format", "json"}, {"--quiet"}, {"--name", "only a name"}} {
		got := runReadableCommand(t, server, append([]string{"webhooks", "create"}, flags...)...)
		if got.code == 0 || requests.Load() != 0 || strings.Contains(got.stdout+got.stderr, "Loading webhook event types") {
			t.Fatalf("nonterminal create entered guided workflow: %+v, requests=%d", got, requests.Load())
		}
	}
}

func TestMainWebhookWorkflowOmitsUnsafeFollowups(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, event string
		flags                 []string
	}{
		{"credential override", "wh_demo", "response.completed", []string{"--api-key", "sk-fake-private-override"}},
		{"custom header", "wh_demo", "response.completed", []string{"--header", "X-Synthetic: fake-private-header"}},
		{"at endpoint", "@synthetic-file", "response.completed", nil},
		{"at event", "wh_demo", "@synthetic-file", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/webhook_endpoints/"+tc.endpoint+"/test" {
					t.Errorf("request changed: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["event_type"] != tc.event {
					t.Errorf("request changed: body=%v error=%v", body, err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, webhookTestResult)
			}))
			defer server.Close()
			event := tc.event
			if strings.HasPrefix(event, "@") {
				event = `\` + event // Keep this request value literal instead of opening a file.
			}
			args := append(append([]string{}, tc.flags...), "webhooks", "test", "--webhook-endpoint-id="+tc.endpoint, "--event-type="+event)
			got := runReadableCommand(t, server, args...)
			if got.code != 0 || calls.Load() != 1 || !strings.Contains(got.stderr, "Next:") {
				t.Fatalf("unsafe-context result failed: %+v", got)
			}
			for _, private := range []string{"sk-fake-private-override", "fake-private-header", "@synthetic-file"} {
				if strings.Contains(got.stderr, private) {
					t.Errorf("followup advice copied unsafe value %q: %q", private, got.stderr)
				}
			}
			if strings.Contains(got.stderr, "After fixing the receiver, retry:") {
				t.Fatalf("unsafe context produced retry command: %q", got.stderr)
			}
		})
	}
}

func TestMainWebhookWorkflowCreateAPIFailureRecovery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		flags  []string
		retry  bool
	}{
		{"timeout", 408, nil, false},
		{"server error", 500, nil, false},
		{"unavailable", 503, nil, false},
		{"gateway timeout", 504, nil, false},
		{"quiet", 500, []string{"--quiet"}, false},
		{"private override", 500, []string{"--api-key", "sk-fake-private-override"}, false},
		{"SDK retries", 500, nil, true},
		{"unauthorized", 401, nil, false},
		{"invalid input", 400, nil, false},
		{"rate limited", 429, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var creates, lists atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := "sk-fake-webhook-workflow"
				if tc.name == "private override" {
					key = "sk-fake-private-override"
				}
				if r.Header.Get("OpenAI-Project") != "proj-f34" || r.Header.Get("OpenAI-Organization") != "org-f34" || r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("create or recovery changed request context")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "POST /webhook_endpoints":
					creates.Add(1)
					if tc.retry {
						w.Header().Set("Retry-After-Ms", "1")
					} else {
						w.Header().Set("X-Should-Retry", "false")
					}
					w.Header().Set("X-Request-ID", "req_synthetic_create")
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, `{"error":{"message":"synthetic-private-create-detail","type":"server_error"}}`)
				case "GET /webhook_endpoints":
					lists.Add(1)
					_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"whe_existing","name":"Synthetic receiver","url":"https://example.com/webhook"}],"has_more":false}`)
				default:
					t.Errorf("unexpected recovery request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			settings := append([]string{"openai", "--base-url=" + server.URL, "--project=proj-f34", "--organization=org-f34"}, tc.flags...)
			args := append(slices.Clone(settings), "webhooks", "create", "--name", "Synthetic receiver", "--url", "https://example.com/webhook", "--event-type", "response.completed")
			env := []string{"OPENAI_API_KEY=sk-fake-webhook-workflow", "FORCE_COLOR=0"}
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			attempts := int32(1)
			if tc.retry {
				attempts = 3
			}
			if got.code != 1 || got.stdout != "" || creates.Load() != attempts || lists.Load() != 0 || !strings.Contains(got.stderr, "Request ID: req_synthetic_create") {
				t.Fatalf("create failure changed lifecycle: %+v, creates=%d lists=%d", got, creates.Load(), lists.Load())
			}
			if strings.Contains(got.stderr, "synthetic-private-create-detail") || strings.Contains(got.stderr, "sk-fake-") {
				t.Fatalf("create guidance exposed private values: %q", got.stderr)
			}
			uncertain := tc.status == 408 || tc.status >= 500
			if strings.Contains(got.stderr, "Webhook creation is unconfirmed.") != uncertain {
				t.Fatalf("create failure misclassified outcome: %q", got.stderr)
			}
			if !uncertain {
				if strings.Contains(got.stderr, "List endpoints:") {
					t.Fatalf("non-server error acquired uncertain-create recovery: %q", got.stderr)
				}
				return
			}
			var copied string
			for line := range strings.SplitSeq(got.stderr, "\n") {
				if after, ok := strings.CutPrefix(line, "List endpoints: "); ok {
					copied = after
				}
			}
			var recovery []string
			if tc.name == "private override" {
				if copied != "" || !strings.Contains(got.stderr, "webhooks list with your original authentication, project, organization, and API settings") {
					t.Fatalf("private override lacks safe manual recovery: %q", got.stderr)
				}
				recovery = append(slices.Clone(settings), "webhooks", "list")
			} else {
				// These fixture arguments are plain tokens in every supported shell.
				// Execute the emitted command through the production entrypoint.
				if copied == "" || strings.ContainsAny(copied, "'\"`\\") {
					t.Fatalf("missing plain-token recovery: %q", got.stderr)
				}
				recovery = strings.Fields(copied)
			}
			listed := runMainDispatchWithEnv(t, "bash", env, recovery...)
			if listed.code != 0 || lists.Load() != 1 || creates.Load() != attempts || !strings.Contains(listed.stdout, "whe_existing") {
				t.Fatalf("read-only recovery failed or repeated create: %+v, creates=%d lists=%d", listed, creates.Load(), lists.Load())
			}
		})
	}
}

func TestMainWebhookWorkflowCreateTransportFailureRecovery(t *testing.T) {
	const message = "Could not connect to the API. Check your connection, proxy, and --base-url setting."
	for _, tc := range []struct {
		name, format string
		flags        []string
	}{
		{"readable", "", nil},
		{"quiet", "", []string{"--quiet"}},
		{"inherited JSON", "json", []string{"--format", "json"}},
		{"explicit JSON", "json", []string{"--format-error", "json"}},
		{"JSONL", "jsonl", []string{"--format-error", "jsonl"}},
		{"raw", "raw", []string{"--format-error", "raw"}},
		{"YAML", "yaml", []string{"--format-error", "yaml"}},
		{"extraction", "extract", []string{"--transform-error", "message"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var creates, lists atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("OpenAI-Project") != "proj-f34" || r.Header.Get("Authorization") != "Bearer sk-fake-webhook-workflow" {
					t.Error("transport recovery changed request context")
				}
				switch r.Method + " " + r.URL.Path {
				case "POST /webhook_endpoints":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["name"] != "Synthetic receiver" {
						t.Errorf("transport fixture received invalid create: %#v, error=%v", body, err)
					}
					creates.Add(1)
					// The API accepts the body before losing the response connection.
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					if err := connection.Close(); err != nil {
						t.Error(err)
					}
				case "GET /webhook_endpoints":
					lists.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"whe_existing","name":"Synthetic receiver","url":"https://example.com/webhook"}],"has_more":false}`)
				default:
					t.Errorf("unexpected transport recovery request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			settings := append([]string{"openai", "--base-url=" + server.URL, "--project=proj-f34"}, tc.flags...)
			args := append(slices.Clone(settings), "webhooks", "create", "--name", "Synthetic receiver", "--url", "https://example.com/webhook", "--event-type", "response.completed")
			env := []string{"OPENAI_API_KEY=sk-fake-webhook-workflow", "FORCE_COLOR=0"}
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 1 || got.stdout != "" || creates.Load() != 3 || lists.Load() != 0 {
				t.Fatalf("transport failure changed SDK retries or exit: %+v, creates=%d lists=%d", got, creates.Load(), lists.Load())
			}
			if tc.format == "extract" {
				// Automatic extraction preserves the existing JSON scalar format.
				quoted, _ := json.Marshal(message)
				if got.stderr != string(quoted)+"\n" {
					t.Fatalf("transport error extraction changed: %q", got.stderr)
				}
				return
			}
			if tc.format != "" {
				if actual := decodeMainErrorObject(t, tc.format, got.stderr); !reflect.DeepEqual(actual, map[string]any{"message": message}) {
					t.Fatalf("transport fallback payload changed: %#v", actual)
				}
				return
			}
			if !strings.Contains(got.stderr, message) || !strings.Contains(got.stderr, "Webhook creation is unconfirmed.") || strings.Contains(got.stderr, "sk-fake-") {
				t.Fatalf("transport failure lacks safe uncertainty guidance: %q", got.stderr)
			}
			var copied string
			for line := range strings.SplitSeq(got.stderr, "\n") {
				if after, ok := strings.CutPrefix(line, "List endpoints: "); ok {
					copied = after
				}
			}
			if copied == "" || strings.ContainsAny(copied, "'\"`\\") {
				t.Fatalf("missing plain-token transport recovery: %q", got.stderr)
			}
			listed := runMainDispatchWithEnv(t, "bash", env, strings.Fields(copied)...)
			if listed.code != 0 || lists.Load() != 1 || creates.Load() != 3 || !strings.Contains(listed.stdout, "whe_existing") {
				t.Fatalf("transport recovery repeated create or failed: %+v, creates=%d lists=%d", listed, creates.Load(), lists.Load())
			}
		})
	}
}

func TestMainWebhookWorkflowCreateLocalFailureDoesNotImplyDispatch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, flags := range [][]string{{"--mtls-client-cert-file", "synthetic-unpaired-cert.pem"}, nil} {
		args := append(slices.Clone(flags), "webhooks", "create", "--name", "Synthetic receiver")
		got := runWebhookWorkflowInput(t, server, "", args...)
		if got.code != 1 || calls.Load() != 0 || strings.Contains(got.stderr, "creation is unconfirmed") || strings.Contains(got.stderr, "List endpoints:") {
			t.Fatalf("local validation acquired uncertain-create guidance: %+v, requests=%d", got, calls.Load())
		}
		if len(flags) != 0 && !strings.Contains(got.stderr, "mTLS client certificate and key files must be configured together") {
			t.Fatalf("mTLS validation lost its actual recovery: %q", got.stderr)
		}
	}
}

func TestMainWebhookWorkflowCreateAPIFailureFormats(t *testing.T) {
	const details = `{"message":"synthetic-private-create-detail","type":"server_error","future_field":{"sequence":9007199254740993}}`
	want := decodeMainErrorObject(t, "json", details)
	for _, flag := range []string{"--format", "--format-error"} {
		for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
			t.Run(flag+"/"+format, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != http.MethodPost || r.URL.Path != "/webhook_endpoints" {
						t.Errorf("unexpected error-format request: %s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("X-Should-Retry", "false")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(w, `{"error":`+details+`}`)
				}))
				defer server.Close()
				args := []string{flag, format, "webhooks", "create", "--name", "Synthetic receiver", "--url", "https://example.com/webhook", "--event-type", "response.completed"}
				got := runWebhookWorkflowInput(t, server, "", args...)
				if got.code != 1 || got.stdout != "" || calls.Load() != 1 || !reflect.DeepEqual(decodeMainErrorObject(t, format, got.stderr), want) {
					t.Fatalf("create failure changed structured errors: %+v, calls=%d", got, calls.Load())
				}
				args = append([]string{"--transform-error", "future_field.sequence"}, args...)
				extracted := runWebhookWorkflowInput(t, server, "", args...)
				if extracted.code != 1 || extracted.stdout != "" || calls.Load() != 2 || strings.TrimSpace(extracted.stderr) != "9007199254740993" {
					t.Fatalf("create failure changed extraction: %+v, calls=%d", extracted, calls.Load())
				}
			})
		}
	}
}

func TestMainWebhookWorkflowPTYCreateAPIFailureStages(t *testing.T) {
	for _, discoveryFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("discovery-fails=%t", discoveryFails), func(t *testing.T) {
			var catalogs, creates, lists atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Should-Retry", "false")
				switch r.Method + " " + r.URL.Path {
				case "GET /webhook_event_types":
					catalogs.Add(1)
					if !discoveryFails {
						_, _ = io.WriteString(w, `{"object":"list","data":["response.completed"]}`)
						return
					}
				case "POST /webhook_endpoints":
					creates.Add(1)
				case "GET /webhook_endpoints":
					lists.Add(1)
					_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"whe_existing","name":"Synthetic receiver","url":"https://example.com/webhook"}],"has_more":false}`)
					return
				default:
					t.Errorf("unexpected guided request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":{"message":"synthetic-private-guided-error","type":"server_error"}}`)
			}))
			defer server.Close()
			got := runWebhookCreateWorkflowPTY(t, server, !discoveryFails)
			if got.code != 1 || catalogs.Load() != 1 || lists.Load() != 0 || strings.Contains(got.stderr, "synthetic-private") {
				t.Fatalf("guided API failure changed lifecycle: %+v, catalogs=%d creates=%d lists=%d", got, catalogs.Load(), creates.Load(), lists.Load())
			}
			if discoveryFails {
				if creates.Load() != 0 || !strings.Contains(got.stderr, "No endpoint was created.") || strings.Contains(got.stderr, "creation is unconfirmed") {
					t.Fatalf("discovery failure acquired a create outcome: %+v, creates=%d", got, creates.Load())
				}
				return
			}
			if creates.Load() != 1 || !strings.Contains(got.stderr, "Webhook creation is unconfirmed.") || strings.Contains(got.stderr, "No endpoint was created.") {
				t.Fatalf("confirmed create lost uncertainty: %+v, creates=%d", got, creates.Load())
			}
			if !strings.Contains(got.stderr, "List endpoints: openai webhooks list\n") {
				t.Fatalf("guided create omitted read-only recovery: %q", got.stderr)
			}
			listed := runWebhookWorkflowInput(t, server, "", "webhooks", "list")
			if listed.code != 0 || lists.Load() != 1 || creates.Load() != 1 || !strings.Contains(listed.stdout, "whe_existing") {
				t.Fatalf("guided recovery failed or repeated create: %+v, creates=%d lists=%d", listed, creates.Load(), lists.Load())
			}
		})
	}
}

func TestMainWebhookWorkflowPTYRejectsInvalidCatalogs(t *testing.T) {
	for _, tc := range []struct{ name, catalog string }{
		{"empty", `{"object":"list","data":[]}`},
		{"malformed JSON", `{"object":"list","data":`},
		{"malformed data", `{"object":"list","data":{}}`},
		{"duplicate object", `{"object":"list","object":"other","data":["response.completed"]}`},
		{"duplicate data", `{"object":"list","data":["response.completed"],"data":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, catalogs, creates := webhookCreateFailureFixture(t, tc.catalog, false)
			got := runWebhookCreateWorkflowPTY(t, server, false)
			if got.code == 0 || catalogs.Load() != 1 || creates.Load() != 0 {
				t.Fatalf("invalid catalog started creation: %+v, catalogs=%d creates=%d", got, catalogs.Load(), creates.Load())
			}
			if !strings.Contains(got.stderr, "webhooks event-types list") && !strings.Contains(got.stderr, "Check your project") {
				t.Fatalf("catalog failure lacks actionable stderr: %+v", got)
			}
			if strings.Contains(got.stdout, "Create webhook endpoint") || strings.Contains(got.stdout, "signing_secret") {
				t.Fatalf("invalid catalog opened the form or displayed a result: %q", got.stdout)
			}
		})
	}
}

func TestMainWebhookWorkflowPTYCreateUncertainOutcome(t *testing.T) {
	for _, drop := range []bool{false, true} {
		t.Run(fmt.Sprintf("dropped-response=%t", drop), func(t *testing.T) {
			server, catalogs, creates := webhookCreateFailureFixture(t, `{"object":"list","data":["response.completed"]}`, drop)
			got := runWebhookCreateWorkflowPTY(t, server, true)
			wantCreates := int32(1)
			if drop {
				// The existing SDK makes two retries after a transport failure.
				// The form must not add another generated-action submission.
				wantCreates = 3
			}
			if catalogs.Load() != 1 || creates.Load() != wantCreates {
				t.Fatalf("wizard resubmitted or changed SDK retries: %+v, catalogs=%d creates=%d", got, catalogs.Load(), creates.Load())
			}
			if drop {
				if got.code == 0 || !strings.Contains(got.stderr, "may already exist") || !strings.Contains(got.stderr, "list") {
					t.Fatalf("uncertain create omitted recovery: %+v", got)
				}
				if strings.Contains(got.stdout+got.stderr, "whsec_fake_for_tests_only") {
					t.Fatalf("dropped response invented a secret: %+v", got)
				}
			} else if got.code != 0 || !strings.Contains(got.stdout, "Signing secret: whsec_fake_for_tests_only") || !strings.Contains(got.stderr, "save the signing secret") {
				t.Fatalf("guided create lost its result or next step: %+v", got)
			}
		})
	}
}

func TestMainWebhookWorkflowCreateStdoutFailure(t *testing.T) {
	server, catalogs, creates := webhookCreateFailureFixture(t, "", false)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "read-only-output")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "webhooks", "create",
		"--name", "Synthetic receiver", "--url", "https://example.com/webhook", "--event-type", "response.completed")
	child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-webhook-workflow", "FORCE_COLOR=0", "GOMAXPROCS=2"}
	var stderr bytes.Buffer
	child.Stdout, child.Stderr = output, &stderr
	err = child.Run()
	if ctx.Err() != nil || err == nil || catalogs.Load() != 0 || creates.Load() != 1 {
		t.Fatalf("stdout failure changed create lifecycle: error=%v context=%v catalogs=%d creates=%d stderr=%q", err, ctx.Err(), catalogs.Load(), creates.Load(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "list") || !strings.Contains(stderr.String(), "before") || strings.Contains(stderr.String(), "whsec_fake_for_tests_only") {
		t.Fatalf("stdout failure lost safe recovery: %q", stderr.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 0 {
		t.Fatalf("read-only stdout changed: %q, error=%v", data, err)
	}
}

// Model an endpoint created before the connection drops. The fixture never
// contacts the receiver URL and counts every HTTP attempt separately.
func webhookCreateFailureFixture(t *testing.T, catalog string, drop bool) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var catalogs, creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /webhook_event_types":
			catalogs.Add(1)
			_, _ = io.WriteString(w, catalog)
		case "POST /webhook_endpoints":
			creates.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["name"] != "Synthetic receiver" || body["url"] != "https://example.com/webhook" || !reflect.DeepEqual(body["event_types"], []any{"response.completed"}) {
				t.Errorf("create fixture received unexpected request: %#v, error=%v", body, err)
			}
			if drop {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				if err := connection.Close(); err != nil {
					t.Error(err)
				}
				return
			}
			_, _ = io.WriteString(w, webhookCreatedResult)
		default:
			t.Errorf("unexpected workflow request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	return server, &catalogs, &creates
}

func runWebhookCreateWorkflowPTY(t *testing.T, server *httptest.Server, confirm bool) mainDispatchResult {
	return runWebhookCreateWorkflowPTYMode(t, server, webhookCreateWorkflowPTYOptions{Confirm: confirm})
}

func TestMainWebhookWorkflowPTYBrowseEndpointsKeepsCreationDraft(t *testing.T) {
	var catalogs, lists, creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /webhook_event_types":
			catalogs.Add(1)
			_, _ = io.WriteString(w, `{"object":"list","data":["response.completed"]}`)
		case "GET /webhook_endpoints":
			lists.Add(1)
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"whe_visible","name":"Synthetic first","url":"https://example.invalid/receiver","event_types":["response.failed"]}],"has_more":false}`)
		case "POST /webhook_endpoints":
			creates.Add(1)
			http.Error(w, "must not create", 500)
		default:
			t.Errorf("unexpected endpoint browser request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", 400)
		}
	}))
	defer server.Close()
	got := runWebhookCreateWorkflowPTYMode(t, server, webhookCreateWorkflowPTYOptions{Browse: true})
	if got.code != 130 || catalogs.Load() != 1 || lists.Load() != 1 || creates.Load() != 0 {
		t.Fatalf("endpoint browsing changed creation: %+v, catalogs=%d lists=%d creates=%d", got, catalogs.Load(), lists.Load(), creates.Load())
	}
	for _, text := range []string{"Synthetic first", "whe_visible", "https://example.invalid/receiver", "response.failed", "Draft receiver"} {
		if !strings.Contains(got.stdout, text) {
			t.Fatalf("endpoint browser omitted %q: %q", text, got.stdout)
		}
	}
}

func TestMainWebhookWorkflowPTYBrowseEndpointsCancelsPendingRequest(t *testing.T) {
	var catalogs, lists, canceled, creates atomic.Int32
	signals := t.TempDir()
	fixtureDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /webhook_event_types":
			catalogs.Add(1)
			_, _ = io.WriteString(w, `{"object":"list","data":["response.completed"]}`)
		case "GET /webhook_endpoints":
			if lists.Add(1) != 1 {
				t.Error("canceled endpoint read was retried")
				http.Error(w, "must not retry", http.StatusBadRequest)
				return
			}
			if err := os.WriteFile(filepath.Join(signals, "started"), nil, 0o600); err != nil {
				t.Error(err)
				return
			}
			select {
			case <-r.Context().Done():
				canceled.Add(1)
				if err := os.WriteFile(filepath.Join(signals, "canceled"), nil, 0o600); err != nil {
					t.Error(err)
				}
			case <-fixtureDone:
			}
		case "POST /webhook_endpoints":
			creates.Add(1)
			http.Error(w, "must not create", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected endpoint browser request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	t.Cleanup(func() {
		close(fixtureDone)
		server.Close()
	})
	got := runWebhookCreateWorkflowPTYMode(t, server, webhookCreateWorkflowPTYOptions{Browse: true, CancellationSignals: signals})
	if got.code != 130 || catalogs.Load() != 1 || lists.Load() != 1 || canceled.Load() != 1 || creates.Load() != 0 {
		t.Fatalf("browser cancellation changed request lifecycle: %+v, catalogs=%d lists=%d canceled=%d creates=%d", got, catalogs.Load(), lists.Load(), canceled.Load(), creates.Load())
	}
	_, returned, ok := strings.Cut(got.stdout, "Loading webhooks...")
	if !ok || !strings.Contains(returned, "Draft receiver") || !strings.Contains(returned, "https://example.invalid/draft") {
		t.Fatalf("browser cancellation did not return to the retained draft: %q", got.stdout)
	}
	if strings.Contains(got.stdout, "API error") || strings.Contains(got.stdout, "Could not load") || strings.Contains(got.stdout, "No webhook endpoints") {
		t.Fatalf("canceled browser read rendered a result after returning: %q", got.stdout)
	}
}

func TestMainWebhookWorkflowPTYBrowseEndpointsKeepsPartialResults(t *testing.T) {
	var catalogs, lists, creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Should-Retry", "false")
		switch r.Method + " " + r.URL.Path {
		case "GET /webhook_event_types":
			catalogs.Add(1)
			_, _ = io.WriteString(w, `{"object":"list","data":["response.completed"]}`)
		case "GET /webhook_endpoints":
			if lists.Add(1) == 1 {
				if after := r.URL.Query().Get("after"); after != "" {
					t.Errorf("first page supplied unexpected cursor: %q", after)
				}
				_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"whe_visible","name":"Synthetic first","url":"https://example.invalid/receiver","event_types":["response.failed"]}],"has_more":true}`)
				return
			}
			if after := r.URL.Query().Get("after"); after != "whe_visible" {
				t.Errorf("next page lost its cursor: %q", after)
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"message":"synthetic-private-browser-error"}}`)
		case "POST /webhook_endpoints":
			creates.Add(1)
			http.Error(w, "must not create", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected endpoint browser request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	got := runWebhookCreateWorkflowPTYMode(t, server, webhookCreateWorkflowPTYOptions{Browse: true})
	if got.code != 130 || catalogs.Load() != 1 || lists.Load() != 2 || creates.Load() != 0 {
		t.Fatalf("partial endpoint browsing changed creation: %+v, catalogs=%d lists=%d creates=%d", got, catalogs.Load(), lists.Load(), creates.Load())
	}
	for _, text := range []string{"API error (HTTP 403)", "Synthetic first", "whe_visible", "https://example.invalid/receiver", "response.failed"} {
		if !strings.Contains(got.stdout, text) {
			t.Fatalf("partial endpoint browser omitted %q: %q", text, got.stdout)
		}
	}
	_, returned, _ := strings.Cut(got.stdout, "API error (HTTP 403)")
	if !strings.Contains(returned, "Draft receiver") || !strings.Contains(returned, "https://example.invalid/draft") {
		t.Fatalf("partial endpoint browsing lost the creation draft: %q", got.stdout)
	}
	if strings.Contains(got.stdout, "No webhook endpoints") || strings.Contains(got.stdout+got.stderr, "synthetic-private-browser-error") {
		t.Fatalf("partial endpoint browser reported empty success or disclosed the error body: %+v", got)
	}
}

type webhookCreateWorkflowPTYOptions struct {
	Confirm, Browse     bool
	CancellationSignals string
}

func runWebhookCreateWorkflowPTYMode(t *testing.T, server *httptest.Server, options webhookCreateWorkflowPTYOptions) mainDispatchResult {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("guided creation PTY checks require a Unix host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for guided creation PTY checks")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(struct {
		Binary, Endpoint, Work string
		webhookCreateWorkflowPTYOptions
	}{binary, server.URL, t.TempDir(), options})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, python, "-c", webhookCreateWorkflowPTYScript)
	child.Env = []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8"}
	child.Stdin = bytes.NewReader(input)
	child.Cancel = func() error { return child.Process.Signal(os.Interrupt) }
	child.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil || ctx.Err() != nil || stderr.Len() != 0 {
		t.Fatalf("guided PTY harness failed: error=%v context=%v stdout=%q stderr=%q", err, ctx.Err(), stdout.String(), stderr.String())
	}
	var result struct {
		Code           int
		Stdout, Stderr string
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode guided PTY result: %v, %q", err, stdout.String())
	}
	return mainDispatchResult{result.Code, result.Stdout, result.Stderr}
}

const webhookCreateWorkflowPTYScript = `
import errno, fcntl, json, os, pty, selectors, struct, subprocess, sys, termios, time, tty
config = json.load(sys.stdin)
env = {"PATH": "/usr/bin:/bin", "HOME": config["Work"], "XDG_CONFIG_HOME": config["Work"],
       "TERM": "xterm-256color", "NO_COLOR": "1", "FORCE_COLOR": "0", "LANG": "en_US.UTF-8",
       "GOMAXPROCS": "2", "OPENAI_CLI_MAIN_DISPATCH_PROCESS": "1",
       "OPENAI_API_KEY": "sk-fake-webhook-workflow", "OPENAI_BASE_URL": config["Endpoint"]}
master, slave = pty.openpty()
tty.setraw(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 100, 0, 0))
selector = selectors.DefaultSelector()
output = [bytearray(), bytearray()]
process = None
completed = False
steps = [(b"\xe2\x80\xba Name", b"Synthetic receiver\x1b[Bhttps://example.com/webhook\x1b[B\x1b[B\x1b[B \r"),
         (b"Create endpoint now?", b"y\r\r\r")] if config["Confirm"] else []
if config["Browse"]:
    steps = [(b"\xe2\x80\xba Name", b"Draft receiver\x1b[Bhttps://example.invalid/draft\x1b[B\x1b[B\x1b[B\t\x1b[C\r"),
             (b"Synthetic first", b"\r"), (b"https://example.invalid/receiver", b"\x1b"),
             (b"\xe2\x80\xba Synthetic first", b"\x1b"), (b"\xe2\x80\xba [ Existing webhooks ]", b"\x1b")]
signals = {}
if config["CancellationSignals"]:
    steps = [steps[0], (b"Loading webhooks...", b"\x1b"),
             (b"\xe2\x80\xba [ Existing webhooks ]", b"\x1b")]
    # Cancel only after the server receives GET. Keep the process alive until
    # the server observes cancellation, so process exit cannot satisfy the test.
    signals = {1: "started", 2: "canceled"}
step = 0
step_start = 0
try:
    # Retain the supervisor's process group so its deadline covers this child.
    process = subprocess.Popen([config["Binary"], "-test.run=^TestMainDispatchProcess$", "--", "openai", "webhooks", "create"],
                               cwd=config["Work"], env=env, stdin=slave, stdout=slave,
                               stderr=subprocess.PIPE)
    os.close(slave)
    slave = None
    selector.register(master, selectors.EVENT_READ, 0)
    selector.register(process.stderr, selectors.EVENT_READ, 1)
    deadline = time.monotonic() + 15
    while selector.get_map() or process.poll() is None:
        if time.monotonic() > deadline:
            raise TimeoutError("guided create timed out; stage=" + str(step) + "; stdout=" + repr(bytes(output[0])) + "; stderr=" + repr(bytes(output[1])))
        for key, _ in selector.select(0.1):
            try:
                data = os.read(key.fd, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b""
            if not data:
                selector.unregister(key.fileobj)
                continue
            output[key.data].extend(data)
            if key.data == 0 and b"\x1b[6n" in data:
                os.write(master, b"\x1b[1;1R")
        if step < len(steps) and steps[step][0] in output[0][step_start:]:
            if step not in signals or os.path.exists(os.path.join(config["CancellationSignals"], signals[step])):
                os.write(master, steps[step][1])
                step_start = len(output[0])
                step += 1
    code = process.wait(timeout=2)
    if step != len(steps):
        raise AssertionError("guided create exited before all steps; stage=" + str(step))
    completed = True
finally:
    if process is not None and not completed:
        try:
            process.kill()
        except ProcessLookupError:
            pass
        process.wait(timeout=2)
    if process is not None and process.stderr is not None:
        process.stderr.close()
    selector.close()
    os.close(master)
    if slave is not None:
        os.close(slave)
print(json.dumps({"Code": code, "Stdout": output[0].decode("utf-8"), "Stderr": output[1].decode("utf-8")}))
`

func TestMainWebhookWorkflowNativeCopiedCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash/zsh invocation checks require a Unix host")
	}
	work := filepath.Join(t.TempDir(), "CLI with spaces & apostrophe's")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(work, "openai")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-p", "2", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	invocation := quote(binary)
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range []nativeShell{
		{"bash", "bash", "", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"--noprofile", "--norc", "-c"}},
		{"zsh", "zsh", "", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"-f", "-c"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required shell unavailable: %v", err)
				}
				t.Skipf("%s is not installed", shell.name)
			}
			shell.executable = path
			for _, lookup := range []string{"absent", "unrelated", "installed"} {
				t.Run(lookup, func(t *testing.T) {
					directory, home, searchPath := t.TempDir(), t.TempDir(), t.TempDir()
					printedInvocation := invocation
					if lookup == "unrelated" {
						if err := os.WriteFile(filepath.Join(searchPath, "openai"), []byte("#!/bin/sh\nprintf 'wrong executable\\n'\nexit 99\n"), 0o700); err != nil {
							t.Fatal(err)
						}
					} else if lookup == "installed" {
						searchPath, printedInvocation = work, "openai"
					}
					t.Setenv("PATH", searchPath)
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.Header.Get("Authorization") != "Bearer fake-native-shell-key" {
							t.Error("copied command changed authentication context")
						}
						w.Header().Set("Content-Type", "application/json")
						switch r.URL.Path {
						case "/webhook_endpoints":
							var body map[string]any
							if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["name"] != "Response notifications" || body["url"] != "https://example.com/webhook" || !reflect.DeepEqual(body["event_types"], []any{"response.completed"}) {
								t.Errorf("copied create example changed request: %#v, error=%v", body, err)
							}
							_, _ = io.WriteString(w, webhookCreatedResult)
						case "/webhook_event_types":
							_, _ = io.WriteString(w, webhookEventCatalog)
						case "/webhook_endpoints/whe_example/test":
							_, _ = io.WriteString(w, strings.Replace(webhookTestResult, "wh_demo", "whe_example", 1))
						default:
							t.Errorf("unexpected copied example: %s %s", r.Method, r.URL.Path)
						}
					}))
					defer server.Close()
					for _, operation := range []string{"create", "test", "event-types list"} {
						for _, full := range []bool{false, true} {
							helpArgs := " webhooks " + operation + " --help"
							if full {
								helpArgs = " help webhooks " + operation
							}
							before := calls.Load()
							help := runNativeShell(t, shell, directory, home, server.URL, invocation+helpArgs)
							if help.code != 0 || help.stderr != "" || calls.Load() != before {
								t.Fatalf("help failed or made a request: %+v", help)
							}
							var command string
							for line := range strings.SplitSeq(webhookHelpExamples(t, help.stdout), "\n") {
								if strings.HasPrefix(strings.TrimSpace(line), printedInvocation+" webhooks ") {
									command = strings.TrimSpace(line)
								}
							}
							if command == "" {
								t.Fatalf("example changed executable identity %q: %s", printedInvocation, help.stdout)
							}
							got := runNativeShell(t, shell, directory, home, server.URL, shell.setKey+command)
							if got.code != 0 || calls.Load() != before+1 || strings.Contains(got.stdout, "wrong executable") {
								t.Fatalf("copied example failed: %+v, requests=%d", got, calls.Load()-before)
							}
						}
					}
				})
			}
			t.Run("followups retain request context", func(t *testing.T) {
				var tests, inspections, creates atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("OpenAI-Project") != "project-'quoted" || r.Header.Get("OpenAI-Organization") != "org-synthetic" || r.Header.Get("Authorization") != "Bearer fake-native-shell-key" {
						t.Error("copied followup lost request context")
					}
					w.Header().Set("Content-Type", "application/json")
					switch r.Method + " " + r.URL.Path {
					case "POST /webhook_endpoints/wh_demo/test":
						tests.Add(1)
						_, _ = io.WriteString(w, webhookTestResult)
					case "GET /webhook_endpoints/wh_demo":
						inspections.Add(1)
						_, _ = io.WriteString(w, webhookCreatedResult)
					case "GET /webhook_endpoints":
						_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
					case "POST /webhook_endpoints":
						creates.Add(1)
						_, _ = io.WriteString(w, webhookCreatedResult)
					default:
						t.Errorf("unexpected copied followup: %s %s", r.Method, r.URL.Path)
					}
				}))
				defer server.Close()
				// Keep the verified native shell as the direct parent for quoting.
				keepParent := `; webhook_status=$?; exit "$webhook_status"`
				directory, home := t.TempDir(), t.TempDir()
				requestContext := invocation + " --base-url=" + quote(server.URL) + " --project=" + quote("project-'quoted") + " --organization=org-synthetic"
				command := requestContext + " webhooks test --webhook-endpoint-id=wh_demo --event-type=response.completed"
				got := runNativeShell(t, shell, directory, home, "http://127.0.0.1:1", shell.setKey+command+keepParent)
				if got.code != 0 || tests.Load() != 1 {
					t.Fatalf("initial context request failed: %+v", got)
				}
				for _, label := range []string{"Inspect the receiver URL: ", "After fixing the receiver, retry: "} {
					var copied string
					for line := range strings.SplitSeq(got.stderr, "\n") {
						if after, ok := strings.CutPrefix(line, label); ok {
							copied = after
						}
					}
					if copied == "" || strings.Contains(copied, "fake-native-shell-key") {
						t.Fatalf("missing or unsafe %q followup: %q", label, got.stderr)
					}
					result := runNativeShell(t, shell, directory, home, "http://127.0.0.1:1", shell.setKey+copied+keepParent)
					if result.code != 0 {
						t.Fatalf("copied followup failed: %+v", result)
					}
				}
				if tests.Load() != 2 || inspections.Load() != 1 {
					t.Fatalf("copied followups used wrong routes: tests=%d inspections=%d", tests.Load(), inspections.Load())
				}
				listed := runNativeShell(t, shell, directory, home, "http://127.0.0.1:1", shell.setKey+requestContext+" webhooks list"+keepParent)
				var create string
				for line := range strings.SplitSeq(listed.stderr, "\n") {
					if after, ok := strings.CutPrefix(line, "Create another endpoint: "); ok {
						create = after
					}
				}
				if listed.code != 0 || create == "" {
					t.Fatalf("list omitted contextual create command: %+v", listed)
				}
				created := runNativeShell(t, shell, directory, home, "http://127.0.0.1:1", shell.setKey+create+` --name "Synthetic receiver" --url https://example.com/webhook --event-type response.completed`+keepParent)
				if created.code != 0 || creates.Load() != 1 {
					t.Fatalf("copied list create command lost context: %+v, creates=%d", created, creates.Load())
				}
			})
		})
	}
}
