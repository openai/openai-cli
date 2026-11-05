package main

import (
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
		helpPrefix := "\nOptions and examples: openai " + strings.Join(route, " ") + " "
		for _, test := range []struct {
			name, stdin, want string
			args              []string
		}{
			{"missing path", "", "Missing required options: --project-id." + helpPrefix + "retrieve --help", []string{"retrieve"}},
			{"missing body", "", "Missing required options: --name." + helpPrefix + "create --help", []string{"create"}},
			{"malformed JSON", `{"project_id":`, "Could not parse piped input as YAML or JSON. Check the input's syntax.", []string{"retrieve"}},
			{"extra positional value", "", "Unexpected extra arguments." + helpPrefix + "retrieve --help", []string{"retrieve", "proj_synthetic", "synthetic-extra-value"}},
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
				want = "Could not open the file for --name. Check the path and permissions."
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
