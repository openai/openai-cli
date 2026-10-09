package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const webhookTestResult = `{"object":"webhook_endpoint.test","webhook_endpoint_id":"wh_demo","event_type":"response.completed","status_code":500,"success":true}`

const webhookTestReadable500 = "Test request completed.\nDelivery failed: endpoint returned HTTP 500.\nWebhook endpoint ID: \"wh_demo\"\nEvent type: \"response.completed\"\n"

func webhookTestCommand(flags ...string) []string {
	return append(flags, "webhooks", "test", "--webhook-endpoint-id", "wh_demo", "--event-type", "response.completed")
}

func webhookTestServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/webhook_endpoints/wh_demo/test" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["event_type"] != "response.completed" {
			t.Errorf("request body = %#v, error = %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "synthetic-webhook-request")
		w.Header().Set("X-Receiver-Detail", "synthetic-private-header")
		if _, err := io.WriteString(w, response); err != nil {
			t.Errorf("write synthetic response: %v", err)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		if calls.Load() != 1 {
			t.Errorf("request count = %d, want 1", calls.Load())
		}
	})
	return server
}

func TestMainWebhookTestReceiverStatus(t *testing.T) {
	for _, status := range []int{200, 202, 300, 500} {
		for _, format := range []string{"", "auto", "text", "TeXt"} {
			t.Run(fmt.Sprintf("%d/%s", status, format), func(t *testing.T) {
				response := strings.Replace(webhookTestResult, `"status_code":500`, fmt.Sprintf(`"status_code":%d`, status), 1)
				server := webhookTestServer(t, response)
				var flags []string
				if format != "" {
					flags = []string{"--format", format}
				}
				got := runReadableCommand(t, server, webhookTestCommand(flags...)...)
				outcome := "failed"
				if status >= 200 && status < 300 {
					outcome = "accepted"
				}
				want := fmt.Sprintf("Test request completed.\nDelivery %s: endpoint returned HTTP %d.\nWebhook endpoint ID: \"wh_demo\"\nEvent type: \"response.completed\"\n", outcome, status)
				if got.code != 0 || got.stderr != "" || got.stdout != want {
					t.Fatalf("receiver status changed process outcome: got %+v, want stdout %q and exit 0", got, want)
				}
			})
		}
	}
}

func TestMainWebhookTestPreservesMachineFormats(t *testing.T) {
	for _, response := range []string{
		webhookTestResult,
		strings.TrimSuffix(webhookTestResult, "}") + `,"future":{"sequence":9007199254740993,"decimal":1.2300,"items":[null,false,{"text":"synthetic"}]}}`,
	} {
		for _, format := range []string{"json", "JSON", "jsonl", "JsOnL", "raw", "RAW"} {
			t.Run(fmt.Sprintf("%s/%d", format, len(response)), func(t *testing.T) {
				server := webhookTestServer(t, response)
				got := runReadableCommand(t, server, webhookTestCommand("--format", format)...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("unexpected machine result: %+v", got)
				}
				if strings.EqualFold(format, "json") {
					var compact bytes.Buffer
					if err := json.Compact(&compact, []byte(got.stdout)); err != nil || compact.String() != response {
						t.Fatalf("JSON field bytes changed: %q, error %v", got.stdout, err)
					}
				} else if got.stdout != response+"\n" {
					t.Fatalf("%s bytes changed: %q, want %q", format, got.stdout, response+"\n")
				}
			})
		}
	}
	t.Run("raw whitespace", func(t *testing.T) {
		response := "{\n  \"object\": \"webhook_endpoint.test\",\n  \"status_code\": 500, \"success\": true\n}"
		server := webhookTestServer(t, response)
		got := runReadableCommand(t, server, webhookTestCommand("--format", "raw")...)
		if got.code != 0 || got.stderr != "" || got.stdout != response+"\n" {
			t.Fatalf("raw bytes changed: %+v", got)
		}
	})
}

