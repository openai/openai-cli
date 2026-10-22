package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const authGuidanceDetails = `{"message":"synthetic-private-auth-detail\u001b]52;c;synthetic-private-data\u0007","type":"invalid_request_error","code":"invalid_api_key","param":null,"future_field":{"sequence":9007199254740993}}`

func TestMainAPIAuthGuidanceCredentialSources(t *testing.T) {
	projectCommand := []string{"models", "retrieve", "--model", "synthetic-model"}
	adminCommand := []string{"admin", "organization", "projects", "retrieve", "--project-id", "proj_synthetic"}
	for _, test := range []struct {
		name, authorization      string
		env, flags, want, absent []string
		admin                    bool
	}{
		{"missing project key", "", nil, nil, []string{"OPENAI_API_KEY"}, []string{"OPENAI_ADMIN_KEY"}, false},
		{"environment project key", "Bearer sk-fake-auth-environment", []string{"OPENAI_API_KEY=sk-fake-auth-environment"}, nil,
			[]string{"OPENAI_API_KEY", "--api-key"}, []string{"OPENAI_ADMIN_KEY"}, false},
		{"explicit key overrides environment", "Bearer sk-fake-auth-explicit", []string{"OPENAI_API_KEY=sk-fake-auth-environment"}, []string{"--api-key=sk-fake-auth-explicit"},
			[]string{"OPENAI_API_KEY", "--api-key"}, []string{"OPENAI_ADMIN_KEY"}, false},
		{"explicit empty project key", "", []string{"OPENAI_API_KEY=sk-fake-auth-environment"}, []string{"--api-key="},
			[]string{"--api-key", "empty"}, []string{"OPENAI_ADMIN_KEY"}, false},
		{"explicit authorization header", "Bearer sk-fake-auth-header", []string{"OPENAI_API_KEY=sk-fake-auth-environment"}, []string{"--header", "Authorization: Bearer sk-fake-auth-header"},
			[]string{"Authorization", "--header"}, nil, false},
		{"environment authorization header", "Bearer sk-fake-auth-header", []string{"OPENAI_CUSTOM_HEADERS=Authorization: Bearer sk-fake-auth-header"}, nil,
			[]string{"Authorization", "OPENAI_CUSTOM_HEADERS"}, nil, false},
		{"key overrides environment authorization", "Bearer sk-fake-auth-environment", []string{"OPENAI_API_KEY=sk-fake-auth-environment", "OPENAI_CUSTOM_HEADERS=Authorization: Bearer sk-fake-auth-header"}, nil,
			[]string{"OPENAI_API_KEY", "--api-key"}, nil, false},
		{"admin key cannot authenticate project command", "", []string{"OPENAI_ADMIN_KEY=sk-fake-auth-admin"}, nil,
			[]string{"OPENAI_API_KEY"}, []string{"OPENAI_ADMIN_KEY"}, false},
		{"project key cannot authenticate admin command", "", []string{"OPENAI_API_KEY=sk-fake-auth-environment"}, nil,
			[]string{"OPENAI_ADMIN_KEY"}, nil, true},
		{"environment admin key", "Bearer sk-fake-auth-admin", []string{"OPENAI_API_KEY=sk-fake-auth-environment", "OPENAI_ADMIN_KEY=sk-fake-auth-admin"}, nil,
			[]string{"OPENAI_ADMIN_KEY", "--admin-api-key"}, nil, true},
		{"explicit empty admin key", "", []string{"OPENAI_ADMIN_KEY=sk-fake-auth-admin"}, []string{"--admin-api-key="},
			[]string{"--admin-api-key", "empty"}, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, path := projectCommand, "/models/synthetic-model"
			if test.admin {
				command, path = adminCommand, "/organization/projects/proj_synthetic"
			}
			server := authGuidanceServer(t, path, test.authorization)
			args := append([]string{"openai", "--base-url", server.URL}, test.flags...)
			args = append(args, command...)
			env := append([]string{"NO_COLOR=1", "FORCE_COLOR=0"}, test.env...)
			got := runMainDispatchWithEnv(t, "bash", env, args...)
			assertAuthGuidanceFailure(t, got)
			for _, want := range append(test.want, "Key setup:", "custom API address") {
				if !strings.Contains(got.stderr, want) {
					t.Errorf("missing recovery action %q: %q", want, got.stderr)
				}
			}
			for _, absent := range append(test.absent, server.URL, "https://platform.openai.com/settings/organization/api-keys") {
				if strings.Contains(got.stderr, absent) {
					t.Errorf("misleading source or private target %q in diagnostic: %q", absent, got.stderr)
				}
			}
		})
	}
}

