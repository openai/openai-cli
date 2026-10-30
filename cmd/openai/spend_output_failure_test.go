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
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A closed local file produces a real output error after the API response.
// Run production main in a child so its command and stdout state stay isolated.
func TestMainSpendOutputFailureProcess(t *testing.T) {
	path := os.Getenv("OPENAI_CLI_SPEND_OUTPUT_FAILURE")
	if path == "" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing subprocess argument separator")
	}
	sink, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = sink
	os.Args = os.Args[separator+1:]
	main()
	os.Exit(0)
}

func runSpendOutputFailure(t *testing.T, env []string, args ...string) mainDispatchResult {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	argv := append([]string{"-test.run=^TestMainSpendOutputFailureProcess$", "--", "openai"}, args...)
	child := exec.CommandContext(ctx, binary, argv...)
	child.Env = append([]string{"PATH=" + os.Getenv("PATH"), "COMPLETION_STYLE=bash", "GOMAXPROCS=2"}, env...)
	if root, ok := os.LookupEnv("SystemRoot"); ok {
		child.Env = append(child.Env, "SystemRoot="+root)
	}
	child.Env = append(child.Env, "OPENAI_CLI_SPEND_OUTPUT_FAILURE="+t.TempDir()+"/synthetic-private-output")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	err = child.Run()
	if ctx.Err() != nil {
		t.Fatalf("output failure child timed out: %v; stderr=%q", ctx.Err(), stderr.String())
	}
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return mainDispatchResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func spendOutputMutationArgs(project bool, resource, action string) []string {
	args := spendThresholdArgs(project, resource, action)
	if resource == "rate-limits" {
		return append(args, "--rate-limit-id", "rl_synthetic", "--max-requests-per-1-minute", "300")
	}
	if action != "delete" {
		args = append(args, "--currency", "USD", "--interval", "month", "--threshold-amount", "10000")
		if resource == "spend-alerts" {
			args = append(args, "--notification-channel", `{"type":"email","recipients":["synthetic@example.invalid"]}`)
		}
	}
	return args
}

func TestMainSpendMutationOutputFailure(t *testing.T) {
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
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					if method == http.MethodDelete {
						if len(body) != 0 {
							t.Errorf("delete acquired a body: %s", body)
						}
					} else {
						var fields map[string]any
						if json.Unmarshal(body, &fields) != nil {
							t.Errorf("invalid mutation body: %s", body)
						} else if operation.resource == "rate-limits" {
							if len(fields) != 1 || fields["max_requests_per_1_minute"] != float64(300) {
								t.Errorf("rate mutation fields changed: %s", body)
							}
						} else if fields["threshold_amount"] != float64(10000) || fields["currency"] != "USD" || fields["interval"] != "month" {
							t.Errorf("spend mutation fields changed: %s", body)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					response := spendThresholdBody(project, operation.resource, `"id":"alert_synthetic","currency":"USD","interval":"month","threshold_amount":10000}`)
					if operation.action == "delete" {
						response = strings.Replace(response, `","id"`, `.deleted","id"`, 1)
						response = strings.TrimSuffix(response, "}") + `,"deleted":true}`
					} else if operation.resource == "rate-limits" {
						response = `{"object":"project.rate_limit","id":"rl_synthetic","model":"synthetic-model","max_requests_per_1_minute":300}`
					}
					io.WriteString(w, response)
				}))
				defer server.Close()
				env := spendRecoveryEnvironment(t, server.URL)
				for _, format := range []string{"text", "json"} {
					before := requests.Load()
					args := append(spendOutputMutationArgs(project, operation.resource, operation.action), "--format", format, "--format-error", "text")
					got := runSpendOutputFailure(t, env, args...)
					checkSpendRecoveryFailure(t, got, "The API returned a response, but the CLI could not display it.", "The change may have been applied.", "Check its current state before repeating", "Keep the original API address")
					if strings.Contains(got.stderr, "confirmed") || strings.Contains(got.stderr, "saved successfully") {
						t.Fatal("output failure claimed persistence", got.stderr)
					}
					followSpendRecoveryHelp(t, env, got.stderr, "Read options")
					if requests.Load() != before+1 {
						t.Fatal("presentation or recovery repeated the mutation", requests.Load()-before)
					}
				}
			})
		}
	}
}

