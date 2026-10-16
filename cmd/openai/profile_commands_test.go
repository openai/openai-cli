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
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/app"
	"github.com/openai/openai-cli/pkg/cmd"
	"github.com/stretchr/testify/require"
)

func profileRegistryPath(home string) string {
	return filepath.Join(filepath.Dir(projectLinkRegistryPath(home)), "profiles.json")
}

func profileRecord(name string, active bool, values map[string]string) map[string]any {
	if values == nil {
		values = map[string]string{}
	}
	return map[string]any{"name": name, "active": active, "values": values}
}

func profileReceipt(action, name string, active bool, values map[string]string) map[string]any {
	return map[string]any{"action": action, "profile": profileRecord(name, active, values)}
}

func assertProfileJSON(t *testing.T, got mainDispatchResult, want any) {
	t.Helper()
	require.Zero(t, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	require.JSONEq(t, string(wantJSON), got.stdout)
	require.True(t, strings.HasSuffix(got.stdout, "\n"), "complete results end with a newline")
}

func profileCommand(t *testing.T, home, directory string, args ...string) mainDispatchResult {
	t.Helper()
	return runProjectLinkMain(t, home, directory, nil, "", append(args, "--format=json")...)
}

func createProfileFixture(t *testing.T, home, directory, name string, edits ...string) {
	t.Helper()
	assertProfileJSON(t, profileCommand(t, home, directory, "profile", "create", name), profileReceipt("create", name, false, nil))
	if len(edits) != 0 {
		got := profileCommand(t, home, directory, append([]string{"profile", "set", name}, edits...)...)
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
	}
}

func TestMainProfileLifecycleAndExactSchemas(t *testing.T) {
	for _, format := range []string{"json", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			home, directory := t.TempDir(), t.TempDir()
			run := func(args ...string) mainDispatchResult {
				return runProjectLinkMain(t, home, directory, nil, "", append(args, "--format="+format)...)
			}
			assertList := func(records ...map[string]any) {
				t.Helper()
				got := run("profile", "list")
				require.Zero(t, got.code, "%s", got.stderr)
				require.Empty(t, got.stderr)
				if format == "json" {
					if records == nil {
						records = []map[string]any{}
					}
					assertProfileJSON(t, got, records)
					return
				}
				if len(records) == 0 {
					require.Empty(t, got.stdout)
					return
				}
				lines := strings.Split(strings.TrimSuffix(got.stdout, "\n"), "\n")
				require.Len(t, lines, len(records))
				for i, record := range records {
					assertProfileJSON(t, mainDispatchResult{stdout: lines[i] + "\n"}, record)
				}
			}
			assertResult := func(got mainDispatchResult, want any) {
				t.Helper()
				assertProfileJSON(t, got, want)
				if format == "jsonl" {
					require.Equal(t, 1, strings.Count(got.stdout, "\n"))
				}
			}
			assertList()
			for _, name := range []string{"work", "équipe", "Zeta"} {
				assertResult(run("profile", "create", name), profileReceipt("create", name, false, nil))
			}
			assertList(profileRecord("Zeta", false, nil), profileRecord("work", false, nil), profileRecord("équipe", false, nil))
			values := map[string]string{"organization": "", "project": "", "base_url": ""}
			assertResult(run("profile", "set", "work", "--organization=", "--project=", "--base-url="), profileReceipt("set", "work", false, values))
			assertResult(run("profile", "get", "work"), profileRecord("work", false, values))
			values = map[string]string{"project": ""}
			assertResult(run("profile", "set", "work", "--unset", "organization", "--unset", "base_url"), profileReceipt("set", "work", false, values))
			assertResult(run("profile", "activate", "work"), profileReceipt("activate", "work", true, values))
			assertResult(run("profile", "get", "work"), profileRecord("work", true, values))
			assertResult(run("--profile", "Zeta", "auth", "switch", "--to-project", "proj_switched"), profileReceipt("switch", "Zeta", false, map[string]string{"project": "proj_switched"}))
			assertResult(run("auth", "switch", "--to-project", "proj_default"), profileReceipt("switch", "work", true, map[string]string{"project": "proj_default"}))
			before, err := os.ReadFile(profileRegistryPath(home))
			require.NoError(t, err)
			got := run("profile", "delete", "work")
			require.NotZero(t, got.code)
			require.Empty(t, got.stdout)
			after, err := os.ReadFile(profileRegistryPath(home))
			require.NoError(t, err)
			require.Equal(t, before, after)
			assertResult(run("profile", "delete", "work", "--deactivate"), map[string]any{"action": "delete", "name": "work", "deactivated": true})
			assertResult(run("profile", "delete", "Zeta"), map[string]any{"action": "delete", "name": "Zeta", "deactivated": false})
			assertList(profileRecord("équipe", false, nil))
		})
	}
}

