package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const costReportFrom = "2026-10-01"
const costReportTo = "2026-10-08"
const costReportStart = "1790812800"
const costReportEnd = "1791417600"

func costReportArgs(extra ...string) []string {
	return append([]string{"openai", "costs", "report", "--from", costReportFrom, "--to", costReportTo}, extra...)
}

func costReportEnv(server *httptest.Server) []string {
	return []string{
		"OPENAI_BASE_URL=" + server.URL,
		"OPENAI_ADMIN_KEY=synthetic-cost-report-admin",
		"OPENAI_API_KEY=synthetic-cost-report-project-key",
		"FORCE_COLOR=0",
		"GOMAXPROCS=2",
	}
}

func costReportPage(results, next string, more bool) string {
	data := "[]"
	if results != "" {
		data = `[{"object":"bucket","start_time":1790812800,"end_time":1790899200,"results":[` + results + `]}]`
	}
	cursor, _ := json.Marshal(next)
	return fmt.Sprintf(`{"object":"page","data":%s,"has_more":%t,"next_page":%s,"future_page_field":{"ignored":true}}`, data, more, cursor)
}

func costReportServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	requests := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/organization/costs" {
			t.Errorf("unexpected cost request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-cost-report-admin" {
			t.Error("cost request did not select the Admin key")
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func TestMainCostReportPaginationAndExactJSON(t *testing.T) {
	first := `{"object":"organization.costs.result","project_id":"proj_b","amount":{"value":0.1,"currency":"usd"}},` +
		`{"object":"organization.costs.result","project_id":"proj_a","amount":{"value":9007199254740993.000000000000000001,"currency":"usd"},"future_result":{"keep_out_of_report":true}},` +
		`{"object":"organization.costs.result","project_id":null,"amount":{"value":0.125,"currency":"usd"}},` +
		`{"object":"organization.costs.result","amount":{"value":0.25,"currency":"usd"}}`
	last := `{"object":"organization.costs.result","project_id":"proj_b","amount":{"value":0.2,"currency":"usd"}},` +
		`{"object":"organization.costs.result","project_id":"proj_b","amount":{"value":2e-2,"currency":"eur"}},` +
		`{"object":"organization.costs.result","project_id":"proj_a","amount":{"value":9e-18,"currency":"usd"}}`
	server, requests := costReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		for key, want := range map[string]string{"start_time": costReportStart, "end_time": costReportEnd, "bucket_width": "1d", "limit": "180"} {
			if query.Get(key) != want {
				t.Errorf("%s = %q; want %q", key, query.Get(key), want)
			}
		}
		if got := query["group_by[]"]; len(got) != 1 || got[0] != "project_id" {
			t.Errorf("group_by[] = %q; want project_id", got)
		}
		switch query.Get("page") {
		case "":
			_, _ = io.WriteString(w, costReportPage(first, "page-two", true))
		case "page-two":
			_, _ = io.WriteString(w, costReportPage("", "page-three", true))
		case "page-three":
			_, _ = io.WriteString(w, costReportPage(last, "ignored-final-cursor", false))
		default:
			t.Errorf("unexpected cursor: %q", query.Get("page"))
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", "json")...)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	require.EqualValues(t, 3, requests.Load())
	require.JSONEq(t, `{"from":"2026-10-01","to":"2026-10-08","timezone":"UTC","start_time":1790812800,"end_time":1791417600,"group_by":"project","rows":[
		{"project_id":null,"currency":"usd","amount":"0.375"},
		{"project_id":"proj_a","currency":"usd","amount":"9007199254740993.00000000000000001"},
		{"project_id":"proj_b","currency":"eur","amount":"0.02"},
		{"project_id":"proj_b","currency":"usd","amount":"0.3"}]}`, got.stdout)
}

func TestMainCostReportLargeUnknownFieldPreservesReport(t *testing.T) {
	// Keep this large compatibility probe sequential to bound peak memory.
	server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
		parts := []string{
			`{"object":"page","data":[{"object":"bucket","start_time":1790812800,"end_time":1790899200,"results":[{"object":"organization.costs.result","project_id":"proj_example","amount":{"value":12.34,"currency":"usd"}}]}],"has_more":false,"future_field":"`,
			strings.Repeat("x", 34<<20),
			`"}`,
		}
		for _, part := range parts {
			if _, err := io.WriteString(w, part); err != nil {
				t.Errorf("write large synthetic Costs response: %v", err)
				return
			}
		}
	})
	got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", "json")...)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	require.EqualValues(t, 1, requests.Load())
	require.JSONEq(t, `{"from":"2026-10-01","to":"2026-10-08","timezone":"UTC","start_time":1790812800,"end_time":1791417600,"group_by":"project","rows":[{"project_id":"proj_example","currency":"usd","amount":"12.34"}]}`, got.stdout)
}

func TestMainCostReportDuplicateKnownFieldsFailWithoutTotals(t *testing.T) {
	const ordinary = `{"object":"organization.costs.result","project_id":"proj_example","amount":{"value":1,"currency":"usd"}}`
	for _, tc := range []struct {
		name, response string
	}{
		{"data", strings.Replace(costReportPage(ordinary, "", false), `"data":`, `"data":[],"data":`, 1)},
		{"amount value", costReportPage(strings.Replace(ordinary, `"value":1`, `"value":1,"value":2`, 1), "", false)},
		{"escaped amount value", costReportPage(strings.Replace(ordinary, `"value":1`, `"value":1,"v\u0061lue":2`, 1), "", false)},
		{"case alias amount value", costReportPage(strings.Replace(ordinary, `"value":1`, `"value":1,"VALUE":2`, 1), "", false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.response)
			})
			got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", "json")...)
			require.Equal(t, 1, got.code, "%+v", got)
			require.Empty(t, got.stdout, "ambiguous source fields must not become report totals")
			require.EqualValues(t, 1, requests.Load())
			requireCostReportDiagnostic(t, "json", got.stderr,
				"The Costs API returned incomplete or invalid report data. No report was written.")
		})
	}
}

