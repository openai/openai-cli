//go:build !windows

package main

import (
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Real Bash parses each command before exec replaces it with the existing test
// payload. The guest needs neither a Go compiler nor a separate CLI binary.
func TestMainBashRequestConfiguration(t *testing.T) {
	bash, err := exec.LookPath("bash")
	require.NoError(t, err, "this request boundary requires real Bash")
	binary, err := os.Executable()
	require.NoError(t, err)
	shell := nativeShell{name: "bash", executable: bash, args: []string{"--noprofile", "--norc", "-c"}}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	const errorDetail = `{"message":"synthetic rejection","type":"invalid_request_error","code":"synthetic_code"}`
	for _, name := range []string{"placement_precedence_and_error", "root_and_body_project", "equals_empty", "separate_empty", "completion_stays_local"} {
		t.Run(name, func(t *testing.T) {
			home, work := t.TempDir(), t.TempDir()
			t.Cleanup(func() { assertBashRequestSettingsAbsent(t, home) })
			status, response := http.StatusOK, globalFlagsPage
			if name == "placement_precedence_and_error" {
				status, response = http.StatusBadRequest, `{"error":`+errorDetail+`}`
			} else if name == "root_and_body_project" {
				response = `{"id":"invite_synthetic"}`
			}
			server, requests := globalFlagsServer(t, response, status)
			baseURL := server.URL
			prefix := "export OPENAI_CLI_MAIN_DISPATCH_PROCESS=1 COMPLETION_STYLE=bash; "
			if name != "completion_stays_local" {
				prefix += "export OPENAI_API_KEY=synthetic-shell-env-key OPENAI_ADMIN_KEY=synthetic-shell-env-admin OPENAI_PROJECT_ID=proj-env OPENAI_ORG_ID=org-env; "
			}
			if name == "equals_empty" || name == "separate_empty" || name == "completion_stays_local" {
				prefix += "export OPENAI_MTLS_CLIENT_CERT_FILE=" + quote(filepath.Join(home, "missing-cert.pem")) +
					" OPENAI_MTLS_CLIENT_KEY_FILE=" + quote(filepath.Join(home, "missing-key.pem")) + "; "
			}
			method, path := http.MethodGet, "/models"
			headers := map[string][]string{
				"Authorization": {"Bearer synthetic-shell-env-key"}, "OpenAI-Project": {""},
				"OpenAI-Organization": {"org-env"}, "X-Probe": nil,
			}
			var args string
			switch name {
			case "placement_precedence_and_error":
				baseURL = "not%url"
				args = `--api-key 'synthetic-shell-explicit' --webhook-secret=synthetic-shell-webhook --header 'OpenAI-Project: first' models --project 'proj with spaces' --organization "org's synthetic" -H 'OpenAI-Project: second' list --admin-api-key=synthetic-shell-unused-admin --base-url ` + quote(server.URL) + ` --header='OpenAI-Project: last' -H 'X-Probe: error' --format=json --format-error=json`
				headers = map[string][]string{
					"Authorization": {"Bearer synthetic-shell-explicit"}, "OpenAI-Project": {"last"},
					"OpenAI-Organization": {"org's synthetic"}, "X-Probe": {"error"},
				}
			case "root_and_body_project":
				args = `--project='proj with spaces' admin organization --admin-api-key 'synthetic-shell-admin' invites create --email=synthetic@example.invalid --role=reader --project '{"id":"membership_project","role":"member"}' --organization=org-admin --format=json`
				method, path = http.MethodPost, "/organization/invites"
				headers = map[string][]string{
					"Authorization": {"Bearer synthetic-shell-admin"}, "OpenAI-Project": {"proj with spaces"},
					"OpenAI-Organization": {"org-admin"}, "X-Probe": nil,
				}
			case "equals_empty":
				args = `models --project= list --base-url= --mtls-client-cert-file= --mtls-client-key-file= --format=raw`
			case "separate_empty":
				args = `models --project '' list --base-url '' --mtls-client-cert-file '' --mtls-client-key-file '' --format=raw`
			case "completion_stays_local":
				baseURL = "not%url"
				args = `@completion bash`
			}
			got := runNativeShell(t, shell, work, home, baseURL,
				prefix+"exec "+quote(binary)+" -test.run='^TestMainDispatchProcess$' -- openai "+args)
			if name == "completion_stays_local" {
				require.Zero(t, got.code)
				require.Empty(t, got.stderr)
				require.Contains(t, got.stdout, "__complete")
				require.Contains(t, got.stdout, "complete -o filenames -F __openai_bash_autocomplete openai")
				require.Empty(t, requests, "completion must not make an API request")
				return // Inspect the generated adapter without evaluating it.
			}
			request := globalFlagsOneRequest(t, requests)
			require.Equal(t, method, request.method)
			require.Equal(t, path, request.path)
			for header, want := range headers {
				if actual := request.header.Values(header); !slices.Equal(actual, want) {
					t.Errorf("%s = %q; want %q", header, actual, want)
				}
			}
			if name == "root_and_body_project" {
				require.JSONEq(t, `{"email":"synthetic@example.invalid","role":"reader","projects":[{"id":"membership_project","role":"member"}]}`, string(request.body))
			} else {
				require.Empty(t, request.body, "GET request must have no body")
			}
			if name == "placement_precedence_and_error" {
				require.Equal(t, 1, got.code)
				require.Empty(t, got.stdout)
				require.JSONEq(t, errorDetail, got.stderr, "the complete error stream must remain one JSON document")
				return
			}
			require.Zero(t, got.code)
			require.Empty(t, got.stderr)
			require.JSONEq(t, response, got.stdout)
			if name == "equals_empty" || name == "separate_empty" {
				require.Equal(t, response+"\n", got.stdout, "raw response bytes must remain unchanged")
			}
		})
	}
}

func assertBashRequestSettingsAbsent(t *testing.T, home string) {
	t.Helper()
	for _, directory := range []string{
		filepath.Join(home, "openai"), filepath.Join(home, ".openai"),
		filepath.Join(home, "Library", "Application Support", "openai"),
	} {
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
		require.NoError(t, err)
	}
}
