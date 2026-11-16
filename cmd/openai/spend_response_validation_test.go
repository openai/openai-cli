package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tidwall/gjson"
)

func TestMainSpendMutationResponseObjects(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, operation := range []struct{ resource, action string }{
			{"spend-limit", "update"}, {"spend-limit", "delete"},
			{"spend-alerts", "create"}, {"spend-alerts", "update"}, {"spend-alerts", "delete"},
			{"rate-limits", "update-rate-limit"},
		} {
			if !project && operation.resource == "rate-limits" {
				continue
			}
			t.Run(fmt.Sprintf("project=%t/%s/%s", project, operation.resource, operation.action), func(t *testing.T) {
				valid := spendThresholdBody(project, operation.resource, `"id":"synthetic","future":{"exact":9007199254740993,"decimal":1.00000000000000001},"unknown":1,"unknown":2}`)
				if operation.action == "delete" {
					valid = strings.Replace(valid, `","id"`, `.deleted","id"`, 1)
				}
				object := gjson.Get(valid, "object").Str
				for _, response := range []struct{ name, body string }{
					{"null", `null`},
					{"wrong_scope", `{"object":"synthetic-private-wrong-scope"}`},
					{"duplicate", fmt.Sprintf(`{"object":%q,"\u006fbject":%q}`, object, object)},
					{"valid", valid},
				} {
					t.Run(response.name, func(t *testing.T) {
						path := spendThresholdPath(project, operation.resource)
						if operation.resource == "spend-alerts" && operation.action != "create" {
							path += "/alert_synthetic"
						} else if operation.resource == "rate-limits" {
							path += "/rl_synthetic"
						}
						method := http.MethodPost
						if operation.action == "delete" {
							method = http.MethodDelete
						}
						var requests atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							requests.Add(1)
							checkSpendRecoveryContext(t, r, "sk-fake-spend-admin")
							if r.Method != method || r.URL.Path != path {
								t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
							}
							w.Header().Set("Content-Type", "application/json")
							io.WriteString(w, response.body)
						}))
						defer server.Close()
						env := spendRecoveryEnvironment(t, server.URL)
						args := append([]string{"openai"}, spendOutputMutationArgs(project, operation.resource, operation.action)...)
						args = append(args, "--format", "json", "--format-error", "text")
						got := runMainDispatchWithEnv(t, "bash", env, args...)
						if response.name == "valid" {
							var compact bytes.Buffer
							if got.code != 0 || got.stderr != "" || json.Compact(&compact, []byte(got.stdout)) != nil || compact.String() != valid {
								t.Fatalf("valid response bytes changed: %+v", got)
							}
						} else {
							checkSpendRecoveryFailure(t, got, "The API may have received the change.", "Check its current state before repeating", "Keep the original API address")
							followSpendRecoveryHelp(t, env, got.stderr, "Read options")
						}
						if requests.Load() != 1 {
							t.Fatal("response validation or recovery repeated the mutation", requests.Load())
						}
					})
				}
			})
		}
	}
}

func TestMainSpendMutationResponseObjectModes(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		checkSpendRecoveryContext(t, r, "sk-fake-spend-admin")
		if r.Method != http.MethodPost || r.URL.Path != "/organization/spend_alerts" {
			t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"future":{"object":"organization.spend_alert"},"private":"synthetic-private-response"}`)
	}))
	defer server.Close()
	env := spendRecoveryEnvironment(t, server.URL)
	for _, mode := range []string{"text", "json", "jsonl", "raw", "yaml", "extraction"} {
		t.Run(mode, func(t *testing.T) {
			args := append([]string{"openai"}, spendOutputMutationArgs(false, "spend-alerts", "create")...)
			args = append(args, "--format-error", "json")
			if mode == "extraction" {
				args = append(args, "--format", "json", "--transform", "future")
			} else {
				args = append(args, "--format", mode)
			}
			before := requests.Load()
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			if got.code != 1 || got.stdout != "" || requests.Load() != before+1 {
				t.Fatalf("response validation changed routing, exit, or request count: %+v", got)
			}
			var diagnostic map[string]string
			if json.Unmarshal([]byte(got.stderr), &diagnostic) != nil || len(diagnostic) != 1 ||
				diagnostic["message"] != "The command could not be completed. Check your arguments with --help." {
				t.Fatalf("machine error schema changed: %q", got.stderr)
			}
		})
	}
}
