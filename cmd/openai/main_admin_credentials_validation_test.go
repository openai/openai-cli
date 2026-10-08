package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainAdminCredentialsLocalValidation(t *testing.T) {
	for _, route := range [][]string{
		{"admin", "organization", "projects"},
		{"admin:organization:projects"},
	} {
		for _, test := range []struct {
			name, stdin, want string
			args              []string
		}{
			{"missing path", "", "Missing required options: --project-id. Check --help for usage.", []string{"retrieve"}},
			{"missing body", "", "Missing required options: --name. Check --help for usage.", []string{"create"}},
			{"malformed JSON", `{"project_id":`, "Could not parse piped input as YAML or JSON. Check the input's syntax.", []string{"retrieve"}},
			{"extra positional value", "", "Unexpected extra arguments. Check the command's accepted arguments with --help.", []string{"retrieve", "proj_synthetic", "synthetic-extra-value"}},
		} {
			for _, format := range []string{"text", "json"} {
				t.Run(strings.Join(route, "/")+"/"+test.name+"/"+format, func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						requests.Add(1)
						w.WriteHeader(http.StatusForbidden)
					}))
					t.Cleanup(server.Close)
					args := append([]string{"openai", "--base-url", "https://api.openai.com/v1", "--format-error", format}, route...)
					args = append(args, test.args...)
					got := runMainDispatchWithStdin(t, "bash", adminCredentialsProxyEnv(server.URL, nil),
						adminCredentialsValidationInput(t, test.stdin), args...)
					if got.code != 1 || got.stdout != "" {
						t.Fatalf("local validation = %+v, want exit 1 and empty stdout", got)
					}
					message := strings.TrimSpace(got.stderr)
					if format == "json" {
						payload := decodeMainStructuredError(t, format, got.stderr)
						message, _ = payload["message"].(string)
						if _, exists := payload["status_code"]; exists {
							t.Error("local validation invented an HTTP status")
						}
					}
					if message != test.want {
						t.Errorf("diagnostic = %q, want %q", message, test.want)
					}
					if count := requests.Load(); count != 0 {
						t.Errorf("invalid input sent %d proxy requests, want 0", count)
					}
				})
			}
		}
	}
}

func TestMainAdminCredentialsValidatedInputs(t *testing.T) {
	for _, route := range [][]string{
		{"admin", "organization", "projects"},
		{"admin:organization:projects"},
	} {
		for _, test := range []struct {
			name, stdin string
			args        []string
		}{
			{"path flag", "", []string{"retrieve", "--project-id", "proj_synthetic"}},
			{"positional path", "", []string{"retrieve", "proj_synthetic"}},
			{"piped path", `{"project_id":"proj_synthetic"}`, []string{"retrieve"}},
			{"body flag", "", []string{"create", "--name", "Synthetic project"}},
			{"piped body", `{"name":"Synthetic project"}`, []string{"create"}},
		} {
			t.Run(strings.Join(route, "/")+"/"+test.name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(http.StatusForbidden)
				}))
				t.Cleanup(server.Close)
				args := append([]string{"openai", "--base-url", "https://api.openai.com/v1"}, route...)
				args = append(args, test.args...)
				env := adminCredentialsProxyEnv(server.URL, []string{"OPENAI_API_KEY=synthetic-project-key"})
				got := runMainDispatchWithStdin(t, "bash", env, adminCredentialsValidationInput(t, test.stdin), args...)
				if got.code != 1 || got.stdout != "" || strings.TrimSpace(got.stderr) != missingAdminCredentialsMessage {
					t.Errorf("validated input without an admin key = %+v, want exit 1 and guidance", got)
				}
				if count := requests.Load(); count != 0 {
					t.Errorf("missing credentials sent %d proxy requests, want 0", count)
				}
			})
		}
	}
}

