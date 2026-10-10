package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

const projectRateLimitResponse = `{"id":"rl_limits","object":"project.rate_limit","model":"model_limits","max_requests_per_1_minute":11,"max_tokens_per_1_minute":1200,"max_images_per_1_minute":13,"max_audio_megabytes_per_1_minute":14,"max_requests_per_1_day":1500,"batch_1_day_max_input_tokens":16000,"future":{"note":"preserve me"}}`

var projectRateLimitRoutes = []string{
	"admin:organization:projects:rate-limits",
	"admin organization projects rate-limits",
	"admin projects rate-limits",
	"projects rate-limits",
}

func projectRateLimitPage(item string, more bool) string {
	if item == "" {
		return `{"object":"list","data":[],"has_more":false}`
	}
	if more {
		return `{"object":"list","data":[` + item + `],"has_more":true,"last_id":"rl_limits"}`
	}
	return `{"object":"list","data":[` + item + `],"has_more":false}`
}

func TestMainProjectRateLimitRoutesPreserveRequests(t *testing.T) {
	for _, route := range projectRateLimitRoutes {
		for _, verb := range []string{"list", "list-rate-limits", "update", "update-rate-limit"} {
			t.Run(route+"/"+verb, func(t *testing.T) {
				update := strings.HasPrefix(verb, "update")
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					wantPath, wantMethod := "/v1/organization/projects/proj_limits/rate_limits", http.MethodGet
					if update {
						wantPath, wantMethod = wantPath+"/rl_limits", http.MethodPost
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, map[string]any{"max_tokens_per_1_minute": float64(0), "batch_1_day_max_input_tokens": float64(17)}) {
							t.Errorf("update body=%#v error=%v", body, err)
						}
					} else if r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("after") != "rl_before" {
						t.Errorf("list query changed: %s", r.URL.RawQuery)
					}
					if r.Method != wantMethod || r.URL.Path != wantPath {
						t.Errorf("request=%s %s; want %s %s", r.Method, r.URL.Path, wantMethod, wantPath)
					}
					for name, want := range map[string]string{"Authorization": "Bearer synthetic-explicit-key", "OpenAI-Project": "proj_context", "OpenAI-Organization": "org_context", "X-Rate-Limit-Test": "explicit", "X-Environment-Test": "preserved"} {
						if r.Header.Get(name) != want {
							t.Errorf("request lost expected %s setting", name)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					if update {
						io.WriteString(w, projectRateLimitResponse)
					} else {
						io.WriteString(w, projectRateLimitPage(projectRateLimitResponse, false))
					}
				}))
				defer server.Close()
				args := []string{"openai", "--base-url", server.URL + "/v1", "--admin-api-key", "synthetic-explicit-key", "--api-key", "synthetic-user-key", "--project", "proj_context", "--organization", "org_context", "--header", "X-Rate-Limit-Test: explicit"}
				args = append(args, strings.Fields(route)...)
				args = append(args, verb, "--project-id", "proj_limits")
				if update {
					args = append(args, "--rate-limit-id", "rl_limits", "--max-tokens-per-1-minute", "0", "--batch-1-day-max-input-tokens", "17")
				} else {
					args = append(args, "--limit", "2", "--after", "rl_before")
				}
				got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-environment-key", "OPENAI_ADMIN_KEY=synthetic-admin-key", "OPENAI_PROJECT_ID=proj_environment", "OPENAI_ORG_ID=org_environment", "OPENAI_CUSTOM_HEADERS=X-Environment-Test: preserved", "FORCE_COLOR=0"}, args...)
				if got.code != 0 || requests.Load() != 1 || got.stderr != "" {
					t.Fatalf("result=%+v requests=%d", got, requests.Load())
				}
				for _, want := range []string{"ID: rl_limits", "Model: model_limits", "Max requests per minute: 11", "Max tokens per minute: 1200", "Max images per minute: 13", "Max audio megabytes per minute: 14", "Max requests per day: 1500", "Max batch input tokens per day: 16000"} {
					if !strings.Contains(got.stdout, want+"\n") {
						t.Errorf("missing %q in %q", want, got.stdout)
					}
				}
				if strings.Contains(got.stdout, "Max tokens per day:") || strings.Contains(got.stdout, resourceSummaryHint) {
					t.Fatalf("misleading units or diagnostics on stdout: %q", got.stdout)
				}
			})
		}
	}
}

