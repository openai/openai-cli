package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func profileProcessEnvironment(home string) []string {
	return []string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home,
		"XDG_CONFIG_HOME=" + home, "LOCALAPPDATA=" + home, "OPENAI_PICKER_SHELL=off",
		"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=127.0.0.1,localhost"}
}

func TestMainProfileContextReachesRequests(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	env := append(profileProcessEnvironment(t.TempDir()), globalFlagsEnv(server)...)
	run := func(extraEnv, flags []string, command ...string) mainDispatchResult {
		t.Helper()
		args := slices.Concat([]string{"openai"}, flags, command)
		return runMainDispatchWithEnv(t, "bash", slices.Concat(env, extraEnv), args...)
	}
	for _, args := range [][]string{
		{"profile", "create", "work"},
		{"profile", "set", "work", "--organization=org_work", "--project=proj_staging"},
		{"profile", "activate", "work"},
	} {
		got := run(nil, nil, args...)
		require.Zero(t, got.code, "%s", got.stderr)
	}
	const custom = "OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_custom"
	for _, tc := range []struct {
		name        string
		env, flags  []string
		wantProject string
		wantSource  string
	}{
		{"fresh process", nil, nil, "proj_staging", "profile:work"},
		{"profile beats custom header", []string{custom}, nil, "proj_staging", "profile:work"},
		{"environment", []string{"OPENAI_PROJECT_ID=proj_env"}, nil, "proj_env", "environment:OPENAI_PROJECT_ID"},
		{"one invocation", []string{"OPENAI_PROJECT_ID=proj_env"}, []string{"--project=proj_flag"}, "proj_flag", "flag:--project"},
		{"literal header", nil, []string{"--project=proj_flag", "-H", "OpenAI-Project: proj_header"}, "proj_header", "flag:--header"},
		{"empty explicit project", nil, []string{"--project="}, "", "flag:--project"},
		{"disable profile", []string{custom}, []string{"--profile="}, "proj_custom", "environment:OPENAI_CUSTOM_HEADERS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := decodeAuthStatus(t, run(tc.env, tc.flags, "auth", "status", "--format=json"))
			require.Equal(t, tc.wantProject, report.Project.Value)
			require.Equal(t, tc.wantSource, report.Project.Source)
			require.Empty(t, requests, "local operations contacted the server")
			got := run(tc.env, tc.flags, "models", "list", "--format=raw")
			require.Zero(t, got.code, "%s", got.stderr)
			req := globalFlagsOneRequest(t, requests)
			require.Equal(t, "/models", req.path)
			require.Equal(t, report.Project.Value, req.header.Get("OpenAI-Project"))
			require.Equal(t, report.Organization.Value, req.header.Get("OpenAI-Organization"))
			require.Equal(t, "Bearer synthetic-env-key", req.header.Get("Authorization"))
		})
	}

	got := run([]string{"OPENAI_PROJECT_ID=proj_from_shell"}, nil,
		"profile", "set", "work", "--project=proj_next", "--format=json")
	require.Zero(t, got.code, "%s", got.stderr)
	var receipt struct {
		Profile struct{ Values map[string]string }
	}
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &receipt))
	require.Equal(t, "proj_next", receipt.Profile.Values["project"])
	require.Equal(t, "proj_from_shell", decodeAuthStatus(t, run([]string{"OPENAI_PROJECT_ID=proj_from_shell"}, nil, "auth", "status", "--format=json")).Project.Value)
	require.Equal(t, "proj_next", decodeAuthStatus(t, run(nil, nil, "auth", "status", "--format=json")).Project.Value)
	got = run(nil, []string{"--profile=missing"}, "models", "list")
	require.NotZero(t, got.code)
	require.Empty(t, requests, "unknown profiles must fail before dispatch")

	denied, attempts := globalFlagsServer(t, `{"error":{"message":"denied","type":"permission_error"}}`, 403)
	got = run([]string{"OPENAI_BASE_URL=" + denied.URL}, []string{"--project=proj_denied"}, "models", "list")
	require.NotZero(t, got.code)
	require.Equal(t, "proj_denied", globalFlagsOneRequest(t, attempts).header.Get("OpenAI-Project"))
	require.Equal(t, "proj_next", decodeAuthStatus(t, run(nil, nil, "auth", "status", "--format=json")).Project.Value)
}