func TestMainCostReportDuplicateUnknownFieldsRemainIgnored(t *testing.T) {
	response := costReportPage(`{"object":"organization.costs.result","project_id":"proj_example","future":1,"future":2,"amount":{"value":12.34,"currency":"usd","unknown":1,"unknown":2}}`, "", false)
	response = strings.Replace(response, `"future_page_field":`, `"unknown":1,"unknown":2,"future_page_field":`, 1)
	server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, response)
	})
	got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", "json")...)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	require.EqualValues(t, 1, requests.Load())
	require.JSONEq(t, `{"from":"2026-10-01","to":"2026-10-08","timezone":"UTC","start_time":1790812800,"end_time":1791417600,"group_by":"project","rows":[{"project_id":"proj_example","currency":"usd","amount":"12.34"}]}`, got.stdout)
}

func TestMainCostReportCompactExtremeExponents(t *testing.T) {
	for _, tc := range []struct {
		name, amount string
		values       []string
	}{
		{"single", "1e1000000000", []string{"1e1000000000"}},
		{"same scale addition", "3e1000000000", []string{"1e1000000000", "2e1000000000"}},
		{"opposite cancellation", "0", []string{"1e1000000000", "-1e1000000000"}},
		{"excessive alignment", "", []string{"1e1000000000", "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results := make([]string, len(tc.values))
			for i, value := range tc.values {
				results[i] = fmt.Sprintf(`{"object":"organization.costs.result","project_id":"proj_example","amount":{"value":%s,"currency":"usd"}}`, value)
			}
			server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, costReportPage(strings.Join(results, ","), "", false))
			})
			// A compact exponent must not trigger a billion-digit expansion or long-running work.
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			child := costReportChild(t, ctx, server, "--format", "json")
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			err := child.Run()
			require.NoError(t, ctx.Err(), "compact decimal processing exceeded the process deadline")
			require.EqualValues(t, 1, requests.Load())
			if tc.amount == "" {
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit)
				require.Equal(t, 1, exit.ExitCode())
				require.Empty(t, stdout.String())
				var diagnostic struct {
					Message string `json:"message"`
				}
				require.NoError(t, json.Unmarshal(stderr.Bytes(), &diagnostic))
				require.Contains(t, diagnostic.Message, "additional decimal digits")
				require.Contains(t, diagnostic.Message, "No report was written.")
				return
			}
			require.NoError(t, err, "stderr=%q", stderr.String())
			require.Empty(t, stderr.String())
			want := fmt.Sprintf(`{"from":"2026-10-01","to":"2026-10-08","timezone":"UTC","start_time":1790812800,"end_time":1791417600,"group_by":"project","rows":[{"project_id":"proj_example","currency":"usd","amount":%q}]}`, tc.amount)
			require.JSONEq(t, want, stdout.String())
		})
	}
}