func TestMainWebhookTestPreservesExtractionAndRawOutput(t *testing.T) {
	var indented bytes.Buffer
	if err := json.Indent(&indented, []byte(webhookTestResult), "", "  "); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		flags []string
		want  string
	}{
		{"status", []string{"--transform", "status_code"}, "500\n"},
		{"success", []string{"--transform", "success"}, "true\n"},
		{"event", []string{"--transform", "event_type"}, "\"response.completed\"\n"},
		{"explicit auto", []string{"--format", "auto", "--transform", "event_type"}, "\"response.completed\"\n"},
		{"explicit text", []string{"--format", "text", "--transform", "event_type"}, "response.completed\n"},
		{"raw string", []string{"--transform", "event_type", "--raw-output"}, "response.completed\n"},
		{"raw object", []string{"--raw-output"}, indented.String() + "\n"},
		{"raw object explicit text", []string{"--format", "text", "--raw-output"}, "Object: webhook_endpoint.test\nWebhook endpoint ID: wh_demo\nEvent type: response.completed\nStatus code: 500\nSuccess: true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := webhookTestServer(t, webhookTestResult)
			got := runReadableCommand(t, server, webhookTestCommand(tc.flags...)...)
			if got.code != 0 || got.stderr != "" || got.stdout != tc.want {
				t.Fatalf("extraction changed: got %+v, want %q", got, tc.want)
			}
		})
	}
}

func TestMainWebhookTestPreservesUnrecognizedResults(t *testing.T) {
	for _, tc := range []struct {
		name, old, replacement, want string
	}{
		{"missing status", `"status_code":500,`, "", "Success: true"},
		{"null status", `"status_code":500`, `"status_code":null`, "Status code: (null)"},
		{"string status", `"status_code":500`, `"status_code":"500"`, "Status code: 500"},
		{"fraction status", `"status_code":500`, `"status_code":200.5`, "Status code: 200.5"},
		{"out of range status", `"status_code":500`, `"status_code":999`, "Status code: 999"},
		{"test execution false", `"success":true`, `"success":false`, "Success: false"},
		{"missing success", `,"success":true`, "", "Status code: 500"},
		{"string success", `"success":true`, `"success":"true"`, "Success: true"},
		{"null success", `"success":true`, `"success":null`, "Success: (null)"},
		{"missing endpoint", `"webhook_endpoint_id":"wh_demo",`, "", "Event type: response.completed"},
		{"null endpoint", `"webhook_endpoint_id":"wh_demo"`, `"webhook_endpoint_id":null`, "Webhook endpoint ID: (null)"},
		{"malformed event", `"event_type":"response.completed"`, `"event_type":false`, "Event type: false"},
		{"unknown field", `"success":true`, `"success":true,"future":{"sequence":9007199254740993,"detail":"retained"}`, "Future:\n  Sequence: 9007199254740993\n  Detail: retained"},
		{"receiver headers", `"success":true`, `"success":true,"response_headers":{"x_receiver":"retained"}`, "Response headers:\n  X receiver: retained"},
		{"duplicate status", `"status_code":500`, `"status_code":500,"status_code":200`, "Status code: 500\nStatus code: 200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := webhookTestServer(t, strings.Replace(webhookTestResult, tc.old, tc.replacement, 1))
			got := runReadableCommand(t, server, webhookTestCommand()...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, tc.want) {
				t.Fatalf("fallback lost fields: %+v, want %q", got, tc.want)
			}
			if strings.Contains(got.stdout, "Test request completed.") || strings.Contains(got.stdout, "Delivery ") {
				t.Fatalf("fallback invented a summary: %q", got.stdout)
			}
		})
	}
}

func TestMainWebhookTestContextEscapesControls(t *testing.T) {
	response := strings.Replace(webhookTestResult, `"wh_demo"`, `"wh_\u001b[31m\r\nforged"`, 1)
	response = strings.Replace(response, `"response.completed"`, `"response.\t\u0007\u202ecompleted"`, 1)
	server := webhookTestServer(t, response)
	got := runReadableCommand(t, server, webhookTestCommand()...)
	want := "Test request completed.\nDelivery failed: endpoint returned HTTP 500.\nWebhook endpoint ID: \"wh_\\x1b[31m\\r\\nforged\"\nEvent type: \"response.\\t\\a\\u202ecompleted\"\n"
	if got.code != 0 || got.stderr != "" || got.stdout != want {
		t.Fatalf("external context changed presentation structure: %+v, want %q", got, want)
	}
}

