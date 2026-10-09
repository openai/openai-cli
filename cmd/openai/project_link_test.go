package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Each invocation receives its own command tree, real cwd, and isolated config.
// Reuse the production-entrypoint subprocess without changing process-wide cwd.
func runProjectLinkMain(t *testing.T, home, directory string, env []string, stdin string, args ...string) mainDispatchResult {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	argv := append([]string{"-test.run=^TestMainDispatchProcess$", "--", "openai"}, args...)
	child := exec.CommandContext(ctx, binary, argv...)
	child.Dir = directory
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if strings.HasPrefix(name, "OPENAI_") || slices.Contains([]string{
			"HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME", "COMPLETION_STYLE",
			"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "TERM", "NO_COLOR", "FORCE_COLOR",
		}, name) {
			continue
		}
		child.Env = append(child.Env, entry)
	}
	child.Env = append(child.Env, "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "COMPLETION_STYLE=bash",
		"OPENAI_BASE_URL=http://127.0.0.1:1", "NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb")
	if home != "" {
		child.Env = append(child.Env, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "XDG_CONFIG_HOME="+home)
	}
	child.Env = append(child.Env, env...)
	if stdin != "" {
		child.Stdin = strings.NewReader(stdin)
	}
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

func projectLinkRegistryPath(home string) string {
	if runtime.GOOS == "darwin" {
		home = filepath.Join(home, "Library", "Application Support")
	}
	return filepath.Join(home, "openai", "project-links.json")
}

func projectLinkCanonicalDirectory(t *testing.T, directory string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(directory)
	require.NoError(t, err)
	return canonical
}

func projectLinkRegistry(t *testing.T, home string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(projectLinkRegistryPath(home))
	require.NoError(t, err)
	var links map[string]string
	require.NoError(t, json.Unmarshal(data, &links))
	return links
}

func projectLinkCreate(t *testing.T, home, directory, project string) {
	t.Helper()
	got := runProjectLinkMain(t, home, directory, nil, "", "link", "--project", project)
	require.Zero(t, got.code, "%s", got.stderr)
	require.Empty(t, got.stderr)
	require.Contains(t, got.stdout, project)
}