func TestMainCostReportPrecisionBudgetDoesNotGrowAcrossPages(t *testing.T) {
	values := []string{"1", "1e1048576", "1e2097152", "1e3145728"}
	cursors := []string{"", "page-two", "page-three", "page-four"}
	server, requests := costReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("page")
		for i, known := range cursors {
			if cursor != known {
				continue
			}
			result := fmt.Sprintf(`{"object":"organization.costs.result","project_id":"proj_example","amount":{"value":%s,"currency":"usd"}}`, values[i])
			more, next := i+1 < len(values), ""
			if more {
				next = cursors[i+1]
			}
			_, _ = io.WriteString(w, costReportPage(result, next, more))
			return
		}
		t.Errorf("unexpected cost report cursor: %q", cursor)
		w.WriteHeader(http.StatusBadRequest)
	})
	got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", "json")...)
	require.Equal(t, 1, got.code, "%+v", got)
	require.Empty(t, got.stdout, "cumulative alignment failure must not expose incomplete totals")
	require.EqualValues(t, 3, requests.Load(), "the third page must exhaust the budget before fetching page four")
	var diagnostic struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(got.stderr), &diagnostic))
	require.Contains(t, diagnostic.Message, "additional decimal digits")
	require.Contains(t, diagnostic.Message, "No report was written.")
}

func TestMainCostReportLegacyRawKeepsExtremeAndDuplicateFields(t *testing.T) {
	response := costReportPage(`{"object":"organization.costs.result","project_id":"proj_example","amount":{"value":1e1000000000,"value":2,"currency":"usd"}}`, "", false)
	server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, response)
	})
	got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), "openai", "--format", "raw",
		"admin:organization:usage", "costs", "--start-time", costReportStart)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	require.Equal(t, response+"\n", got.stdout)
	require.EqualValues(t, 1, requests.Load())
}

func TestMainCostReportFiltersAndGlobalRequestOptions(t *testing.T) {
	server, requests := costReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, []string{"proj_filter", "@literal", "proj,comma"}, r.URL.Query()["project_ids[]"])
		assert.Equal(t, "proj_header", r.Header.Get("OpenAI-Project"))
		assert.Equal(t, "org_synthetic", r.Header.Get("OpenAI-Organization"))
		assert.Equal(t, "synthetic", r.Header.Get("X-Cost-Report"))
		_, _ = io.WriteString(w, costReportPage("", "", false))
	})
	args := []string{"openai", "--project", "proj_header", "--organization", "org_synthetic", "costs", "--format", "json", "report",
		"--from", costReportFrom, "--to", costReportTo, "--group-by", "project", "--timezone", "UTC",
		"--project-id", "proj_filter", "--project-id", "@literal", "--project-id", "proj,comma", "--header", "X-Cost-Report: synthetic"}
	got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), args...)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	require.EqualValues(t, 1, requests.Load())
}

func TestMainCostReportIgnoresOpenStdin(t *testing.T) {
	server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, costReportPage("", "", false))
	})
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	defer writer.Close()
	_, err = io.WriteString(writer, `{"ignored":"synthetic stdin"}`)
	require.NoError(t, err)
	// Keep the producer open. Any attempt to consume stdin would block the command.
	got := runMainDispatchWithStdin(t, "bash", costReportEnv(server), reader, costReportArgs("--format", "json")...)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	require.True(t, json.Valid([]byte(got.stdout)))
	require.EqualValues(t, 1, requests.Load())
}

func TestMainCostReportHelpNeedsNoRequest(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "costs", "report", "--help"},
		{"openai", "help", "costs", "report"},
	} {
		t.Run(strings.Join(args[1:], "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=http://127.0.0.1:1"}, args...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			for _, value := range []string{"costs report", "--from", "--to", "--timezone", "--project-id", "--export"} {
				require.Contains(t, got.stdout, value)
			}
		})
	}
}

