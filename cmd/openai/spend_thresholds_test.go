package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func runSpendThresholdCommand(t *testing.T, server *httptest.Server, args ...string) mainDispatchResult {
	t.Helper()
	return runMainDispatchWithEnv(t, "bash", []string{
		"OPENAI_BASE_URL=" + server.URL,
		"OPENAI_API_KEY=sk-fake-spend-test",
		"OPENAI_ADMIN_KEY=sk-fake-spend-admin",
		"FORCE_COLOR=0", "NO_COLOR=1", "COLUMNS=28",
	}, append([]string{"openai"}, args...)...)
}

func spendThresholdArgs(project bool, resource, operation string) []string {
	args := []string{"admin", "organization"}
	if project {
		args = append(args, "projects")
	}
	args = append(args, resource, operation)
	if project {
		args = append(args, "--project-id", "proj_synthetic")
	}
	if resource == "spend-alerts" && operation != "create" && operation != "list" {
		args = append(args, "--alert-id", "alert_synthetic")
	}
	return args
}

func spendThresholdPath(project bool, resource string) string {
	path := "/organization"
	if project {
		path += "/projects/proj_synthetic"
	}
	return path + "/" + strings.ReplaceAll(resource, "-", "_")
}

func spendThresholdBody(project bool, resource, fields string) string {
	scope := "organization"
	if project {
		scope = "project"
	}
	object := scope + "." + strings.TrimSuffix(strings.ReplaceAll(resource, "-", "_"), "s")
	return `{"object":"` + object + `",` + strings.TrimPrefix(fields, "{")
}

func assertSpendThresholdJSON(t *testing.T, output string, records ...string) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	for _, record := range records {
		original := json.NewDecoder(strings.NewReader(record))
		original.UseNumber()
		var actual, want any
		if err := original.Decode(&want); err != nil {
			t.Fatal(err)
		}
		if err := decoder.Decode(&actual); err != nil || !reflect.DeepEqual(actual, want) {
			t.Fatalf("spend response changed: actual=%v want=%v error=%v; output=%q", actual, want, err, output)
		}
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("unexpected trailing spend response: %v; output=%q", err, output)
	}
}

func TestMainSpendThresholdRoutesAndRequests(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, operation := range []struct{ resource, method string }{
			{"spend-limit", "retrieve"}, {"spend-limit", "update"},
			{"spend-alerts", "create"}, {"spend-alerts", "retrieve"}, {"spend-alerts", "update"},
		} {
			for _, format := range []string{"", "text"} {
				t.Run(fmt.Sprintf("project=%t/%s/%s/%s", project, operation.resource, operation.method, format), func(t *testing.T) {
					path := spendThresholdPath(project, operation.resource)
					if operation.resource == "spend-alerts" && operation.method != "create" {
						path += "/alert_synthetic"
					}
					mutation := operation.method != "retrieve"
					bodies := make(chan string, 8)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						method := http.MethodGet
						if mutation {
							method = http.MethodPost
						}
						if r.Method != method || r.URL.Path != path {
							t.Errorf("unexpected request: %s %s, want %s %s", r.Method, r.URL.Path, method, path)
						}
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						bodies <- string(body)
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, spendThresholdBody(project, operation.resource, `{"id":"alert_synthetic","currency":"USD","interval":"month","threshold_amount":10000,"future":{"note":"preserved"}}`))
					}))
					defer server.Close()
					args := spendThresholdArgs(project, operation.resource, operation.method)
					if mutation {
						args = append(args, "--currency", "USD", "--interval", "month", "--threshold-amount", "9007199254740993")
						if operation.resource == "spend-alerts" {
							args = append(args, "--notification-channel", `{"type":"email","recipients":["synthetic@example.invalid"],"subject_prefix":null}`)
						}
					}
					if format != "" {
						args = append([]string{"--format", format}, args...)
					}
					got := runSpendThresholdCommand(t, server, args...)
					if got.code != 0 || got.stderr != "" {
						t.Fatalf("spend command failed: %+v", got)
					}
					if len(bodies) != 1 {
						t.Fatalf("received %d requests, want one", len(bodies))
					}
					body := <-bodies
					if mutation {
						want := `{"currency":"USD","interval":"month","threshold_amount":9007199254740993}`
						if operation.resource == "spend-alerts" {
							want = `{"currency":"USD","interval":"month","threshold_amount":9007199254740993,"notification_channel":{"type":"email","recipients":["synthetic@example.invalid"],"subject_prefix":null}}`
						}
						assertSpendThresholdJSON(t, body, want)
					} else if body != "" {
						t.Errorf("retrieve sent a request body: %q", body)
					}
					for _, want := range []string{"Spend threshold: USD 100.00 per month", "Currency: USD", "Interval: month", "Note: preserved"} {
						if !strings.Contains(got.stdout, want) {
							t.Errorf("missing %q in %q", want, got.stdout)
						}
					}
					want := "Enforcement: not reported in this response"
					if operation.resource == "spend-alerts" {
						want = "Alert behavior: Alerts notify; they are not spending caps."
					}
					if !strings.Contains(got.stdout, want) || strings.Contains(got.stdout, "Threshold amount:") {
						t.Errorf("unclear spend threshold presentation: %q; want %q", got.stdout, want)
					}
				})
			}
		}
	}
}