func TestMainProfileEditsNeverSaveAmbientValues(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	env := []string{"OPENAI_PROFILE=missing", "OPENAI_PROJECT_ID=proj_environment", "OPENAI_ORG_ID=org_environment",
		"OPENAI_BASE_URL=https://environment.invalid/v1", "OPENAI_API_KEY=synthetic-private-profile-key"}
	run := func(args ...string) mainDispatchResult {
		return runProjectLinkMain(t, home, directory, env, "", append(args, "--format=json")...)
	}
	assertProfileJSON(t, run("--project=proj_root", "--organization=org_root", "--base-url=https://root.invalid/v1", "profile", "create", "work"), profileReceipt("create", "work", false, nil))
	values := map[string]string{"organization": "org_saved"}
	assertProfileJSON(t, run("--project=proj_root", "--base-url=https://root.invalid/v1", "profile", "set", "work", "--organization=org_saved"), profileReceipt("set", "work", false, values))
	assertProfileJSON(t, run("profile", "get", "work"), profileRecord("work", false, values))
	assertProfileJSON(t, run("profile", "list"), []any{profileRecord("work", false, values)})
	assertProfileJSON(t, run("profile", "activate", "work"), profileReceipt("activate", "work", true, values))
	assertProfileJSON(t, run("--profile=work", "auth", "switch", "--to-project=proj_saved"), profileReceipt("switch", "work", true, map[string]string{"organization": "org_saved", "project": "proj_saved"}))
	data, err := os.ReadFile(profileRegistryPath(home))
	require.NoError(t, err)
	require.JSONEq(t, `{"version":1,"default_profile":"work","profiles":{"work":{"organization":"org_saved","project":"proj_saved"}}}`, string(data))
	assertProfileJSON(t, run("profile", "delete", "work", "--deactivate"), map[string]any{"action": "delete", "name": "work", "deactivated": true})
	assertProfileJSON(t, run("profile", "list"), []any{})
}