func TestMainProjectLinkRejectsRelativeConfiguration(t *testing.T) {
	directory := t.TempDir()
	registry := filepath.Join(directory, projectLinkRegistryPath("."))
	require.NoError(t, os.MkdirAll(filepath.Dir(registry), 0700))
	data, err := json.Marshal(map[string]string{projectLinkCanonicalDirectory(t, directory): "proj_repository"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(registry, data, 0600))
	server, requests := globalFlagsServer(t, globalFlagsPage)
	got := runProjectLinkMain(t, ".", directory, globalFlagsEnv(server), "", "files", "list", "--format=raw")
	require.Equal(t, mainDispatchResult{stdout: globalFlagsPage + "\n"}, got)
	require.Empty(t, globalFlagsOneRequest(t, requests).header.Values("OpenAI-Project"))
	for _, args := range [][]string{{"link"}, {"link", "--project=proj_new"}, {"unlink"}} {
		got := runProjectLinkMain(t, ".", directory, nil, "", args...)
		require.NotZero(t, got.code)
		require.Empty(t, got.stdout)
		require.Contains(t, got.stderr, "configuration folder")
	}
	after, err := os.ReadFile(registry)
	require.NoError(t, err)
	require.Equal(t, data, after)
}

func TestMainProjectLinkOfflineLifecycle(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	for _, configuration := range []struct {
		name string
		env  []string
	}{
		{"without credentials", []string{"OPENAI_BASE_URL=" + server.URL}},
		{"invalid remote configuration", []string{"OPENAI_BASE_URL=://synthetic-private-endpoint",
			"OPENAI_CUSTOM_HEADERS=synthetic-private-headers", "OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing"}},
	} {
		t.Run(configuration.name, func(t *testing.T) {
			home, directory := t.TempDir(), t.TempDir()
			for _, format := range []string{"text", "json"} {
				for _, args := range [][]string{
					{"link", "--project=proj_first"}, {"link"},
					{"link", "--project=proj_replacement"}, {"unlink"}, {"unlink"},
				} {
					argv := append(slices.Clone(args), "--format", format)
					got := runProjectLinkMain(t, home, directory, configuration.env, "", argv...)
					require.Zero(t, got.code, "%v: %s", argv, got.stderr)
					require.Empty(t, got.stderr)
					require.NotEmpty(t, got.stdout)
					require.NotContains(t, got.stdout, "synthetic-private-")
					if format == "json" {
						require.True(t, json.Valid([]byte(got.stdout)), "%s", got.stdout)
					}
					if strings.Contains(strings.Join(args, " "), "--project=") {
						project := strings.TrimPrefix(args[1], "--project=")
						require.Equal(t, map[string]string{projectLinkCanonicalDirectory(t, directory): project}, projectLinkRegistry(t, home))
					}
				}
			}
			for _, args := range [][]string{
				{"link", "--help"}, {"unlink", "--help"}, {"help", "link"}, {"help", "unlink"},
			} {
				got := runProjectLinkMain(t, home, directory, configuration.env, "", args...)
				require.Zero(t, got.code, "%v: %s", args, got.stderr)
				require.Empty(t, got.stderr)
				require.Contains(t, got.stdout, "openai ")
				require.NotContains(t, got.stdout, "Key setup:")
			}
		})
	}
	require.Zero(t, requests.Load(), "local linking or help reached the API")
}

func TestMainProjectLinkRequestPrecedence(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	projectLinkCreate(t, home, directory, "proj_folder")
	const response = `{"object":"list","data":[],"has_more":false}`
	for _, test := range []struct {
		name            string
		env, args, want []string
	}{
		{"saved folder", nil, []string{"files", "list"}, []string{"proj_folder"}},
		{"environment", []string{"OPENAI_PROJECT_ID=proj_environment"}, []string{"files", "list"}, []string{"proj_environment"}},
		{"empty environment", []string{"OPENAI_PROJECT_ID="}, []string{"files", "list"}, []string{""}},
		{"root flag before", []string{"OPENAI_PROJECT_ID=proj_environment"}, []string{"--project=proj_explicit", "files", "list"}, []string{"proj_explicit"}},
		{"root flag between", nil, []string{"files", "--project=proj_explicit", "list"}, []string{"proj_explicit"}},
		{"root flag after", nil, []string{"files", "list", "--project=proj_explicit"}, []string{"proj_explicit"}},
		{"empty root flag", []string{"OPENAI_PROJECT_ID=proj_environment"}, []string{"files", "list", "--project", ""}, []string{""}},
		{"explicit header", nil, []string{"files", "list", "--header=OpenAI-Project: proj_header"}, []string{"proj_header"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, requests := globalFlagsServer(t, response)
			args := append(slices.Clone(test.args), "--format=raw")
			got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server, test.env...), "", args...)
			require.Equal(t, mainDispatchResult{stdout: response + "\n"}, got)
			request := globalFlagsOneRequest(t, requests)
			require.Equal(t, http.MethodGet, request.method)
			require.Equal(t, "/files", request.path)
			require.Equal(t, test.want, request.header.Values("OpenAI-Project"))
			require.Equal(t, "Bearer synthetic-env-key", request.header.Get("Authorization"), "linking must preserve the credential")
		})
	}
	require.Equal(t, map[string]string{projectLinkCanonicalDirectory(t, directory): "proj_folder"}, projectLinkRegistry(t, home))
}