func TestMainAdminCredentialsCheckpointPermissions(t *testing.T) {
	for _, route := range [][]string{
		{"fine-tuning", "checkpoints", "permissions"},
		{"fine-tuning:checkpoints:permissions"},
	} {
		for _, operation := range [][]string{
			{"create", "--fine-tuned-model-checkpoint", "ft_synthetic", "--project-id", "proj_synthetic"},
			{"retrieve", "--fine-tuned-model-checkpoint", "ft_synthetic"},
			{"list", "--fine-tuned-model-checkpoint", "ft_synthetic"},
			{"delete", "--fine-tuned-model-checkpoint", "ft_synthetic", "--permission-id", "perm_synthetic"},
		} {
			t.Run(strings.Join(route, "/")+"/"+operation[0], func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(http.StatusForbidden)
				}))
				t.Cleanup(server.Close)
				args := append([]string{"openai", "--base-url", "https://api.openai.com/v1"}, route...)
				args = append(args, operation...)
				env := adminCredentialsProxyEnv(server.URL, []string{"OPENAI_API_KEY=synthetic-project-key"})
				got := runMainDispatchWithEnv(t, "bash", env, args...)
				if got.code != 1 || got.stdout != "" || strings.TrimSpace(got.stderr) != missingAdminCredentialsMessage {
					t.Errorf("checkpoint permissions without an admin key = %+v, want exit 1 and guidance", got)
				}
				if count := requests.Load(); count != 0 {
					t.Errorf("missing credentials sent %d proxy requests, want 0", count)
				}
			})
		}
	}
}

