package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainWebhookTestMinimalResults(t *testing.T) {
	for _, tc := range []struct {
		name, response, outcome string
	}{
		{"minimal receiver failure", `{"success":true,"status_code":500}`, "Delivery failed: endpoint returned HTTP 500."},
		{"minimal accepted delivery", `{"success":true,"status_code":202}`, "Delivery accepted: endpoint returned HTTP 202."},
		{"missing endpoint", strings.Replace(webhookTestResult, `"webhook_endpoint_id":"wh_demo",`, "", 1), "Delivery failed: endpoint returned HTTP 500."},
		{"missing event", strings.Replace(webhookTestResult, `"event_type":"response.completed",`, "", 1), "Delivery failed: endpoint returned HTTP 500."},
		{"missing object", strings.Replace(webhookTestResult, `"object":"webhook_endpoint.test",`, "", 1), "Delivery failed: endpoint returned HTTP 500."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := webhookTestServer(t, tc.response)
			got := runReadableCommand(t, server, webhookTestCommand()...)
			want := webhookTestRequestContext + "Test request completed.\n" + tc.outcome + "\n"
			if got.code != 0 || got.stdout != want || !strings.Contains(got.stderr, "Next:") {
				t.Fatalf("minimal result omitted outcome or recovery: %+v, want %q", got, want)
			}
			if strings.Contains(got.stderr, "unexpected test result") {
				t.Fatalf("minimal result used an unhelpful fallback: %q", got.stderr)
			}
		})
	}
}

func TestMainWebhookTestContextEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, input, event, response string
	}{
		{"repeated resource in API base", "/webhook_endpoints/proxy/v1", `{"event_type":"response.completed"}`, "response.completed", webhookTestResult},
		{"canonical null is not case alias", "", `{"EVENT_TYPE":"batch.failed","event_type":null}`, "(unavailable)", webhookTestResult},
		{"unknown result retains context", "", `{"event_type":"response.completed"}`, "response.completed", `{"success":true,"status_code":500,"future_field":9007199254740993}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != tc.prefix+"/webhook_endpoints/wh_demo/test" {
					t.Errorf("request changed: %s %s", r.Method, r.URL.Path)
				}
				var got, want any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal([]byte(tc.input), &want); err != nil {
					t.Fatal(err)
				}
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(want)
				if string(gotJSON) != string(wantJSON) {
					t.Errorf("body changed: got %s want %s", gotJSON, wantJSON)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(path, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			stdin, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			got := runMainDispatchWithStdin(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL + tc.prefix, "OPENAI_API_KEY=sk-fake-webhook-context", "FORCE_COLOR=0"}, stdin,
				"openai", "webhooks", "test", "wh_demo")
			prefix := "Project: determined by API key (ID unavailable)\nEndpoint: wh_demo\nEvent: " + tc.event + "\n\n"
			if got.code != 0 || calls.Load() != 1 || !strings.HasPrefix(got.stdout, prefix) {
				t.Fatalf("request context mismatch: %+v calls=%d want prefix=%q", got, calls.Load(), prefix)
			}
			if strings.Contains(tc.response, "future_field") && (!strings.Contains(got.stdout, "9007199254740993") || !strings.Contains(got.stderr, "No delivery outcome was inferred")) {
				t.Fatalf("unknown response data lost: %+v", got)
			}
			if tc.event == "(unavailable)" && (strings.Contains(got.stdout, "batch.failed") || strings.Contains(got.stderr, "--event-type=")) {
				t.Fatalf("noncanonical event became request context: %+v", got)
			}
		})
	}
}

func TestMainWebhookTestEffectiveProjectContext(t *testing.T) {
	for _, tc := range []struct {
		name, project string
		env, flags    []string
	}{
		{name: "key determines project"},
		{name: "environment", project: "proj_environment", env: []string{"OPENAI_PROJECT_ID=proj_environment"}},
		{name: "flag overrides environment", project: "proj_flag", env: []string{"OPENAI_PROJECT_ID=proj_environment"}, flags: []string{"--project=proj_flag"}},
		{name: "empty flag overrides environment", env: []string{"OPENAI_PROJECT_ID=proj_environment"}, flags: []string{"--project="}},
		{name: "environment header", project: "proj_environment_header", env: []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_environment_header"}},
		{name: "flag overrides environment header", project: "proj_flag", env: []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_environment_header"}, flags: []string{"--project=proj_flag"}},
		{name: "header overrides project", project: "proj_header", env: []string{"OPENAI_PROJECT_ID=proj_environment"}, flags: []string{"--project=proj_flag", "--header=OpenAI-Project: proj_header"}},
		{name: "last header wins", project: "proj_last_header", flags: []string{"--header=OpenAI-Project: proj_first_header", "-H=openai-project: proj_last_header"}},
		{name: "empty header overrides project", flags: []string{"--project=proj_flag", "--header=OpenAI-Project:"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/webhook_endpoints/wh_demo/test" || r.Header.Get("OpenAI-Project") != tc.project {
					t.Errorf("request context changed: method=%q path=%q project=%q", r.Method, r.URL.Path, r.Header.Get("OpenAI-Project"))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, webhookTestResult)
			}))
			defer server.Close()
			env := append([]string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-webhook-context", "FORCE_COLOR=0"}, tc.env...)
			got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, webhookTestCommand(tc.flags...)...)...)
			project := tc.project
			if project == "" {
				project = "determined by API key (ID unavailable)"
			}
			want := "Project: " + project + "\nEndpoint: wh_demo\nEvent: response.completed\n\nTest request completed.\nDelivery failed: endpoint returned HTTP 500.\n"
			if got.code != 0 || got.stdout != want || calls.Load() != 1 {
				t.Fatalf("display did not match effective project: %+v, calls=%d, want %q", got, calls.Load(), want)
			}
			if strings.Contains(got.stdout+got.stderr, "sk-fake-webhook-context") {
				t.Fatal("project context revealed the credential")
			}
		})
	}
}

func TestMainWebhookTestContextUsesPreparedRequest(t *testing.T) {
	eventFile := filepath.Join(t.TempDir(), "event with spaces.txt")
	if err := os.WriteFile(eventFile, []byte("response.completed"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, stdin, event, eventDisplay string
		args                             []string
		padding                          int
	}{
		{name: "explicit flags", event: "response.completed", args: []string{"--webhook-endpoint-id=wh_requested", "--event-type=response.completed"}},
		{name: "positional endpoint", event: "response.completed", args: []string{"wh_requested", "--event-type=response.completed"}},
		{name: "JSON stdin", event: "response.completed", stdin: `{"webhook_endpoint_id":"wh_requested","event_type":"response.completed"}`},
		{name: "YAML stdin", event: "response.completed", stdin: "webhook_endpoint_id: wh_requested\nevent_type: response.completed\n"},
		{name: "flags override stdin", event: "response.completed", stdin: `{"webhook_endpoint_id":"wh_from_stdin","event_type":"batch.failed"}`, args: []string{"--webhook-endpoint-id=wh_requested", "--event-type=response.completed"}},
		{name: "event file", event: "response.completed", args: []string{"wh_requested", "--event-type=@" + eventFile}},
		{name: "escaped literal event", event: "@sample.literal", eventDisplay: `"@sample.literal"`, args: []string{"wh_requested", `--event-type=\@sample.literal`}},
		{name: "large unrelated request field", event: "response.completed", stdin: `{"webhook_endpoint_id":"wh_requested","event_type":"response.completed","padding":"` + strings.Repeat("x", 2<<20) + `"}`, padding: 2 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/webhook_endpoints/wh_requested/test" {
					t.Errorf("request target changed: %q %q", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["event_type"] != tc.event {
					t.Errorf("request event changed: event=%q error=%v", body["event_type"], err)
				}
				if _, found := body["webhook_endpoint_id"]; found {
					t.Error("path parameter leaked into the body")
				}
				if tc.padding != 0 {
					padding, _ := body["padding"].(string)
					if len(padding) != tc.padding || strings.Trim(padding, "x") != "" {
						t.Error("request inspection changed the large unrelated field")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"object":"webhook_endpoint.test","webhook_endpoint_id":"wh_response_other","event_type":"batch.failed","status_code":500,"success":true}`)
			}))
			defer server.Close()
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
			args := append([]string{"openai", "webhooks", "test"}, tc.args...)
			got := runMainDispatchWithStdin(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-webhook-context", "FORCE_COLOR=0"}, stdin, args...)
			display := tc.eventDisplay
			if display == "" {
				display = tc.event
			}
			want := "Project: determined by API key (ID unavailable)\nEndpoint: wh_requested\nEvent: " + display + "\n\nTest request completed.\nDelivery failed: endpoint returned HTTP 500.\n"
			if got.code != 0 || got.stdout != want || calls.Load() != 1 {
				t.Fatalf("display did not match prepared request: %+v, calls=%d, want %q", got, calls.Load(), want)
			}
			if strings.Contains(got.stderr, "wh_response_other") || strings.Contains(got.stderr, "batch.failed") || strings.Contains(got.stderr, eventFile) {
				t.Fatalf("recovery used response data or a file reference: %q", got.stderr)
			}
			if tc.eventDisplay != "" && strings.Contains(got.stderr, "After fixing the receiver, retry:") {
				t.Fatalf("literal @ event became a file-expanding retry: %q", got.stderr)
			}
		})
	}
}

