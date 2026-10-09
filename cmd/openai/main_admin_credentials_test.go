package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const missingAdminCredentialsMessage = `Admin API key required.
A project API key cannot run organization admin commands.

To enter a key securely and verify access, run:
  openai setup admin

For key creation and manual setup instructions, run:
  openai help setup admin`

func TestMainAdminCredentialsMissing(t *testing.T) {
	for _, test := range []struct {
		name string
		env  []string
		args []string
	}{
		{name: "absent"},
		{name: "normal flag only", args: []string{"--api-key", "synthetic-normal-key"}},
		{name: "normal environment only", env: []string{"OPENAI_API_KEY=synthetic-normal-key"}},
		{name: "empty admin flag", args: []string{"--admin-api-key="}},
		{name: "empty admin flag overrides environment", env: []string{"OPENAI_ADMIN_KEY=synthetic-admin-key"}, args: []string{"--admin-api-key="}},
		{name: "unrelated custom header", args: []string{"-H", "X-Trace: synthetic-trace"}},
		{name: "empty authorization overrides key", env: []string{"OPENAI_ADMIN_KEY=synthetic-admin-key"}, args: []string{"-H", "Authorization:"}},
		{name: "last authorization is empty", args: []string{"-H", "Authorization: Custom synthetic-token", "-H", "authorization:"}},
		{name: "last environment authorization is empty", env: []string{"OPENAI_CUSTOM_HEADERS=Authorization: Custom synthetic-token\nauthorization:"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			t.Cleanup(server.Close)
			args := append([]string{"openai", "--base-url", "https://api.openai.com/v1"}, test.args...)
			args = append(args, "admin", "organization", "projects", "list")
			got := runMainDispatchWithEnv(t, "bash", adminCredentialsProxyEnv(server.URL, test.env), args...)
			if got.code != 1 || got.stdout != "" || strings.TrimSpace(got.stderr) != missingAdminCredentialsMessage {
				t.Errorf("missing admin credentials = %+v, want exit 1 and guidance on stderr", got)
			}
			if count := requests.Load(); count != 0 {
				t.Errorf("missing admin credentials sent %d requests, want 0", count)
			}
		})
	}
}

func TestMainAdminCredentialsErrorFormats(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	for _, test := range []struct {
		name, format string
		args         []string
	}{
		{"text", "text", []string{"--format-error", "text"}},
		{"JSON", "json", []string{"--format-error", "json"}},
		{"JSONL", "jsonl", []string{"--format-error", "jsonl"}},
		{"raw", "raw", []string{"--format-error", "raw"}},
		{"YAML", "yaml", []string{"--format-error", "yaml"}},
		{"inherited JSON", "json", []string{"--format", "json"}},
		{"JSON override", "json", []string{"--format", "text", "--format-error", "json"}},
		{"text override", "text", []string{"--format", "json", "--format-error", "text"}},
		{"extracted JSON", "string", []string{"--format-error", "json", "--transform-error", "message"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"openai", "--base-url", "https://api.openai.com/v1"}, test.args...)
			args = append(args, "admin", "organization", "projects", "list")
			got := runMainDispatchWithEnv(t, "bash", adminCredentialsProxyEnv(server.URL, nil), args...)
			if got.code != 1 || got.stdout != "" {
				t.Fatalf("missing credentials = %+v, want exit 1 and empty stdout", got)
			}
			message := strings.TrimSpace(got.stderr)
			switch test.format {
			case "text":
			case "string":
				if err := json.Unmarshal([]byte(got.stderr), &message); err != nil {
					t.Fatalf("error extraction is not JSON: %v; %q", err, got.stderr)
				}
			default:
				payload := decodeMainStructuredError(t, test.format, got.stderr)
				message, _ = payload["message"].(string)
				if _, exists := payload["status_code"]; exists {
					t.Errorf("local error invented an HTTP status: %#v", payload)
				}
			}
			if message != missingAdminCredentialsMessage {
				t.Errorf("message = %q, want %q", message, missingAdminCredentialsMessage)
			}
		})
	}
	if count := requests.Load(); count != 0 {
		t.Errorf("missing admin credentials sent %d proxy requests, want 0", count)
	}
}