func TestMainSpendThresholdPrecisionAndUnknownFields(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		want, absent []string
	}{
		{"zero", `{"threshold_amount":0,"currency":"USD","interval":"month"}`, []string{"Spend threshold: USD 0.00 per month"}, nil},
		{"negative", `{"threshold_amount":-1,"currency":"USD","interval":"month"}`, []string{"Spend threshold: USD -0.01 per month"}, nil},
		{"large", `{"threshold_amount":9223372036854775807,"currency":"USD","interval":"month"}`, []string{"Spend threshold: USD 92233720368547758.07 per month"}, nil},
		{"decimal", `{"threshold_amount":100.005,"currency":"USD","interval":"month"}`, []string{"100.005", "cents"}, []string{"USD 1.00"}},
		{"exponent", `{"threshold_amount":1e4,"currency":"USD","interval":"month"}`, []string{"1e4", "cents"}, []string{"USD 100.00"}},
		{"unknown units", `{"threshold_amount":123,"currency":"ZZZ","interval":"quarter"}`, []string{"123 cents", "ZZZ", "(interval: quarter)"}, []string{"USD", "per month"}},
		{"missing units", `{"threshold_amount":123}`, []string{"123 cents"}, []string{"USD", "per month"}},
		{"missing interval", `{"threshold_amount":123,"currency":"USD"}`, []string{"USD 1.23"}, []string{"per month"}},
		{"null enforcement", `{"threshold_amount":123,"currency":"USD","interval":"month","enforcement":null}`, []string{"Enforcement: not reported in this response"}, nil},
		{"reported enforcement", `{"threshold_amount":123,"currency":"USD","interval":"month","enforcement":{"mode":"reported_mode","future":9007199254740993}}`, []string{"Mode: reported_mode", "Future: 9007199254740993"}, []string{"not reported in this response"}},
		{"null threshold", `{"threshold_amount":null,"future":"preserved"}`, []string{"Threshold amount: (null)", "Future: preserved"}, []string{"Spend threshold:", "USD", "per month"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, spendThresholdBody(false, "spend-limit", tc.body))
			}))
			defer server.Close()
			got := runSpendThresholdCommand(t, server, spendThresholdArgs(false, "spend-limit", "retrieve")...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("threshold response failed: %+v", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("missing %q in %q", want, got.stdout)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(got.stdout, absent) {
					t.Errorf("invented or changed %q in %q", absent, got.stdout)
				}
			}
		})
	}
}

func TestMainSpendThresholdShortAlias(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/organization/spend_limit" {
			t.Errorf("unexpected alias request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, spendThresholdBody(false, "spend-limit", `{"threshold_amount":10000,"currency":"USD","interval":"month"}`))
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"admin", "spend-limit", "retrieve"},
		{"admin:organization:spend-limit", "retrieve"},
		{"--format", "TEXT", "admin", "spend-limit", "retrieve"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runSpendThresholdCommand(t, server, args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "Spend threshold: USD 100.00 per month") || !strings.Contains(got.stdout, "Enforcement: not reported in this response") {
				t.Fatalf("spend-limit alias failed: %+v", got)
			}
		})
	}
}

