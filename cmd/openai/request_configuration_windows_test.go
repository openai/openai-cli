//go:build windows

package main

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// Observe the shell's native argv without forwarding it to the CLI.
func TestWindowsRequestArgumentObserver(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) < 2 {
		t.Fatal("missing capture path or request arguments")
	}
	data, err := json.Marshal(windowsRequestArguments{args[1:], os.Getenv("OPENAI_BASE_URL")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(args[0], data, 0600); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

type windowsRequestArguments struct {
	Args    []string
	BaseURL string
}

func testWindowsRequestConfiguration(t *testing.T, shell nativeShell, work, binary, helper string, command func(string, ...string) string, legacyArguments bool) {
	const errorDetail = `{"message":"synthetic rejection","type":"invalid_request_error","code":"synthetic_code"}`
	for _, name := range []string{"placement_precedence_and_error", "root_and_body_project", "equals_empty", "separate_empty", "completion_stays_local"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			defer assertWindowsRequestSettingsAbsent(t, home)
			status, response := http.StatusOK, globalFlagsPage
			if name == "placement_precedence_and_error" {
				status, response = http.StatusBadRequest, `{"error":`+errorDetail+`}`
			} else if name == "root_and_body_project" {
				response = `{"id":"invite_synthetic"}`
			}
			requests := make(chan globalFlagsRequest, 32)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				requests <- globalFlagsRequest{r.Method, r.URL.Path, r.Header.Clone(), body}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if _, err := io.WriteString(w, response); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			baseURL := server.URL
			environment := []string{"LOCALAPPDATA=" + home, "TEMP=" + home, "TMP=" + home}
			if name != "completion_stays_local" {
				environment = append(environment, "OPENAI_API_KEY=synthetic-shell-env-key", "OPENAI_ADMIN_KEY=synthetic-shell-env-admin",
					"OPENAI_PROJECT_ID=proj-env", "OPENAI_ORG_ID=org-env")
			}
			if name == "equals_empty" || name == "separate_empty" || name == "completion_stays_local" {
				environment = append(environment, "OPENAI_MTLS_CLIENT_CERT_FILE="+filepath.Join(home, "missing-cert.pem"),
					"OPENAI_MTLS_CLIENT_KEY_FILE="+filepath.Join(home, "missing-key.pem"))
			}
			method, path := http.MethodGet, "/models"
			headers := map[string][]string{
				"Authorization": {"Bearer synthetic-shell-env-key"}, "OpenAI-Project": {""},
				"OpenAI-Organization": {"org-env"}, "X-Probe": nil,
			}
			var args []string
			switch name {
			case "placement_precedence_and_error":
				baseURL = "not%url"
				args = []string{"--api-key", "synthetic-shell-explicit", "--webhook-secret=synthetic-shell-webhook",
					"--header", "OpenAI-Project: first", "models", "--project", "proj with spaces", "--organization", "org's synthetic",
					"-H", "OpenAI-Project: second", "list", "--admin-api-key=synthetic-shell-unused-admin", "--base-url", server.URL,
					"--header=OpenAI-Project: last", "-H", "X-Probe: error", "--format=json", "--format-error=json"}
				headers = map[string][]string{"Authorization": {"Bearer synthetic-shell-explicit"}, "OpenAI-Project": {"last"},
					"OpenAI-Organization": {"org's synthetic"}, "X-Probe": {"error"}}
			case "root_and_body_project":
				args = []string{"--project=proj with spaces", "admin", "organization", "--admin-api-key", "synthetic-shell-admin",
					"invites", "create", "--email=synthetic@example.invalid", "--role=reader", "--project",
					`{"id":"membership_project","role":"member"}`, "--organization=org-admin", "--format=json"}
				method, path = http.MethodPost, "/organization/invites"
				headers = map[string][]string{"Authorization": {"Bearer synthetic-shell-admin"}, "OpenAI-Project": {"proj with spaces"},
					"OpenAI-Organization": {"org-admin"}, "X-Probe": nil}
			case "equals_empty":
				args = []string{"models", "--project=", "list", "--base-url=", "--mtls-client-cert-file=", "--mtls-client-key-file=", "--format=raw"}
			case "separate_empty":
				args = []string{"models", "--project", "", "list", "--base-url", "", "--mtls-client-cert-file", "", "--mtls-client-key-file", "", "--format=raw"}
			case "completion_stays_local":
				baseURL = "not%url"
				args = []string{"@completion", "pwsh"}
			}
			suffix := windowsRequestArgumentSuffix(t, shell, args, legacyArguments)
			prefix := windowsRequestEnvironment(shell, environment)
			capture := filepath.Join(t.TempDir(), "arguments.json")
			observed := runNativeShell(t, shell, work, home, baseURL,
				prefix+command(helper, "-test.run=^TestWindowsRequestArgumentObserver$", "--", capture)+suffix)
			if observed.code != 0 || observed.stdout != "" || observed.stderr != "" {
				t.Fatalf("native argv observer failed: %+v", observed)
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var received windowsRequestArguments
			if err := json.Unmarshal(data, &received); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(received.Args, args) || received.BaseURL != baseURL {
				t.Fatalf("shell changed intended request arguments: got=%+v want=%+v", received, windowsRequestArguments{args, baseURL})
			}
			// Run the production CLI separately with the exact suffix observed above.
			got := runNativeShell(t, shell, work, home, baseURL, prefix+command(binary)+suffix)
			if name == "completion_stays_local" {
				if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "__complete") || len(requests) != 0 {
					t.Fatalf("completion depends on request configuration: %+v; requests=%d", got, len(requests))
				}
				return // Never evaluate the generated completion script.
			}
			request := globalFlagsOneRequest(t, requests)
			if request.method != method || request.path != path {
				t.Fatalf("request = %s %s; want %s %s", request.method, request.path, method, path)
			}
			for header, want := range headers {
				if actual := request.header.Values(header); !slices.Equal(actual, want) {
					t.Errorf("%s = %q; want %q", header, actual, want)
				}
			}
			if name == "root_and_body_project" {
				assertWindowsRequestJSON(t, string(request.body), `{"email":"synthetic@example.invalid","role":"reader","projects":[{"id":"membership_project","role":"member"}]}`)
			} else if len(request.body) != 0 {
				t.Fatalf("GET request has a body: %q", request.body)
			}
			if name == "placement_precedence_and_error" {
				if got.code != 1 || got.stdout != "" {
					t.Fatalf("API error lost exit status or output routing: %+v", got)
				}
				if shell.name == "cmd" {
					assertWindowsRequestJSON(t, got.stderr, errorDetail)
				} else {
					// PowerShell can decorate native stderr. Do not strip decoration to claim JSON preservation.
					for _, detail := range []string{"synthetic rejection", "invalid_request_error", "synthetic_code"} {
						if !strings.Contains(got.stderr, detail) {
							t.Errorf("API error lost %q: %q", detail, got.stderr)
						}
					}
				}
				return
			}
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("request failed: %+v", got)
			}
			assertWindowsRequestJSON(t, got.stdout, response)
			if shell.name == "cmd" && (name == "equals_empty" || name == "separate_empty") && got.stdout != response+"\n" {
				t.Fatalf("raw response bytes changed: %q", got.stdout)
			}
		})
	}
}

