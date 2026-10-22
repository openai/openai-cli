package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/tidwall/gjson"
)

const fineTuningModelLimitsResponse = `{"object":"synthetic.future_limits","data":[{"model":"synthetic-model","future":{"exact":9007199254740993,"decimal":0.100000000000000001,"unset":null,"text":"quoted \"value\""}}],"has_more":true,"next_page":"synthetic-unfollowed-cursor"}`

func fineTuningModelLimitsEnvironment(t *testing.T, endpoint string) []string {
	t.Helper()
	home := t.TempDir()
	return []string{
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
		"OPENAI_BASE_URL=" + endpoint, "OPENAI_API_KEY=sk-fake-fine-tuning",
		"OPENAI_ADMIN_KEY=sk-fake-admin-unused", "OPENAI_ORG_ID=org_synthetic_fine_tuning",
		"OPENAI_PROJECT_ID=proj_synthetic_fine_tuning", "OPENAI_CUSTOM_HEADERS=X-Synthetic-Context: retained",
		"NO_COLOR=1", "FORCE_COLOR=0", "CI=1",
	}
}

func TestMainFineTuningModelLimitsRequestAndCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, key, admin, headers, authorization string
		flags                                    []string
		missing, overrideContext                 bool
	}{
		{name: "ordinary only", key: "sk-fake-fine-tuning", authorization: "Bearer sk-fake-fine-tuning"},
		{name: "both keys", key: "sk-fake-fine-tuning", admin: "sk-fake-admin-unused", authorization: "Bearer sk-fake-fine-tuning"},
		{name: "root overrides", key: "sk-fake-environment-unused", admin: "sk-fake-admin-unused", authorization: "Bearer sk-fake-flag",
			flags: []string{"--api-key=sk-fake-flag", "--admin-api-key=sk-fake-admin-flag-unused", "--organization=org_synthetic_override", "--project=proj_synthetic_override"}, overrideContext: true},
		{name: "explicit authorization", key: "sk-fake-fine-tuning-unused", admin: "sk-fake-admin-unused", authorization: "Bearer synthetic-header",
			flags: []string{"--header=Authorization: Bearer synthetic-header"}},
		{name: "explicit authorization without API key", admin: "sk-fake-admin-unused", authorization: "Bearer synthetic-header",
			flags: []string{"--header=Authorization: Bearer synthetic-header"}},
		{name: "environment authorization", admin: "sk-fake-admin-unused", authorization: "Bearer synthetic-last",
			headers: "Authorization: Bearer synthetic-first\nAUTHORIZATION: Bearer synthetic-last"},
		{name: "API key before environment authorization", key: "sk-fake-fine-tuning", admin: "sk-fake-admin-unused",
			authorization: "Bearer sk-fake-fine-tuning", headers: "Authorization: Bearer synthetic-header-unused"},
		{name: "empty API key with environment authorization", key: "sk-fake-environment-unused", admin: "sk-fake-admin-unused",
			authorization: "Bearer synthetic-header", headers: "Authorization: Bearer synthetic-header", flags: []string{"--api-key="}},
		{name: "admin only rejected", admin: "sk-fake-admin-unused", missing: true},
		{name: "missing credentials", missing: true},
		{name: "empty API key override", key: "sk-fake-environment-unused", admin: "sk-fake-admin-unused",
			flags: []string{"--api-key="}, missing: true},
		{name: "empty authorization override", key: "sk-fake-fine-tuning", admin: "sk-fake-admin-unused",
			flags: []string{"--header=Authorization:"}, missing: true},
		{name: "empty last environment authorization", admin: "sk-fake-admin-unused",
			headers: "Authorization: Bearer synthetic-first\nAuthorization:", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.RequestURI() != "/tenant/v1/fine_tuning/model_limits" {
					t.Errorf("unexpected read request: %s %s", r.Method, r.URL.RequestURI())
				}
				wantOrg, wantProject := "org_synthetic_fine_tuning", "proj_synthetic_fine_tuning"
				if tc.overrideContext {
					wantOrg, wantProject = "org_synthetic_override", "proj_synthetic_override"
				}
				if r.Header.Get("Authorization") != tc.authorization ||
					r.Header.Get("OpenAI-Organization") != wantOrg || r.Header.Get("OpenAI-Project") != wantProject ||
					r.Header.Get("X-Synthetic-Context") != "retained" {
					t.Error("ordinary credential selection or request context changed")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 {
					t.Errorf("read request has a body or read error: bytes=%d error=%v", len(body), err)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, fineTuningModelLimitsResponse)
			}))
			defer server.Close()
			env := fineTuningModelLimitsEnvironment(t, server.URL+"/tenant/v1/")
			env = spendRecoverySetEnvironment(env, "OPENAI_API_KEY", tc.key)
			env = spendRecoverySetEnvironment(env, "OPENAI_ADMIN_KEY", tc.admin)
			env = spendRecoverySetEnvironment(env, "OPENAI_CUSTOM_HEADERS", "X-Synthetic-Context: retained\n"+tc.headers)
			flags := slices.Clone(tc.flags)
			if tc.overrideContext {
				env = spendRecoverySetEnvironment(env, "OPENAI_BASE_URL", "http://127.0.0.1:1/unused/")
				flags = append(flags, "--base-url="+server.URL+"/tenant/v1/")
			}
			args := append([]string{"openai", "fine-tuning", "model-limits", "--format=json"}, flags...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if tc.missing {
				if got.code != 1 || got.stdout != "" || requests.Load() != 0 {
					t.Fatalf("missing credentials reached HTTP or changed exit: code=%d requests=%d", got.code, requests.Load())
				}
				payload := decodeMainStructuredError(t, "json", got.stderr)
				if !strings.Contains(fmt.Sprint(payload["message"]), "OPENAI_API_KEY") ||
					!strings.Contains(fmt.Sprint(payload["message"]), "admin key cannot replace") {
					t.Fatal("missing ordinary credentials lost actionable guidance")
				}
				if strings.Contains(got.stderr, "sk-fake-") || strings.Contains(got.stderr, "synthetic-first") {
					t.Fatal("credential failure exposed a synthetic credential")
				}
				return
			}
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 || !json.Valid([]byte(got.stdout)) {
				t.Fatalf("read failed or followed an undocumented cursor: code=%d requests=%d stderr=%q", got.code, requests.Load(), got.stderr)
			}
			if gjson.Get(got.stdout, "data.0.future.exact").Raw != "9007199254740993" ||
				gjson.Get(got.stdout, "next_page").String() != "synthetic-unfollowed-cursor" {
				t.Fatal("read lost unfamiliar response fields")
			}
		})
	}
}