func TestMainSpendMutationOutputFailureMachineModes(t *testing.T) {
	var requests atomic.Int32
	const response = `{"object":"organization.spend_alert","id":"alert_synthetic","threshold_amount":9007199254740993,"currency":"USD","interval":"month","future":{"exact":1.00000000000000001}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		checkSpendRecoveryContext(t, r, "sk-fake-spend-admin")
		if r.Method != http.MethodPost || r.URL.Path != "/organization/spend_alerts" {
			t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, response)
	}))
	defer server.Close()
	env := spendRecoveryEnvironment(t, server.URL)
	const baseline = "A local file could not be read or written. Check your file arguments and permissions."
	for _, flags := range [][]string{
		{"--format", "json"},
		{"--format", "text", "--format-error", "json"},
		{"--format", "json", "--format-error", "jsonl"},
		{"--format", "raw", "--transform", "id", "--format-error", "text", "--transform-error", "message"},
		{"--format", "json", "--transform", "threshold_amount", "--raw-output", "--format-error", "text"},
	} {
		before := requests.Load()
		got := runSpendOutputFailure(t, env, append(spendOutputMutationArgs(false, "spend-alerts", "create"), flags...)...)
		checkSpendRecoveryFailure(t, got)
		if slices.Contains(flags, "threshold_amount") {
			checkSpendRecoveryFailure(t, got, "The change may have been applied.", "Check its current state before repeating")
		} else if slices.Contains(flags, "--transform-error") {
			if got.stderr != baseline+"\n" {
				t.Fatal("error extraction changed", got.stderr)
			}
		} else {
			var fields map[string]string
			if json.Unmarshal([]byte(got.stderr), &fields) != nil || len(fields) != 1 || fields["message"] != baseline {
				t.Fatal("machine error schema or fallback changed", got.stderr)
			}
		}
		if requests.Load() != before+1 {
			t.Fatal("machine error presentation repeated the mutation")
		}
	}
	for _, flags := range [][]string{{"--format", "json"}, {"--format", "raw"}, {"--format", "json", "--transform", "threshold_amount"}} {
		before := requests.Load()
		args := append([]string{"openai"}, spendOutputMutationArgs(false, "spend-alerts", "create")...)
		got := runMainDispatchWithEnv(t, "bash", env, append(args, flags...)...)
		if got.code != 0 || got.stderr != "" || requests.Load() != before+1 {
			t.Fatalf("successful output changed: %+v", got)
		}
		want := response
		if slices.Contains(flags, "--transform") {
			want = "9007199254740993"
		}
		assertSpendThresholdJSON(t, got.stdout, want)
	}
}

func TestMainSpendMutationOutputFailureControls(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"organization.spend_limit","currency":"USD","interval":"month","threshold_amount":10000}`)
	}))
	defer server.Close()
	env := spendRecoveryEnvironment(t, server.URL)
	got := runSpendOutputFailure(t, env, "admin", "organization", "spend-limit", "retrieve", "--format", "text")
	checkSpendRecoveryFailure(t, got, "A local file could not be read or written.")
	if requests.Load() != 1 || strings.Contains(got.stderr, "may have been applied") || strings.Contains(got.stderr, "before repeating") {
		t.Fatal("read output failure acquired mutation guidance", got)
	}
	args := spendOutputMutationArgs(false, "spend-alerts", "create")
	args[len(args)-1] = `[synthetic-private-input not closed`
	got = runSpendOutputFailure(t, env, append(args, "--format-error", "text")...)
	checkSpendRecoveryFailure(t, got)
	if count := requests.Load(); count != 1 {
		t.Fatalf("input rejection sent a request: got %d total requests, want only the earlier read", count)
	}
	if got.stderr == "" || strings.Contains(got.stderr, "may have been applied") || strings.Contains(got.stderr, "before repeating") {
		t.Fatal("input rejection acquired response provenance", got)
	}
}
