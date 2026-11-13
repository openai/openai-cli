package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestMainLimitsListScopeFailuresPreservePartialOutput(t *testing.T) {
	for _, scope := range []struct {
		name, route, field, object, other string
		project                           bool
	}{
		{"organization_alerts", "spend-alerts", "spend_alerts", "organization.spend_alert", "project.spend_alert", false},
		{"project_alerts", "spend-alerts", "spend_alerts", "project.spend_alert", "organization.spend_alert", true},
		{"project_rates", "rate-limits", "project_rate_limits", "project.rate_limit", "project.spend_alert", true},
	} {
		for _, invalid := range []string{"wrong_scope", "duplicate_object", "wrong_wrapper"} {
			for _, mode := range []string{"text", "json", "raw", "extraction"} {
				t.Run(scope.name+"/"+invalid+"/"+mode, func(t *testing.T) {
					_, record := limitsWorkflowRecords(scope.project)
					if scope.route == "rate-limits" {
						record = limitsWorkflowRate
					}
					first := strings.Replace(record, gjson.Get(record, "id").Str, "first_valid", 1)
					second := strings.Replace(record, gjson.Get(record, "id").Str, "second_valid", 1)
					wanted := []string{first, second}
					if invalid == "wrong_wrapper" {
						wanted = wanted[:1]
					}
					bad := fmt.Sprintf(`{"object":%q,"id":"rejected_synthetic","private":"synthetic-private-scope"}`, scope.other)
					if invalid == "duplicate_object" {
						bad = fmt.Sprintf(`{"object":%q,"object":%q,"id":"rejected_synthetic","private":"synthetic-private-scope"}`, scope.object, scope.object)
					}
					path := spendThresholdPath(scope.project, scope.route)
					server, requests := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
						if r.URL.Path != path {
							return limitsWorkflowSuccess(t, scope.project, r)
						}
						if r.URL.Query().Get("after") == "" {
							return http.StatusOK, limitsWorkflowPage(first, true, "first_valid")
						}
						if r.URL.Query().Get("after") != "first_valid" {
							t.Error("overview changed the list cursor")
						}
						if invalid == "wrong_wrapper" {
							return http.StatusOK, strings.Replace(limitsWorkflowPage(second, false, "second_valid"), `"object":"list"`, `"object":"synthetic-private-wrapper"`, 1)
						}
						return http.StatusOK, limitsWorkflowPage(second+","+bad, false, "rejected_synthetic")
					})
					args := []string{"--format-error", "json", "--format", mode}
					if mode == "extraction" {
						args = []string{"--format-error", "json", "--format", "json", "--transform", scope.field}
					}
					args = append(args, limitsWorkflowArgs(scope.project, true)...)
					got := runSpendThresholdCommand(t, server, args...)
					if got.code != 1 || requests.count(path) != 2 {
						t.Fatalf("invalid list did not retain failure: exit=%d reads=%d", got.code, requests.count(path))
					}
					var diagnostic map[string]string
					if err := json.Unmarshal([]byte(got.stderr), &diagnostic); err != nil || len(diagnostic) != 1 ||
						!strings.Contains(diagnostic["message"], "overview is incomplete") || !strings.Contains(diagnostic["message"], "help") {
						t.Fatalf("missing safe structured recovery: %q", got.stderr)
					}
					forbiddenValues := []string{"rejected_synthetic", "synthetic-private-scope", "synthetic-private-wrapper", "sk-fake-", "\x1b"}
					if invalid == "wrong_wrapper" {
						forbiddenValues = append(forbiddenValues, "second_valid")
					}
					for _, forbidden := range forbiddenValues {
						if strings.Contains(got.stdout+got.stderr, forbidden) {
							t.Fatalf("rejected record or private data reached output: %q", forbidden)
						}
					}
					if mode == "text" {
						label := "Spend alerts complete: false"
						if scope.route == "rate-limits" {
							label = "Project rate limits complete: false"
						}
						values := []string{"first_valid", label, "request_failed", "123456789012345678901234567890"}
						if invalid != "wrong_wrapper" {
							values = append(values, "second_valid")
						}
						for _, value := range values {
							if !strings.Contains(got.stdout, value) {
								t.Errorf("partial readable output lost %q", value)
							}
						}
						return
					}
					if !json.Valid([]byte(got.stdout)) {
						t.Fatal("partial output is not valid JSON")
					}
					value := gjson.Parse(got.stdout)
					rows := value.Get(scope.field)
					if mode == "extraction" {
						rows = value
					} else if value.Get(scope.field+"_complete").Raw != "false" || value.Get("source_errors.#").Int() != 1 ||
						value.Get("source_errors.0.resource").Str != scope.field || value.Get("source_errors.0.status").Str != "request_failed" {
						t.Fatalf("partial overview lost its exact source state: %s", got.stdout)
					}
					assertSpendThresholdJSON(t, rows.Raw, "["+strings.Join(wanted, ",")+"]")
				})
			}
		}
	}
}