func TestMainProjectRateLimitExplicitFormats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			io.WriteString(w, projectRateLimitPage(projectRateLimitResponse, false))
		} else {
			io.WriteString(w, projectRateLimitResponse)
		}
	}))
	defer server.Close()
	for _, verb := range []string{"list", "update"} {
		for _, format := range []string{"json", "jsonl", "yaml", "raw"} {
			t.Run(verb+"/"+format, func(t *testing.T) {
				args := []string{"--format", format, "projects", "rate-limits", verb, "--project-id", "proj_limits"}
				if verb == "update" {
					args = append(args, "--rate-limit-id", "rl_limits", "--max-tokens-per-1-minute", "1")
				}
				got := runReadableCommand(t, server, args...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("explicit format result: %+v", got)
				}
				if format == "yaml" {
					for _, want := range []string{"max_tokens_per_1_minute: 1200", "batch_1_day_max_input_tokens: 16000", "note: preserve me"} {
						if !strings.Contains(got.stdout, want) {
							t.Errorf("YAML lost %q: %q", want, got.stdout)
						}
					}
					return
				}
				want := projectRateLimitResponse
				if verb == "list" && format == "raw" {
					want = projectRateLimitPage(projectRateLimitResponse, false)
				}
				var actual, expected any
				if err := json.Unmarshal([]byte(got.stdout), &actual); err != nil {
					t.Fatalf("invalid JSON: %v; %q", err, got.stdout)
				}
				if err := json.Unmarshal([]byte(want), &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("API fields changed: got=%#v want=%#v", actual, expected)
				}
				if format == "raw" && got.stdout != want+"\n" || format == "jsonl" && strings.Count(got.stdout, "\n") != 1 {
					t.Fatalf("format framing changed: %q", got.stdout)
				}
			})
		}
	}
	got := runReadableCommand(t, server, "projects", "rate-limits", "list", "proj_limits", "--transform", "future.note", "--raw-output")
	if got.code != 0 || got.stderr != "" || got.stdout != "preserve me\n" {
		t.Fatalf("explicit extraction changed: %+v", got)
	}
}

func TestMainProjectRateLimitBodyInputs(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		pipe        bool
		flags       []string
		want        map[string]any
	}{
		{"JSON pipe preserves null", `{"max_tokens_per_1_minute":null,"future":false}`, true, nil, map[string]any{"max_tokens_per_1_minute": nil, "future": false}},
		{"YAML pipe preserves zero", "max_tokens_per_1_minute: 0\nbatch_1_day_max_input_tokens: null\n", true, nil, map[string]any{"max_tokens_per_1_minute": float64(0), "batch_1_day_max_input_tokens": nil}},
		{"JSON file flag overrides value", `{"max_tokens_per_1_minute":99,"future":null}`, false, []string{"--max-tokens-per-1-minute", "0"}, map[string]any{"max_tokens_per_1_minute": float64(0), "future": nil}},
		{"YAML file preserves unknown", "max_requests_per_1_day: 21\nfuture: retained\n", false, nil, map[string]any{"max_requests_per_1_day": float64(21), "future": "retained"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, projectRateLimitResponse)
			}))
			defer server.Close()
			input := shellFileInput(t, []byte(tc.input))
			if tc.pipe {
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { reader.Close() })
				if _, err := io.WriteString(writer, tc.input); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				input = reader
			}
			args := append([]string{"projects", "rate-limits", "update", "--project-id", "proj_limits", "--rate-limit-id", "rl_limits"}, tc.flags...)
			got := runShellFileCommand(t, server, input, nil, args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("input result: %+v", got)
			}
			if body := <-requests; !reflect.DeepEqual(body, tc.want) {
				t.Fatalf("body=%#v want=%#v", body, tc.want)
			}
		})
	}
}

func TestMainProjectRateLimitPaginationAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, maxItems string
		failPage       int
		wantItems      int
		wantRequests   int
	}{
		{"unlimited", "-1", 0, 2, 2},
		{"zero", "0", 0, 0, 1},
		{"first page error", "-1", 1, 0, 1},
		{"later page error", "-1", 2, 1, 2},
		{"zero preserves first page error", "0", 1, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page := int(requests.Add(1))
				w.Header().Set("Content-Type", "application/json")
				if page == tc.failPage {
					w.WriteHeader(http.StatusBadRequest)
					io.WriteString(w, `{"error":{"message":"synthetic rate-limit failure","type":"invalid_request_error","code":"invalid_request"}}`)
					return
				}
				if page > 2 || page == 2 && r.URL.Query().Get("after") != "rl_limits" {
					t.Errorf("unexpected pagination: page=%d query=%s", page, r.URL.RawQuery)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				item := projectRateLimitResponse
				if page == 2 {
					item = strings.ReplaceAll(item, "rl_limits", "rl_second")
				}
				io.WriteString(w, projectRateLimitPage(item, page == 1))
			}))
			defer server.Close()
			got := runReadableCommand(t, server, "--format", "jsonl", "--format-error", "json", "projects", "rate-limits", "list", "proj_limits", "--max-items", tc.maxItems)
			wantCode := 0
			if tc.failPage > 0 {
				wantCode = 1
				if !json.Valid([]byte(got.stderr)) || !strings.Contains(got.stderr, "synthetic rate-limit failure") {
					t.Errorf("structured error lost: %q", got.stderr)
				}
			} else if got.stderr != "" {
				t.Errorf("unexpected diagnostic: %q", got.stderr)
			}
			if got.code != wantCode || int(requests.Load()) != tc.wantRequests || strings.Count(got.stdout, "\n") != tc.wantItems || strings.Contains(got.stdout+got.stderr, "No rate limits") {
				t.Fatalf("result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}

func TestMainProjectRateLimitEmptyResults(t *testing.T) {
	for _, tc := range []struct {
		name, response, want string
		status               int
		flags                []string
	}{
		{"readable", projectRateLimitPage("", false), "No rate limits returned for proj_limits.\n", http.StatusOK, nil},
		{"JSON stays empty", projectRateLimitPage("", false), "", http.StatusOK, []string{"--format", "json"}},
		{"raw retains envelope", projectRateLimitPage("", false), projectRateLimitPage("", false) + "\n", http.StatusOK, []string{"--format", "raw"}},
		{"extraction stays empty", projectRateLimitPage("", false), "", http.StatusOK, []string{"--transform", "model"}},
		{"zero suppresses message", projectRateLimitPage("", false), "", http.StatusOK, []string{"--max-items", "0"}},
		{"API failure precedes empty", `{"error":{"message":"synthetic denial","type":"invalid_request_error"}}`, "", http.StatusForbidden, nil},
		{"empty API error remains failure", `{}`, "", http.StatusBadRequest, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.response)
			}))
			defer server.Close()
			args := append([]string{"projects", "rate-limits", "list", "proj_limits"}, tc.flags...)
			got := runReadableCommand(t, server, args...)
			wantCode := 0
			if tc.status != http.StatusOK {
				wantCode = 1
				if got.stderr == "" || strings.Contains(got.stderr, "No rate limits") {
					t.Errorf("API failure lost: %q", got.stderr)
				}
			} else if got.stderr != "" {
				t.Errorf("unexpected diagnostic: %q", got.stderr)
			}
			if got.code != wantCode || got.stdout != tc.want || requests.Load() != 1 {
				t.Fatalf("result=%+v requests=%d; want stdout=%q", got, requests.Load(), tc.want)
			}
		})
	}
}

func TestMainProjectRateLimitRejectsUnsupportedAndMalformedInput(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, input string
		flags       []string
	}{
		{"unsupported daily tokens", "", []string{"--max-tokens-per-1-day", "1000"}},
		{"invalid integer", "", []string{"--max-tokens-per-1-minute", "not-an-integer"}},
		{"malformed JSON", `{"max_tokens_per_1_minute":`, nil},
		{"malformed YAML", "max_tokens_per_1_minute: [\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"projects", "rate-limits", "update", "--project-id", "proj_limits", "--rate-limit-id", "rl_limits"}, tc.flags...)
			got := runShellFileCommand(t, server, shellFileInput(t, []byte(tc.input)), nil, args...)
			if got.code == 0 || got.stdout != "" || got.stderr == "" || requests.Load() != 0 {
				t.Fatalf("invalid input reached API or lost failure: %+v requests=%d", got, requests.Load())
			}
		})
	}
}

