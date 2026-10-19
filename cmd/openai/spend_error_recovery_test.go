package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func spendRecoveryEnvironment(t *testing.T, endpoint string) []string {
	t.Helper()
	home := t.TempDir()
	return []string{
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home,
		"OPENAI_BASE_URL=" + endpoint, "OPENAI_API_KEY=sk-fake-spend-project",
		"OPENAI_ADMIN_KEY=sk-fake-spend-admin", "OPENAI_ORG_ID=org_synthetic_recovery",
		"OPENAI_PROJECT_ID=proj_synthetic_context", "OPENAI_CUSTOM_HEADERS=X-Spend-Recovery: retained",
		"NO_COLOR=1", "FORCE_COLOR=0", "CI=1",
	}
}

func spendRecoverySetEnvironment(env []string, key, value string) []string {
	result := slices.Clone(env)
	for i, entry := range result {
		if strings.HasPrefix(entry, key+"=") {
			result[i] = key + "=" + value
			return result
		}
	}
	return append(result, key+"="+value)
}

func checkSpendRecoveryContext(t *testing.T, r *http.Request, adminKey string) {
	t.Helper()
	if r.Header.Get("Authorization") != "Bearer "+adminKey ||
		r.Header.Get("OpenAI-Organization") != "org_synthetic_recovery" ||
		r.Header.Get("OpenAI-Project") != "proj_synthetic_context" ||
		r.Header.Get("X-Spend-Recovery") != "retained" {
		t.Error("recovery changed the original synthetic request context")
	}
}