func authGuidanceServer(t *testing.T, path, authorization string) *httptest.Server {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != path {
			t.Errorf("unexpected auth probe route: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != authorization {
			t.Error("request changed the selected synthetic credential")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Should-Retry", "false")
		w.WriteHeader(http.StatusUnauthorized)
		if _, err := io.WriteString(w, `{"error":`+authGuidanceDetails+`}`); err != nil {
			t.Errorf("write synthetic authentication failure: %v", err)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		if calls.Load() != 1 {
			t.Errorf("401 or copied help made %d requests; want exactly one", calls.Load())
		}
	})
	return server
}

func assertAuthGuidanceFailure(t *testing.T, got mainDispatchResult) {
	t.Helper()
	if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "HTTP 401: Unauthorized") {
		t.Fatalf("401 changed exit or output routing: %+v", got)
	}
	for _, secret := range []string{"sk-fake-auth-", "synthetic-private-", "\x1b", "\a"} {
		if strings.Contains(got.stderr, secret) {
			t.Errorf("readable authentication diagnostic exposed private data: %q", got.stderr)
		}
	}
}

func TestMainAPIAuthGuidancePreservesMachineErrors(t *testing.T) {
	want := decodeMainErrorObject(t, "json", authGuidanceDetails)
	for _, flag := range []string{"--format", "--format-error"} {
		for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
			t.Run(flag+"/"+format, func(t *testing.T) {
				got := runMainAPIErrorResponse(t, http.StatusUnauthorized, "application/json", `{"error":`+authGuidanceDetails+`}`, flag, format)
				if format == "yaml" {
					// YAML supports hexadecimal escapes. The existing YAML-to-JSON
					// test helper leaves them invalid for JSON; compare the full
					// documented YAML bytes, including controls and the large integer.
					const wantYAML = "message: \"synthetic-private-auth-detail\\x1B]52;c;synthetic-private-data\\x07\"\n" +
						"type: invalid_request_error\ncode: invalid_api_key\nparam: null\nfuture_field:\n  sequence: 9007199254740993\n"
					if got.stderr != wantYAML {
						t.Fatalf("401 guidance changed original YAML error bytes: %q", got.stderr)
					}
					return
				}
				if actual := decodeMainErrorObject(t, format, got.stderr); !reflect.DeepEqual(actual, want) {
					t.Fatalf("401 guidance changed original API error fields: got %#v, want %#v", actual, want)
				}
				if (format == "raw" || format == "jsonl") && got.stderr != authGuidanceDetails+"\n" {
					t.Errorf("%s error bytes changed: %q", format, got.stderr)
				}
			})
		}
	}
	for _, flags := range [][]string{nil, {"--format", "json"}, {"--format-error", "text"}} {
		t.Run("extraction/"+strings.Join(flags, "/"), func(t *testing.T) {
			args := append(slices.Clone(flags), "--transform-error", "future_field.sequence")
			got := runMainAPIErrorResponse(t, http.StatusUnauthorized, "application/json", `{"error":`+authGuidanceDetails+`}`, args...)
			if got.stderr != "9007199254740993\n" {
				t.Fatalf("401 guidance changed error extraction: %q", got.stderr)
			}
		})
	}
	got := runMainAPIErrorResponse(t, http.StatusUnauthorized, "application/json", `{"error":`+authGuidanceDetails+`}`, "--quiet")
	assertAuthGuidanceFailure(t, got)
}

func TestMainAPIAuthGuidanceIPAllowlist(t *testing.T) {
	const details = `{"error":{"message":"synthetic-private-auth-detail","code":"ip_not_authorized","type":"invalid_request_error"}}`
	got := runMainAPIErrorResponse(t, http.StatusUnauthorized, "application/json", details)
	assertAuthGuidanceFailure(t, got)
	for _, want := range []string{"IP", "allowlist"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("IP denial omitted %q: %q", want, got.stderr)
		}
	}
	if strings.Contains(got.stderr, "https://platform.openai.com/settings/organization/api-keys") {
		t.Errorf("custom-provider IP denial recommended creating an OpenAI key: %q", got.stderr)
	}
}