func TestMainFineTuningModelLimitsOutputModes(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, fineTuningModelLimitsResponse)
	}))
	defer server.Close()
	env := fineTuningModelLimitsEnvironment(t, server.URL)
	for _, format := range []string{"json", "jsonl", "pretty", "raw", "yaml"} {
		t.Run(format, func(t *testing.T) {
			before := requests.Load()
			got := runMainDispatchWithEnv(t, "bash", env, "openai", "fine-tuning", "model-limits", "--format="+format)
			if got.code != 0 || got.stderr != "" || requests.Load() != before+1 {
				t.Fatalf("format failed: code=%d stderr=%q", got.code, got.stderr)
			}
			if format == "pretty" {
				for _, value := range []string{"fine-tuning model-limits", "synthetic-model", "9007199254740993", "0.100000000000000001", "synthetic-unfollowed-cursor"} {
					if !strings.Contains(got.stdout, value) {
						t.Fatalf("pretty output omitted %q", value)
					}
				}
				return
			}
			output := []byte(got.stdout)
			if format == "yaml" {
				if !strings.Contains(got.stdout, "decimal: 0.100000000000000001") {
					t.Fatal("YAML output rounded an unfamiliar decimal")
				}
				var err error
				output, err = yaml.YAMLToJSON(output)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !json.Valid(output) || gjson.GetBytes(output, "data.0.future.exact").Raw != "9007199254740993" ||
				gjson.GetBytes(output, "data.0.future.unset").Raw != "null" ||
				gjson.GetBytes(output, "data.0.future.text").String() != `quoted "value"` ||
				!gjson.GetBytes(output, "has_more").Bool() {
				t.Fatal("output dropped or coerced unfamiliar response fields")
			}
			// YAML uses the shared converter. JSON modes must retain original decimal tokens.
			if format != "yaml" && gjson.GetBytes(output, "data.0.future.decimal").Raw != "0.100000000000000001" {
				t.Fatal("JSON output rounded an unfamiliar decimal")
			}
			if format == "raw" && strings.TrimSpace(got.stdout) != fineTuningModelLimitsResponse {
				t.Fatal("raw output changed the response bytes")
			}
		})
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"extraction", []string{"--transform=data.0.future.exact"}, "9007199254740993"},
		{"raw string", []string{"--transform=data.0.future.text", "--raw-output"}, `quoted "value"`},
		{"quiet", []string{"--quiet", "--format=json", "--transform=data.0.future.decimal"}, "0.100000000000000001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"openai", "fine-tuning", "model-limits"}, tc.args...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || got.stderr != "" || strings.TrimSpace(got.stdout) != tc.want {
				t.Fatalf("extraction contract changed: %+v", got)
			}
		})
	}
}

