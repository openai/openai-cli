package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

const projectLifecycleActive = `{"id":"proj_returned","object":"organization.project","name":"Returned name","created_at":1700000000,"status":"active","residency":"EU_STORAGE_PROCESSING"}`
const projectLifecycleArchived = `{"id":"proj_returned","object":"organization.project","name":"Returned name","created_at":1700000000,"status":"archived","archived_at":1700000001,"residency":"EU_STORAGE_PROCESSING"}`

func runProjectLifecycle(t *testing.T, server *httptest.Server, input *os.File, args ...string) mainDispatchResult {
	t.Helper()
	home := t.TempDir()
	env := []string{
		"HOME=" + home, "USERPROFILE=" + home, "XDG_CONFIG_HOME=" + home, "FORCE_COLOR=0",
		"OPENAI_ADMIN_KEY=sk-fake-environment-admin", "OPENAI_API_KEY=sk-fake-environment-user",
		"OPENAI_ORG_ID=org_environment", "OPENAI_PROJECT_ID=proj_environment",
	}
	argv := []string{"openai", "--base-url", server.URL, "--admin-api-key", "sk-fake-project-lifecycle-admin",
		"--organization", "org_context", "--project", "proj_context"}
	return runMainDispatchWithStdin(t, "bash", env, input, append(argv, args...)...)
}

func TestMainProjectLifecycleRoutes(t *testing.T) {
	for _, route := range [][]string{
		{"admin:organization:projects"}, {"admin", "organization", "projects"}, {"admin", "projects"}, {"projects"},
	} {
		for _, operation := range []struct {
			name, action, path, response string
			flags                        []string
		}{
			{"create", "created", "/organization/projects", projectLifecycleActive, []string{"--name", "Requested name", "--residency", "EU_STORAGE_PROCESSING"}},
			{"update", "updated", "/organization/projects/proj_requested", projectLifecycleActive, []string{"--project-id", "proj_requested", "--name", "Requested name"}},
			{"archive", "archived", "/organization/projects/proj_requested/archive", projectLifecycleArchived, []string{"--project-id", "proj_requested"}},
		} {
			t.Run(strings.Join(route, "/")+"/"+operation.name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodPost || r.URL.Path != operation.path {
						t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, operation.path)
					}
					for name, want := range map[string]string{
						"Authorization": "Bearer sk-fake-project-lifecycle-admin", "OpenAI-Organization": "org_context",
						"OpenAI-Project": "proj_context", "X-Trace": "project-lifecycle",
					} {
						if got := r.Header.Get(name); got != want {
							t.Errorf("header %s = %q, want %q", name, got, want)
						}
					}
					if operation.name != "archive" {
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["name"] != "Requested name" {
							t.Errorf("request body = %#v, error = %v", body, err)
						}
						if operation.name == "create" && body["residency"] != "EU_STORAGE_PROCESSING" {
							t.Errorf("request residency = %#v", body["residency"])
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, operation.response)
				}))
				t.Cleanup(server.Close)
				args := append(append([]string{}, route...), operation.name)
				args = append(args, operation.flags...)
				args = append(args, "--header", "X-Trace: project-lifecycle")
				got := runProjectLifecycle(t, server, nil, args...)
				require.Zero(t, got.code, "%+v", got)
				require.EqualValues(t, 1, requests.Load())
				require.True(t, strings.HasPrefix(got.stdout, "Project proj_returned "+operation.action+".\n"), "%+v", got)
				for _, value := range []string{"Returned name", "1700000000", "EU_STORAGE_PROCESSING"} {
					require.Contains(t, got.stdout, value)
				}
				require.NotContains(t, got.stdout, "Requested name")
				require.NotContains(t, got.stdout, "proj_requested")
				require.NotContains(t, got.stdout+got.stderr, "sk-fake-")
				if operation.name == "archive" {
					require.Contains(t, got.stdout, "Status: archived")
					require.Contains(t, got.stdout, "1700000001")
					require.Contains(t, got.stderr, "Archived projects cannot be used or updated.")
					require.Contains(t, got.stderr, "Archive is not a delete operation.")
					require.NotContains(t, got.stdout, "delete operation")
				} else {
					require.Contains(t, got.stdout, "Status: active")
					require.Empty(t, got.stderr)
				}
			})
		}
	}
}