func TestMainWebhookTestRequestContextEscapesControls(t *testing.T) {
	const project = "proj_\u202eevil"
	const endpoint = "wh_\x1b[31m\r\nforged"
	const event = "response.\t\a\u202ecompleted"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhook_endpoints/"+endpoint+"/test" || r.Header.Get("OpenAI-Project") != project {
			t.Errorf("request context changed: path=%q project=%q", r.URL.Path, r.Header.Get("OpenAI-Project"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["event_type"] != event {
			t.Errorf("request event changed: event=%q error=%v", body["event_type"], err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, webhookTestResult)
	}))
	defer server.Close()
	got := runReadableCommand(t, server, "--project="+project, "webhooks", "test", endpoint, "--event-type="+event)
	want := "Project: \"proj_\\u202eevil\"\nEndpoint: \"wh_\\x1b[31m\\r\\nforged\"\nEvent: \"response.\\t\\a\\u202ecompleted\"\n\nTest request completed.\nDelivery failed: endpoint returned HTTP 500.\n"
	if got.code != 0 || got.stdout != want || !strings.Contains(got.stderr, "Next:") {
		t.Fatalf("request context was not safely displayed: %+v, want %q", got, want)
	}
	if strings.ContainsAny(got.stdout+got.stderr, "\x1b\r\a\u202e") || strings.Contains(got.stderr, "webhooks retrieve") || strings.Contains(got.stderr, "webhooks test") {
		t.Fatalf("unsafe request context reached terminal or copied commands: %+v", got)
	}
}

func TestMainWebhookTestCopiedRecoveryUsesRequestContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash/zsh execution requires a Unix host")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range []nativeShell{
		{name: "bash", executable: "bash", args: []string{"--noprofile", "--norc", "-c"}},
		{name: "zsh", executable: "zsh", args: []string{"-f", "-c"}},
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
			var tests, inspections atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("OpenAI-Project") != "proj_requested" || r.Header.Get("OpenAI-Organization") != "org_requested" || r.Header.Get("Authorization") != "Bearer sk-fake-readable-test" {
					t.Error("copied recovery changed request headers")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "POST /webhook_endpoints/wh_requested/test":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["event_type"] != "response.completed" {
						t.Errorf("copied recovery changed event: event=%q error=%v", body["event_type"], err)
					}
					status := 500
					if tests.Add(1) == 2 {
						status = 200
					}
					_, _ = fmt.Fprintf(w, `{"object":"webhook_endpoint.test","webhook_endpoint_id":"wh_response_other","event_type":"batch.failed","status_code":%d,"success":true}`, status)
				case "GET /webhook_endpoints/wh_requested":
					inspections.Add(1)
					_, _ = io.WriteString(w, `{"id":"wh_requested","object":"webhook_endpoint","url":"https://receiver.example.invalid/webhook","event_types":["response.completed"]}`)
				default:
					t.Errorf("recovery targeted another resource: %q %q", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "--project=proj_requested", "--organization=org_requested", "--base-url="+server.URL, "webhooks", "test", "wh_requested", "--event-type=response.completed")
			if got.code != 0 || tests.Load() != 1 {
				t.Fatalf("initial test failed: %+v", got)
			}
			// Reuse the compiled test's production entrypoint; no nested build is needed.
			adapter := "openai() { OPENAI_CLI_MAIN_DISPATCH_PROCESS=1 " + quote(binary) + " -test.run=^TestMainDispatchProcess$ -- openai \"$@\"; }\nexport OPENAI_API_KEY=sk-fake-readable-test\n"
			for _, label := range []string{"Inspect the receiver URL: ", "After fixing the receiver, retry: "} {
				var command string
				for line := range strings.SplitSeq(got.stderr, "\n") {
					if value, ok := strings.CutPrefix(line, label); ok {
						command = value
					}
				}
				if command == "" || strings.Contains(command, "wh_response_other") || strings.Contains(command, "batch.failed") || strings.Contains(command, "sk-fake-") {
					t.Fatalf("missing or incorrect recovery command: %q", got.stderr)
				}
				result := runNativeShell(t, shell, t.TempDir(), t.TempDir(), "http://127.0.0.1:1", adapter+command)
				if result.code != 0 {
					t.Fatalf("copied command failed: %+v", result)
				}
				if strings.Contains(label, "retry:") && !strings.Contains(result.stdout, "Delivery accepted: endpoint returned HTTP 200.") {
					t.Fatalf("retry did not report recovery: %+v", result)
				}
			}
			if tests.Load() != 2 || inspections.Load() != 1 {
				t.Fatalf("unexpected recovery requests: tests=%d inspections=%d", tests.Load(), inspections.Load())
			}
		})
	}
}
