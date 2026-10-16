package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func profileCommandTestRoot(out io.Writer) *cli.Command {
	root := authStatusTestRoot(out)
	configureProfileContext(root)
	registerProfileCommands(root)
	configureOutputPolicy(root)
	return root
}

func profileCommandTestEnvironment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME"} {
		t.Setenv(name, home)
	}
	authStatusTestEnvironment(t, nil)
	t.Setenv("OPENAI_PROFILE", "")
	require.NoError(t, os.Unsetenv("OPENAI_PROFILE"))
	path, err := profileStorePath()
	require.NoError(t, err)
	return path
}

func runProfileCommand(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, profileCommandTestRoot(&out).Run(t.Context(), append([]string{"openai"}, args...)))
	return out.String()
}

func TestProfileCommandLifecycle(t *testing.T) {
	path := profileCommandTestEnvironment(t)
	output := runProfileCommand(t, "--format=json", "profile", "create", "work")
	require.JSONEq(t, `{"action":"create","profile":{"name":"work","active":false,"values":{}}}`, output)
	registry, err := loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Nil(t, registry.DefaultProfile)
	require.Equal(t, profileValues{}, registry.Profiles["work"])
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.ErrorContains(t, profileCommandTestRoot(io.Discard).Run(t.Context(), []string{"openai", "profile", "create", "work"}), "already exists")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)

	runProfileCommand(t, "profile", "set", "work", "--organization=", "--project=proj_demo", "--base-url=")
	output = runProfileCommand(t, "--format=jsonl", "profile", "get", "work")
	require.JSONEq(t, `{"name":"work","active":false,"values":{"organization":"","project":"proj_demo","base_url":""}}`, output)
	require.Equal(t, 1, strings.Count(output, "\n"))
	output = runProfileCommand(t, "--format=json", "profile", "activate", "work")
	require.JSONEq(t, `{"action":"activate","profile":{"name":"work","active":true,"values":{"organization":"","project":"proj_demo","base_url":""}}}`, output)
	before, err = os.ReadFile(path)
	require.NoError(t, err)
	require.ErrorContains(t, profileCommandTestRoot(io.Discard).Run(t.Context(), []string{"openai", "profile", "delete", "work"}), "--deactivate")
	after, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	output = runProfileCommand(t, "--format=json", "profile", "delete", "work", "--deactivate")
	require.JSONEq(t, `{"action":"delete","name":"work","deactivated":true}`, output)
	registry, err = loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Nil(t, registry.DefaultProfile)
	require.Empty(t, registry.Profiles)
}

func TestProfileSetOnlyPersistsLocalExplicitEdits(t *testing.T) {
	path := profileCommandTestEnvironment(t)
	t.Setenv("OPENAI_ORG_ID", "org_environment")
	t.Setenv("OPENAI_PROJECT_ID", "proj_environment")
	t.Setenv("OPENAI_BASE_URL", "https://environment.invalid/v1")
	t.Setenv("OPENAI_PROFILE", "unknown")
	runProfileCommand(t, "--project=proj_root", "--organization=org_root", "profile", "create", "work")
	registry, err := loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, profileValues{}, registry.Profiles["work"])
	for _, flags := range [][]string{nil, {"--project=proj_root"}, {"--organization=org_root"}, {"--base-url=https://root.invalid/v1"}} {
		args := append([]string{"openai"}, flags...)
		args = append(args, "profile", "set", "work")
		require.ErrorContains(t, profileCommandTestRoot(io.Discard).Run(t.Context(), args), "No saved edits")
	}
	runProfileCommand(t, "--project=proj_root", "--base-url=https://root.invalid/v1", "profile", "set", "work", "--organization=org_saved")
	registry, err = loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, "org_saved", *registry.Profiles["work"].Organization)
	require.Nil(t, registry.Profiles["work"].Project)
	require.Nil(t, registry.Profiles["work"].BaseURL)
	runProfileCommand(t, "profile", "set", "work", "--project=", "--base-url=", "--unset", "organization")
	registry, err = loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Nil(t, registry.Profiles["work"].Organization)
	require.Equal(t, "", *registry.Profiles["work"].Project)
	require.Equal(t, "", *registry.Profiles["work"].BaseURL)
	runProfileCommand(t, "profile", "set", "work", "--unset", "project", "--unset", "base_url", "--unset", "project")
	registry, err = loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, profileValues{}, registry.Profiles["work"])
}