func TestMainAdminCredentialsCheckpointAuthentication(t *testing.T) {
	const listResponse = `{"object":"list","data":[{"id":"perm_synthetic","object":"checkpoint.permission","created_at":1,"project_id":"proj_synthetic"}],"has_more":false}`
	for _, route := range [][]string{
		{"fine-tuning", "checkpoints", "permissions"},
		{"fine-tuning:checkpoints:permissions"},
	} {
		for _, test := range []struct {
			name, method, suffix, authorization, response string
			env, args, operation                          []string
		}{
			{
				name: "explicit admin key overrides environment", method: http.MethodPost,
				authorization: "Bearer synthetic-admin-flag", response: listResponse,
				env:       []string{"OPENAI_ADMIN_KEY=synthetic-admin-env", "OPENAI_API_KEY=synthetic-project-key"},
				args:      []string{"--admin-api-key", "synthetic-admin-flag"},
				operation: []string{"create", "--fine-tuned-model-checkpoint", "ft_synthetic", "--project-id", "proj_synthetic"},
			},
			{
				name: "admin environment", method: http.MethodGet,
				authorization: "Bearer synthetic-admin-env", response: listResponse,
				env:       []string{"OPENAI_ADMIN_KEY=synthetic-admin-env"},
				operation: []string{"list", "--fine-tuned-model-checkpoint", "ft_synthetic"},
			},
			{
				name: "explicit Authorization overrides admin key", method: http.MethodDelete, suffix: "/perm_synthetic",
				authorization: "Custom synthetic-authorization", response: `{"id":"perm_synthetic","object":"checkpoint.permission","deleted":true}`,
				env:       []string{"OPENAI_ADMIN_KEY=synthetic-admin-env"},
				args:      []string{"-H", "Authorization: Custom synthetic-authorization"},
				operation: []string{"delete", "--fine-tuned-model-checkpoint", "ft_synthetic", "--permission-id", "perm_synthetic"},
			},
			{
				name: "environment Authorization with empty admin override", method: http.MethodGet,
				authorization: "Custom synthetic-authorization", response: listResponse,
				env:       []string{"OPENAI_ADMIN_KEY=synthetic-admin-env", "OPENAI_API_KEY=synthetic-project-key", "OPENAI_CUSTOM_HEADERS=Authorization: Custom synthetic-authorization"},
				args:      []string{"--admin-api-key="},
				operation: []string{"retrieve", "--fine-tuned-model-checkpoint", "ft_synthetic"},
			},
			{
				name: "custom endpoint owns authentication", method: http.MethodGet, response: listResponse,
				env:       []string{"OPENAI_API_KEY=synthetic-project-key"},
				operation: []string{"list", "--fine-tuned-model-checkpoint", "ft_synthetic"},
			},
		} {
			t.Run(strings.Join(route, "/")+"/"+test.name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					wantPath := "/v1/fine_tuning/checkpoints/ft_synthetic/permissions" + test.suffix
					if r.Method != test.method || r.URL.Path != wantPath {
						t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, test.method, wantPath)
					}
					if r.Header.Get("Authorization") != test.authorization {
						t.Error("request did not preserve the expected synthetic Authorization")
					}
					if test.method == http.MethodPost {
						var body struct {
							Projects []string `json:"project_ids"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Errorf("decode synthetic request: %v", err)
						} else if len(body.Projects) != 1 || body.Projects[0] != "proj_synthetic" {
							t.Errorf("project_ids = %q, want [proj_synthetic]", body.Projects)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					if _, err := io.WriteString(w, test.response); err != nil {
						t.Errorf("write synthetic response: %v", err)
					}
				}))
				t.Cleanup(server.Close)
				args := append([]string{"openai", "--base-url", server.URL + "/v1", "--format", "json"}, test.args...)
				args = append(args, route...)
				args = append(args, test.operation...)
				got := runMainDispatchWithEnv(t, "bash", test.env, args...)
				if got.code != 0 || got.stderr != "" || !json.Valid([]byte(got.stdout)) || !strings.Contains(got.stdout, "perm_synthetic") {
					t.Errorf("checkpoint authentication = %+v, want exit 0 and synthetic permission JSON", got)
				}
				if count := requests.Load(); count != 1 {
					t.Errorf("checkpoint authentication sent %d requests, want 1", count)
				}
				for _, secret := range []string{"synthetic-admin-flag", "synthetic-admin-env", "synthetic-project-key", "synthetic-authorization"} {
					if strings.Contains(got.stdout+got.stderr, secret) {
						t.Error("command output exposed a synthetic credential")
					}
				}
			})
		}
	}
}

func TestMainAdminCredentialsLeavesProjectOperationsUnmarked(t *testing.T) {
	for _, command := range [][]string{
		{"models", "retrieve", "--model", "model_synthetic"},
		{"responses", "create", "--model", "model_synthetic", "--input", "Synthetic input"},
		{"files", "retrieve", "--file-id", "file_synthetic"},
	} {
		t.Run(command[0], func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodConnect || r.Host != "api.openai.com:443" {
					t.Errorf("proxy received %s %s, want CONNECT api.openai.com:443", r.Method, r.Host)
				}
				w.WriteHeader(http.StatusForbidden)
			}))
			t.Cleanup(server.Close)
			args := append([]string{"openai", "--base-url", "https://api.openai.com/v1"}, command...)
			env := adminCredentialsProxyEnv(server.URL, []string{"OPENAI_API_KEY=synthetic-project-key"})
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			want := "Could not connect to the API. Check your connection, proxy, and --base-url setting."
			if got.code != 1 || got.stdout != "" || strings.TrimSpace(got.stderr) != want {
				t.Errorf("project operation = %+v, want the existing connection failure", got)
			}
			if requests.Load() == 0 {
				t.Error("project operation stopped before the ordinary API request path")
			}
		})
	}
}

func TestMainAdminCredentialsPrecedesFileExpansion(t *testing.T) {
	for _, route := range [][]string{
		{"admin", "organization", "projects"},
		{"admin:organization:projects"},
	} {
		for _, configured := range []bool{false, true} {
			name := "missing admin key"
			want := missingAdminCredentialsMessage
			var keyEnv []string
			if configured {
				name = "configured admin key"
				want = "A local file could not be found. Check your file arguments and @file references."
				keyEnv = []string{"OPENAI_ADMIN_KEY=synthetic-admin-key"}
			}
			t.Run(strings.Join(route, "/")+"/"+name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(http.StatusForbidden)
				}))
				t.Cleanup(server.Close)
				missingFile := filepath.Join(t.TempDir(), "synthetic-private-missing-file")
				args := append([]string{"openai", "--base-url", "https://api.openai.com/v1"}, route...)
				args = append(args, "create", "--name", "@"+missingFile)
				got := runMainDispatchWithEnv(t, "bash", adminCredentialsProxyEnv(server.URL, keyEnv), args...)
				if got.code != 1 || got.stdout != "" || strings.TrimSpace(got.stderr) != want {
					t.Errorf("file-expansion ordering = %+v, want exit 1 and %q", got, want)
				}
				if count := requests.Load(); count != 0 {
					t.Errorf("missing key or local file sent %d proxy requests, want 0", count)
				}
				if strings.Contains(got.stdout+got.stderr, missingFile) || strings.Contains(got.stdout+got.stderr, "synthetic-admin-key") {
					t.Error("diagnostic exposed a local path or synthetic admin key")
				}
			})
		}
	}
}

// A closed writer supplies deterministic EOF while exercising the actual pipe path.
func adminCredentialsValidationInput(t *testing.T, value string) *os.File {
	t.Helper()
	if value == "" {
		return nil
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	if _, err := io.WriteString(writer, value); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return reader
}