func TestMainProjectLifecycleRequestInputs(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		flags       []string
		want        map[string]any
	}{
		{"JSON", `{"name":"From JSON","residency":"GLOBAL","external_key_id":null}`, nil,
			map[string]any{"name": "From JSON", "residency": "GLOBAL", "external_key_id": nil}},
		{"YAML", "name: From YAML\nresidency: GLOBAL\nexternal_key_id: null\n", nil,
			map[string]any{"name": "From YAML", "residency": "GLOBAL", "external_key_id": nil}},
		{"flags override JSON", `{"name":"From JSON","residency":"GLOBAL"}`, []string{"--name", "From flag", "--residency", "EU_STORAGE_PROCESSING"},
			map[string]any{"name": "From flag", "residency": "EU_STORAGE_PROCESSING"}},
		{"deprecated geography remains accepted", "", []string{"--name", "Geography fixture", "--geography", "us"},
			map[string]any{"name": "Geography fixture", "geography": "us"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, projectLifecycleActive)
			}))
			t.Cleanup(server.Close)
			var input *os.File
			if tc.input != "" {
				input = shellFileInput(t, []byte(tc.input))
			}
			args := append([]string{"admin", "projects", "create"}, tc.flags...)
			got := runProjectLifecycle(t, server, input, args...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "Project proj_returned created.")
			select {
			case body := <-requests:
				require.Equal(t, tc.want, body)
			default:
				t.Fatal("create sent no request")
			}
		})
	}
}

func TestMainProjectLifecyclePreservesSelectedOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, projectLifecycleArchived)
	}))
	defer server.Close()
	for _, flags := range [][]string{
		{"--format", "json"}, {"--format", "jsonl"}, {"--format", "yaml"}, {"--format", "raw"},
		{"--transform", "id"}, {"--transform", "id", "--raw-output"}, {"--raw-output"}, {"--quiet"},
	} {
		t.Run(strings.Join(flags, "/"), func(t *testing.T) {
			// Retrieve never receives a mutation receipt and provides the preserved output contract.
			control := runProjectLifecycle(t, server, nil, append([]string{"admin", "projects", "retrieve", "proj_requested"}, flags...)...)
			got := runProjectLifecycle(t, server, nil, append([]string{"admin", "projects", "archive", "proj_requested"}, flags...)...)
			require.Zero(t, control.code, "%+v", control)
			require.NotEmpty(t, control.stdout)
			require.Empty(t, control.stderr)
			require.Equal(t, control, got)
			require.NotContains(t, got.stdout, "Project proj_returned archived.")
			if len(flags) == 2 && (flags[1] == "json" || flags[1] == "jsonl" || flags[1] == "raw") {
				require.JSONEq(t, projectLifecycleArchived, got.stdout)
				if flags[1] == "raw" {
					require.Equal(t, projectLifecycleArchived+"\n", got.stdout)
				}
			}
		})
	}
}

func TestMainProjectLifecycleRecoveryReads(t *testing.T) {
	t.Run("retrieve known project", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.Method != http.MethodGet || r.URL.Path != "/organization/projects/proj_returned" {
				t.Errorf("recovery request = %s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, projectLifecycleArchived)
		}))
		defer server.Close()
		got := runProjectLifecycle(t, server, nil, "admin", "projects", "retrieve", "proj_returned", "--format", "json")
		require.Zero(t, got.code, "%+v", got)
		require.Empty(t, got.stderr)
		require.EqualValues(t, 1, requests.Load())
		require.JSONEq(t, projectLifecycleArchived, got.stdout)
	})

	for _, failSecondPage := range []bool{false, true} {
		name := "include archived across pages"
		if failSecondPage {
			name = "second page failure preserves first result"
		}
		t.Run(name, func(t *testing.T) {
			first := strings.Replace(projectLifecycleActive, "proj_returned", "proj_first", 1)
			second := strings.Replace(projectLifecycleArchived, "proj_returned", "proj_second", 1)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/organization/projects" || r.URL.Query().Get("include_archived") != "true" {
					t.Errorf("recovery list request = %s %s", r.Method, r.URL.RequestURI())
				}
				w.Header().Set("Content-Type", "application/json")
				switch after := r.URL.Query().Get("after"); after {
				case "":
					_, _ = io.WriteString(w, `{"object":"list","data":[`+first+`],"has_more":true,"last_id":"proj_first"}`)
				case "proj_first":
					if failSecondPage {
						w.Header().Set("x-should-retry", "false")
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `{"error":{"message":"synthetic page rejection","type":"permission_error","code":"insufficient_permissions"}}`)
						return
					}
					_, _ = io.WriteString(w, `{"object":"list","data":[`+second+`],"has_more":false,"last_id":"proj_second"}`)
				default:
					t.Errorf("unexpected cursor %q", after)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			got := runProjectLifecycle(t, server, nil, "admin", "projects", "list", "--include-archived=true", "--max-items", "-1", "--format", "jsonl")
			require.EqualValues(t, 2, requests.Load())
			lines := strings.Split(strings.TrimSpace(got.stdout), "\n")
			if failSecondPage {
				require.Equal(t, 1, got.code, "%+v", got)
				require.Len(t, lines, 1)
				require.True(t, json.Valid([]byte(got.stderr)), "invalid structured page error: %q", got.stderr)
				require.Contains(t, got.stderr, "insufficient_permissions")
			} else {
				require.Zero(t, got.code, "%+v", got)
				require.Empty(t, got.stderr)
				require.Len(t, lines, 2)
				require.JSONEq(t, second, lines[1])
			}
			require.JSONEq(t, first, lines[0])
			require.NotContains(t, got.stdout+got.stderr, "Project proj_")
		})
	}
}