func TestMainAdminCredentialsEffectiveAuthentication(t *testing.T) {
	const response = `{"object":"list","data":[{"id":"proj_synthetic","object":"organization.project","name":"Synthetic project","created_at":1,"status":"active"}],"has_more":false}`
	for _, test := range []struct {
		name, wantAuthorization string
		env, args               []string
		userinfo                bool
	}{
		{name: "admin flag", args: []string{"--admin-api-key", "synthetic-admin-flag"}, wantAuthorization: "Bearer synthetic-admin-flag"},
		{name: "admin environment", env: []string{"OPENAI_ADMIN_KEY=synthetic-admin-env"}, wantAuthorization: "Bearer synthetic-admin-env"},
		{name: "admin flag overrides environment", env: []string{"OPENAI_ADMIN_KEY=synthetic-admin-env", "OPENAI_API_KEY=synthetic-normal-env"}, args: []string{"--admin-api-key", "synthetic-admin-flag", "--api-key", "synthetic-normal-flag"}, wantAuthorization: "Bearer synthetic-admin-flag"},
		{name: "custom authorization flag", args: []string{"-H", "Authorization: Custom synthetic-token"}, wantAuthorization: "Custom synthetic-token"},
		{name: "custom authorization environment", env: []string{"OPENAI_CUSTOM_HEADERS=Authorization: Custom synthetic-env-token"}, wantAuthorization: "Custom synthetic-env-token"},
		{name: "admin environment preserves existing precedence", env: []string{"OPENAI_ADMIN_KEY=synthetic-admin-env", "OPENAI_CUSTOM_HEADERS=Authorization: Custom synthetic-env-token"}, wantAuthorization: "Bearer synthetic-admin-env"},
		{name: "custom authorization with normal API key", env: []string{"OPENAI_API_KEY=synthetic-normal-env", "OPENAI_CUSTOM_HEADERS=Authorization: Custom synthetic-env-token"}, wantAuthorization: "Custom synthetic-env-token"},
		{name: "custom authorization with empty admin flag", env: []string{"OPENAI_ADMIN_KEY=synthetic-admin-env", "OPENAI_CUSTOM_HEADERS=Authorization: Custom synthetic-env-token"}, args: []string{"--admin-api-key="}, wantAuthorization: "Custom synthetic-env-token"},
		{name: "custom authorization overrides admin", env: []string{"OPENAI_ADMIN_KEY=synthetic-admin-env"}, args: []string{"-H", "Authorization: Custom synthetic-token"}, wantAuthorization: "Custom synthetic-token"},
		{name: "custom authorization overrides empty admin", env: []string{"OPENAI_ADMIN_KEY=synthetic-admin-env"}, args: []string{"--admin-api-key=", "-H", "Authorization: Custom synthetic-token"}, wantAuthorization: "Custom synthetic-token"},
		{name: "custom authorization flag overrides environment", env: []string{"OPENAI_CUSTOM_HEADERS=Authorization: Custom synthetic-env-token"}, args: []string{"-H", "authorization: Custom synthetic-flag-token"}, wantAuthorization: "Custom synthetic-flag-token"},
		{name: "last authorization wins", args: []string{"-H", "Authorization:", "-H", "authorization: Custom synthetic-last-token"}, wantAuthorization: "Custom synthetic-last-token"},
		{name: "last environment authorization wins", env: []string{"OPENAI_CUSTOM_HEADERS=Authorization:\nauthorization: Custom synthetic-last-token"}, wantAuthorization: "Custom synthetic-last-token"},
		{name: "URL userinfo", userinfo: true, wantAuthorization: "Basic " + base64.StdEncoding.EncodeToString([]byte("synthetic-user:synthetic-password"))},
		{name: "URL userinfo after empty authorization", userinfo: true, args: []string{"-H", "Authorization:"}, wantAuthorization: "Basic " + base64.StdEncoding.EncodeToString([]byte("synthetic-user:synthetic-password"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/synthetic-proxy/v1/organization/projects" {
					t.Errorf("request = %s %s, want GET /synthetic-proxy/v1/organization/projects", r.Method, r.URL.Path)
				}
				if got := r.Header.Values("Authorization"); len(got) != 1 || got[0] != test.wantAuthorization {
					t.Errorf("effective Authorization = %q, want [%q]", got, test.wantAuthorization)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, response)
			}))
			t.Cleanup(server.Close)
			baseURL := server.URL + "/synthetic-proxy/v1"
			if test.userinfo {
				baseURL = strings.Replace(baseURL, "http://", "http://synthetic-user:synthetic-password@", 1)
			}
			args := append([]string{"openai", "--base-url", baseURL, "--format", "json"}, test.args...)
			args = append(args, "admin", "organization", "projects", "list")
			got := runMainDispatchWithEnv(t, "bash", test.env, args...)
			if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "proj_synthetic") || !json.Valid([]byte(got.stdout)) {
				t.Errorf("supported authentication = %+v, want exit 0 and project JSON", got)
			}
			if count := requests.Load(); count != 1 {
				t.Errorf("supported authentication sent %d requests, want 1", count)
			}
		})
	}
}