func TestMainProjectLinkDirectoryScope(t *testing.T) {
	home, parent, unrelated := t.TempDir(), t.TempDir(), t.TempDir()
	child := filepath.Join(parent, "child")
	grandchild := filepath.Join(child, "grandchild")
	require.NoError(t, os.MkdirAll(grandchild, 0o700))
	projectLinkCreate(t, home, parent, "proj_parent")
	server, requests := globalFlagsServer(t, globalFlagsPage)
	assertHeader := func(directory string, want []string) {
		t.Helper()
		got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server), "", "models", "list", "--format=raw")
		require.Equal(t, mainDispatchResult{stdout: globalFlagsPage + "\n"}, got)
		require.Equal(t, want, globalFlagsOneRequest(t, requests).header.Values("OpenAI-Project"))
	}
	assertHeader(grandchild, []string{"proj_parent"})
	assertHeader(unrelated, nil)
	projectLinkCreate(t, home, child, "proj_child")
	assertHeader(grandchild, []string{"proj_child"})
	got := runProjectLinkMain(t, home, grandchild, []string{"OPENAI_PROJECT_ID=proj_environment"}, "", "link", "--format=json")
	require.Zero(t, got.code, "%s", got.stderr)
	var inspection struct {
		Directory        string `json:"directory"`
		LinkedDirectory  string `json:"linked_directory"`
		Project          string `json:"project"`
		Inherited        bool   `json:"inherited"`
		EffectiveProject string `json:"effective_project"`
		Source           string `json:"source"`
	}
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &inspection))
	require.Equal(t, projectLinkCanonicalDirectory(t, grandchild), inspection.Directory)
	require.Equal(t, projectLinkCanonicalDirectory(t, child), inspection.LinkedDirectory)
	require.Equal(t, "proj_child", inspection.Project)
	require.True(t, inspection.Inherited)
	require.Equal(t, "proj_environment", inspection.EffectiveProject)
	require.Equal(t, "environment", inspection.Source)
	got = runProjectLinkMain(t, home, grandchild, nil, "", "unlink")
	require.Zero(t, got.code, "%s", got.stderr)
	assertHeader(grandchild, []string{"proj_child"})
	got = runProjectLinkMain(t, home, child, nil, "", "unlink")
	require.Zero(t, got.code, "%s", got.stderr)
	assertHeader(grandchild, []string{"proj_parent"})
	projectLinkCreate(t, home, unrelated, "proj_moved")
	moved := filepath.Join(t.TempDir(), "moved")
	require.NoError(t, os.Rename(unrelated, moved))
	assertHeader(moved, nil)
	assertHeader(parent, []string{"proj_parent"})
}

func TestMainProjectLinkSymlinkUsesCanonicalDirectory(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	alias := filepath.Join(t.TempDir(), "linked-directory")
	if err := os.Symlink(directory, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("Windows symlink privilege unavailable: %v", err)
		}
		t.Fatal(err)
	}
	projectLinkCreate(t, home, alias, "proj_canonical")
	require.Equal(t, map[string]string{projectLinkCanonicalDirectory(t, directory): "proj_canonical"}, projectLinkRegistry(t, home))
	server, requests := globalFlagsServer(t, globalFlagsPage)
	for _, path := range []string{directory, alias} {
		got := runProjectLinkMain(t, home, path, globalFlagsEnv(server), "", "models", "list", "--format=raw")
		require.Equal(t, mainDispatchResult{stdout: globalFlagsPage + "\n"}, got)
		require.Equal(t, []string{"proj_canonical"}, globalFlagsOneRequest(t, requests).header.Values("OpenAI-Project"))
	}
}

func TestMainProjectLinkNestedRequestFieldsAndStdin(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	projectLinkCreate(t, home, directory, "proj_folder")
	const requestJSON = `{"email":"synthetic@example.invalid","role":"reader","projects":[{"id":"proj_membership","role":"member"}]}`
	for _, route := range [][]string{{"admin", "organization", "invites"}, {"admin:organization:invites"}} {
		for _, input := range []struct {
			name, stdin, body string
			args              []string
		}{
			{"flags", "", requestJSON, []string{"--email=synthetic@example.invalid", "--role=reader", "--project", `{"id":"proj_membership","role":"member"}`}},
			{"stdin JSON", requestJSON, requestJSON, nil},
			{"stdin null projects", `{"email":"synthetic@example.invalid","role":"reader","projects":null}`, `{"email":"synthetic@example.invalid","role":"reader","projects":null}`, nil},
		} {
			t.Run(strings.Join(route, "/")+"/"+input.name, func(t *testing.T) {
				server, requests := globalFlagsServer(t, `{"id":"invite_synthetic"}`)
				args := append([]string{"--format=json"}, route...)
				args = append(args, "create")
				args = append(args, input.args...)
				got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server, "OPENAI_ADMIN_KEY=synthetic-admin-key"), input.stdin, args...)
				require.Zero(t, got.code, "%s", got.stderr)
				require.Empty(t, got.stderr)
				require.JSONEq(t, `{"id":"invite_synthetic"}`, got.stdout)
				request := globalFlagsOneRequest(t, requests)
				require.Equal(t, "/organization/invites", request.path)
				require.Equal(t, http.MethodPost, request.method)
				require.Equal(t, []string{"proj_folder"}, request.header.Values("OpenAI-Project"))
				require.Equal(t, "Bearer synthetic-admin-key", request.header.Get("Authorization"))
				require.JSONEq(t, input.body, string(request.body))
			})
		}
	}
}