func TestMainProjectLifecycleUncertainResponses(t *testing.T) {
	for _, tc := range []struct{ name, response, retained string }{
		{"unknown field", strings.TrimSuffix(projectLifecycleActive, "}") + `,"future_field":"preserved value"}`, "preserved value"},
		{"error-bearing response", strings.TrimSuffix(projectLifecycleActive, "}") + `,"error":{"message":"synthetic failure"}}`, "synthetic failure"},
		{"unexpected status", strings.Replace(projectLifecycleActive, `"active"`, `"pending"`, 1), "pending"},
		{"missing ID", strings.Replace(projectLifecycleActive, `"id":"proj_returned",`, "", 1), "Returned name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}))
			t.Cleanup(server.Close)
			control := runProjectLifecycle(t, server, nil, "admin", "projects", "retrieve", "proj_requested")
			got := runProjectLifecycle(t, server, nil, "admin", "projects", "update", "proj_requested", "--name", "Requested name")
			require.Zero(t, control.code, "%+v", control)
			require.Equal(t, control, got)
			require.Contains(t, got.stdout, tc.retained)
			require.NotContains(t, got.stdout+got.stderr, "Project proj_returned updated.")
		})
	}
}

func TestMainProjectLifecycleFailures(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		truncated      bool
	}{
		{"API rejection", `{"error":{"message":"synthetic rejection","type":"invalid_request_error","code":"invalid_value"}}`, http.StatusBadRequest, false},
		{"malformed response", `{"id":"proj_returned","object":"organization.project",`, http.StatusOK, false},
		{"interrupted response", projectLifecycleArchived, http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, flags := range [][]string{nil, {"--format-error", "json"}} {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("x-should-retry", "false")
					if tc.truncated {
						w.Header().Set("Content-Length", "10000")
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.response)
				}))
				t.Cleanup(server.Close)
				args := append([]string{"admin", "projects", "archive", "proj_requested"}, flags...)
				got := runProjectLifecycle(t, server, nil, args...)
				require.Equal(t, 1, got.code, "%+v", got)
				require.Empty(t, got.stdout)
				require.NotEmpty(t, got.stderr)
				require.EqualValues(t, 1, requests.Load(), "presentation must not retry the mutation")
				require.NotContains(t, got.stderr, "Project proj_returned archived.")
				require.NotContains(t, got.stderr, "Archive is not a delete operation.")
				require.NotContains(t, got.stderr, "sk-fake-")
				if len(flags) > 0 {
					require.True(t, json.Valid([]byte(got.stderr)), "invalid structured error: %q", got.stderr)
				}
			}
		})
	}
}

func TestMainProjectLifecycleHelpClones(t *testing.T) {
	for _, route := range [][]string{
		{"admin:organization:projects"}, {"admin", "organization", "projects"}, {"admin", "projects"}, {"projects"},
	} {
		for _, tc := range []struct {
			operation string
			want      []string
		}{
			{"create", []string{"--residency", "--geography is deprecated.", "check the project list before creating again.", `admin projects create --name "Demo"`}},
			{"update", []string{"Use the returned name and status", "retrieve the project before retrying.", `admin projects update --project-id proj_demo --name "Renamed"`}},
			{"archive", []string{"Archive is not a delete operation.", "list --include-archived", "retrieve the project to check its status.", "admin projects archive --project-id proj_demo"}},
		} {
			t.Run(strings.Join(route, "/")+"/"+tc.operation, func(t *testing.T) {
				args := append([]string{"openai"}, route...)
				got := runMainDispatch(t, "bash", append(args, tc.operation, "--help")...)
				require.Zero(t, got.code, "%+v", got)
				require.Empty(t, got.stderr)
				text := strings.Join(strings.Fields(got.stdout), " ")
				for _, want := range tc.want {
					require.Contains(t, text, want)
				}
			})
		}
	}
}