func TestMainProfileValidationLeavesStateUnchanged(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	createProfileFixture(t, home, directory, "work", "--project=proj_original")
	before, err := os.ReadFile(profileRegistryPath(home))
	require.NoError(t, err)
	for _, args := range [][]string{
		{"profile", "create", "work"}, {"profile", "create", ""}, {"profile", "create", " edged"},
		{"profile", "create", "line\nbreak"}, {"profile", "set", "work"},
		{"--project=proj_root", "profile", "set", "work"},
		{"profile", "set", "work", "--project=", "--unset=project"},
		{"profile", "set", "work", "--unset=api_key"},
		{"profile", "set", "work", "--base-url=https://user:synthetic-private-password@example.invalid/v1"},
		{"profile", "set", "work", "--base-url=https://example.invalid/v1?token=synthetic-private-query"},
		{"profile", "set", "work", "--base-url=https://example.invalid/v1#synthetic-private-fragment"},
		{"--profile=work", "auth", "switch"}, {"--profile=work", "auth", "switch", "--to-project="},
		{"--profile=", "auth", "switch", "--to-project=proj_new"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got := runProjectLinkMain(t, home, directory, nil, "", append(slices.Clone(args), "--format-error=json")...)
			require.NotZero(t, got.code)
			require.Empty(t, got.stdout)
			require.True(t, json.Valid([]byte(got.stderr)), "%s", got.stderr)
			assertAuthStatusPrivate(t, got, "synthetic-private-password", "synthetic-private-query", "synthetic-private-fragment")
			after, err := os.ReadFile(profileRegistryPath(home))
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
	for _, command := range [][]string{
		{"profile", "create", "new"}, {"profile", "set", "work", "--project=proj_changed"},
		{"profile", "activate", "work"}, {"profile", "delete", "work"},
		{"--profile=work", "auth", "switch", "--to-project=proj_changed"},
	} {
		for _, mode := range [][]string{{"--format=yaml"}, {"--format=raw"}, {"--transform=values"}, {"--raw-output"}} {
			args := append(slices.Clone(command), mode...)
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				got := runProjectLinkMain(t, home, directory, nil, "", append(args, "--format-error=json")...)
				require.NotZero(t, got.code)
				require.Empty(t, got.stdout)
				require.True(t, json.Valid([]byte(got.stderr)), "%s", got.stderr)
				after, err := os.ReadFile(profileRegistryPath(home))
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
	}
}

func TestMainProfileStatusMatchesRequestContext(t *testing.T) {
	home, parent := t.TempDir(), t.TempDir()
	child := filepath.Join(parent, "child")
	directory := filepath.Join(child, "nested")
	require.NoError(t, os.MkdirAll(directory, 0700))
	projectLinkCreate(t, home, parent, "proj_parent")
	projectLinkCreate(t, home, child, "proj_folder")
	createProfileFixture(t, home, directory, "work", "--project=proj_profile", "--organization=org_profile")
	createProfileFixture(t, home, directory, "empty", "--project=", "--organization=")
	createProfileFixture(t, home, directory, "absent")
	assertProfileJSON(t, profileCommand(t, home, directory, "profile", "activate", "work"), profileReceipt("activate", "work", true, map[string]string{"project": "proj_profile", "organization": "org_profile"}))
	custom := "OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_custom_first\nOpenAI-Project: proj_custom\nOpenAI-Organization: org_custom_first\nOpenAI-Organization: org_custom"
	for _, tc := range []struct {
		name                  string
		env, flags            []string
		project, organization authStatusValueResult
		unlinked              bool
	}{
		{"saved default", nil, nil, authStatusValueResult{Value: "proj_profile", Source: "profile:work"}, authStatusValueResult{Value: "org_profile", Source: "profile:work"}, false},
		{"explicit root over custom headers", nil, []string{"--project=proj_flag", "--organization=org_flag"}, authStatusValueResult{Value: "proj_flag", Source: "flag:--project"}, authStatusValueResult{Value: "org_flag", Source: "flag:--organization"}, false},
		{"root over environment", []string{"OPENAI_PROJECT_ID=proj_env", "OPENAI_ORG_ID=org_env"}, []string{"--project=proj_flag", "--organization=org_flag"}, authStatusValueResult{Value: "proj_flag", Source: "flag:--project"}, authStatusValueResult{Value: "org_flag", Source: "flag:--organization"}, false},
		{"empty root", []string{"OPENAI_PROJECT_ID=proj_env", "OPENAI_ORG_ID=org_env"}, []string{"--project=", "--organization="}, authStatusValueResult{Source: "flag:--project"}, authStatusValueResult{Source: "flag:--organization"}, false},
		{"environment over profile", []string{"OPENAI_PROJECT_ID=proj_env", "OPENAI_ORG_ID=org_env"}, nil, authStatusValueResult{Value: "proj_env", Source: "environment:OPENAI_PROJECT_ID"}, authStatusValueResult{Value: "org_env", Source: "environment:OPENAI_ORG_ID"}, false},
		{"empty environment", []string{"OPENAI_PROJECT_ID=", "OPENAI_ORG_ID="}, nil, authStatusValueResult{Source: "environment:OPENAI_PROJECT_ID"}, authStatusValueResult{Source: "environment:OPENAI_ORG_ID"}, false},
		{"empty profile fields", nil, []string{"--profile=empty"}, authStatusValueResult{Source: "profile:empty"}, authStatusValueResult{Source: "profile:empty"}, false},
		{"absent profile fields", nil, []string{"--profile=absent"}, authStatusValueResult{Value: "proj_folder", Source: "folder"}, authStatusValueResult{Value: "org_custom", Source: "environment:OPENAI_CUSTOM_HEADERS"}, false},
		{"empty flag selector", []string{"OPENAI_PROFILE=work"}, []string{"--profile="}, authStatusValueResult{Value: "proj_folder", Source: "folder"}, authStatusValueResult{Value: "org_custom", Source: "environment:OPENAI_CUSTOM_HEADERS"}, false},
		{"empty environment selector", []string{"OPENAI_PROFILE="}, nil, authStatusValueResult{Value: "proj_folder", Source: "folder"}, authStatusValueResult{Value: "org_custom", Source: "environment:OPENAI_CUSTOM_HEADERS"}, false},
		{"flag selector over environment", []string{"OPENAI_PROFILE=empty"}, []string{"--profile=work"}, authStatusValueResult{Value: "proj_profile", Source: "profile:work"}, authStatusValueResult{Value: "org_profile", Source: "profile:work"}, false},
		{"custom header fallback", nil, []string{"--profile="}, authStatusValueResult{Value: "proj_custom", Source: "environment:OPENAI_CUSTOM_HEADERS"}, authStatusValueResult{Value: "org_custom", Source: "environment:OPENAI_CUSTOM_HEADERS"}, true},
		{"literal headers", []string{"OPENAI_PROJECT_ID=proj_env", "OPENAI_ORG_ID=org_env"}, []string{"--project=proj_flag", "--organization=org_flag", "--header=OpenAI-Project: proj_header", "--header=OpenAI-Organization: org_header"}, authStatusValueResult{Value: "proj_header", Source: "flag:--header"}, authStatusValueResult{Value: "org_header", Source: "flag:--header"}, false},
		{"empty literal headers", nil, []string{"--project=proj_flag", "--organization=org_flag", "--header=OpenAI-Project:", "--header=OpenAI-Organization:"}, authStatusValueResult{Source: "flag:--header"}, authStatusValueResult{Source: "flag:--header"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := directory
			if tc.unlinked {
				cwd = t.TempDir()
			}
			server, requests := globalFlagsServer(t, globalFlagsPage)
			env := globalFlagsEnv(server, append([]string{custom}, tc.env...)...)
			statusArgs := append([]string{"auth", "status", "--format=json"}, tc.flags...)
			status := decodeAuthStatus(t, runProjectLinkMain(t, home, cwd, env, "", statusArgs...))
			require.Equal(t, tc.project, status.Project)
			require.Equal(t, tc.organization, status.Organization)
			require.Empty(t, requests, "status must remain offline")
			args := append([]string{"models", "list", "--format=raw"}, tc.flags...)
			got := runProjectLinkMain(t, home, cwd, env, "", args...)
			require.Equal(t, mainDispatchResult{stdout: globalFlagsPage + "\n"}, got)
			request := globalFlagsOneRequest(t, requests)
			require.Equal(t, []string{status.Project.Value}, request.header.Values("OpenAI-Project"))
			require.Equal(t, []string{status.Organization.Value}, request.header.Values("OpenAI-Organization"))
			require.Equal(t, "Bearer synthetic-env-key", request.header.Get("Authorization"))
		})
	}
}

func TestMainProfileUnknownSelectorsFailWithoutFallback(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	projectLinkCreate(t, home, directory, "proj_folder")
	createProfileFixture(t, home, directory, "work", "--project=proj_profile")
	assertProfileJSON(t, profileCommand(t, home, directory, "profile", "activate", "work"), profileReceipt("activate", "work", true, map[string]string{"project": "proj_profile"}))
	server, requests := localUtilitiesRequestTrap(t)
	for _, selector := range []struct{ env, flags []string }{
		{[]string{"OPENAI_PROFILE=missing"}, nil}, {nil, []string{"--profile=missing"}},
	} {
		for _, command := range [][]string{{"auth", "status"}, {"auth", "switch", "--to-project=proj_new"}, {"models", "list"}} {
			args := append(slices.Clone(selector.flags), command...)
			args = append(args, "--format-error=json")
			got := runProjectLinkMain(t, home, directory, append([]string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-key"}, selector.env...), "", args...)
			require.NotZero(t, got.code, "%q", args)
			require.Empty(t, got.stdout)
			require.True(t, json.Valid([]byte(got.stderr)), "%s", got.stderr)
		}
	}
	require.Zero(t, requests.Load())
	assertProfileJSON(t, profileCommand(t, home, directory, "profile", "get", "work"), profileRecord("work", true, map[string]string{"project": "proj_profile"}))
	const dangling = `{"version":1,"default_profile":"missing","profiles":{"work":{"project":"proj_profile"}}}`
	require.NoError(t, os.WriteFile(profileRegistryPath(home), []byte(dangling), 0600))
	for _, command := range [][]string{{"auth", "status"}, {"models", "list"}} {
		got := runProjectLinkMain(t, home, directory, []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-key"}, "", command...)
		require.NotZero(t, got.code)
		require.Empty(t, got.stdout)
	}
	require.Zero(t, requests.Load(), "dangling defaults must not fall back to folder links")
	data, err := os.ReadFile(profileRegistryPath(home))
	require.NoError(t, err)
	require.Equal(t, dangling, string(data))
}

func TestMainProfileProjectBypassesMalformedFolderRegistry(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	createProfileFixture(t, home, directory, "work", "--project=proj_profile")
	createProfileFixture(t, home, directory, "empty", "--project=")
	const malformed = "{synthetic-invalid-folder-registry"
	require.NoError(t, os.WriteFile(projectLinkRegistryPath(home), []byte(malformed), 0600))
	for _, tc := range []struct{ name, project string }{{"work", "proj_profile"}, {"empty", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := globalFlagsServer(t, globalFlagsPage)
			env := globalFlagsEnv(server)
			status := decodeAuthStatus(t, runProjectLinkMain(t, home, directory, env, "", "--profile="+tc.name, "auth", "status", "--format=json"))
			require.Equal(t, authStatusValueResult{Value: tc.project, Source: "profile:" + tc.name}, status.Project)
			got := runProjectLinkMain(t, home, directory, env, "", "--profile="+tc.name, "models", "list", "--format=raw")
			require.Equal(t, mainDispatchResult{stdout: globalFlagsPage + "\n"}, got)
			require.Equal(t, []string{tc.project}, globalFlagsOneRequest(t, requests).header.Values("OpenAI-Project"))
		})
	}
	data, err := os.ReadFile(projectLinkRegistryPath(home))
	require.NoError(t, err)
	require.Equal(t, malformed, string(data))
}

func TestMainProfileMissingSelectionPreservesCommittedFolderLink(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			home, directory := t.TempDir(), t.TempDir()
			createProfileFixture(t, home, directory, "work", "--project=proj_profile")
			assertProfileJSON(t, profileCommand(t, home, directory, "profile", "activate", "work"), profileReceipt("activate", "work", true, map[string]string{"project": "proj_profile"}))
			before, err := os.ReadFile(profileRegistryPath(home))
			require.NoError(t, err)
			projectLinkCreate(t, home, directory, "proj_original")

			got := runProjectLinkMain(t, home, directory, []string{"OPENAI_PROFILE=missing", "OPENAI_BASE_URL=" + server.URL}, "",
				"link", "--project=proj_saved", "--format-error="+format)
			require.NotZero(t, got.code)
			require.Empty(t, got.stdout)
			if format == "json" {
				decodeMainStructuredError(t, format, got.stderr)
			}
			require.Contains(t, got.stderr, "The folder link was saved.")
			require.Contains(t, got.stderr, "Its effective project could not be resolved.")
			require.Contains(t, got.stderr, "Selected profile does not exist.")
			require.Contains(t, got.stderr, "profile list")
			require.NotContains(t, got.stderr, "project-links.json")
			require.NotContains(t, got.stderr, "did not change saved links")
			require.Equal(t, map[string]string{projectLinkCanonicalDirectory(t, directory): "proj_saved"}, projectLinkRegistry(t, home))
			after, err := os.ReadFile(profileRegistryPath(home))
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
	require.Zero(t, requests.Load(), "folder links and profile selection must remain offline")
}

func TestMainProfileEndpointProjectRemainsBodyField(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	createProfileFixture(t, home, directory, "work", "--project=proj_profile")
	for _, rootFlags := range [][]string{nil, {"--project=proj_root"}} {
		server, requests := globalFlagsServer(t, `{"id":"invite_synthetic"}`)
		args := append([]string{"--profile=work"}, rootFlags...)
		args = append(args, "admin", "organization", "invites", "create", "--email=synthetic@example.invalid", "--role=reader",
			"--project", `{"id":"membership_project","role":"member"}`, "--format=json")
		got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server, "OPENAI_ADMIN_KEY=synthetic-admin-key"), "", args...)
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		request := globalFlagsOneRequest(t, requests)
		project := "proj_profile"
		if len(rootFlags) != 0 {
			project = "proj_root"
		}
		require.Equal(t, []string{project}, request.header.Values("OpenAI-Project"))
		require.Equal(t, http.MethodPost, request.method)
		require.Equal(t, "/organization/invites", request.path)
		require.JSONEq(t, `{"email":"synthetic@example.invalid","role":"reader","projects":[{"id":"membership_project","role":"member"}]}`, string(request.body))
	}
}

func TestMainProfileStreamingPreservesContextAndLifecycle(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	createProfileFixture(t, home, directory, "work", "--project=proj_profile", "--organization=org_profile")
	for _, tc := range []struct {
		name, project string
		flags         []string
		earlyExit     bool
	}{
		{"profile failure", "proj_profile", nil, false},
		{"literal override failure", "proj_header", []string{"--project=proj_flag", "--header=OpenAI-Project: proj_header"}, false},
		{"profile early exit", "proj_profile", []string{"--max-items=1"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release, closed := make(chan struct{}), make(chan struct{}, 1)
			requests := make(chan globalFlagsRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() {
					select {
					case closed <- struct{}{}:
					default:
					}
				}()
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				select {
				case requests <- globalFlagsRequest{r.Method, r.URL.Path, r.Header.Clone(), body}:
				default:
					t.Error("unexpected repeated streaming request")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamingTextEvent(w, `{"type":"response.output_text.delta","item_id":"msg_profile","output_index":0,"content_index":0,"delta":"Partial answer"}`)
				w.(http.Flusher).Flush()
				select {
				case <-release:
					writeStreamingTextEvent(w, `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"synthetic failure"}}}`)
				case <-r.Context().Done():
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(server.CloseClientConnections)
			binary, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			args := streamingTextArgs("responses", "--profile=work", "--format=text", "--format-error=json")
			args = append(args, tc.flags...)
			childTest := "TestMainDispatchProcess"
			if tc.earlyExit {
				childTest = "TestMainDispatchOutputStreamExitChild"
			}
			child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^" + childTest + "$", "--", "openai"}, args...)...)
			child.Dir = directory
			child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=synthetic-stream-key", "OPENAI_BASE_URL=" + server.URL,
				"OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_custom", "HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home,
				"XDG_CONFIG_HOME=" + home, "NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb", "GOMAXPROCS=2"}
			barrier := t.TempDir()
			if tc.earlyExit {
				child.Env = append(child.Env, "OPENAI_CLI_OUTPUT_STREAM_BARRIER="+barrier)
			} else if len(tc.flags) != 0 {
				child.Env = append(child.Env, "OPENAI_PROJECT_ID=proj_environment")
			}
			stderr := new(bytes.Buffer)
			child.Stderr = stderr
			stdout, err := child.StdoutPipe()
			require.NoError(t, err)
			require.NoError(t, child.Start())
			t.Cleanup(func() {
				cancel()
				stdout.Close()
				if child.ProcessState == nil {
					child.Wait()
				}
			})
			readStreamingTextPrefix(t, ctx, stdout, "Partial answer")
			request := globalFlagsOneRequest(t, requests)
			require.Equal(t, http.MethodPost, request.method)
			require.Equal(t, "/responses", request.path)
			require.Equal(t, []string{tc.project}, request.header.Values("OpenAI-Project"))
			require.Equal(t, []string{"org_profile"}, request.header.Values("OpenAI-Organization"))
			require.JSONEq(t, `{"model":"fake-model","input":"synthetic input","stream":true}`, string(request.body))
			if tc.earlyExit {
				select {
				case <-closed:
				case <-ctx.Done():
					t.Fatal("early output exit retained the blocked HTTP stream")
				}
				// The existing child cannot exit successfully before this release.
				require.NoError(t, os.WriteFile(filepath.Join(barrier, "release-child"), nil, 0600))
			} else {
				close(release)
			}
			rest, err := io.ReadAll(stdout)
			require.NoError(t, err)
			err = child.Wait()
			require.NoError(t, ctx.Err())
			require.Empty(t, requests, "stream must not restart after partial output")
			if tc.earlyExit {
				require.NoError(t, err, "%s", stderr)
				require.Equal(t, "\n", string(rest))
				require.Empty(t, stderr.String())
			} else {
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit)
				require.Greater(t, exit.ExitCode(), 0)
				require.Contains(t, string(rest), "synthetic failure")
				require.True(t, json.Valid(stderr.Bytes()), "%s", stderr)
			}
		})
	}
}

func TestMainProfileCommandsPreserveStdinAndStayOffline(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	createProfileFixture(t, home, directory, "work")
	server, requests := localUtilitiesRequestTrap(t)
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home, "XDG_CONFIG_HOME=" + home,
		"OPENAI_BASE_URL=" + server.URL, "OPENAI_PROFILE=work", "OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing",
		"OPENAI_CUSTOM_HEADERS=synthetic-private-invalid-header", "NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb"}
	for _, args := range [][]string{
		{"profile", "create", "new"}, {"profile", "set", "work", "--project=proj_saved"},
		{"profile", "get", "work"}, {"profile", "list"}, {"profile", "activate", "work"},
		{"auth", "switch", "--to-project=proj_switched"}, {"profile", "delete", "new"},
		{"profile", "set", "work", "--project=proj_rejected", "--format=yaml"},
		{"profile", "--help"}, {"auth", "switch", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			const input = "synthetic-private-unread-input\n"
			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			defer reader.Close()
			defer writer.Close()
			_, err = io.WriteString(writer, input)
			require.NoError(t, err)
			got := runMainDispatchWithStdin(t, "bash", env, reader, append([]string{"openai"}, args...)...)
			if slices.Contains(args, "--format=yaml") {
				require.NotZero(t, got.code)
			} else {
				require.Zero(t, got.code, "%s", got.stderr)
			}
			assertAuthStatusPrivate(t, got, input, "synthetic-private-missing", "synthetic-private-invalid-header")
			require.NoError(t, writer.Close())
			remaining, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, input, string(remaining))
		})
	}
	require.Zero(t, requests.Load())
}

func TestMainProfileQuietPreservesResults(t *testing.T) {
	for _, format := range []string{"text", "json", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			home, quietHome, directory := t.TempDir(), t.TempDir(), t.TempDir()
			ordinary := runProjectLinkMain(t, home, directory, nil, "", "profile", "create", "work", "--format="+format)
			quiet := runProjectLinkMain(t, quietHome, directory, nil, "", "profile", "create", "work", "--format="+format, "--quiet")
			require.Zero(t, ordinary.code, "%s", ordinary.stderr)
			require.NotEmpty(t, ordinary.stdout)
			require.Equal(t, ordinary, quiet)
			for _, command := range [][]string{
				{"profile", "set", "work", "--project=proj_saved"}, {"profile", "get", "work"},
				{"profile", "list"}, {"profile", "--help"}, {"auth", "switch", "--help"},
			} {
				args := append(slices.Clone(command), "--format="+format)
				ordinary := runProjectLinkMain(t, home, directory, nil, "", args...)
				quiet := runProjectLinkMain(t, quietHome, directory, nil, "", append(args, "--quiet")...)
				require.Zero(t, ordinary.code, "%s", ordinary.stderr)
				require.NotEmpty(t, ordinary.stdout)
				require.Equal(t, ordinary, quiet)
			}
		})
	}
}

