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
		"auth", "switch", "--to-project=proj_next", "--format=json")
	require.Zero(t, got.code, "%s", got.stderr)
	var receipt struct {
		Profile          struct{ Values map[string]string }
		EffectiveProject authStatusValueResult `json:"effective_project"`
	}
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &receipt))
	require.Equal(t, "proj_next", receipt.Profile.Values["project"])
	require.Equal(t, authStatusValueResult{Value: "proj_from_shell", Source: "environment:OPENAI_PROJECT_ID"}, receipt.EffectiveProject)
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

func TestMainProfileContextPreservesMalformedRegistry(t *testing.T) {
	home := t.TempDir()
	env := profileProcessEnvironment(home)
	run := func(args ...string) mainDispatchResult {
		return runMainDispatchWithEnv(t, "bash", env, append([]string{"openai"}, args...)...)
	}
	got := run("profile", "create", "work")
	require.Zero(t, got.code, "%s", got.stderr)
	var registry string
	require.NoError(t, filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.Name() == "profiles.json" {
			registry = path
		}
		return err
	}))
	require.NotEmpty(t, registry)
	const damaged = "{damaged"
	require.NoError(t, os.WriteFile(registry, []byte(damaged), 0600))
	got = run("--profile=work", "auth", "switch", "--to-project=proj_other")
	require.NotZero(t, got.code)
	after, err := os.ReadFile(registry)
	require.NoError(t, err)
	require.Equal(t, damaged, string(after))
}