func TestMainSpendThresholdReportedEnforcementStatus(t *testing.T) {
	for _, status := range []string{"inactive", "enforcing", "future_status"} {
		t.Run(status, func(t *testing.T) {
			body := spendThresholdBody(false, "spend-limit", `{"threshold_amount":10000,"currency":"USD","interval":"month","enforcement":{"status":"`+status+`","future":9007199254740993}}`)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, body)
			}))
			defer server.Close()
			args := spendThresholdArgs(false, "spend-limit", "retrieve")
			got := runSpendThresholdCommand(t, server, args...)
			want := "Object: organization.spend_limit\nSpend threshold: USD 100.00 per month\nCurrency: USD\nInterval: month\nEnforcement:\n  Status: " + status + "\n  Future: 9007199254740993\n"
			if got.code != 0 || got.stderr != "" || got.stdout != want {
				t.Fatalf("reported enforcement status changed: %+v; want %q", got, want)
			}
			got = runSpendThresholdCommand(t, server, append([]string{"--transform", "enforcement"}, args...)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("enforcement extraction failed: %+v", got)
			}
			assertSpendThresholdJSON(t, got.stdout, `{"status":"`+status+`","future":9007199254740993}`)
		})
	}
}

func TestMainSpendThresholdMachineFormatsAndExtraction(t *testing.T) {
	for _, resource := range []string{"spend-limit", "spend-alerts"} {
		body := spendThresholdBody(true, resource, `{"id":"alert_synthetic","currency":"USD","interval":"month","threshold_amount":9007199254740993,"future":{"exact":123456789012345678901234567890,"decimal":0.100000000000000001},"enforcement":null}`)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, body)
		}))
		defer server.Close()
		for _, format := range []string{"json", "JSON", "jsonl", "raw", "yaml", "pretty", "explore"} {
			t.Run(resource+"/"+format, func(t *testing.T) {
				args := append([]string{"--format", format}, spendThresholdArgs(true, resource, "retrieve")...)
				got := runSpendThresholdCommand(t, server, args...)
				wantStderr := ""
				if format == "explore" {
					wantStderr = "Warning: Output format 'explore' not supported for non-terminal output; falling back to 'json'\n"
				}
				if got.code != 0 || got.stderr != wantStderr {
					t.Fatalf("explicit format failed: %+v", got)
				}
				if format == "yaml" || format == "pretty" {
					for _, value := range []string{"threshold_amount: 9007199254740993", "exact: 123456789012345678901234567890", "decimal: 0.100000000000000001", "enforcement: null"} {
						if !strings.Contains(got.stdout, value) {
							t.Errorf("%s lost %q: %q", format, value, got.stdout)
						}
					}
				} else {
					assertSpendThresholdJSON(t, got.stdout, body)
				}
				if format == "raw" && got.stdout != body+"\n" {
					t.Errorf("raw response bytes changed: %q", got.stdout)
				}
				if strings.Contains(got.stdout, "spend_threshold") || strings.Contains(got.stdout, "alert_behavior") {
					t.Errorf("projection entered explicit output: %q", got.stdout)
				}
			})
		}
		for _, tc := range []struct {
			name  string
			flags []string
			want  string
		}{
			{"raw-output", []string{"--raw-output"}, body},
			{"number extraction", []string{"--transform", "threshold_amount"}, "9007199254740993"},
			{"text extraction", []string{"--format", "text", "--transform", "threshold_amount"}, "9007199254740993"},
			{"raw extraction", []string{"--transform", "future.decimal", "--raw-output"}, "0.100000000000000001"},
		} {
			t.Run(resource+"/"+tc.name, func(t *testing.T) {
				got := runSpendThresholdCommand(t, server, append(tc.flags, spendThresholdArgs(true, resource, "retrieve")...)...)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("explicit extraction failed: %+v", got)
				}
				assertSpendThresholdJSON(t, got.stdout, tc.want)
			})
		}
	}
}

