package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tidwall/gjson"
)

const limitsWorkflowProject = `{"id":"proj_synthetic","object":"organization.project","name":"Synthetic project","status":"active","future":0.100000000000000001}`
const limitsWorkflowRate = `{"id":"rl_synthetic","object":"project.rate_limit","model":"synthetic-model","max_requests_per_1_minute":0,"max_tokens_per_1_minute":9007199254740993,"max_audio_megabytes_per_1_minute":1.25,"future":{"exact":123456789012345678901234567890}}`

func limitsWorkflowArgs(project bool, show bool) []string {
	args := []string{"admin", "limits"}
	if project {
		args = []string{"projects", "limits", "--project-id", "proj_synthetic"}
	}
	if show {
		args = append(args, "show")
	}
	return args
}

func limitsWorkflowRecords(project bool) (limit, alert string) {
	limit = spendThresholdBody(project, "spend-limit", `{"threshold_amount":9007199254740993,"currency":"USD","interval":"month","enforcement":{"status":"enforcing"},"future":{"decimal":0.100000000000000001}}`)
	alert = spendThresholdBody(project, "spend-alerts", `{"id":"alert_synthetic","threshold_amount":10000,"currency":"USD","interval":"month","notification_channel":{"type":"email","recipients":["synthetic@example.invalid"],"subject_prefix":null},"future":{"exact":123456789012345678901234567890}}`)
	return limit, alert
}

func limitsWorkflowPage(items string, more bool, last string) string {
	return fmt.Sprintf(`{"object":"list","data":[%s],"has_more":%t,"last_id":%q}`, items, more, last)
}

func limitsWorkflowEnvelope(project bool, limit, alerts string, configured string, alertsComplete bool, rates, errors string) string {
	fields := `"scope":"organization",`
	if project {
		fields = `"scope":"project","project_id":"proj_synthetic","project":` + limitsWorkflowProject + `,`
	}
	fields += `"spend_limit":` + limit + `,"spend_limit_configured":` + configured + `,"spend_alerts":[` + alerts + `],"spend_alerts_complete":` + fmt.Sprint(alertsComplete)
	if project {
		fields += `,"project_rate_limits":[` + rates + `],"project_rate_limits_complete":true`
	}
	return `{` + fields + `,"source_errors":[` + errors + `]}`
}

type limitsWorkflowRequests struct {
	mu    sync.Mutex
	paths []string
}

func (r *limitsWorkflowRequests) count(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, request := range r.paths {
		if strings.SplitN(request, "?", 2)[0] == path {
			count++
		}
	}
	return count
}