func TestMainProfileCompletionFlagOwnership(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	createProfileFixture(t, home, directory, "work", "--project=proj_saved")
	before, err := os.ReadFile(profileRegistryPath(home))
	require.NoError(t, err)
	startup := filepath.Join(home, "startup with spaces")
	const original = "# personal startup\n"
	require.NoError(t, os.WriteFile(startup, []byte(original), 0600))
	for _, action := range []string{"--install-picker", "--uninstall-picker"} {
		got := runProjectLinkMain(t, home, directory, []string{"OPENAI_PROFILE=missing", "ZDOTDIR=" + home}, "",
			"--profile", "work", "@completion", "zsh", action, "--profile", startup)
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stdout)
		contents, err := os.ReadFile(startup)
		require.NoError(t, err)
		if action == "--install-picker" {
			require.True(t, strings.HasPrefix(string(contents), original))
			require.Equal(t, 1, strings.Count(string(contents), "# >>> openai image picker"))
		} else {
			require.Equal(t, original, string(contents))
		}
	}
	after, err := os.ReadFile(profileRegistryPath(home))
	require.NoError(t, err)
	require.Equal(t, before, after, "completion's file option must not edit the named profile")
}

// The transport records the SDK's final URL without opening a socket, including production-reset cases.
type profileURLCaptureTransport struct{}