func TestMainProfileHeaderWhitespaceMatchesWire(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	home := t.TempDir()
	env := append(profileProcessEnvironment(home), globalFlagsEnv(server, "OPENAI_CUSTOM_HEADERS=OpenAI-Project: lower_default")...)
	run := func(args ...string) mainDispatchResult {
		t.Helper()
		return runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, args...)...)
	}
	require.Zero(t, run("profile", "create", "work").code)
	require.Zero(t, run("profile", "activate", "work").code)
	check := func(project string) {
		t.Helper()
		report := decodeAuthStatus(t, run("auth", "status", "--format=json"))
		require.Equal(t, authStatusValueResult{Value: project, Source: "profile:work"}, report.Project)
		got := run("models", "list", "--format=raw")
		require.Zero(t, got.code, "%s", got.stderr)
		require.Equal(t, project, globalFlagsOneRequest(t, requests).header.Get("OpenAI-Project"))
	}
	for _, tc := range []struct{ saved, effective string }{
		{"proj_team", "proj_team"}, {"", ""}, {"   ", ""}, {"  proj team/a  ", "proj team/a"},
	} {
		got := run("profile", "set", "work", "--project="+tc.saved, "--format=json")
		require.Zero(t, got.code, "%s", got.stderr)
		var receipt struct {
			Profile struct{ Values map[string]string }
		}
		require.NoError(t, json.Unmarshal([]byte(got.stdout), &receipt))
		require.Equal(t, tc.effective, receipt.Profile.Values["project"])
		check(tc.effective)
	}
	require.NotZero(t, run("profile", "set", "work", "--project=\tproj_bad").code)
	check("proj team/a")
	// Restore an older profile file and verify its effective value in a fresh process.
	path := profileRegistryPath(t, home)
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"default_profile":"work","profiles":{"work":{"project":" proj_old "}}}`), 0600))
	check("proj_old")
}

func profileRegistryPath(t *testing.T, home string) string {
	t.Helper()
	var registry string
	require.NoError(t, filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.Name() == "profiles.json" {
			registry = path
		}
		return err
	}))
	require.NotEmpty(t, registry)
	return registry
}

func TestMainProfileContextPreservesMalformedRegistry(t *testing.T) {
	home := t.TempDir()
	env := profileProcessEnvironment(home)
	run := func(args ...string) mainDispatchResult {
		return runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, args...)...)
	}
	got := run("profile", "create", "work")
	require.Zero(t, got.code, "%s", got.stderr)
	registry := profileRegistryPath(t, home)
	const damaged = "{damaged"
	require.NoError(t, os.WriteFile(registry, []byte(damaged), 0600))
	got = run("profile", "set", "work", "--project=proj_other")
	require.NotZero(t, got.code)
	after, err := os.ReadFile(registry)
	require.NoError(t, err)
	require.Equal(t, damaged, string(after))
}

func TestMainProfileRejectsConfiguredCredentialsBeforeDispatch(t *testing.T) {
	server, requests := globalFlagsServer(t, globalFlagsPage)
	env := append(profileProcessEnvironment(t.TempDir()), globalFlagsEnv(server)...)
	run := func(settings []string, args ...string) mainDispatchResult {
		t.Helper()
		return runMainDispatchWithEnv(t, "bash", slices.Concat(env, settings), append([]string{"openai"}, args...)...)
	}
	const secret = "synthetic-credential-for-profile-test"
	key := []string{"OPENAI_ADMIN_KEY=" + secret + " "}
	require.Zero(t, run(nil, "profile", "create", "team").code)
	require.Zero(t, run(nil, "profile", "activate", "team").code)

	for _, field := range []string{"organization", "project"} {
		t.Run(field, func(t *testing.T) {
			// A profile created before this credential was configured remains inspectable and repairable.
			flag := "--" + field
			got := run(key, "profile", "set", "team", flag+"="+secret)
			require.NotZero(t, got.code)
			require.NotContains(t, got.stdout+got.stderr, secret)
			require.Zero(t, run(nil, "profile", "set", "team", flag+"="+secret).code)
			for _, args := range [][]string{{"auth", "status", "--format=json"}, {"profile", "get", "team"}, {"profile", "list", "--format=jsonl"}} {
				got = run(key, args...)
				require.Zero(t, got.code, "%s", got.stderr)
				require.Contains(t, got.stdout, "[redacted]")
				require.NotContains(t, got.stdout+got.stderr, secret)
			}
			got = run(key, "--debug", "models", "list")
			require.NotZero(t, got.code)
			require.Contains(t, got.stderr, "contains a configured credential")
			require.NotContains(t, got.stdout+got.stderr, secret)
			require.Empty(t, requests)
			got = run(key, flag+"=explicit_safe", "models", "list", "--format=raw")
			require.Zero(t, got.code, "%s", got.stderr)
			req := globalFlagsOneRequest(t, requests)
			require.Equal(t, "explicit_safe", req.header.Get("OpenAI-"+field))
			require.Equal(t, "Bearer synthetic-env-key", req.header.Get("Authorization"))
			got = run(key, "profile", "set", "team", "--unset="+field)
			require.Zero(t, got.code, "%s", got.stderr)
			require.NotContains(t, got.stdout+got.stderr, secret)
		})
	}
	require.Zero(t, run(nil, "profile", "create", secret).code)
	got := run(key, "profile", "activate", secret)
	require.NotZero(t, got.code)
	require.NotContains(t, got.stdout+got.stderr, secret)
	got = run(key, "profile", "delete", secret)
	require.Zero(t, got.code, "%s", got.stderr)
	require.NotContains(t, got.stdout+got.stderr, secret)
	require.Empty(t, requests)
}