func newLimitsWorkflowServer(t *testing.T, respond func(*http.Request) (int, string)) (*httptest.Server, *limitsWorkflowRequests) {
	t.Helper()
	requests := &limitsWorkflowRequests{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.mu.Lock()
		requests.paths = append(requests.paths, r.URL.RequestURI())
		requests.mu.Unlock()
		if r.Method != http.MethodGet {
			t.Errorf("read-only overview sent %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer sk-fake-spend-admin" {
			t.Error("overview did not select the synthetic admin credential")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 {
			t.Errorf("read-only request has a body or read error: bytes=%d error=%v", len(body), err)
		}
		status, response := respond(r)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-should-retry", "false")
		w.WriteHeader(status)
		io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func limitsWorkflowSuccess(t *testing.T, project bool, r *http.Request) (int, string) {
	t.Helper()
	limit, alert := limitsWorkflowRecords(project)
	switch r.URL.Path {
	case "/organization/costs":
		return http.StatusOK, `{"object":"page","data":[],"has_more":false,"next_page":null}`
	case "/organization/projects/proj_synthetic":
		return http.StatusOK, limitsWorkflowProject
	case spendThresholdPath(project, "spend-limit"):
		return http.StatusOK, limit
	case spendThresholdPath(project, "spend-alerts"):
		return http.StatusOK, limitsWorkflowPage(alert, false, "alert_synthetic")
	case spendThresholdPath(true, "rate-limits"):
		return http.StatusOK, limitsWorkflowPage(limitsWorkflowRate, false, "rl_synthetic")
	default:
		t.Errorf("unexpected overview route: %s", r.URL.Path)
		return http.StatusNotFound, `{"error":{"message":"unexpected synthetic route"}}`
	}
}

func TestMainLimitsWorkflowBaseURLSecurity(t *testing.T) {
	const remote = "http://fake-user:fake-password@remote.example/v1?token=fake-private"
	for _, project := range []bool{false, true} {
		t.Run(fmt.Sprintf("project=%t", project), func(t *testing.T) {
			var proxyRequests atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				proxyRequests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"error":{"message":"synthetic proxy rejects all destinations"}}`)
			}))
			t.Cleanup(proxy.Close)
			home := t.TempDir()
			env := adminCredentialsProxyEnv(proxy.URL, []string{"HOME=" + home, "XDG_CONFIG_HOME=" + home,
				"OPENAI_API_KEY=sk-fake-spend-project", "OPENAI_ADMIN_KEY=sk-fake-spend-admin"})
			for _, source := range []string{"flag", "environment"} {
				for _, format := range []string{"text", "json"} {
					t.Run(source+"/"+format, func(t *testing.T) {
						args := []string{"openai", "--format-error", format}
						childEnv := append([]string{}, env...)
						wantSource := "OPENAI_BASE_URL"
						if source == "flag" {
							args = append(args, "--base-url", remote)
							wantSource = "--base-url"
						} else {
							childEnv = append(childEnv, "OPENAI_BASE_URL="+remote)
						}
						got := runMainDispatchWithEnv(t, "bash", childEnv, append(args, limitsWorkflowArgs(project, true)...)...)
						if got.code != 1 || got.stdout != "" || proxyRequests.Load() != 0 {
							t.Fatalf("remote HTTP reached the overview: code=%d requests=%d", got.code, proxyRequests.Load())
						}
						message := got.stderr
						if format == "json" {
							var value map[string]string
							if err := json.Unmarshal([]byte(got.stderr), &value); err != nil || len(value) != 1 {
								t.Fatal("remote HTTP rejection did not preserve a structured diagnostic")
							}
							message = value["message"]
						}
						if !strings.Contains(message, wantSource) || !strings.Contains(message, "HTTPS") {
							t.Fatal("remote HTTP rejection omitted its setting or recovery")
						}
						for _, private := range []string{"fake-", "remote.example", "\x1b"} {
							if strings.Contains(got.stderr, private) {
								t.Fatal("remote HTTP diagnostic exposed URL or credential contents")
							}
						}
					})
				}
			}
			t.Run("loopback flag overrides remote environment", func(t *testing.T) {
				server, requests := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
					return limitsWorkflowSuccess(t, project, r)
				})
				childEnv := append(append([]string{}, env...), "OPENAI_BASE_URL="+remote)
				args := append([]string{"openai", "--format", "json", "--base-url", server.URL}, limitsWorkflowArgs(project, true)...)
				got := runMainDispatchWithEnv(t, "bash", childEnv, args...)
				if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) || proxyRequests.Load() != 0 {
					t.Fatalf("loopback override failed: code=%d proxy_requests=%d", got.code, proxyRequests.Load())
				}
				paths := []string{spendThresholdPath(project, "spend-limit"), spendThresholdPath(project, "spend-alerts"), "/organization/costs"}
				if project {
					paths = append(paths, "/organization/projects/proj_synthetic", spendThresholdPath(true, "rate-limits"))
				}
				for _, path := range paths {
					if requests.count(path) != 1 {
						t.Errorf("loopback overview requested %s %d times", path, requests.count(path))
					}
				}
			})
		})
	}
}

func TestMainLimitsWorkflowReadableAndFormats(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, mode := range []string{"bare", "show", "text", "JSON", "jsonl", "raw", "yaml", "pretty", "extraction", "raw-output"} {
			t.Run(fmt.Sprintf("project=%t/%s", project, mode), func(t *testing.T) {
				server, requests := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
					return limitsWorkflowSuccess(t, project, r)
				})
				args := limitsWorkflowArgs(project, mode != "bare")
				switch mode {
				case "bare", "show":
				case "extraction":
					args = append([]string{"--transform", "spend_limit.threshold_amount"}, args...)
				case "raw-output":
					args = append([]string{"--raw-output"}, args...)
				default:
					args = append([]string{"--format", mode}, args...)
				}
				got := runSpendThresholdCommand(t, server, args...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("overview failed: %+v", got)
				}
				for _, resource := range []string{"spend-limit", "spend-alerts"} {
					if n := requests.count(spendThresholdPath(project, resource)); n != 1 {
						t.Errorf("%s requests=%d, want one", resource, n)
					}
				}
				if project && (requests.count("/organization/projects/proj_synthetic") != 1 || requests.count(spendThresholdPath(true, "rate-limits")) != 1) {
					t.Error("project overview must validate the project and fetch its rates")
				}
				limit, alert := limitsWorkflowRecords(project)
				want := limitsWorkflowEnvelope(project, limit, alert, "true", true, limitsWorkflowRate, "")
				switch mode {
				case "bare", "show", "text":
					for _, text := range []string{"USD 90071992547409.93 per month", "USD 100.00 per month", "Alerts notify; they are not spending caps.", "0.100000000000000001", "123456789012345678901234567890"} {
						if !strings.Contains(got.stdout, text) {
							t.Errorf("readable overview lost %q: %q", text, got.stdout)
						}
					}
					if project {
						for _, text := range []string{"Max requests per minute: 0", "Max tokens per minute: 9007199254740993", "Max audio megabytes per minute: 1.25"} {
							if !strings.Contains(got.stdout, text) {
								t.Errorf("rate units lost %q: %q", text, got.stdout)
							}
						}
					}
				case "yaml", "pretty":
					for _, text := range []string{"threshold_amount: 9007199254740993", "decimal: 0.100000000000000001", "exact: 123456789012345678901234567890"} {
						if !strings.Contains(got.stdout, text) {
							t.Errorf("%s lost %q: %q", mode, text, got.stdout)
						}
					}
				case "extraction":
					if got.stdout != "9007199254740993\n" {
						t.Errorf("overview extraction changed: %q", got.stdout)
					}
				default:
					costs := gjson.Get(got.stdout, "costs")
					if costs.Get("total").Str != "0.00" || costs.Get("currency").Str != "USD" || !costs.Get("complete").Bool() {
						t.Fatalf("Costs summary missing: %s", costs.Raw)
					}
					want = strings.TrimSuffix(want, "}") + `,"costs":` + costs.Raw + "}"
					assertSpendThresholdJSON(t, got.stdout, want)
				}
				if strings.Contains(got.stdout, "\x1b") {
					t.Error("nonterminal overview emitted terminal control sequences")
				}
			})
		}
	}
}

func TestMainLimitsWorkflowMissingLimitAndPartialFailures(t *testing.T) {
	for _, tc := range []struct {
		name, failedResource string
		project              bool
		status               int
	}{
		{"organization no limit", "spend-limit", false, http.StatusNotFound},
		{"validated project no limit", "spend-limit", true, http.StatusNotFound},
		{"limit forbidden", "spend-limit", true, http.StatusForbidden},
		{"missing project", "project", true, http.StatusNotFound},
		{"alerts forbidden", "spend-alerts", false, http.StatusForbidden},
		{"rates unavailable", "rate-limits", true, http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
				failedPath := spendThresholdPath(tc.project, tc.failedResource)
				if tc.failedResource == "project" {
					failedPath = "/organization/projects/proj_synthetic"
				}
				if r.URL.Path == failedPath {
					return tc.status, `{"error":{"type":"synthetic_error","message":"synthetic private limits response"}}`
				}
				return limitsWorkflowSuccess(t, tc.project, r)
			})
			args := append([]string{"--format", "json"}, limitsWorkflowArgs(tc.project, true)...)
			got := runSpendThresholdCommand(t, server, args...)
			if !json.Valid([]byte(got.stdout)) {
				t.Fatalf("partial overview is not complete JSON: %+v", got)
			}
			value := gjson.Parse(got.stdout)
			{
				if tc.failedResource == "spend-limit" && value.Get("spend_limit_configured").Type != gjson.Null {
					t.Fatalf("failed lookup incorrectly established configuration: %s", got.stdout)
				}
				if got.code != 1 || strings.TrimSpace(got.stderr) == "" || value.Get("source_errors.#").Int() == 0 {
					t.Fatalf("partial failure disappeared: %+v", got)
				}
				if !json.Valid([]byte(got.stderr)) {
					t.Errorf("structured error stream is invalid: %q", got.stderr)
				}
				resource := strings.ReplaceAll(tc.failedResource, "-", "_")
				if resource == "rate_limits" {
					resource = "project_rate_limits"
				}
				matched := false
				for _, failure := range value.Get("source_errors").Array() {
					if failure.Get("resource").Str == resource {
						matched = failure.Get("status").Str == "api_error" && failure.Get("status_code").Int() == int64(tc.status)
					}
					failure.ForEach(func(key, _ gjson.Result) bool {
						if key.Str != "resource" && key.Str != "status" && key.Str != "status_code" {
							t.Errorf("source failure includes undocumented field %q", key.Str)
						}
						return true
					})
				}
				if !matched {
					t.Errorf("missing safe source/status record: %s", got.stdout)
				}
				if tc.failedResource == "project" {
					if value.Get("spend_limit_configured").Raw != "null" || requests.count(spendThresholdPath(true, "spend-limit")) != 0 {
						t.Errorf("unvalidated project inferred an absent limit: %s", got.stdout)
					}
				} else if tc.failedResource == "spend-limit" {
					if value.Get("spend_limit_configured").Raw != "null" || value.Get("spend_limit").Raw != "null" || value.Get("spend_alerts.#").Int() != 1 {
						t.Errorf("failed limit became unconfigured or lost independent alerts: %s", got.stdout)
					}
				} else if value.Get("spend_limit.threshold_amount").Raw != "9007199254740993" {
					t.Errorf("partial failure lost successful spend data: %s", got.stdout)
				}
			}
			for _, secret := range []string{"synthetic private limits response", "sk-fake-spend-admin", "sk-fake-spend-test"} {
				if strings.Contains(got.stdout+got.stderr, secret) {
					t.Errorf("overview diagnostics included private fixture data %q", secret)
				}
			}
		})
	}
}

func TestMainLimitsWorkflowAmbiguousAndOptionalRecords(t *testing.T) {
	const limit = `{"object":"project.spend_limit","threshold_amount":10000,"threshold_amount":90000,"currency":"USD","interval":"month"}`
	const alert = `{"object":"project.spend_alert","id":"alert_synthetic","threshold_amount":10000,"currency":"USD","currency":"EUR","interval":"month"}`
	const rate = `{"object":"project.rate_limit","id":"rl_synthetic","model":"synthetic-model","max_requests_per_1_minute":0,"max_tokens_per_1_minute":7,"future":"first","future":"second"}`
	server, _ := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case spendThresholdPath(true, "spend-limit"):
			return http.StatusOK, limit
		case spendThresholdPath(true, "spend-alerts"):
			return http.StatusOK, limitsWorkflowPage(alert, false, "alert_synthetic")
		case spendThresholdPath(true, "rate-limits"):
			return http.StatusOK, limitsWorkflowPage(rate, false, "rl_synthetic")
		default:
			return limitsWorkflowSuccess(t, true, r)
		}
	})
	args := limitsWorkflowArgs(true, true)
	got := runSpendThresholdCommand(t, server, args...)
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("ambiguous records lost read-only access: %+v", got)
	}
	// Ambiguous rates keep source keys; numeric segments require quoted labels.
	for _, text := range []string{
		"Threshold amount: 10000", "Threshold amount: 90000", "Currency: USD", "Currency: EUR",
		`"max_requests_per_1_minute": 0`, `"max_tokens_per_1_minute": 7`, "Future: first", "Future: second",
	} {
		if !strings.Contains(got.stdout, text) {
			t.Errorf("readable overview lost %q: %q", text, got.stdout)
		}
	}
	for _, absent := range []string{"Spend threshold:", "Alerts notify;", "not reported in this response", "Max requests per minute:", "Max tokens per minute:", "Max images", "Max audio", "Max requests per day", "Max batch"} {
		if strings.Contains(got.stdout, absent) {
			t.Errorf("ambiguous overview invented %q: %q", absent, got.stdout)
		}
	}
	got = runSpendThresholdCommand(t, server, append([]string{"--format", "raw"}, args...)...)
	if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) {
		t.Fatalf("raw ambiguous overview failed: %+v", got)
	}
	for _, record := range []string{limit, alert, rate} {
		if !strings.Contains(got.stdout, record) {
			t.Errorf("raw overview discarded duplicate fields: %q", got.stdout)
		}
	}
}

func TestMainLimitsWorkflowNoninteractiveGuards(t *testing.T) {
	for _, tc := range []struct {
		name       string
		flags, env []string
	}{
		{"quiet", []string{"--quiet"}, nil},
		{"CI", nil, []string{"CI=1"}},
		{"dumb", nil, []string{"TERM=dumb"}},
		{"explicit auto", []string{"--format", "auto"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
				return limitsWorkflowSuccess(t, false, r)
			})
			env := append([]string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-spend-test", "OPENAI_ADMIN_KEY=sk-fake-spend-admin", "FORCE_COLOR=0"}, tc.env...)
			args := append([]string{"openai"}, tc.flags...)
			args = append(args, limitsWorkflowArgs(false, false)...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "USD 90071992547409.93 per month") || strings.Contains(got.stdout, "\x1b") {
				t.Fatalf("noninteractive invocation entered a workspace or lost data: %+v", got)
			}
		})
	}
}

func TestMainLimitsWorkflowEnvironmentHeadersAndDebug(t *testing.T) {
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%t", debug), func(t *testing.T) {
			server, _ := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
				if r.Header.Get("X-Limits-Env") != "synthetic-private-header" {
					t.Error("overview lost configured environment headers")
				}
				return limitsWorkflowSuccess(t, false, r)
			})
			env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=sk-fake-spend-test", "OPENAI_ADMIN_KEY=sk-fake-spend-admin", "OPENAI_CUSTOM_HEADERS=X-Limits-Env: synthetic-private-header", "FORCE_COLOR=0"}
			args := []string{"openai", "--format", "json"}
			if debug {
				args = append(args, "--debug")
			}
			args = append(args, limitsWorkflowArgs(false, true)...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 0 || !json.Valid([]byte(got.stdout)) || (got.stderr != "") != debug {
				t.Fatalf("debug or environment-header handling changed: %+v", got)
			}
			for _, secret := range []string{"synthetic-private-header", "sk-fake-spend-admin", "sk-fake-spend-test"} {
				if strings.Contains(got.stdout+got.stderr, secret) {
					t.Errorf("debug output exposed fixture credential/header %q", secret)
				}
			}
		})
	}
}

func TestMainLimitsWorkflowPaginationAndBounds(t *testing.T) {
	for _, resource := range []string{"spend-alerts", "rate-limits"} {
		for _, tc := range []struct {
			name, max       string
			count, calls    int
			complete, cycle bool
		}{
			{name: "unlimited", max: "-1", count: 3, calls: 2, complete: true},
			{name: "zero", max: "0", calls: 1},
			{name: "one", max: "1", count: 1, calls: 1},
			{name: "page boundary", max: "2", count: 2, calls: 1},
			{name: "cursor cycle", max: "-1", count: -1, calls: 2, cycle: true},
		} {
			t.Run(resource+"/"+tc.name, func(t *testing.T) {
				project := resource == "rate-limits"
				path := spendThresholdPath(project, resource)
				_, alert := limitsWorkflowRecords(project)
				item := alert
				field := "spend_alerts"
				if project {
					item, field = limitsWorkflowRate, "project_rate_limits"
				}
				originalID := gjson.Get(item, "id").Str
				first := strings.Replace(item, originalID, "item_a", 1)
				second := strings.Replace(item, originalID, "item_b", 1)
				third := strings.Replace(item, originalID, "item_c", 1)
				server, requests := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
					if r.URL.Path != path {
						return limitsWorkflowSuccess(t, project, r)
					}
					if tc.max == "0" && r.URL.Query().Get("limit") != "1" {
						t.Errorf("zero bound requested limit=%q, want 1", r.URL.Query().Get("limit"))
					}
					switch r.URL.Query().Get("after") {
					case "":
						return http.StatusOK, limitsWorkflowPage(first+","+second, true, "item_b")
					case "item_b":
						if tc.cycle {
							return http.StatusOK, limitsWorkflowPage(first+","+second, true, "item_b")
						}
						return http.StatusOK, limitsWorkflowPage(third, false, "item_c")
					default:
						t.Errorf("unexpected pagination cursor: %s", r.URL.Query().Get("after"))
						return http.StatusBadRequest, `{"error":{"message":"unexpected cursor"}}`
					}
				})
				args := append([]string{"--format", "json"}, limitsWorkflowArgs(project, false)...)
				args = append(args, "--max-items", tc.max, "show")
				got := runSpendThresholdCommand(t, server, args...)
				if !json.Valid([]byte(got.stdout)) || requests.count(path) != tc.calls {
					t.Fatalf("pagination failed: %+v; calls=%d", got, requests.count(path))
				}
				value := gjson.Parse(got.stdout)
				if tc.cycle {
					if got.code != 1 || value.Get("source_errors.#").Int() == 0 || value.Get(field+".#").Int() == 0 {
						t.Errorf("cursor cycle lost failure or partial data: %+v", got)
					}
				} else if got.code != 0 || got.stderr != "" || value.Get(field+".#").Int() != int64(tc.count) {
					t.Errorf("bounded list changed: %+v", got)
				}
				if value.Get(field+"_complete").Bool() != tc.complete {
					t.Errorf("list completeness changed: %s", got.stdout)
				}
			})
		}
	}
}

func TestMainLimitsWorkflowRootFlagsAndHelp(t *testing.T) {
	for _, trailing := range []bool{false, true} {
		t.Run(fmt.Sprintf("trailing=%t", trailing), func(t *testing.T) {
			server, _ := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
				if r.Header.Get("OpenAI-Organization") != "org_header" || r.Header.Get("OpenAI-Project") != "proj_header" || r.Header.Get("X-Limits-Test") != "synthetic" {
					t.Error("root context or custom headers changed")
				}
				return limitsWorkflowSuccess(t, true, r)
			})
			flags := []string{"--format", "json", "--organization", "org_header", "--project", "proj_header", "-H", "X-Limits-Test: synthetic"}
			args := limitsWorkflowArgs(true, true)
			if trailing {
				args = append(args, flags...)
			} else {
				args = append(flags, args...)
			}
			got := runSpendThresholdCommand(t, server, args...)
			if got.code != 0 || got.stderr != "" || gjson.Get(got.stdout, "project_id").Str != "proj_synthetic" {
				t.Fatalf("root flags changed project route or output: %+v", got)
			}
		})
	}
	for _, args := range [][]string{
		{"admin", "limits", "--help"},
		{"admin", "limits", "show", "--help"},
		{"projects", "limits", "--help"},
		{"projects", "limits", "show", "--help"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatch(t, "bash", append([]string{"openai"}, args...)...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "limits") || !strings.Contains(got.stdout, "--max-items") {
				t.Fatalf("limits help unavailable: %+v", got)
			}
		})
	}
}

func TestMainLimitsWorkflowRequiresAdminCredentials(t *testing.T) {
	for _, project := range []bool{false, true} {
		t.Run(fmt.Sprintf("project=%t", project), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusForbidden)
			}))
			t.Cleanup(server.Close)
			args := append([]string{"openai", "--base-url", "https://api.openai.com/v1"}, limitsWorkflowArgs(project, true)...)
			got := runMainDispatchWithEnv(t, "bash", adminCredentialsProxyEnv(server.URL, []string{"OPENAI_API_KEY=synthetic-project-key"}), args...)
			if got.code != 1 || got.stdout != "" || strings.TrimSpace(got.stderr) != missingAdminCredentialsMessage || requests.Load() != 0 {
				t.Fatalf("workspace bypassed admin credential validation: result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}

func TestMainLimitsWorkflowCostsPrecisionAndPartialFailure(t *testing.T) {
	for _, failNext := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", failNext), func(t *testing.T) {
			server, requests := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
				if r.URL.Path != "/organization/costs" {
					return limitsWorkflowSuccess(t, true, r)
				}
				query := r.URL.Query()
				if query.Get("project_ids[]") != "proj_synthetic" {
					t.Errorf("Costs lost the selected project: %s", r.URL.RawQuery)
				}
				if query.Get("page") == "next_costs" {
					if failNext {
						return http.StatusServiceUnavailable, `{"error":{"message":"private synthetic costs failure"}}`
					}
					return http.StatusOK, `{"object":"page","data":[],"has_more":false,"next_page":null}`
				}
				start, err := strconv.ParseInt(query.Get("start_time"), 10, 64)
				end, endErr := strconv.ParseInt(query.Get("end_time"), 10, 64)
				if err != nil || endErr != nil || start%86400 != 0 || end%86400 != 0 || end <= start {
					t.Fatalf("Costs period is not explicit UTC days: %s", r.URL.RawQuery)
				}
				return http.StatusOK, fmt.Sprintf(`{"object":"page","data":[{"object":"bucket","start_time":%d,"end_time":%d,"results":[{"object":"organization.costs.result","amount":{"value":0.100000000000000001,"currency":"usd"},"project_id":"proj_synthetic","future":9007199254740993},{"object":"organization.costs.result","amount":{"value":0.2,"currency":"usd"},"project_id":"proj_synthetic"}]}],"has_more":true,"next_page":"next_costs"}`, start, start+86400)
			})
			got := runSpendThresholdCommand(t, server, "projects", "limits", "show", "--project-id", "proj_synthetic", "--format", "json")
			value := gjson.Parse(got.stdout)
			if !json.Valid([]byte(got.stdout)) || requests.count("/organization/costs") != 2 {
				t.Fatalf("Costs pagination failed: %+v", got)
			}
			if value.Get("costs.buckets.0.results.0.amount.value").Raw != "0.100000000000000001" || value.Get("costs.buckets.0.results.0.future").Raw != "9007199254740993" {
				t.Fatalf("Costs raw data lost precision: %s", got.stdout)
			}
			if failNext {
				if got.code != 1 || value.Get("costs.complete").Bool() || value.Get("costs.total").Str != "0.300000000000000001" || value.Get("source_errors.#").Int() != 1 {
					t.Fatalf("Partial Costs became a complete total: %+v", got)
				}
				if strings.Contains(got.stderr, "private synthetic") || !json.Valid([]byte(got.stderr)) {
					t.Fatalf("Costs recovery exposed source data or broke JSON: %s", got.stderr)
				}
			} else if got.code != 0 || got.stderr != "" || !value.Get("costs.complete").Bool() || value.Get("costs.total").Str != "0.300000000000000001" {
				t.Fatalf("Costs total is not exact: %+v", got)
			}
			if !value.Get("spend_limit").IsObject() || value.Get("spend_alerts.#").Int() != 1 {
				t.Fatalf("Costs handling lost independent sources: %s", got.stdout)
			}
		})
	}
}