func TestMainSpendAlertsPagination(t *testing.T) {
	for _, project := range []bool{false, true} {
		first := spendThresholdBody(project, "spend-alerts", `{"id":"alert_first","currency":"USD","interval":"month","threshold_amount":10000,"future":9007199254740993}`)
		second := spendThresholdBody(project, "spend-alerts", `{"id":"alert_second","currency":"USD","interval":"month","threshold_amount":0}`)
		page := `{"object":"list","data":[` + first + `],"has_more":true,"first_id":"alert_first","last_id":"alert_first"}`
		for _, tc := range []struct {
			name         string
			flags        []string
			max          string
			count, calls int
			failure, raw bool
		}{
			{name: "default", count: 2, calls: 2},
			{name: "unlimited", max: "-1", count: 2, calls: 2},
			{name: "zero", max: "0", calls: 1},
			{name: "one", max: "1", count: 1, calls: 1},
			{name: "raw page", flags: []string{"--format", "raw"}, count: 1, calls: 1, raw: true},
			{name: "jsonl", flags: []string{"--format", "jsonl"}, count: 2, calls: 2},
			{name: "partial failure", count: 1, calls: 2, failure: true},
			{name: "structured partial failure", flags: []string{"--format", "jsonl"}, count: 1, calls: 2, failure: true},
		} {
			t.Run(fmt.Sprintf("project=%t/%s", project, tc.name), func(t *testing.T) {
				var mu sync.Mutex
				var cursors []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					cursor := r.URL.Query().Get("after")
					mu.Lock()
					cursors = append(cursors, cursor)
					mu.Unlock()
					if r.Method != http.MethodGet || r.URL.Path != spendThresholdPath(project, "spend-alerts") || r.URL.Query().Get("limit") != "1" {
						t.Errorf("unexpected list request: %s %s", r.Method, r.URL)
					}
					w.Header().Set("Content-Type", "application/json")
					switch cursor {
					case "":
						io.WriteString(w, page)
					case "alert_first":
						if tc.failure {
							w.Header().Set("x-should-retry", "false")
							w.WriteHeader(http.StatusBadRequest)
							io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"Synthetic page failure"}}`)
						} else {
							io.WriteString(w, `{"object":"list","data":[`+second+`],"has_more":false,"first_id":"alert_second","last_id":"alert_second"}`)
						}
					default:
						http.Error(w, "unexpected cursor", http.StatusBadRequest)
					}
				}))
				defer server.Close()
				args := append(tc.flags, spendThresholdArgs(project, "spend-alerts", "list")...)
				args = append(args, "--limit", "1")
				if tc.max != "" {
					args = append(args, "--max-items", tc.max)
				}
				got := runSpendThresholdCommand(t, server, args...)
				mu.Lock()
				actualCursors := slices.Clone(cursors)
				mu.Unlock()
				if want := []string{"", "alert_first"}[:tc.calls]; !slices.Equal(actualCursors, want) {
					t.Fatalf("cursors=%q, want %q", actualCursors, want)
				}
				if tc.failure {
					if got.code != 1 {
						t.Fatalf("partial API failure disappeared: %+v", got)
					}
					if slices.Contains(tc.flags, "jsonl") {
						payload := decodeMainStructuredError(t, "jsonl", got.stderr)
						if payload["message"] != "Synthetic page failure" {
							t.Errorf("structured page failure changed: %#v", payload)
						}
					} else if !strings.Contains(got.stderr, "HTTP 400: Bad Request.") || strings.Contains(got.stderr, "Synthetic page failure") {
						t.Errorf("readable page failure framing changed: %q", got.stderr)
					}
				} else if got.code != 0 || got.stderr != "" {
					t.Fatalf("spend alerts list failed: %+v", got)
				}
				if tc.raw {
					if got.stdout != page+"\n" {
						t.Fatalf("raw page changed: %q", got.stdout)
					}
				} else if slices.Contains(tc.flags, "jsonl") {
					assertSpendThresholdJSON(t, got.stdout, []string{first, second}[:tc.count]...)
				} else {
					if strings.Count(got.stdout, "Spend threshold:") != tc.count || strings.Count(got.stdout, "Alerts notify; they are not spending caps.") != tc.count {
						t.Fatalf("list lost threshold or alert clarification: %+v", got)
					}
					if tc.count > 0 && (!strings.Contains(got.stdout, "ID: alert_first") || !strings.Contains(got.stdout, "9007199254740993")) {
						t.Errorf("partial result lost original metadata: %q", got.stdout)
					}
					if tc.count == 0 && got.stdout != "" {
						t.Errorf("zero item list emitted output: %q", got.stdout)
					}
				}
			})
		}
	}
}

func TestMainSpendThresholdMalformedAPIErrors(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty", ""},
		{"malformed", `{"error":{"message":"synthetic private spend detail"`},
		{"empty envelope", `{}`},
		{"empty error", `{"error":{}}`},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("x-should-retry", "false")
					w.WriteHeader(http.StatusBadGateway)
					io.WriteString(w, tc.body)
				}))
				defer server.Close()
				// Preserve the shared error contract, including an empty error object.
				reference := runSpendThresholdCommand(t, server, "--format", format, "models", "retrieve", "model_synthetic")
				args := append([]string{"--format", format}, spendThresholdArgs(false, "spend-limit", "retrieve")...)
				got := runSpendThresholdCommand(t, server, args...)
				if got != reference || got.code != 1 || got.stdout != "" || requests.Load() != 2 {
					t.Fatalf("spend error framing changed: got=%+v reference=%+v requests=%d", got, reference, requests.Load())
				}
				if format == "json" {
					payload := decodeMainErrorObject(t, "json", got.stderr)
					if len(payload) > 0 && (payload["status_code"] != json.Number("502") || payload["message"] != "HTTP 502 Bad Gateway: the server returned no usable JSON error details.") {
						t.Errorf("fallback status or message changed: %#v", payload)
					}
				} else if !strings.Contains(got.stderr, "HTTP 502: Bad Gateway.") || !strings.Contains(got.stderr, "API error details: --format-error json.") {
					t.Errorf("readable error lacks status or guidance: %q", got.stderr)
				}
				for _, absent := range []string{"synthetic private spend detail", "sk-fake-spend", "Spend threshold:", "spend_threshold"} {
					if strings.Contains(got.stderr, absent) {
						t.Errorf("error included %q: %q", absent, got.stderr)
					}
				}
			})
		}
	}
}

func TestMainSpendThresholdControlsAndDeleteIdentity(t *testing.T) {
	const body = `{"object":"organization.spend_limit","threshold_amount":123,"currency":"ZZZ\u001b[31m","interval":"quarter\r\u001b[2J","future":"keep\u0007value"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer server.Close()
	got := runSpendThresholdCommand(t, server, spendThresholdArgs(false, "spend-limit", "retrieve")...)
	if got.code != 0 || got.stderr != "" || strings.ContainsAny(got.stdout, "\x1b\r\a") || !strings.Contains(got.stdout, "123 cents") || !strings.Contains(got.stdout, "keep") {
		t.Fatalf("readable spend threshold emitted controls or lost content: %+v", got)
	}
	for _, resource := range []string{"spend-limit", "spend-alerts"} {
		got := runSpendThresholdCommand(t, server, spendThresholdArgs(false, resource, "delete")...)
		if got.code != 0 || got.stderr != "" || strings.Contains(got.stdout, "Spend threshold:") || strings.Contains(got.stdout, "Alert behavior:") || strings.Contains(got.stdout, "Enforcement:") || !strings.Contains(got.stdout, "Threshold amount: 123") {
			t.Fatalf("delete response incorrectly projected: %+v", got)
		}
	}
}

func TestMainSpendThresholdLargeUnknownField(t *testing.T) {
	// Keep a complete unknown field larger than common line and buffer limits.
	value := strings.Repeat("synthetic", 1<<20)
	body := spendThresholdBody(false, "spend-limit", `{"threshold_amount":10000,"currency":"USD","interval":"month","future":"`+value+`"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer server.Close()
	args := spendThresholdArgs(false, "spend-limit", "retrieve")
	got := runSpendThresholdCommand(t, server, args...)
	if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "Spend threshold: USD 100.00 per month") || !strings.Contains(got.stdout, "Future: "+value+"\n") {
		t.Fatalf("large readable response lost data: code=%d stderr=%q output bytes=%d", got.code, got.stderr, len(got.stdout))
	}
	got = runSpendThresholdCommand(t, server, append([]string{"--format", "raw"}, args...)...)
	if got.code != 0 || got.stderr != "" || got.stdout != body+"\n" {
		t.Fatalf("large raw response changed: code=%d stderr=%q output bytes=%d", got.code, got.stderr, len(got.stdout))
	}
	got = runSpendThresholdCommand(t, server, append([]string{"--transform", "future", "--raw-output"}, args...)...)
	if got.code != 0 || got.stderr != "" || got.stdout != value+"\n" {
		t.Fatalf("large extracted field changed: code=%d stderr=%q output bytes=%d", got.code, got.stderr, len(got.stdout))
	}
}