func TestMainFineTuningModelLimitsLargeResponse(t *testing.T) {
	const size = 32<<20 + 1
	body := `{"future":{"payload":"` + strings.Repeat("x", size) + `","exact":9007199254740993},"data":[]}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer server.Close()
	got := runMainDispatchWithEnv(t, "bash", fineTuningModelLimitsEnvironment(t, server.URL),
		"openai", "fine-tuning", "model-limits", "--format=raw")
	if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
		t.Fatalf("large read failed: code=%d requests=%d stderr=%q", got.code, requests.Load(), got.stderr)
	}
	if strings.TrimSpace(got.stdout) != body {
		t.Fatal("large unfamiliar response was truncated or rewritten")
	}
}

func TestMainFineTuningModelLimitsResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		invalid    bool
	}{
		{"empty", "", true},
		{"malformed", "synthetic-private-response\x1b]52;c;private\a", true},
		{"array", `[{"unfamiliar":9007199254740993}]`, false},
		{"string", `"unfamiliar response"`, false},
		{"number", "1e10000", false},
		{"true", "true", false},
		{"false", "false", false},
		{"null", "null", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			got := runMainDispatchWithEnv(t, "bash", fineTuningModelLimitsEnvironment(t, server.URL),
				"openai", "fine-tuning", "model-limits", "--format=json")
			if requests.Load() != 1 {
				t.Fatalf("read issued %d requests", requests.Load())
			}
			if tc.invalid {
				if got.code != 1 || got.stdout != "" {
					t.Fatalf("invalid response reported success: code=%d", got.code)
				}
				payload := decodeMainStructuredError(t, "json", got.stderr)
				if payload["message"] != "Fine-tuning model limits returned an invalid JSON response. Check --base-url, then retry this read." {
					t.Fatal("invalid response lost safe recovery guidance")
				}
				if strings.Contains(got.stderr, "synthetic-private") || strings.ContainsAny(got.stderr, "\x1b\a") {
					t.Fatal("invalid response exposed private contents")
				}
				return
			}
			var compact bytes.Buffer
			if got.code != 0 || got.stderr != "" || json.Compact(&compact, []byte(got.stdout)) != nil || compact.String() != tc.body {
				t.Fatalf("valid JSON shape changed: code=%d stderr=%q stdout=%q", got.code, got.stderr, got.stdout)
			}
		})
	}
}

func TestMainFineTuningModelLimitsErrors(t *testing.T) {
	const details = `{"message":"synthetic-private-response\u001b]52;c;private\u0007","type":"invalid_request_error","code":"synthetic_code","param":null,"future":{"exact":9007199254740993}}`
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
		flags                   []string
	}{
		{"ordinary authentication", "application/json", `{"error":` + details + `}`, 401, []string{"--format-error=text"}},
		{"unavailable route", "application/json", `{"error":` + details + `}`, 404, []string{"--format-error=text"}},
		{"JSON", "application/json", `{"error":` + details + `}`, 400, []string{"--format-error=json"}},
		{"YAML", "application/json", `{"error":` + details + `}`, 400, []string{"--format-error=yaml"}},
		{"extraction", "application/json", `{"error":` + details + `}`, 400, []string{"--format-error=json", "--transform-error=future.exact"}},
		{"empty server error", "text/html", "synthetic-private-response", 502, []string{"--format-error=json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runMainCommandAPIErrorResponse(t, tc.status, tc.contentType, tc.body,
				[]string{"fine-tuning", "model-limits"}, tc.flags...)
			control := runMainAPIErrorResponse(t, tc.status, tc.contentType, tc.body, tc.flags...)
			if got != control {
				t.Fatalf("ordinary API error contract changed: got=%+v control=%+v", got, control)
			}
			if strings.HasSuffix(tc.name, "authentication") || tc.name == "unavailable route" {
				if strings.Contains(got.stderr, "synthetic-private") || strings.Contains(got.stderr, "\x1b") ||
					strings.Contains(got.stderr, "setup admin") {
					t.Fatal("human error exposed private data or requested admin authentication")
				}
			}
		})
	}
}

func TestMainFineTuningModelLimitsHelpAndValidation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, fineTuningModelLimitsResponse)
	}))
	defer server.Close()
	env := fineTuningModelLimitsEnvironment(t, server.URL)
	for _, argv := range [][]string{
		{"openai", "fine-tuning", "--help"},
		{"openai", "fine-tuning", "model-limits", "--help"},
		{"openai", "help", "fine-tuning", "model-limits"},
	} {
		t.Run(strings.Join(argv[1:], " "), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", env, argv...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "model-limits") || requests.Load() != 0 {
				t.Fatalf("help hid the command or made a request: %+v", got)
			}
			if len(argv) == 4 && argv[2] == "model-limits" || argv[1] == "help" {
				if !strings.Contains(got.stdout, "openai fine-tuning model-limits") {
					t.Fatal("command help omitted its invocation")
				}
			}
		})
	}
	for _, args := range [][]string{{"synthetic-private-operand"}, {"--unknown-synthetic=private"}, {"--max-items=1"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			argv := append([]string{"openai", "--format-error=json", "fine-tuning", "model-limits"}, args...)
			got := runMainDispatchWithEnv(t, "bash", env, argv...)
			if got.code != 1 || got.stdout != "" || requests.Load() != 0 {
				t.Fatalf("invalid arguments reached the endpoint: %+v", got)
			}
			decodeMainStructuredError(t, "json", got.stderr)
			if strings.Contains(got.stderr, "synthetic-private-operand") || strings.Contains(got.stderr, "=private") {
				t.Fatal("argument validation exposed a rejected value")
			}
		})
	}
}

func TestFineTuningModelLimitsErrorCoverage(t *testing.T) {
	env := errorCoverageEnvironment(t.TempDir(), "http://127.0.0.1:1")
	manifest := readErrorCoverageManifest(t, env)
	var found bool
	for _, command := range manifest.Commands {
		if strings.Join(command.Path, " ") == "fine-tuning model-limits" {
			if command.HasChildren || command.Compatibility || command.Class != "public" {
				t.Fatal("fine-tuning model limits is not an ordinary public leaf")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("error inventory omitted fine-tuning model limits")
	}
	for _, probe := range declaredErrorCoverageCases(manifest) {
		if probe.Name != "fine-tuning model-limits/unknown-option/" {
			continue
		}
		got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai", "--format-error=json"}, probe.Args...)...)
		payload := decodeMainStructuredError(t, "json", got.stderr)
		if got.code != probe.Code || got.stdout != "" || payload["message"] != probe.Message {
			t.Fatal("the inventoried parser probe did not preserve its public diagnostic")
		}
		return
	}
	t.Fatal("error inventory omitted the new leaf's unknown-option probe")
}

func TestMainFineTuningModelLimitsBaseURLSecurity(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer proxy.Close()
	env := adminCredentialsProxyEnv(proxy.URL, fineTuningModelLimitsEnvironment(t,
		"http://synthetic-user:synthetic-password@remote.example/v1?token=synthetic-private"))
	got := runMainDispatchWithEnv(t, "bash", env, "openai", "fine-tuning", "model-limits", "--format-error=json")
	if got.code != 1 || got.stdout != "" || requests.Load() != 0 {
		t.Fatal("insecure remote base URL reached HTTP")
	}
	payload := decodeMainStructuredError(t, "json", got.stderr)
	if !strings.Contains(fmt.Sprint(payload["message"]), "HTTPS") {
		t.Fatal("base URL rejection omitted recovery")
	}
	if bytes.ContainsAny([]byte(got.stderr), "\x1b\a") || strings.Contains(got.stderr, "synthetic-password") ||
		strings.Contains(got.stderr, "remote.example") {
		t.Fatal("base URL rejection exposed private URL contents")
	}
}