func (profileURLCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	fmt.Fprintln(os.Stderr, "captured-url="+request.URL.String())
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(globalFlagsPage)), Request: request}, nil
}

func TestMainProfileURLProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_PROFILE_TEST_PROCESS") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	require.GreaterOrEqual(t, separator, 0)
	http.DefaultTransport = profileURLCaptureTransport{}
	http.DefaultClient = &http.Client{Transport: http.DefaultTransport}
	os.Args = os.Args[separator+1:]
	main()
	os.Exit(0)
}

func runProfileURLMain(t *testing.T, home, directory string, env, args []string) mainDispatchResult {
	t.Helper()
	return runProfileTestMain(t, "TestMainProfileURLProcess", home, directory, env, args)
}

func runProfileTestMain(t *testing.T, processTest, home, directory string, env, args []string) mainDispatchResult {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^" + processTest + "$", "--", "openai"}, args...)...)
	child.Dir = directory
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if strings.HasPrefix(name, "OPENAI_") || slices.Contains([]string{"HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME", "COMPLETION_STYLE", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}, name) {
			continue
		}
		child.Env = append(child.Env, entry)
	}
	child.Env = append(child.Env, "OPENAI_CLI_PROFILE_TEST_PROCESS=1", "COMPLETION_STYLE=bash", "OPENAI_API_KEY=synthetic-url-key",
		"HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "XDG_CONFIG_HOME="+home, "NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb")
	child.Env = append(child.Env, env...)
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	err = child.Run()
	require.NoError(t, ctx.Err(), "command timed out")
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return mainDispatchResult{code, stdout.String(), stderr.String()}
}