func TestMainProjectLinkMalformedRegistryFailsBeforeRequests(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	path := projectLinkRegistryPath(home)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	const malformed = `{"synthetic-private-path":`
	require.NoError(t, os.WriteFile(path, []byte(malformed), 0o600))
	server, requests := globalFlagsServer(t, globalFlagsPage)
	for _, format := range []string{"text", "json"} {
		for _, args := range [][]string{{"models", "list"}, {"link"}, {"link", "--project=proj_safe"}, {"unlink"}} {
			argv := append(slices.Clone(args), "--format-error", format)
			got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server), "", argv...)
			require.NotZero(t, got.code, "%v", argv)
			require.Empty(t, got.stdout)
			require.NotEmpty(t, got.stderr)
			require.NotContains(t, got.stderr, "synthetic-private-path")
			require.NotContains(t, got.stderr, home)
			if format == "json" {
				decodeMainStructuredError(t, format, got.stderr)
			}
			require.Empty(t, requests, "invalid registry reached the API")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, malformed, string(data), "failed mutation replaced prior registry")
		}
	}
	for _, override := range []struct {
		env, flags, want []string
	}{
		{nil, []string{"--project=proj_explicit"}, []string{"proj_explicit"}},
		{nil, []string{"--project="}, []string{""}},
		{nil, []string{"--header=OpenAI-Project: proj_header"}, []string{"proj_header"}},
		{nil, []string{"--header=OpenAI-Project:"}, []string{""}},
		{[]string{"OPENAI_PROJECT_ID=proj_environment"}, nil, []string{"proj_environment"}},
		{[]string{"OPENAI_PROJECT_ID="}, nil, []string{""}},
	} {
		args := append([]string{"models", "list", "--format=raw"}, override.flags...)
		got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server, override.env...), "", args...)
		require.Equal(t, mainDispatchResult{stdout: globalFlagsPage + "\n"}, got)
		require.Equal(t, override.want, globalFlagsOneRequest(t, requests).header.Values("OpenAI-Project"))
	}
	for _, args := range [][]string{{"link", "--help"}, {"unlink", "--help"}, {"help", "link"}} {
		got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server), "", args...)
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		require.Empty(t, requests)
	}
}