func TestMainCostReportReadableAndEmpty(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, format := range []string{"", "auto", "text", "json"} {
			t.Run(fmt.Sprintf("empty=%t/format=%s", empty, format), func(t *testing.T) {
				results := `{"object":"organization.costs.result","project_id":"proj_example","amount":{"value":12.34,"currency":"usd"}}`
				if empty {
					results = ""
				}
				server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, costReportPage(results, "", false))
				})
				args := costReportArgs()
				if format != "" {
					args = append(args, "--format", format)
				}
				got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), args...)
				require.Zero(t, got.code, "%+v", got)
				require.Empty(t, got.stderr)
				require.EqualValues(t, 1, requests.Load())
				if format == "json" {
					var report struct {
						Rows []json.RawMessage `json:"rows"`
					}
					require.NoError(t, json.Unmarshal([]byte(got.stdout), &report))
					require.NotNil(t, report.Rows, "empty reports must contain [] rather than null")
					require.Len(t, report.Rows, map[bool]int{true: 0, false: 1}[empty])
				} else {
					require.Contains(t, got.stdout, "Period: 2026-10-01 to 2026-10-08 UTC (end exclusive)")
					if !empty {
						for _, value := range []string{"PROJECT", "CURRENCY", "AMOUNT", "proj_example", "usd", "12.34"} {
							require.Contains(t, got.stdout, value)
						}
					}
				}
				if empty {
					require.NotContains(t, strings.ToLower(got.stdout), "usd", "an empty report must not infer currency")
				}
			})
		}
	}
}