type profileCancelReceiptWriter struct{ cancel context.CancelFunc }

func (writer profileCancelReceiptWriter) Write([]byte) (int, error) {
	writer.cancel()
	return 0, context.Canceled
}

func TestMainProfileCanceledReceiptProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_PROFILE_TEST_PROCESS") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	require.GreaterOrEqual(t, separator, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cmd.Command.Writer = profileCancelReceiptWriter{cancel: cancel}
	if os.Getenv("OPENAI_CLI_PROFILE_CANCEL_BEFORE") == "1" {
		cancel()
	}
	os.Exit(app.Run(ctx, os.Args[separator+1:]))
}

func TestMainProfileCancellationPreservesCommittedOutcomes(t *testing.T) {
	const profileCommitted = "Saved profiles were updated, but the result could not be written. Inspect with openai profile list."
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			home, directory := t.TempDir(), t.TempDir()
			run := func(message string, env []string, args ...string) {
				t.Helper()
				args = append(args, "--format="+format, "--format-error="+format)
				got := runProfileTestMain(t, "TestMainProfileCanceledReceiptProcess", home, directory, env, args)
				require.Equal(t, 1, got.code, "%s", got.stderr)
				require.Empty(t, got.stdout)
				if format == "json" {
					want, err := json.Marshal(map[string]string{"message": message})
					require.NoError(t, err)
					require.JSONEq(t, string(want), got.stderr)
				} else {
					require.Equal(t, message+"\n", got.stderr)
				}
				assertAuthStatusPrivate(t, got, "synthetic-url-key", home, directory)
			}
			run("Request canceled.", []string{"OPENAI_CLI_PROFILE_CANCEL_BEFORE=1"}, "profile", "create", "work")
			_, err := os.Stat(profileRegistryPath(home))
			require.ErrorIs(t, err, os.ErrNotExist)
			for _, step := range []struct {
				args    []string
				want    string
				inspect bool
			}{
				{[]string{"profile", "create", "work"}, `{"version":1,"default_profile":null,"profiles":{"work":{}}}`, false},
				{[]string{"profile", "set", "work", "--project=proj_saved"}, `{"version":1,"default_profile":null,"profiles":{"work":{"project":"proj_saved"}}}`, false},
				{[]string{"profile", "activate", "work"}, `{"version":1,"default_profile":"work","profiles":{"work":{"project":"proj_saved"}}}`, false},
				{[]string{"profile", "get", "work"}, `{"version":1,"default_profile":"work","profiles":{"work":{"project":"proj_saved"}}}`, true},
				{[]string{"profile", "delete", "work", "--deactivate"}, `{"version":1,"default_profile":null,"profiles":{}}`, false},
			} {
				message := profileCommitted
				var before []byte
				if step.inspect {
					message = "Request canceled."
					before, err = os.ReadFile(profileRegistryPath(home))
					require.NoError(t, err)
				}
				run(message, nil, step.args...)
				after, err := os.ReadFile(profileRegistryPath(home))
				require.NoError(t, err)
				require.JSONEq(t, step.want, string(after))
				if step.inspect {
					require.Equal(t, before, after)
				}
			}

			run("The folder link was saved, but its confirmation could not be written. Inspect with openai link.", nil,
				"link", "--project=proj_folder")
			require.Equal(t, map[string]string{projectLinkCanonicalDirectory(t, directory): "proj_folder"}, projectLinkRegistry(t, home))
			before, err := os.ReadFile(projectLinkRegistryPath(home))
			require.NoError(t, err)
			run("Request canceled.", nil, "link")
			after, err := os.ReadFile(projectLinkRegistryPath(home))
			require.NoError(t, err)
			require.Equal(t, before, after)
			run("This folder has no saved link of its own. Its confirmation could not be written. Inspect with openai link.", nil, "unlink")
			require.Empty(t, projectLinkRegistry(t, home))
		})
	}
}