func TestMainProjectRateLimitHelp(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/organization/projects/proj_demo/rate_limits") {
			t.Errorf("help example sent unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			io.WriteString(w, projectRateLimitPage(projectRateLimitResponse, false))
		} else {
			io.WriteString(w, projectRateLimitResponse)
		}
	}))
	defer server.Close()
	for _, route := range projectRateLimitRoutes {
		for _, verb := range []string{"list", "update"} {
			t.Run(route+"/"+verb, func(t *testing.T) {
				args := append([]string{"openai"}, strings.Fields(route)...)
				got := runMainDispatch(t, "bash", append(args, verb, "--help")...)
				if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "--project-id") || !strings.Contains(got.stdout, "openai admin projects rate-limits "+verb) {
					t.Fatalf("help unavailable: %+v", got)
				}
				if verb == "update" {
					prose := strings.Join(strings.Fields(got.stdout), " ")
					for _, want := range []string{"--max-tokens-per-1-day is not supported", "batch input tokens per day", "no reset option", "zero and null are not reset shortcuts"} {
						if !strings.Contains(prose, want) {
							t.Errorf("help lost %q: %q", want, got.stdout)
						}
					}
				}
				if route == projectRateLimitRoutes[0] {
					found := false
					for _, line := range strings.Split(got.stdout, "\n") {
						line = strings.TrimSpace(line)
						if !strings.HasPrefix(line, "openai admin projects rate-limits "+verb+" ") {
							continue
						}
						found = true
						result := runReadableCommand(t, server, strings.Fields(line)[1:]...)
						if result.code != 0 || result.stderr != "" || !strings.Contains(result.stdout, "Model: model_limits") {
							t.Errorf("printed help example failed: %+v", result)
						}
					}
					if !found {
						t.Fatal("help did not contain an executable example")
					}
				}
			})
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("printed examples made %d requests; want 2", requests.Load())
	}
}

func TestMainProjectRateLimitCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal cannot send os.Interrupt on Windows")
	}
	for _, verb := range []string{"list", "list-rate-limits"} {
		t.Run(verb, func(t *testing.T) {
			started, disconnected := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-r.Context().Done()
				close(disconnected)
			}))
			t.Cleanup(server.Close)
			child, _, stderr, ctx := startStreamingTextCommand(t, server, "--api-key", "synthetic-cancellation-key", "projects", "rate-limits", verb, "proj_limits")
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("list did not reach synthetic API")
			}
			if err := child.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			if err := child.Wait(); err == nil || child.ProcessState.ExitCode() == 0 || ctx.Err() != nil {
				t.Fatalf("list did not report interruption: error=%v stderr=%q", err, stderr.String())
			}
			select {
			case <-disconnected:
			case <-ctx.Done():
				t.Fatal("interrupted list left the HTTP request open")
			}
		})
	}
}

func TestMainProjectRateLimitPermissionWorkflows(t *testing.T) {
	for _, tc := range []struct {
		resource, verb, method, response string
		flags                            []string
		body                             map[string]any
	}{
		{"model-permissions", "retrieve", http.MethodGet, `{"object":"project.model_permissions","mode":"allow_list","model_ids":["model_limits"]}`, nil, nil},
		{"model-permissions", "update", http.MethodPost, `{"object":"project.model_permissions","mode":"deny_list","model_ids":["model_limits"]}`, []string{"--mode", "deny_list", "--model-id", "model_limits"}, map[string]any{"mode": "deny_list", "model_ids": []any{"model_limits"}}},
		{"model-permissions", "delete", http.MethodDelete, `{"object":"project.model_permissions.deleted","deleted":true}`, nil, nil},
		{"hosted-tool-permissions", "retrieve", http.MethodGet, `{"object":"project.hosted_tool_permissions","web_search":{"enabled":true}}`, nil, nil},
		{"hosted-tool-permissions", "update", http.MethodPost, `{"object":"project.hosted_tool_permissions","web_search":{"enabled":false}}`, []string{"--web-search.enabled=false"}, map[string]any{"web_search": map[string]any{"enabled": false}}},
	} {
		t.Run(tc.resource+"/"+tc.verb, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != tc.method || r.URL.Path != "/organization/projects/proj_limits/"+strings.ReplaceAll(tc.resource, "-", "_") {
					t.Errorf("permission request changed: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-admin-key" || r.Header.Get("X-Permission-Test") != "preserved" {
					t.Error("permission request lost admin credentials or custom headers")
				}
				if tc.body != nil {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, tc.body) {
						t.Errorf("permission body=%#v want=%#v error=%v", body, tc.body, err)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.response)
			}))
			defer server.Close()
			args := []string{"openai", "--format", "raw", "projects", tc.resource, tc.verb, "--project-id", "proj_limits", "--header", "X-Permission-Test: preserved"}
			args = append(args, tc.flags...)
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-user-key", "OPENAI_ADMIN_KEY=synthetic-admin-key"}, args...)
			if got.code != 0 || got.stderr != "" || got.stdout != tc.response+"\n" || requests.Load() != 1 {
				t.Fatalf("permission result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}