func TestMainAPIAuthGuidanceWebhookDiscoveryPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("guided discovery PTY checks require a Unix host")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python 3 is required for guided discovery PTY checks")
	}
	server := authGuidanceServer(t, "/webhook_event_types", "Bearer sk-fake-webhook-workflow")
	got := runWebhookCreateWorkflowPTY(t, server, false)
	if got.code != 1 || !strings.Contains(got.stderr, "HTTP 401: Unauthorized") || !strings.Contains(got.stderr, "No endpoint was created") {
		t.Fatalf("discovery failure lost status or creation outcome: %+v", got)
	}
	if !strings.Contains(got.stderr, "Key setup:") || strings.Contains(got.stdout+got.stderr, "Name (optional") || strings.Contains(got.stdout+got.stderr, "synthetic-private-") {
		t.Fatalf("failed discovery opened the form or omitted safe recovery: %+v", got)
	}
}

func TestMainAPIAuthGuidanceNativeCopiedSetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash/zsh executable identity checks require a Unix host")
	}
	work := filepath.Join(t.TempDir(), "CLI preview's folder")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(work, "openai-current-main")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-p", "2", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOMAXPROCS=2")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build renamed CLI: %v\n%s", err, output)
	}
	quotedName := "openai preview's build"
	if err := os.Link(binary, filepath.Join(work, quotedName)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"true", "time", "if"} {
		if err := os.Link(binary, filepath.Join(work, name)); err != nil {
			t.Fatal(err)
		}
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range []nativeShell{
		{"bash", "bash", "", "export OPENAI_API_KEY='sk-fake-auth-native'; ", []string{"--noprofile", "--norc", "-c"}},
		{"zsh", "zsh", "", "export OPENAI_API_KEY='sk-fake-auth-native'; ", []string{"-f", "-c"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required shell unavailable: %v", err)
				}
				t.Skipf("%s is not installed", shell.name)
			}
			shell.executable = path
			for _, name := range []string{"openai-current-main", quotedName} {
				for _, local := range []bool{false, true} {
					t.Run("named-invocation/"+name+"/local="+map[bool]string{true: "yes", false: "no"}[local], func(t *testing.T) {
						directory, home, searchPath := t.TempDir(), t.TempDir(), t.TempDir()
						for _, decoy := range []string{"openai", name} {
							if err := os.WriteFile(filepath.Join(searchPath, decoy), []byte("#!/bin/sh\nprintf 'wrong executable\\n'\nexit 99\n"), 0o700); err != nil {
								t.Fatal(err)
							}
						}
						t.Setenv("PATH", searchPath)
						invocation := quote(filepath.Join(work, name))
						if local {
							directory, invocation = work, quote("./"+name)
						}
						server := authGuidanceServer(t, "/models/synthetic-model", "Bearer sk-fake-auth-native")
						failure := runNativeShell(t, shell, directory, home, server.URL, shell.setKey+invocation+" models retrieve --model synthetic-model")
						assertAuthGuidanceFailure(t, failure)
						help := runNativeShell(t, shell, directory, home, "not-a-url", invocation+" webhooks create --help")
						if help.code != 0 || help.stderr != "" {
							t.Fatalf("renamed executable help failed: %+v", help)
						}
						for _, output := range []string{failure.stderr, help.stdout} {
							command := authGuidanceSetupCommand(t, output)
							copied := runNativeShell(t, shell, directory, home, "not-a-url", command)
							if copied.code != 0 || copied.stderr != "" || !strings.Contains(copied.stdout, "OPENAI_API_KEY") || strings.Contains(copied.stdout, "wrong executable") {
								t.Fatalf("copied setup command %q did not run the same executable offline: %+v", command, copied)
							}
						}
					})
				}
			}
			for _, name := range []string{"true", "time", "if"} {
				t.Run("bare-argv0/"+name, func(t *testing.T) {
					directory, home := t.TempDir(), t.TempDir()
					t.Setenv("PATH", work)
					server := authGuidanceServer(t, "/models/synthetic-model", "Bearer sk-fake-auth-native")
					ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
					defer cancel()
					child := exec.CommandContext(ctx, binary, "models", "retrieve", "--model", "synthetic-model")
					child.Args[0] = name
					child.Dir = directory
					child.Env = []string{"PATH=" + work, "HOME=" + home, "XDG_CONFIG_HOME=" + home,
						"OPENAI_API_KEY=sk-fake-auth-native", "OPENAI_BASE_URL=" + server.URL,
						"NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb", "GOMAXPROCS=2"}
					var stdout, stderr bytes.Buffer
					child.Stdout, child.Stderr = &stdout, &stderr
					err := child.Run()
					if ctx.Err() != nil {
						t.Fatalf("bare argv0 authentication probe timed out: %v", ctx.Err())
					}
					exit, ok := err.(*exec.ExitError)
					if !ok {
						t.Fatalf("bare argv0 authentication probe must fail: %v", err)
					}
					failure := mainDispatchResult{exit.ExitCode(), stdout.String(), stderr.String()}
					assertAuthGuidanceFailure(t, failure)
					command := authGuidanceSetupCommand(t, failure.stderr)
					copied := runNativeShell(t, shell, directory, home, "not-a-url", command)
					if copied.code != 0 || copied.stderr != "" || !strings.Contains(copied.stdout, "OPENAI_API_KEY") {
						t.Fatalf("bare argv0 %q selected a builtin, keyword, or wrong executable with %q: %+v", name, command, copied)
					}
				})
			}
		})
	}
}