func TestMainWebhookTestQuietAndDebugHeaders(t *testing.T) {
	for _, flags := range [][]string{{"--quiet"}, {"--debug"}} {
		t.Run(strings.Join(flags, "/"), func(t *testing.T) {
			server := webhookTestServer(t, webhookTestResult)
			got := runReadableCommand(t, server, webhookTestCommand(flags...)...)
			if got.code != 0 || got.stdout != webhookTestReadable500 {
				t.Fatalf("output policy changed result: %+v", got)
			}
			if flags[0] == "--quiet" && got.stderr != "" {
				t.Fatalf("quiet produced diagnostics: %q", got.stderr)
			}
			if flags[0] == "--debug" {
				for _, want := range []string{"X-Request-Id: synthetic-webhook-request", "X-Receiver-Detail: <REDACTED>"} {
					if !strings.Contains(got.stderr, want) {
						t.Errorf("debug headers missing %q: %q", want, got.stderr)
					}
				}
				if strings.Contains(got.stdout+got.stderr, "synthetic-private-header") || strings.Contains(got.stdout+got.stderr, "sk-fake-readable-test") {
					t.Error("debug output exposed a protected header")
				}
			}
		})
	}
}

func TestMainWebhookTestAcceptedInputs(t *testing.T) {
	for _, tc := range []struct {
		name, stdin string
		args        []string
	}{
		{"positional endpoint", "", []string{"webhooks", "test", "wh_demo", "--event-type", "response.completed"}},
		{"JSON body", `{"event_type":"response.completed"}`, []string{"webhooks", "test", "--webhook-endpoint-id", "wh_demo"}},
		{"JSON path parameter", `{"webhook_endpoint_id":"wh_demo","event_type":"response.completed"}`, []string{"webhooks", "test"}},
		{"YAML body", "event_type: response.completed\n", []string{"webhooks", "test", "--webhook-endpoint-id", "wh_demo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := webhookTestServer(t, webhookTestResult)
			var stdin *os.File
			if tc.stdin != "" {
				path := filepath.Join(t.TempDir(), "request")
				if err := os.WriteFile(path, []byte(tc.stdin), 0o600); err != nil {
					t.Fatal(err)
				}
				var err error
				stdin, err = os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer stdin.Close()
			}
			got := runMainDispatchWithStdin(t, "bash", []string{
				"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-webhook-test", "FORCE_COLOR=0",
			}, stdin, append([]string{"openai"}, tc.args...)...)
			if got.code != 0 || got.stderr != "" || got.stdout != webhookTestReadable500 {
				t.Fatalf("accepted input changed: %+v", got)
			}
		})
	}
}

func TestMainWebhookTestAPIFailures(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"synthetic failure\u001b[31m","type":"invalid_request_error"}}`,
		"", "{}", `{"error":`,
	} {
		for _, flags := range [][]string{nil, {"--format", "json"}, {"--quiet", "--format-error", "jsonl"}} {
			t.Run(fmt.Sprintf("%s/%s", body, strings.Join(flags, "/")), func(t *testing.T) {
				got := runMainCommandAPIErrorResponse(t, http.StatusBadRequest, "application/json", body, webhookTestCommand(), flags...)
				if strings.Contains(got.stderr, "Test request completed.") || strings.Contains(got.stderr, "Delivery ") || strings.ContainsAny(got.stderr, "\x1b\r") {
					t.Fatalf("API failure received success formatting or terminal controls: %+v", got)
				}
				if len(flags) > 0 {
					decodeMainStructuredError(t, flags[len(flags)-1], got.stderr)
				} else if strings.TrimSpace(got.stderr) == "" {
					t.Fatal("API failure produced no diagnostic")
				}
			})
		}
	}
}