func TestMainAdminCredentialsHelpAndCompatibility(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	for _, command := range [][]string{
		{"admin", "organization", "projects", "list"},
		{"admin:organization:projects", "list"},
	} {
		t.Run(strings.Join(command, "/"), func(t *testing.T) {
			args := append([]string{"openai", "--base-url", "https://api.openai.com/v1"}, command...)
			env := adminCredentialsProxyEnv(server.URL, nil)
			help := runMainDispatchWithEnv(t, "bash", env, append(args, "--help")...)
			if help.code != 0 || help.stderr != "" || !strings.Contains(help.stdout, "--limit") {
				t.Errorf("help without credentials = %+v, want exit 0 and command help", help)
			}
			missing := runMainDispatchWithEnv(t, "bash", env, args...)
			if missing.code != 1 || missing.stdout != "" || strings.TrimSpace(missing.stderr) != missingAdminCredentialsMessage {
				t.Errorf("missing credentials = %+v, want exit 1 and guidance", missing)
			}
			positional := runMainDispatchWithEnv(t, "bash", env, append(args, "synthetic-positional-key")...)
			if positional.code != 1 || positional.stdout != "" || !strings.Contains(positional.stderr, "Unexpected extra arguments.") {
				t.Errorf("positional key = %+v, want an argument error", positional)
			}
			if strings.Contains(positional.stderr, "synthetic-positional-key") {
				t.Error("argument error exposed the synthetic positional credential")
			}
		})
	}
	if count := requests.Load(); count != 0 {
		t.Errorf("help, missing credentials, or invalid arguments sent %d requests, want 0", count)
	}
}

// Every standard-service test uses a loopback proxy that cannot forward traffic.
func adminCredentialsProxyEnv(proxyURL string, env []string) []string {
	return append(append([]string{}, env...), "HTTPS_PROXY="+proxyURL, "HTTP_PROXY="+proxyURL, "NO_PROXY=", "no_proxy=")
}

func TestMainAdminCredentialsPreservesRejectedCredentials(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		env    []string
	}{
		{"rejected admin key", http.StatusUnauthorized, []string{"OPENAI_ADMIN_KEY=synthetic-rejected-admin-key"}},
		{"denied admin key", http.StatusForbidden, []string{"OPENAI_ADMIN_KEY=synthetic-rejected-admin-key"}},
		{"custom endpoint without credentials", http.StatusUnauthorized, nil},
		{"custom endpoint denies missing credentials", http.StatusForbidden, nil},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if test.env == nil && r.Header.Get("Authorization") != "" {
						t.Error("custom endpoint received unexpected Authorization")
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("x-should-retry", "false")
					w.WriteHeader(test.status)
					io.WriteString(w, `{"error":{"message":"synthetic rejected credential or permissions","type":"invalid_request_error","code":"synthetic_rejected"}}`)
				}))
				t.Cleanup(server.Close)
				got := runMainDispatchWithEnv(t, "bash", test.env,
					"openai", "--base-url", server.URL, "--format-error", format, "admin", "organization", "projects", "list")
				if got.code != 1 || got.stdout != "" || requests.Load() != 1 {
					t.Fatalf("API error = %+v, requests=%d, want exit 1 and one request", got, requests.Load())
				}
				if strings.Contains(got.stderr, "Admin API key required.") || strings.Contains(got.stderr, "synthetic-rejected-admin-key") {
					t.Errorf("API error was relabeled or exposed credentials: %q", got.stderr)
				}
				if format == "json" {
					payload := decodeMainErrorObject(t, format, got.stderr)
					if payload["message"] != "synthetic rejected credential or permissions" || payload["code"] != "synthetic_rejected" {
						t.Errorf("API error fields changed: %#v", payload)
					}
				} else {
					want := "Authentication failed."
					if test.status == http.StatusForbidden {
						want = "Access denied."
					}
					if !strings.Contains(got.stderr, want) {
						t.Errorf("API error = %q, want %q", got.stderr, want)
					}
				}
			})
		}
	}
}