func authGuidanceSetupCommand(t *testing.T, output string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(output, "\n") {
		if command, ok := strings.CutPrefix(strings.TrimSpace(line), "Key setup: "); ok {
			found = append(found, command)
		}
	}
	if len(found) != 1 || !strings.HasSuffix(found[0], " help setup") {
		t.Fatalf("expected one executable setup command: %q", output)
	}
	return found[0]
}

func TestMainAPIPermissionGuidanceRecovery(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			var roleAllowed atomic.Bool
			var keyPermission atomic.Value
			keyPermission.Store("")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/webhook_endpoints" || r.URL.Query().Get("limit") != "1" {
					t.Errorf("recovery changed the read-only request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer sk-fake-permission-explicit" || r.Header.Get("OpenAI-Project") != "proj_synthetic_permission" {
					t.Error("recovery changed the selected key or project")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", "req_permission_recovery")
				if keyPermission.Load().(string) == "api.webhooks.read" && roleAllowed.Load() {
					if _, err := io.WriteString(w, `{"object":"list","data":[],"has_more":false}`); err != nil {
						t.Errorf("write recovery result: %v", err)
					}
					return
				}
				message := "You have insufficient permissions for this operation."
				if keyPermission.Load().(string) != "api.webhooks.read" {
					message += " Missing scopes: api.webhooks.read. Check that synthetic-private-permission-detail\x1b[31m"
				}
				w.WriteHeader(status)
				if err := json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
					"message": message, "type": "invalid_request_error", "code": nil, "param": nil,
				}}); err != nil {
					t.Errorf("write permission failure: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			args := []string{"openai", "--base-url=" + server.URL + "/v1", "--project=proj_synthetic_permission",
				"--api-key=sk-fake-permission-explicit", "--format=jsonl", "--format-error=text",
				"webhooks", "list", "--limit=1", "--max-items=1"}
			env := []string{"OPENAI_API_KEY=sk-fake-permission-environment", "OPENAI_PROJECT_ID=proj_synthetic_decoy", "NO_COLOR=1", "FORCE_COLOR=0"}
			first := runMainDispatchWithEnv(t, "bash", env, args...)
			assertPermissionFailure := func(result mainDispatchResult, expectedCalls int32) {
				t.Helper()
				if result.code != 1 || result.stdout != "" || calls.Load() != expectedCalls {
					t.Fatalf("permission failure changed exit, output, or request count: %+v calls=%d", result, calls.Load())
				}
				for _, expected := range []string{http.StatusText(status), "Request ID: req_permission_recovery",
					"The server denied this operation because required permissions are missing.",
					"Next: check this credential's permissions and your role for this API endpoint."} {
					if !strings.Contains(result.stderr, expected) {
						t.Errorf("missing permission recovery detail %q: %q", expected, result.stderr)
					}
				}
				for _, secret := range []string{"sk-fake-permission-", "synthetic-private-", "\x1b", server.URL} {
					if strings.Contains(result.stderr, secret) {
						t.Errorf("permission guidance exposed private data: %q", result.stderr)
					}
				}
				if strings.Contains(result.stderr, "did not accept authentication") || strings.Contains(result.stderr, "replace") {
					t.Errorf("permission denial recommended credential replacement: %q", result.stderr)
				}
			}
			assertPermissionFailure(first, 1)
			// Follow the missing-permission instruction in the controlled service.
			// The credential value stays unchanged through every invocation.
			var missing []string
			for _, line := range strings.Split(first.stderr, "\n") {
				if value, ok := strings.CutPrefix(line, "Missing permission: "); ok {
					missing = append(missing, strings.TrimSuffix(value, "."))
				}
			}
			if !reflect.DeepEqual(missing, []string{"api.webhooks.read"}) {
				t.Fatalf("recovery did not identify the exact required permission: %#v", missing)
			}
			keyPermission.Store(missing[0])
			second := runMainDispatchWithEnv(t, "bash", env, args...)
			assertPermissionFailure(second, 2)
			if strings.Contains(second.stderr, "Missing permission:") {
				t.Fatalf("role-only denial invented a missing scope: %q", second.stderr)
			}
			// Follow the separate role instruction. Key permissions alone must not
			// bypass the fixture's role check.
			roleAllowed.Store(true)
			third := runMainDispatchWithEnv(t, "bash", env, args...)
			if third.code != 0 || third.stderr != "" || strings.TrimSpace(third.stdout) != "" || calls.Load() != 3 {
				t.Fatalf("same-key read did not recover after permission and role correction: %+v calls=%d", third, calls.Load())
			}
		})
	}
}