func TestProfileCommandReusePreservesExplicitEdits(t *testing.T) {
	path := profileCommandTestEnvironment(t)
	root := profileCommandTestRoot(io.Discard)
	run := func(args ...string) {
		t.Helper()
		require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, args...)))
	}
	check := func(organization, project, baseURL *string) {
		t.Helper()
		registry, err := loadProfiles(t.Context(), path)
		require.NoError(t, err)
		require.Equal(t, profileValues{Organization: organization, Project: project, BaseURL: baseURL}, registry.Profiles["work"])
	}
	run("profile", "create", "work")
	run("profile", "set", "work", "--project=proj_first")
	run("profile", "set", "work", "--organization=org_demo")
	check(profileCommandString("org_demo"), profileCommandString("proj_first"), nil)
	run("profile", "set", "work", "--project=proj_second", "--base-url=https://example.invalid/v1")
	check(profileCommandString("org_demo"), profileCommandString("proj_second"), profileCommandString("https://example.invalid/v1"))
	run("profile", "set", "work", "--unset=project", "--unset=organization")
	check(nil, nil, profileCommandString("https://example.invalid/v1"))
	run("profile", "set", "work", "--project=", "--organization=org_again")
	check(profileCommandString("org_again"), profileCommandString(""), profileCommandString("https://example.invalid/v1"))
	run("profile", "set", "work", "--unset=base_url")
	check(profileCommandString("org_again"), profileCommandString(""), nil)
	run("profile", "set", "work", "--base-url=")
	check(profileCommandString("org_again"), profileCommandString(""), profileCommandString(""))
	require.ErrorContains(t, root.Run(t.Context(), []string{"openai", "profile", "set", "work"}), "No saved edits")
	check(profileCommandString("org_again"), profileCommandString(""), profileCommandString(""))
	for _, duplicate := range [][]string{
		{"--project=proj_one", "--project=proj_two"},
		{"--organization=org_one", "--organization=org_two"},
		{"--base-url=", "--base-url=https://example.invalid/v1"},
	} {
		args := append([]string{"openai", "profile", "set", "work"}, duplicate...)
		require.Error(t, root.Run(t.Context(), args))
		check(profileCommandString("org_again"), profileCommandString(""), profileCommandString(""))
	}
	run("profile", "set", "work", "--project=proj_after_failure")
	check(profileCommandString("org_again"), profileCommandString("proj_after_failure"), profileCommandString(""))
}

func TestProfileCommandReuseResetsSwitchAndDeactivation(t *testing.T) {
	path := profileCommandTestEnvironment(t)
	root := profileCommandTestRoot(io.Discard)
	run := func(args ...string) {
		t.Helper()
		require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, args...)))
	}
	run("profile", "create", "work")
	run("profile", "activate", "work")
	run("auth", "switch", "--to-project=proj_first")
	run("auth", "switch", "--to-project=proj_second")
	require.Error(t, root.Run(t.Context(), []string{"openai", "auth", "switch"}))
	require.Error(t, root.Run(t.Context(), []string{"openai", "auth", "switch", "--to-project=proj_three", "--to-project=proj_four"}))
	registry, err := loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, "proj_second", *registry.Profiles["work"].Project)
	run("auth", "switch", "--to-project=proj_after_failure")
	run("profile", "delete", "work", "--deactivate")
	for range 2 {
		run("profile", "create", "work")
		run("profile", "activate", "work")
		for _, flags := range [][]string{nil, {"--deactivate=false"}, {"--deactivate", "--deactivate"}} {
			args := append([]string{"openai", "profile", "delete", "work"}, flags...)
			require.Error(t, root.Run(t.Context(), args))
			registry, err = loadProfiles(t.Context(), path)
			require.NoError(t, err)
			require.Equal(t, "work", *registry.DefaultProfile)
			require.Contains(t, registry.Profiles, "work")
		}
		run("profile", "delete", "work", "--deactivate")
	}
}