func writeSpendRecoveryFailure(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Should-Retry", "false")
	w.Header().Set("X-Request-Id", "req_synthetic_recovery")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%s}`, spendRecoveryDetails(code))
}

func spendRecoveryDetails(code string) string {
	return `{"message":"synthetic-private-response\u001b]52;c;synthetic-private\u0007","type":"invalid_request_error","code":"` + code + `","param":null,"future":{"sequence":9007199254740993}}`
}

func checkSpendRecoveryFailure(t *testing.T, got mainDispatchResult, wants ...string) {
	t.Helper()
	if got.code != 1 || got.stdout != "" {
		t.Fatalf("failure changed exit or output routing: %+v", got)
	}
	for _, want := range wants {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("missing recovery instruction %q: %q", want, got.stderr)
		}
	}
	for _, private := range []string{"synthetic-private", "sk-fake-", "\x1b", "\a"} {
		if strings.Contains(got.stderr, private) {
			t.Errorf("readable recovery exposed synthetic private data: %q", got.stderr)
		}
	}
}

// These fixtures invoke the public entrypoint with the literal openai identity.
// Follow its displayed help, rather than maintaining a second recovery command.
func followSpendRecoveryHelp(t *testing.T, env []string, diagnostic, label string) mainDispatchResult {
	t.Helper()
	for _, line := range strings.Split(diagnostic, "\n") {
		command, ok := strings.CutPrefix(line, label+": ")
		if !ok {
			continue
		}
		if !strings.HasPrefix(command, "openai help ") {
			t.Fatalf("unexpected recovery command: %q", command)
		}
		got := runMainDispatchWithEnv(t, "bash", env, strings.Fields(command)...)
		if got.code != 0 || got.stderr != "" || got.stdout == "" {
			t.Fatalf("displayed recovery help failed: %+v", got)
		}
		return got
	}
	t.Fatalf("missing %s recovery line: %q", label, diagnostic)
	return mainDispatchResult{}
}

func TestMainSpendErrorRecoveryAdminSetup(t *testing.T) {
	for _, args := range [][]string{
		{"admin", "organization", "spend-limit", "retrieve"},
		{"admin", "spend-limit", "retrieve"},
		{"admin:organization:spend-limit", "retrieve"},
		{"projects", "spend-limit", "retrieve", "--project-id", "proj_synthetic"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var requests atomic.Int32
			project := args[0] == "projects"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != spendThresholdPath(project, "spend-limit") {
					t.Error("unexpected authentication recovery request")
				}
				if attempt == 1 {
					checkSpendRecoveryContext(t, r, "sk-fake-rejected-admin")
					writeSpendRecoveryFailure(w, http.StatusUnauthorized, "invalid_api_key")
					return
				}
				checkSpendRecoveryContext(t, r, "sk-fake-spend-admin")
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, spendThresholdBody(project, "spend-limit", `{"threshold_amount":10000,"currency":"USD","interval":"month"}`))
			}))
			defer server.Close()
			env := spendRecoveryEnvironment(t, server.URL)
			env = spendRecoverySetEnvironment(env, "OPENAI_ADMIN_KEY", "sk-fake-rejected-admin")
			argv := append([]string{"openai"}, args...)
			got := runMainDispatchWithEnv(t, "bash", env, argv...)
			checkSpendRecoveryFailure(t, got, "HTTP 401", "OPENAI_ADMIN_KEY", "help setup admin")
			help := followSpendRecoveryHelp(t, env, got.stderr, "Key setup")
			if !strings.Contains(help.stdout, "organization/admin-keys") || !strings.Contains(help.stdout, "OPENAI_ADMIN_KEY") || requests.Load() != 1 {
				t.Fatal("admin setup recovery omitted admin instructions or performed a request")
			}
			env = spendRecoverySetEnvironment(env, "OPENAI_ADMIN_KEY", "sk-fake-spend-admin")
			recovered := runMainDispatchWithEnv(t, "bash", env, argv...)
			if recovered.code != 0 || recovered.stderr != "" || requests.Load() != 2 {
				t.Fatalf("following admin setup did not recover the fixture: %+v", recovered)
			}
		})
	}
}

func TestMainSpendErrorRecoveryNotFound(t *testing.T) {
	for _, test := range []struct {
		name, resource          string
		project, missingProject bool
	}{
		{"organization limit", "spend-limit", false, false},
		{"project missing", "spend-limit", true, true},
		{"project limit missing", "spend-limit", true, false},
		{"organization alert", "spend-alerts", false, false},
		{"project alert", "spend-alerts", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests, writes atomic.Int32
			var configured atomic.Bool
			path := spendThresholdPath(test.project, test.resource)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				checkSpendRecoveryContext(t, r, "sk-fake-spend-admin")
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == path && test.name == "organization limit":
					writes.Add(1)
					configured.Store(true)
					io.WriteString(w, spendThresholdBody(false, "spend-limit", `{"threshold_amount":10000,"currency":"USD","interval":"month"}`))
				case r.Method == http.MethodGet && r.URL.Path == path && test.resource == "spend-alerts":
					alert := spendThresholdBody(test.project, "spend-alerts", `{"id":"alert_recovered","threshold_amount":10000,"currency":"USD","interval":"month"}`)
					fmt.Fprintf(w, `{"object":"list","data":[%s],"has_more":false}`, alert)
				case r.Method == http.MethodGet && r.URL.Path == "/organization/projects/proj_synthetic":
					if test.missingProject {
						writeSpendRecoveryFailure(w, 404, "not_found")
					} else {
						io.WriteString(w, `{"id":"proj_synthetic","object":"organization.project","name":"Synthetic project","status":"active"}`)
					}
				case r.Method == http.MethodGet && r.URL.Path == path && configured.Load():
					io.WriteString(w, spendThresholdBody(false, "spend-limit", `{"threshold_amount":10000,"currency":"USD","interval":"month"}`))
				default:
					writeSpendRecoveryFailure(w, 404, "not_found")
				}
			}))
			defer server.Close()
			env := spendRecoveryEnvironment(t, server.URL)
			args := spendThresholdArgs(test.project, test.resource, "retrieve")
			got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, args...)...)
			checkSpendRecoveryFailure(t, got, "HTTP 404", "Keep the original API address")
			label := "Limit options"
			if test.project && test.resource == "spend-limit" {
				label = "Project lookup"
				if !strings.Contains(got.stderr, "If the project exists") {
					t.Error("project404 lost missing-project versus missing-limit uncertainty")
				}
			} else if test.resource == "spend-alerts" {
				label = "List options"
			} else if !strings.Contains(got.stderr, "may not be configured") {
				t.Error("organization404 falsely classified an unknown server state")
			}
			followSpendRecoveryHelp(t, env, got.stderr, label)
			if requests.Load() != 1 || writes.Load() != 0 {
				t.Fatal("recovery help performed a request")
			}
			var read []string
			if test.resource == "spend-alerts" {
				read = spendThresholdArgs(test.project, test.resource, "list")
			} else if test.project {
				read = []string{"admin", "organization", "projects", "retrieve", "--project-id", "proj_synthetic"}
			} else {
				update := []string{"openai", "admin", "organization", "spend-limit", "update", "--currency", "USD", "--interval", "month", "--threshold-amount", "10000"}
				if saved := runMainDispatchWithEnv(t, "bash", env, update...); saved.code != 0 {
					t.Fatalf("documented limit configuration failed: %+v", saved)
				}
				read = args
			}
			recovered := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, read...)...)
			wantExit := 0
			if test.missingProject {
				wantExit = 1
			}
			if recovered.code != wantExit {
				t.Fatalf("documented recovery did not identify the fixture state: %+v", recovered)
			}
			if test.resource == "spend-alerts" && !strings.Contains(recovered.stdout, "alert_recovered") {
				t.Error("alert recovery did not expose the available alert ID")
			}
		})
	}
}

func TestMainSpendErrorRecoveryOrganizationCap(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Error("cap recovery attempted a mutation")
		}
		if r.URL.Path == "/models/synthetic-model" {
			checkSpendRecoveryContext(t, r, "sk-fake-spend-project")
			writeSpendRecoveryFailure(w, 429, "organization_spend_limit_exceeded")
			return
		}
		if r.URL.Path != "/organization/spend_limit" {
			t.Error("cap recovery selected the wrong scope")
		}
		checkSpendRecoveryContext(t, r, "sk-fake-spend-admin")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, spendThresholdBody(false, "spend-limit", `{"threshold_amount":10000,"currency":"USD","interval":"month"}`))
	}))
	defer server.Close()
	env := spendRecoverySetEnvironment(spendRecoveryEnvironment(t, server.URL), "OPENAI_ADMIN_KEY", "")
	got := runMainDispatchWithEnv(t, "bash", env, "openai", "models", "retrieve", "--model", "synthetic-model")
	checkSpendRecoveryFailure(t, got, "HTTP 429", "organization spend limit", "admin API key", "Keep the original API address")
	if strings.Contains(got.stderr, "project's billing") || strings.Contains(got.stderr, "Wait before trying") {
		t.Error("organization cap used project-billing or rate-throttling guidance")
	}
	followSpendRecoveryHelp(t, env, got.stderr, "Limit lookup")
	followSpendRecoveryHelp(t, env, got.stderr, "Admin key setup")
	if requests.Load() != 1 {
		t.Fatal("cap recovery help performed a request")
	}
	env = spendRecoverySetEnvironment(env, "OPENAI_ADMIN_KEY", "sk-fake-spend-admin")
	read := runMainDispatchWithEnv(t, "bash", env, "openai", "admin", "organization", "spend-limit", "retrieve")
	if read.code != 0 || read.stderr != "" || requests.Load() != 2 {
		t.Fatalf("cap recovery failed with retained context and an admin key: %+v", read)
	}
}

func TestMainSpendErrorRecoveryMachineContracts(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
	}{
		{401, "invalid_api_key"}, {404, "not_found"}, {429, "organization_spend_limit_exceeded"},
	} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeSpendRecoveryFailure(w, test.status, test.code)
			}))
			defer server.Close()
			env := spendRecoveryEnvironment(t, server.URL)
			args := []string{"admin", "organization", "spend-limit", "retrieve"}
			for _, flag := range []string{"--format", "--format-error"} {
				for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
					argv := append([]string{"openai", flag, format}, args...)
					got := runMainDispatchWithEnv(t, "bash", env, argv...)
					if got.code != 1 || got.stdout != "" {
						t.Fatalf("machine error changed exit or output routing: %+v", got)
					}
					if format == "yaml" {
						want := "message: \"synthetic-private-response\\x1B]52;c;synthetic-private\\x07\"\n" +
							"type: invalid_request_error\ncode: " + test.code + "\nparam: null\nfuture:\n  sequence: 9007199254740993\n"
						if got.stderr != want {
							t.Errorf("YAML error bytes changed: %q", got.stderr)
						}
					} else {
						assertSpendThresholdJSON(t, got.stderr, spendRecoveryDetails(test.code))
						if (format == "raw" || format == "jsonl") && got.stderr != spendRecoveryDetails(test.code)+"\n" {
							t.Errorf("%s error bytes changed", format)
						}
					}
				}
			}
			got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai", "--format-error", "text", "--transform-error", "future.sequence"}, args...)...)
			if got.code != 1 || got.stdout != "" || got.stderr != "9007199254740993\n" {
				t.Fatalf("error extraction changed: %+v", got)
			}
		})
	}
}

func TestMainSpendErrorRecoveryLostAndTruncatedResponse(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		t.Run(fmt.Sprintf("truncated=%t", truncated), func(t *testing.T) {
			var writes, reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				checkSpendRecoveryContext(t, r, "sk-fake-spend-admin")
				if r.URL.Path != "/organization/spend_limit" {
					t.Error("unexpected mutation recovery route")
				}
				if r.Method == http.MethodGet {
					reads.Add(1)
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, spendThresholdBody(false, "spend-limit", `{"threshold_amount":10000,"currency":"USD","interval":"month"}`))
					return
				}
				if r.Method != http.MethodPost {
					t.Error("unexpected mutation method")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["threshold_amount"] != float64(10000) {
					t.Error("mutation body changed")
				}
				writes.Add(1)
				connection, buffer, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer connection.Close()
				if truncated {
					io.WriteString(buffer, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 1000\r\nConnection: close\r\n\r\n{\"object\":\"organization.spend_limit\"")
					buffer.Flush()
				}
			}))
			defer server.Close()
			env := spendRecoveryEnvironment(t, server.URL)
			mutation := []string{"admin", "organization", "spend-limit", "update", "--currency", "USD", "--interval", "month", "--threshold-amount", "10000"}
			got := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, mutation...)...)
			checkSpendRecoveryFailure(t, got, "The API may have received the change", "before repeating the command")
			wantAttempts := int32(3)
			if truncated {
				wantAttempts = 1
			}
			if writes.Load() != wantAttempts {
				t.Errorf("request retry behavior changed: got %d attempts, want %d", writes.Load(), wantAttempts)
			}
			followSpendRecoveryHelp(t, env, got.stderr, "Read options")
			if writes.Load() != wantAttempts || reads.Load() != 0 {
				t.Fatal("response-loss recovery help performed a request")
			}
			read := runMainDispatchWithEnv(t, "bash", env, "openai", "admin", "organization", "spend-limit", "retrieve")
			if read.code != 0 || read.stderr != "" || writes.Load() != wantAttempts || reads.Load() != 1 {
				t.Fatalf("recovery did not inspect state without another mutation: %+v", read)
			}
			baseline := "Could not connect to the API. Check your connection, proxy, and --base-url setting."
			if truncated {
				baseline = "The command could not be completed. Check your arguments with --help."
			}
			for _, flags := range [][]string{{"--format-error", "json"}, {"--format-error", "text", "--transform-error", "message"}} {
				argv := append(append([]string{"openai"}, flags...), mutation...)
				machine := runMainDispatchWithEnv(t, "bash", env, argv...)
				if machine.code != 1 || machine.stdout != "" {
					t.Fatalf("machine transport error changed exit or output routing: %+v", machine)
				}
				if flags[1] == "json" {
					var value map[string]string
					if err := json.Unmarshal([]byte(machine.stderr), &value); err != nil || len(value) != 1 || value["message"] != baseline {
						t.Errorf("machine transport fallback changed: %q", machine.stderr)
					}
				} else if machine.stderr != baseline+"\n" {
					t.Errorf("transport extraction changed: %q", machine.stderr)
				}
			}
		})
	}
}