func windowsRequestArgumentSuffix(t *testing.T, shell nativeShell, args []string, legacyArguments bool) string {
	t.Helper()
	stopParsing := shell.name != "cmd" && legacyArguments && slices.ContainsFunc(args, func(arg string) bool {
		return arg == "" || strings.Contains(arg, `"`)
	})
	quoted := make([]string, len(args))
	for i, arg := range args {
		if shell.name == "cmd" || stopParsing {
			// EscapeArg handles native quotes, not cmd operators or --% variable expansion.
			if strings.ContainsAny(arg, "%!&|<>^\r\n") {
				t.Fatalf("fixture needs explicit shell metacharacter handling: %q", arg)
			}
			quoted[i] = syscall.EscapeArg(arg)
		} else {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", "''") + "'"
		}
	}
	if stopParsing {
		// Legacy PowerShell drops ordinary empty arguments and embedded JSON quotes.
		// --% ends at a newline. Keep the exit command outside its literal suffix.
		return " --% " + strings.Join(quoted, " ") + "\nexit $LASTEXITCODE"
	}
	return " " + strings.Join(quoted, " ")
}

func windowsRequestEnvironment(shell nativeShell, environment []string) string {
	var script strings.Builder
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		if shell.name == "cmd" {
			script.WriteString(`set "` + name + "=" + strings.ReplaceAll(value, "%", "%%") + `" && `)
		} else {
			script.WriteString("$env:" + name + " = '" + strings.ReplaceAll(value, "'", "''") + "'; ")
		}
	}
	return script.String()
}

func assertWindowsRequestJSON(t *testing.T, actual, expected string) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal([]byte(actual), &got); err != nil {
		t.Fatalf("output is not one JSON document: %q; error=%v", actual, err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON changed: %s; want %s", actual, expected)
	}
}

func assertWindowsRequestSettingsAbsent(t *testing.T, home string) {
	t.Helper()
	for _, directory := range []string{filepath.Join(home, "openai"), filepath.Join(home, ".openai")} {
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) && path == directory {
				return nil
			}
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				t.Errorf("request configuration persisted an OpenAI file: %s", path)
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	}
}