func TestProfileCommandsRejectBeforeMutation(t *testing.T) {
	path := profileCommandTestEnvironment(t)
	for _, args := range [][]string{
		{"profile", "create", "work", "--format=yaml"},
		{"profile", "create", "work", "--transform=name"},
		{"profile", "create", "work", "--raw-output=false"},
		{"profile", "create"}, {"profile", "create", ""},
		{"profile", "create", " work"}, {"profile", "create", "work\nspoof"},
		{"profile", "create", "work", "extra"}, {"profile", "list", "extra"},
		{"auth", "switch"}, {"auth", "switch", "--to-project="},
		{"auth", "switch", "--to-project=proj_demo", "extra"},
	} {
		var out bytes.Buffer
		require.Error(t, profileCommandTestRoot(&out).Run(t.Context(), append([]string{"openai"}, args...)), "%q", args)
		require.Empty(t, out.String(), "%q", args)
		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	runProfileCommand(t, "profile", "create", "work")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, flags := range [][]string{
		{"--project=proj_demo", "--unset=project"},
		{"--base-url=", "--unset=base_url"},
		{"--unset=api_key"}, {"--unset=project,organization"},
		{"--base-url=https://user:synthetic-secret@example.invalid/v1"},
		{"--base-url=https://example.invalid/v1?token=synthetic-secret"},
		{"--base-url=https://example.invalid/v1#synthetic-secret"},
	} {
		args := append([]string{"openai", "profile", "set", "work"}, flags...)
		err := profileCommandTestRoot(io.Discard).Run(t.Context(), args)
		require.Error(t, err, "%q", flags)
		require.NotContains(t, err.Error(), "synthetic-secret")
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
}

func TestProfileListOrderingFormatsAndSelectorRecovery(t *testing.T) {
	profileCommandTestEnvironment(t)
	t.Setenv("OPENAI_PROFILE", "missing")
	require.JSONEq(t, `[]`, runProfileCommand(t, "--format=json", "profile", "list"))
	require.Empty(t, runProfileCommand(t, "--format=jsonl", "profile", "list"))
	for _, name := range []string{"é", "work", "Work", "work/qa"} {
		runProfileCommand(t, "--profile=also-missing", "profile", "create", name)
	}
	runProfileCommand(t, "profile", "activate", "work")
	var records []profileRecord
	require.NoError(t, json.Unmarshal([]byte(runProfileCommand(t, "--format=json", "profile", "list")), &records))
	var names []string
	for _, record := range records {
		names = append(names, record.Name)
		require.Equal(t, record.Name == "work", record.Active)
	}
	require.Equal(t, []string{"Work", "work", "work/qa", "é"}, names)
	lines := strings.Split(strings.TrimSuffix(runProfileCommand(t, "--format=jsonl", "profile", "list"), "\n"), "\n")
	require.Len(t, lines, len(records))
	for index, line := range lines {
		var record profileRecord
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		require.Equal(t, records[index], record)
	}
	runProfileCommand(t, "profile", "set", "work", "--project=proj_demo")
	runProfileCommand(t, "profile", "get", "work")
	require.JSONEq(t, `{"action":"delete","name":"Work","deactivated":false}`, runProfileCommand(t, "--format=json", "profile", "delete", "Work"))
}

func TestAuthSwitchMutatesOnlySelectedProject(t *testing.T) {
	path := profileCommandTestEnvironment(t)
	for _, name := range []string{"default", "environment", "explicit"} {
		runProfileCommand(t, "profile", "create", name)
		runProfileCommand(t, "profile", "set", name, "--organization=org_demo", "--project=proj_old", "--base-url=https://example.invalid/v1")
	}
	runProfileCommand(t, "profile", "activate", "default")
	t.Setenv("OPENAI_PROJECT_ID", "proj_environment_override")
	for _, tc := range []struct {
		selector []string
		env      *string
		name     string
	}{
		{name: "default"},
		{name: "environment", env: profileCommandString("environment")},
		{name: "explicit", env: profileCommandString("environment"), selector: []string{"--profile=explicit"}},
	} {
		if tc.env != nil {
			t.Setenv("OPENAI_PROFILE", *tc.env)
		}
		args := append([]string{"--format=jsonl", "--project=proj_root_override"}, tc.selector...)
		args = append(args, "auth", "switch", "--to-project=proj_new")
		out := runProfileCommand(t, args...)
		var receipt profileReceipt
		require.NoError(t, json.Unmarshal([]byte(out), &receipt))
		require.Equal(t, "switch", receipt.Action)
		require.Equal(t, tc.name, receipt.Profile.Name)
		require.Equal(t, tc.name == "default", receipt.Profile.Active)
		require.Equal(t, "proj_new", *receipt.Profile.Values.Project)
		require.Equal(t, "org_demo", *receipt.Profile.Values.Organization)
		require.Equal(t, "https://example.invalid/v1", *receipt.Profile.Values.BaseURL)
		require.Equal(t, 1, strings.Count(out, "\n"))
		registry, err := loadProfiles(t.Context(), path)
		require.NoError(t, err)
		require.Equal(t, "default", *registry.DefaultProfile)
		require.Equal(t, "proj_environment_override", os.Getenv("OPENAI_PROJECT_ID"))
	}
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, selector := range []string{"--profile=", "--profile=missing"} {
		require.Error(t, profileCommandTestRoot(io.Discard).Run(t.Context(), []string{"openai", selector, "auth", "switch", "--to-project=proj_bad"}))
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
}

func profileCommandString(value string) *string { return &value }

func TestProfileCommandsPreserveReceiptsUnderQuiet(t *testing.T) {
	profileCommandTestEnvironment(t)
	for _, format := range []string{"auto", "text", "json", "jsonl"} {
		out := runProfileCommand(t, "--quiet", "--format="+format, "profile", "create", format)
		require.NotEmpty(t, out)
		if format == "json" || format == "jsonl" {
			var receipt profileReceipt
			require.NoError(t, json.Unmarshal([]byte(out), &receipt))
			require.Equal(t, "create", receipt.Action)
		} else {
			require.Contains(t, out, "Saved profile (create).")
		}
	}
}

func TestProfileCommandsOutputFailureAndCancellation(t *testing.T) {
	path := profileCommandTestEnvironment(t)
	failure := errors.New("synthetic output failure")
	err := profileCommandTestRoot(authStatusFailWriter{err: failure}).Run(t.Context(), []string{"openai", "profile", "create", "work"})
	require.ErrorIs(t, err, failure)
	require.ErrorContains(t, err, "Saved profiles were updated")
	registry, err := loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Contains(t, registry.Profiles, "work")
	err = profileCommandTestRoot(authStatusFailWriter{}).Run(t.Context(), []string{"openai", "--format=json", "profile", "get", "work"})
	require.ErrorIs(t, err, io.ErrShortWrite)
	require.ErrorContains(t, err, "did not change saved profiles")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = profileCommandTestRoot(io.Discard).Run(ctx, []string{"openai", "profile", "delete", "work"})
	require.ErrorIs(t, err, context.Canceled)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestProfileCommandsKeepStoreErrorsPrivate(t *testing.T) {
	path := profileCommandTestEnvironment(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	data := []byte(`{"version":1,"default_profile":null,"profiles":{},"api_key":"synthetic-secret"}`)
	require.NoError(t, os.WriteFile(path, data, 0600))
	err := profileCommandTestRoot(io.Discard).Run(t.Context(), []string{"openai", "profile", "create", "work"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "synthetic-secret")
	require.NotContains(t, err.Error(), filepath.Dir(path))
	require.Contains(t, err.Error(), "profiles.json")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, after)
}

func TestProfileTextEscapesEveryExternalField(t *testing.T) {
	value := "synthetic\x1b[31m\nspoof\tcolumn\r"
	var out bytes.Buffer
	err := writeProfileResult(t.Context(), &out, "text", profileRecord{
		Name: value, Values: profileValues{Organization: &value, Project: &value, BaseURL: &value},
	})
	require.NoError(t, err)
	require.NotContains(t, out.String(), "\x1b")
	require.NotContains(t, out.String(), "\nspoof")
	require.NotContains(t, out.String(), "\t")
	require.NotContains(t, out.String(), "\r")
	require.Equal(t, 4, strings.Count(out.String(), `\nspoof\tcolumn`))
}

func TestProfileHelpDescribesLocalFlagOwnership(t *testing.T) {
	path := profileCommandTestEnvironment(t)
	var out bytes.Buffer
	root := profileCommandTestRoot(&out)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "profile", "set", "--help"}))
	require.Contains(t, out.String(), "openai profile set work --project proj_demo")
	require.Equal(t, 1, strings.Count(out.String(), "EXAMPLE"))
	require.Contains(t, out.String(), "Root request flags and environment values never persist")
	require.Contains(t, out.String(), "base_url")
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	for _, name := range []string{"create", "set", "get", "list", "activate", "delete"} {
		command := root.Command("profile").Command(name)
		require.Equal(t, map[string][]string{"format": {"auto", "text", "json", "jsonl"}}, command.Metadata["completion-root-flag-values"])
	}
}