func TestMainProfileBaseURLUsesActualEffectiveEndpoint(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	createProfileFixture(t, home, directory, "work", "--base-url=https://profile.invalid/custom")
	createProfileFixture(t, home, directory, "empty", "--base-url=")
	for _, tc := range []struct {
		name, profile, baseURL, source, requestURL string
		redacted                                   bool
		env, flags                                 []string
	}{
		// A1 hides custom endpoint paths in status; the request must retain the complete path.
		{"profile", "work", "https://profile.invalid/", "profile:work", "https://profile.invalid/custom/models", true, nil, nil},
		{"empty profile production reset", "empty", "https://api.openai.com/v1/", "profile:empty", "https://api.openai.com/v1/models", false, nil, nil},
		{"empty flag keeps profile", "work", "https://profile.invalid/", "profile:work", "https://profile.invalid/custom/models", true, nil, []string{"--base-url="}},
		{"empty flag keeps production reset", "empty", "https://api.openai.com/v1/", "profile:empty", "https://api.openai.com/v1/models", false, nil, []string{"--base-url="}},
		{"environment", "work", "https://environment.invalid/v1", "environment:OPENAI_BASE_URL", "https://environment.invalid/v1/models", false, []string{"OPENAI_BASE_URL=https://environment.invalid/v1"}, nil},
		{"empty flag keeps environment", "work", "https://environment.invalid/v1", "environment:OPENAI_BASE_URL", "https://environment.invalid/v1/models", false, []string{"OPENAI_BASE_URL=https://environment.invalid/v1"}, []string{"--base-url="}},
		{"root overrides environment", "work", "https://flag.invalid/v1", "flag:--base-url", "https://flag.invalid/v1/models", false, []string{"OPENAI_BASE_URL=https://environment.invalid/v1"}, []string{"--base-url=https://flag.invalid/v1"}},
		// SDK URL resolution adds a slash but preserves the empty scheme and host.
		{"empty environment remains empty", "work", "", "environment:OPENAI_BASE_URL", "/models", false, []string{"OPENAI_BASE_URL="}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := append([]string{"--profile=" + tc.profile}, tc.flags...)
			statusArgs := append(slices.Clone(flags), "auth", "status", "--format=json")
			status := decodeAuthStatus(t, runProfileURLMain(t, home, directory, tc.env, statusArgs))
			require.Equal(t, authStatusValueResult{Value: tc.baseURL, Source: tc.source, Redacted: tc.redacted}, status.BaseURL)
			requestArgs := append(slices.Clone(flags), "models", "list", "--format=raw")
			got := runProfileURLMain(t, home, directory, tc.env, requestArgs)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Equal(t, globalFlagsPage+"\n", got.stdout)
			require.Equal(t, "captured-url="+tc.requestURL+"\n", got.stderr)
		})
	}
	assertProfileJSON(t, profileCommand(t, home, directory, "profile", "get", "empty"), profileRecord("empty", false, map[string]string{"base_url": ""}))
}