func TestMainCostReportQuietAndVerbosePreserveDataAndFailures(t *testing.T) {
	const failure = `{"message":"synthetic rejected project filter","type":"invalid_request_error"}`
	server, requests := costReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project_ids[]") == "reject" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":`+failure+`}`)
			return
		}
		_, _ = io.WriteString(w, costReportPage(`{"object":"organization.costs.result","project_id":"proj_example","amount":{"value":12.34,"currency":"usd"}}`, "", false))
	})
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			ordinary := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", format)...)
			require.Zero(t, ordinary.code, "%+v", ordinary)
			require.Empty(t, ordinary.stderr)
			require.NotEmpty(t, ordinary.stdout)
			for _, flags := range [][]string{{"--quiet"}, {"--quiet", "--verbose"}, {"--verbose"}} {
				t.Run(strings.Join(flags, "/"), func(t *testing.T) {
					before := requests.Load()
					got := runMainDispatchWithEnv(t, "bash", costReportEnv(server),
						costReportArgs(append([]string{"--format", format}, flags...)...)...)
					require.Zero(t, got.code, "%+v", got)
					require.Equal(t, ordinary.stdout, got.stdout)
					require.Equal(t, before+1, requests.Load())
					if format == "text" && len(flags) == 1 && flags[0] == "--verbose" {
						diagnostics, _ := removeVerboseElapsed(t, got.stderr)
						require.Equal(t, "Command: costs report\nFormat option: text\nCommand result: completed\n", diagnostics)
					} else {
						require.Empty(t, got.stderr, "quiet and JSON modes must suppress optional diagnostics")
					}
				})
			}
			before := requests.Load()
			got := runMainDispatchWithEnv(t, "bash", costReportEnv(server),
				costReportArgs("--format", format, "--quiet", "--verbose", "--project-id", "reject")...)
			require.Equal(t, 1, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.Equal(t, before+1, requests.Load())
			require.NotContains(t, got.stderr, "Command result:")
			require.NotContains(t, got.stderr, "Elapsed:")
			if format == "json" {
				require.JSONEq(t, failure, got.stderr)
			} else {
				require.Contains(t, got.stderr, "HTTP 400: Bad Request.")
				require.Contains(t, got.stderr, "API error details: --format-error json.")
			}
		})
	}
}

func TestMainCostReportCSVAndReadableControls(t *testing.T) {
	results := `{"object":"organization.costs.result","project_id":"=SUM(1,2)","amount":{"value":-12.34,"currency":"usd"}},` +
		`{"object":"organization.costs.result","project_id":"\tproject","amount":{"value":1,"currency":"+currency"}},` +
		`{"object":"organization.costs.result","project_id":"proj_\u001b[31m\nsecond","amount":{"value":2,"currency":"usd"}},` +
		`{"object":"organization.costs.result","project_id":null,"amount":{"value":0,"currency":"usd"}},` +
		`{"object":"organization.costs.result","project_id":"","amount":{"value":3,"currency":"usd"}}`
	server, _ := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, costReportPage(results, "", false))
	})
	got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--export", "csv")...)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	rows, err := csv.NewReader(strings.NewReader(got.stdout)).ReadAll()
	require.NoError(t, err)
	require.Equal(t, [][]string{
		{"project_id", "currency", "amount"},
		{"", "usd", "0"},
		{"", "usd", "3"},
		{"'\tproject", "'+currency", "1"},
		{"'=SUM(1,2)", "usd", "-12.34"},
		{"'proj_\x1b[31m\nsecond", "usd", "2"},
	}, rows)
	got = runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", "text")...)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	require.NotContains(t, got.stdout, "\x1b")
	require.NotContains(t, got.stdout, "proj_\x1b[31m\nsecond")
	require.NotContains(t, got.stdout, "\t", "an API field must not create extra table columns")
	require.Contains(t, got.stdout, `\tproject`)
	require.Contains(t, got.stdout, `\nsecond`)
	require.Len(t, strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n"), 7,
		"five API rows must remain five table rows below the two heading lines")
	require.Contains(t, got.stdout, "(unattributed)")
	require.Contains(t, got.stdout, "(empty project ID)")
}

func TestMainCostReportValidationPrecedesRequests(t *testing.T) {
	server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, costReportPage("", "", false))
	})
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing dates", nil},
		{"missing to", []string{"--from", costReportFrom}},
		{"missing from", []string{"--to", costReportTo}},
		{"same dates", []string{"--from", costReportFrom, "--to", costReportFrom}},
		{"reversed dates", []string{"--from", costReportTo, "--to", costReportFrom}},
		{"invalid date", []string{"--from", "2026-02-30", "--to", costReportTo}},
		{"unpadded date", []string{"--from", "2026-2-01", "--to", costReportTo}},
		{"timestamp date", []string{"--from", "2026-10-01T00:00:00Z", "--to", costReportTo}},
		{"unknown timezone", []string{"--from", costReportFrom, "--to", costReportTo, "--timezone", "Unknown/Location"}},
		{"skipped local date", []string{"--from", "2011-12-30", "--to", "2012-01-02", "--timezone", "Pacific/Apia"}},
		{"unsupported grouping", []string{"--from", costReportFrom, "--to", costReportTo, "--group-by", "model"}},
		{"unsupported format", []string{"--from", costReportFrom, "--to", costReportTo, "--format", "yaml"}},
		{"unsupported export", []string{"--from", costReportFrom, "--to", costReportTo, "--export", "json"}},
		{"CSV format conflict", []string{"--from", costReportFrom, "--to", costReportTo, "--export", "csv", "--format", "json"}},
		{"CSV explicit auto conflict", []string{"--from", costReportFrom, "--to", costReportTo, "--export", "csv", "--format", "auto"}},
		{"transform", []string{"--from", costReportFrom, "--to", costReportTo, "--transform", "rows"}},
		{"empty transform", []string{"--from", costReportFrom, "--to", costReportTo, "--transform="}},
		{"raw output", []string{"--from", costReportFrom, "--to", costReportTo, "--raw-output"}},
		{"explicit false raw output", []string{"--from", costReportFrom, "--to", costReportTo, "--raw-output=false"}},
		{"extra argument", []string{"--from", costReportFrom, "--to", costReportTo, "unexpected"}},
		{"empty project filter", []string{"--from", costReportFrom, "--to", costReportTo, "--project-id="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := requests.Load()
			got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), append([]string{"openai", "costs", "report"}, tc.args...)...)
			require.NotZero(t, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.NotEmpty(t, got.stderr)
			require.Equal(t, before, requests.Load(), "validation must precede the request")
		})
	}
}

func requireCostReportDiagnostic(t *testing.T, format, actual, want string) {
	t.Helper()
	if format == "json" {
		encoded, err := json.Marshal(map[string]string{"message": want})
		require.NoError(t, err)
		require.JSONEq(t, string(encoded), actual)
		return
	}
	require.Equal(t, want+"\n", actual)
}

func TestMainCostReportValidationDiagnostics(t *testing.T) {
	server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	for _, tc := range []struct {
		name, from, to, want string
	}{
		{"invalid date", "2026-02-30", costReportTo, "--from: use an explicit YYYY-MM-DD date on or after 1970-01-01"},
		{"invalid range", costReportTo, costReportFrom, "--to must be later than --from; the end date is exclusive"},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), "openai", "costs", "report",
					"--from", tc.from, "--to", tc.to, "--format", format)
				require.Equal(t, 1, got.code, "%+v", got)
				require.Empty(t, got.stdout)
				requireCostReportDiagnostic(t, format, got.stderr, tc.want)
			})
		}
	}
	require.Zero(t, requests.Load())
}

func TestMainCostReportDSTRangeInstants(t *testing.T) {
	for _, tc := range []struct {
		name, from, to, start, end string
	}{
		{"spring", "2026-03-08", "2026-03-09", "2026-03-08T08:00:00Z", "2026-03-09T07:00:00Z"},
		{"autumn", "2026-11-01", "2026-11-02", "2026-11-01T07:00:00Z", "2026-11-02T08:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, err := time.Parse(time.RFC3339, tc.start)
			require.NoError(t, err)
			end, err := time.Parse(time.RFC3339, tc.end)
			require.NoError(t, err)
			server, requests := costReportServer(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, strconv.FormatInt(start.Unix(), 10), r.URL.Query().Get("start_time"))
				assert.Equal(t, strconv.FormatInt(end.Unix(), 10), r.URL.Query().Get("end_time"))
				_, _ = io.WriteString(w, costReportPage("", "", false))
			})
			got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), "openai", "costs", "report", "--from", tc.from,
				"--to", tc.to, "--timezone", "America/Los_Angeles", "--format", "json")
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.EqualValues(t, 1, requests.Load())
			var report struct {
				Timezone  string `json:"timezone"`
				StartTime int64  `json:"start_time"`
				EndTime   int64  `json:"end_time"`
			}
			require.NoError(t, json.Unmarshal([]byte(got.stdout), &report))
			require.Equal(t, "America/Los_Angeles", report.Timezone)
			require.Equal(t, start.Unix(), report.StartTime)
			require.Equal(t, end.Unix(), report.EndTime)
		})
	}
}

func TestMainCostReportPaginationFailuresDoNotEmitPartialReport(t *testing.T) {
	for _, tc := range []struct {
		name, second string
		status       int
		requests     int32
	}{
		{"API failure", `{"error":{"message":"synthetic page rejected","type":"invalid_request_error"}}`, http.StatusBadRequest, 2},
		{"empty API failure", `{}`, http.StatusBadRequest, 2},
		{"missing cursor", `{"object":"page","data":[],"has_more":true}`, http.StatusOK, 2},
		{"null cursor", `{"object":"page","data":[],"has_more":true,"next_page":null}`, http.StatusOK, 2},
		{"empty cursor", costReportPage("", "", true), http.StatusOK, 2},
		{"repeated cursor", costReportPage("", "page-two", true), http.StatusOK, 2},
		{"malformed page", `{"object":"page","data":`, http.StatusOK, 2},
		{"missing result object", costReportPage(`{"project_id":"proj_bad","amount":{"value":99,"currency":"usd"}}`, "", false), http.StatusOK, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := costReportServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") == "" {
					_, _ = io.WriteString(w, costReportPage(`{"object":"organization.costs.result","project_id":"proj_first_page","amount":{"value":1,"currency":"usd"}}`, "page-two", true))
					return
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.second)
			})
			got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", "json")...)
			require.NotZero(t, got.code, "%+v", got)
			require.Empty(t, got.stdout, "a failed report must not expose incomplete totals")
			require.True(t, json.Valid([]byte(got.stderr)), "structured failure must remain valid JSON: %q", got.stderr)
			require.Equal(t, tc.requests, requests.Load())
		})
	}
}

func TestMainCostReportCursorCycleStopsBeforeRepeatedRequest(t *testing.T) {
	for _, missing := range []bool{false, true} {
		for _, format := range []string{"text", "json"} {
			t.Run(fmt.Sprintf("missing=%t/%s", missing, format), func(t *testing.T) {
				server, requests := costReportServer(t, func(w http.ResponseWriter, r *http.Request) {
					next := "page-two"
					if r.URL.Query().Get("page") == "page-two" {
						next = "page-three"
					}
					if missing {
						next = ""
					}
					_, _ = io.WriteString(w, costReportPage("", next, true))
				})
				got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", format)...)
				require.Equal(t, 1, got.code, "%+v", got)
				require.Empty(t, got.stdout)
				requireCostReportDiagnostic(t, format, got.stderr,
					"cost report pagination stalled: missing or repeated next_page; no report was written")
				wantRequests := int32(3)
				if missing {
					wantRequests = 1
				}
				require.Equal(t, wantRequests, requests.Load(), "invalid pagination must stop before repeating a request")
			})
		}
	}
}

func TestMainCostReportForwardsOpaqueCursorExactly(t *testing.T) {
	const cursor = " \t "
	server, requests := costReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.URL.Query()["page"]; !present {
			_, _ = io.WriteString(w, costReportPage("", cursor, true))
			return
		}
		assert.Equal(t, cursor, r.URL.Query().Get("page"))
		_, _ = io.WriteString(w, costReportPage(`{"object":"organization.costs.result","amount":{"value":3,"currency":"usd"}}`, "", false))
	})
	got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", "json")...)
	require.Zero(t, got.code, "%+v", got)
	require.Empty(t, got.stderr)
	require.Contains(t, got.stdout, `"amount":"3"`)
	require.EqualValues(t, 2, requests.Load())
}

func TestMainCostReportMissingCurrencyDoesNotInventTotal(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, costReportPage(`{"object":"organization.costs.result","project_id":"proj_example","amount":{"value":12.34}}`, "", false))
			})
			got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), costReportArgs("--format", format)...)
			require.Equal(t, 1, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			requireCostReportDiagnostic(t, format, got.stderr,
				"The Costs API returned incomplete or invalid report data. No report was written.")
			require.EqualValues(t, 1, requests.Load())
		})
	}
}

func TestMainCostReportLegacyGeneratedRoute(t *testing.T) {
	const response = `{"object":"page","data":[],"has_more":true,"next_page":"legacy-next","future_field":9007199254740993}`
	for _, route := range [][]string{{"admin:organization:usage", "costs"}, {"admin", "organization", "usage", "costs"}} {
		t.Run(strings.Join(route, "/"), func(t *testing.T) {
			server, requests := costReportServer(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "7", r.URL.Query().Get("start_time"))
				_, _ = io.WriteString(w, response)
			})
			args := append([]string{"openai", "--format", "raw"}, route...)
			got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), append(args, "--start-time", "7")...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.Equal(t, response+"\n", got.stdout)
			require.EqualValues(t, 1, requests.Load(), "the existing API command still returns one raw response")
		})
	}
}

func TestMainCostReportAdminAPIErrorsPreserveFormatsAndLegacyGuidance(t *testing.T) {
	const details = `{"message":"synthetic private organization detail\u001b[2J","type":"permission_error","code":"synthetic_code","param":null,"future_field":{"sequence":9007199254740993}}`
	const requestID = "req_cost_report_test"
	for _, tc := range []struct {
		status                 int
		reportText, legacyText string
	}{
		{
			http.StatusUnauthorized,
			"Cost reports require an organization Admin API key.\nReplace an explicit --admin-api-key value; otherwise set OPENAI_ADMIN_KEY.\nA project API key cannot replace an Admin key.",
			"Check API key, organization and project.\nKey setup: openai help setup",
		},
		{
			http.StatusForbidden,
			"Check your Admin API key's access to organization costs and the selected organization.\nAn explicit --admin-api-key overrides OPENAI_ADMIN_KEY.",
			"Check your key's permissions.\nCheck project access to this resource.",
		},
	} {
		for _, route := range []struct {
			name, message string
			args          []string
		}{
			{"report", tc.reportText, costReportArgs()},
			{"generated", tc.legacyText, []string{"openai", "admin:organization:usage", "costs", "--start-time", costReportStart}},
		} {
			for _, format := range []string{"text", "json", "raw-error"} {
				t.Run(fmt.Sprintf("%d/%s/%s", tc.status, route.name, format), func(t *testing.T) {
					server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("X-Request-ID", requestID)
						w.Header().Set("X-Should-Retry", "false")
						w.WriteHeader(tc.status)
						_, _ = io.WriteString(w, `{"error":`+details+`}`)
					})
					args := append([]string{}, route.args...)
					if format == "raw-error" {
						args = append(args, "--format-error", "raw")
					} else {
						args = append(args, "--format", format)
					}
					got := runMainDispatchWithEnv(t, "bash", costReportEnv(server), args...)
					require.Equal(t, 1, got.code, "%+v", got)
					require.Empty(t, got.stdout)
					require.EqualValues(t, 1, requests.Load())
					switch format {
					case "text":
						want := fmt.Sprintf("HTTP %d: %s.\nRequest ID: %s\n%s\nAPI error details: --format-error json.\n",
							tc.status, http.StatusText(tc.status), requestID, route.message)
						require.Equal(t, want, got.stderr)
						require.NotContains(t, got.stderr, "synthetic private organization detail")
						require.NotContains(t, got.stderr, "\x1b")
					case "json":
						// Use json.Number to preserve unfamiliar large numeric fields.
						require.Equal(t, decodeMainErrorObject(t, "json", details), decodeMainErrorObject(t, "json", got.stderr))
					case "raw-error":
						require.Equal(t, details+"\n", got.stderr)
					}
					require.NotContains(t, got.stderr, "synthetic-cost-report-admin")
					require.NotContains(t, got.stderr, "synthetic-cost-report-project-key")
				})
			}
		}
	}
}

func costReportChild(t *testing.T, ctx context.Context, server *httptest.Server, extra ...string) *exec.Cmd {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	args := append([]string{"-test.run=^TestMainDispatchProcess$", "--"}, costReportArgs(extra...)...)
	child := exec.CommandContext(ctx, binary, args...)
	child.Env = append(costReportEnv(server), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1")
	return child
}

func TestMainCostReportInterruptCancelsSecondPage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal cannot send os.Interrupt on Windows")
	}
	ready, canceled := make(chan struct{}), make(chan struct{})
	server, requests := costReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, costReportPage(`{"object":"organization.costs.result","project_id":"proj_first_page","amount":{"value":1,"currency":"usd"}}`, "page-two", true))
			return
		}
		close(ready)
		<-r.Context().Done()
		close(canceled)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := costReportChild(t, ctx, server, "--format", "json")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	require.NoError(t, child.Start())
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	waited := false
	defer func() {
		cancel()
		if !waited {
			<-done
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		waited = true
		t.Fatalf("report exited before the second page: %v; stderr=%q", err, stderr.String())
	case <-ctx.Done():
		t.Fatal("report did not request the second page")
	}
	require.NoError(t, child.Process.Signal(os.Interrupt))
	err := <-done
	waited = true
	require.NoError(t, ctx.Err(), "report did not finish promptly after SIGINT")
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 130, exit.ExitCode(), "stderr=%q", stderr.String())
	require.Empty(t, stdout.String(), "cancellation must not expose incomplete totals")
	require.True(t, json.Valid(stderr.Bytes()), "stderr=%q", stderr.String())
	require.EqualValues(t, 2, requests.Load())
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("SIGINT did not cancel the pending HTTP request")
	}
}

func TestMainCostReportOutputFailureReturnsNonzero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this check passes a Unix read-only descriptor as stdout")
	}
	server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, costReportPage(`{"object":"organization.costs.result","project_id":"proj_example","amount":{"value":12.34,"currency":"usd"}}`, "", false))
	})
	for _, extra := range [][]string{{"--format", "text"}, {"--format", "json"}, {"--export", "csv"}} {
		t.Run(strings.Join(extra, "/"), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "read-only-output")
			require.NoError(t, os.WriteFile(path, nil, 0o600))
			output, err := os.Open(path)
			require.NoError(t, err)
			defer output.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			child := costReportChild(t, ctx, server, extra...)
			var stderr bytes.Buffer
			child.Stdout, child.Stderr = output, &stderr
			before := requests.Load()
			err = child.Run()
			require.NoError(t, ctx.Err())
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			require.NotZero(t, exit.ExitCode())
			format := "text"
			if extra[1] == "json" {
				format = "json"
			}
			requireCostReportDiagnostic(t, format, stderr.String(),
				"Could not write the cost report. Output may be incomplete; check the output file or pipe.")
			require.Equal(t, before+1, requests.Load())
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Empty(t, data)
		})
	}
}