func TestMainAPIPermissionGuidancePreservesMachineErrors(t *testing.T) {
	const details = `{"message":"You have insufficient permissions for this operation. Missing scopes: api.webhooks.read, synthetic-private-scope. Check that synthetic-private-detail","type":"invalid_request_error","code":null,"param":null,"future_field":{"sequence":9007199254740993}}`
	want := decodeMainErrorObject(t, "json", details)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, flag := range []string{"--format", "--format-error"} {
			for _, format := range []string{"json", "jsonl", "raw", "yaml"} {
				t.Run(http.StatusText(status)+"/"+flag+"/"+format, func(t *testing.T) {
					got := runMainAPIErrorResponse(t, status, "application/json", `{"error":`+details+`}`, flag, format)
					if actual := decodeMainErrorObject(t, format, got.stderr); !reflect.DeepEqual(actual, want) {
						t.Fatalf("permission guidance changed %s API error fields: got %#v, want %#v", format, actual, want)
					}
					if (format == "raw" || format == "jsonl") && got.stderr != details+"\n" {
						t.Errorf("%s error bytes changed: %q", format, got.stderr)
					}
				})
			}
		}
		t.Run(http.StatusText(status)+"/quiet", func(t *testing.T) {
			got := runMainAPIErrorResponse(t, status, "application/json", `{"error":`+details+`}`, "--quiet")
			if got.code != 1 || got.stdout != "" {
				t.Fatalf("quiet permission denial changed exit or output routing: %+v", got)
			}
			for _, expected := range []string{"The server denied this operation because required permissions are missing.", "Missing permission: api.webhooks.read."} {
				if !strings.Contains(got.stderr, expected) {
					t.Errorf("quiet permission denial omitted %q: %q", expected, got.stderr)
				}
			}
			if strings.Contains(got.stderr, "synthetic-private-") || strings.Contains(got.stderr, "You have insufficient permissions for this operation.") {
				t.Errorf("quiet permission denial exposed API prose or unknown scopes: %q", got.stderr)
			}
		})
		for _, flags := range [][]string{nil, {"--format", "json"}, {"--format-error", "text"}} {
			t.Run(http.StatusText(status)+"/extraction/"+strings.Join(flags, "/"), func(t *testing.T) {
				args := append(slices.Clone(flags), "--transform-error", "message")
				got := runMainAPIErrorResponse(t, status, "application/json", `{"error":`+details+`}`, args...)
				var message string
				if err := json.Unmarshal([]byte(got.stderr), &message); err != nil {
					// Explicit text extraction emits the string without JSON quotes.
					message = strings.TrimSuffix(got.stderr, "\n")
				}
				if message != want["message"] {
					t.Fatalf("permission guidance replaced explicit message extraction: %q", got.stderr)
				}
			})
		}
	}
}