func TestMainProjectLinkKeepsAPIAccessFailure(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	projectLinkCreate(t, home, directory, "proj_no_access")
	requests := make(chan http.Header, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"Synthetic key cannot access this project","type":"permission_error","code":"project_forbidden"}}`))
	}))
	t.Cleanup(server.Close)
	got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server), "", "files", "list", "--format-error=json")
	require.NotZero(t, got.code)
	require.Empty(t, got.stdout)
	decodeMainStructuredError(t, "json", got.stderr)
	require.Contains(t, got.stderr, "Synthetic key cannot access this project")
	require.NotContains(t, got.stderr, "synthetic-env-key")
	require.Len(t, requests, 1)
	header := <-requests
	require.Equal(t, "proj_no_access", header.Get("OpenAI-Project"))
	require.Equal(t, "Bearer synthetic-env-key", header.Get("Authorization"))
	require.Equal(t, "proj_no_access", projectLinkRegistry(t, home)[projectLinkCanonicalDirectory(t, directory)])
}

func TestMainProjectLinkRejectsInvalidIdentifiersWithoutMutation(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	projectLinkCreate(t, home, directory, "proj_retained")
	before, err := os.ReadFile(projectLinkRegistryPath(home))
	require.NoError(t, err)
	server, requests := localUtilitiesRequestTrap(t)
	for _, project := range []string{"", "synthetic-key", "proj_bad\nvalue", "proj_bad\x1b[31mvalue"} {
		got := runProjectLinkMain(t, home, directory, []string{"OPENAI_BASE_URL=" + server.URL}, "", "link", "--project", project)
		require.NotZero(t, got.code)
		require.Empty(t, got.stdout)
		require.NotContains(t, got.stderr, "\x1b")
		require.NotContains(t, got.stderr, "synthetic-key")
		after, err := os.ReadFile(projectLinkRegistryPath(home))
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
	require.Zero(t, requests.Load())
}

func TestMainProjectLinkEscapesDirectoryControls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames do not accept these control characters")
	}
	home := t.TempDir()
	directory := filepath.Join(t.TempDir(), "folder-\x1b]0;synthetic-title\a")
	require.NoError(t, os.Mkdir(directory, 0o700))
	for _, args := range [][]string{{"link", "--project=proj_controls"}, {"link"}, {"unlink"}} {
		got := runProjectLinkMain(t, home, directory, nil, "", args...)
		require.Zero(t, got.code, "%s", got.stderr)
		require.Empty(t, got.stderr)
		require.NotContains(t, got.stdout, "\x1b")
		require.NotContains(t, got.stdout, "\a")
	}
}

func TestMainProjectLinkUnlinkedConfigurationCompatibility(t *testing.T) {
	const response = `{"object":"list","data":[],"has_more":false}`
	for _, name := range []string{
		"existing shared-readable configuration directory", "symlinked configuration directory",
		"no configuration location", "repository-provided configuration",
	} {
		t.Run(name, func(t *testing.T) {
			home, directory := t.TempDir(), t.TempDir()
			switch name {
			case "existing shared-readable configuration directory":
				config := filepath.Dir(projectLinkRegistryPath(home))
				require.NoError(t, os.MkdirAll(config, 0o755))
				require.NoError(t, os.Chmod(config, 0o755))
			case "symlinked configuration directory":
				if runtime.GOOS == "windows" {
					t.Skip("this case exercises Unix configuration-directory symlinks")
				}
				config := filepath.Dir(projectLinkRegistryPath(home))
				require.NoError(t, os.MkdirAll(filepath.Dir(config), 0o700))
				require.NoError(t, os.Symlink(t.TempDir(), config))
			case "no configuration location":
				home = ""
			case "repository-provided configuration":
				data, err := json.Marshal(map[string]string{projectLinkCanonicalDirectory(t, directory): "proj_untrusted_repository"})
				require.NoError(t, err)
				for _, path := range []string{
					filepath.Join(directory, "project-links.json"),
					filepath.Join(directory, ".openai", "project-links.json"),
				} {
					require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
					require.NoError(t, os.WriteFile(path, data, 0o600))
				}
			}
			server, requests := globalFlagsServer(t, response)
			got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server), "", "files", "list", "--format=raw")
			require.Equal(t, mainDispatchResult{stdout: response + "\n"}, got)
			request := globalFlagsOneRequest(t, requests)
			require.Equal(t, "/files", request.path)
			require.Nil(t, request.header.Values("OpenAI-Project"))
			require.Equal(t, "Bearer synthetic-env-key", request.header.Get("Authorization"))
		})
	}
}

func TestMainProjectLinkInspectionRedactsUnexpectedEnvironmentValues(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	projectLinkCreate(t, home, directory, "proj_saved")
	server, requests := localUtilitiesRequestTrap(t)
	for _, test := range []struct {
		name, value, privateMarker string
		redacted                   bool
	}{
		{"credential-looking value", "sk-synthetic-secret", "synthetic-secret", true},
		{"terminal controls", "proj_\x1b]0;synthetic-control\a", "synthetic-control", true},
		{"URL-like value", "https://synthetic.invalid/private?token=synthetic-url-token", "synthetic-url-token", true},
		{"valid identifier", "proj_environment", "", false},
		{"explicit empty", "", "", false},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_PROJECT_ID=" + test.value}
				got := runProjectLinkMain(t, home, directory, env, "", "link", "--format", format)
				require.Zero(t, got.code, "%s", got.stderr)
				require.Empty(t, got.stderr)
				require.Contains(t, got.stdout, "proj_saved")
				require.NotContains(t, got.stdout, "\x1b")
				require.NotContains(t, got.stdout, "\a")
				if test.redacted {
					require.NotContains(t, got.stdout, test.value)
					require.NotContains(t, got.stdout, test.privateMarker)
				} else if test.value != "" {
					require.Contains(t, got.stdout, test.value)
				}
				if format == "json" {
					var status struct {
						Project          string `json:"project"`
						EffectiveProject string `json:"effective_project"`
						Source           string `json:"source"`
						Redacted         bool   `json:"effective_project_redacted"`
					}
					require.NoError(t, json.Unmarshal([]byte(got.stdout), &status))
					require.Equal(t, "proj_saved", status.Project)
					require.Equal(t, "environment", status.Source)
					require.Equal(t, test.redacted, status.Redacted)
					want := test.value
					if test.redacted {
						want = ""
					}
					require.Equal(t, want, status.EffectiveProject)
				} else if test.value == "" {
					require.Contains(t, got.stdout, "OPENAI_PROJECT_ID is empty")
				}
			})
		}
	}
	require.Zero(t, requests.Load(), "inspection reached the API")
	require.Equal(t, map[string]string{projectLinkCanonicalDirectory(t, directory): "proj_saved"}, projectLinkRegistry(t, home))
}
