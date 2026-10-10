package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These cases compare local inspection with the existing request-option order.
// They use synthetic headers and do not establish live project access.
func TestMainProjectLinkPlanExpInspectionMatchesRequestHeaders(t *testing.T) {
	const response = `{"object":"list","data":[],"has_more":false}`
	for _, test := range []struct {
		name, saved, source, effective string
		env, flags, headers            []string
	}{
		{name: "default", source: "default"},
		{name: "custom header fallback", source: "custom_headers", effective: "proj_custom",
			env: []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_custom"}, headers: []string{"proj_custom"}},
		{name: "custom header duplicate casing", source: "custom_headers", effective: "proj_last",
			env: []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_first\nopenai-project: proj_last"}, headers: []string{"proj_last"}},
		{name: "custom header whitespace", source: "custom_headers", effective: "proj_trimmed",
			env: []string{"OPENAI_CUSTOM_HEADERS= \tOpEnAi-PrOjEcT\t : \tproj_trimmed\t "}, headers: []string{"proj_trimmed"}},
		{name: "custom header empty", source: "custom_headers",
			env: []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_first\nopenai-project:"}, headers: []string{""}},
		{name: "unrelated malformed environment lines", source: "custom_headers", effective: "proj_custom",
			env: []string{"OPENAI_CUSTOM_HEADERS=synthetic-ignored-line\nOpenAI-Project: proj_custom\n\nsecond-ignored-line"}, headers: []string{"proj_custom"}},
		{name: "folder overrides custom header", saved: "proj_folder", source: "folder", effective: "proj_folder",
			env: []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_custom"}, headers: []string{"proj_folder"}},
		{name: "environment overrides folder and custom header", saved: "proj_folder", source: "environment", effective: "proj_environment",
			env: []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_custom", "OPENAI_PROJECT_ID=proj_environment"}, headers: []string{"proj_environment"}},
		{name: "empty environment overrides custom header", saved: "proj_folder", source: "environment",
			env: []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_custom", "OPENAI_PROJECT_ID="}, headers: []string{""}},
		{name: "explicit header overrides folder", saved: "proj_folder", source: "header", effective: "proj_explicit",
			flags: []string{"--header=OpenAI-Project: proj_explicit"}, headers: []string{"proj_explicit"}},
		{name: "explicit header overrides environment", saved: "proj_folder", source: "header", effective: "proj_explicit",
			env:   []string{"OPENAI_PROJECT_ID=proj_environment", "OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_custom"},
			flags: []string{"--header=OpenAI-Project: proj_explicit"}, headers: []string{"proj_explicit"}},
		{name: "explicit duplicate casing", saved: "proj_folder", source: "header", effective: "proj_last",
			flags: []string{"--header=OpenAI-Project: proj_first", "-H=openai-project: proj_last"}, headers: []string{"proj_last"}},
		{name: "explicit empty header", saved: "proj_folder", source: "header",
			env: []string{"OPENAI_PROJECT_ID=proj_environment"}, flags: []string{"--header=OpenAI-Project:"}, headers: []string{""}},
		{name: "unrelated explicit header preserves custom fallback", source: "custom_headers", effective: "proj_custom",
			env: []string{"OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_custom"}, flags: []string{"--header=X-Synthetic: unrelated"}, headers: []string{"proj_custom"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, directory := t.TempDir(), t.TempDir()
			if test.saved != "" {
				projectLinkCreate(t, home, directory, test.saved)
			}
			server, requests := globalFlagsServer(t, response)
			// Local inspection must tolerate invalid remote configuration without credentials.
			localEnv := append(slices.Clone(test.env), "OPENAI_BASE_URL=://synthetic-unusable-endpoint",
				"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-unusable/certificate")
			args := append([]string{"link", "--format=json"}, test.flags...)
			got := runProjectLinkMain(t, home, directory, localEnv, "", args...)
			require.Zero(t, got.code, "%s", got.stderr)
			require.Empty(t, got.stderr)
			var status struct {
				Project          string `json:"project"`
				EffectiveProject string `json:"effective_project"`
				Source           string `json:"source"`
				Redacted         bool   `json:"effective_project_redacted"`
			}
			require.NoError(t, json.Unmarshal([]byte(got.stdout), &status))
			require.Equal(t, test.saved, status.Project)
			require.Equal(t, test.source, status.Source)
			require.Equal(t, test.effective, status.EffectiveProject)
			require.False(t, status.Redacted)
			require.Empty(t, requests)
			args = append([]string{"files", "list", "--format=raw"}, test.flags...)
			got = runProjectLinkMain(t, home, directory, globalFlagsEnv(server, test.env...), "", args...)
			require.Equal(t, mainDispatchResult{stdout: response + "\n"}, got)
			request := globalFlagsOneRequest(t, requests)
			require.Equal(t, http.MethodGet, request.method)
			require.Equal(t, "/files", request.path)
			require.Equal(t, test.headers, request.header.Values("OpenAI-Project"))
			require.Equal(t, status.EffectiveProject, request.header.Get("OpenAI-Project"))
			require.Equal(t, "Bearer synthetic-env-key", request.header.Get("Authorization"))
			if test.saved != "" {
				require.Equal(t, map[string]string{projectLinkCanonicalDirectory(t, directory): test.saved}, projectLinkRegistry(t, home))
			}
		})
	}
}

func TestMainProjectLinkPlanExpCustomProjectRedaction(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	for _, source := range []string{"custom_headers", "header"} {
		for _, value := range []string{"sk-synthetic-project-secret", "https://synthetic.invalid/private?token=synthetic-project-secret"} {
			for _, format := range []string{"text", "json"} {
				t.Run(source+"/"+strings.SplitN(value, ":", 2)[0]+"/"+format, func(t *testing.T) {
					home, directory := t.TempDir(), t.TempDir()
					env := []string{"OPENAI_BASE_URL=" + server.URL}
					args := []string{"link", "--format", format}
					if source == "header" {
						projectLinkCreate(t, home, directory, "proj_saved")
						args = append(args, "--header=OpenAI-Project: "+value)
					} else {
						env = append(env, "OPENAI_CUSTOM_HEADERS=OpenAI-Project: "+value)
					}
					got := runProjectLinkMain(t, home, directory, env, "", args...)
					require.Zero(t, got.code, "%s", got.stderr)
					require.Empty(t, got.stderr)
					require.NotContains(t, got.stdout, value)
					require.NotContains(t, got.stdout, "synthetic-project-secret")
					if format == "json" {
						var status map[string]any
						require.NoError(t, json.Unmarshal([]byte(got.stdout), &status))
						require.Equal(t, source, status["source"])
						require.Equal(t, true, status["effective_project_redacted"])
						require.Equal(t, "", status["effective_project"])
						if source == "header" {
							require.Equal(t, "proj_saved", status["project"])
						}
					} else {
						require.Contains(t, strings.ToLower(got.stdout), "hidden")
					}
				})
			}
		}
	}
	require.Zero(t, requests.Load(), "local inspection contacted the API")
}

func TestMainProjectLinkPlanExpRemoteFilenameRecipe(t *testing.T) {
	home, directory := t.TempDir(), t.TempDir()
	projectLinkCreate(t, home, directory, "proj_work")
	require.NoError(t, os.WriteFile(filepath.Join(directory, "local-only.txt"), []byte("synthetic local file"), 0o600))
	const response = `{"object":"list","data":[{"id":"file_remote_notes","object":"file","filename":"remote-notes.txt"},{"id":"file_remote_data","object":"file","filename":"remote-data.jsonl"}],"has_more":false}`
	server, requests := globalFlagsServer(t, response)
	got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server), "", "files", "list", "--transform", "filename", "--raw-output")
	require.Equal(t, mainDispatchResult{stdout: "remote-notes.txt\nremote-data.jsonl\n"}, got)
	request := globalFlagsOneRequest(t, requests)
	require.Equal(t, "/files", request.path)
	require.Equal(t, "proj_work", request.header.Get("OpenAI-Project"))
}

func TestMainProjectLinkPlanExpStoreFailuresExplainKnownCause(t *testing.T) {
	for _, kind := range []string{"size", "directory privacy", "file privacy", "permission denial"} {
		t.Run(kind, func(t *testing.T) {
			if kind != "size" && runtime.GOOS == "windows" {
				t.Skip("Unix permission semantics require native Unix execution")
			}
			home, directory := t.TempDir(), t.TempDir()
			path := projectLinkRegistryPath(home)
			config := filepath.Dir(path)
			require.NoError(t, os.MkdirAll(config, 0o700))
			project := "proj_saved"
			if kind == "size" {
				project = "proj_" + strings.Repeat("x", 1<<20)
			}
			before, err := json.Marshal(map[string]string{projectLinkCanonicalDirectory(t, directory): project})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, before, 0o600))
			var required []string
			switch kind {
			case "size":
				required = []string{"1 MiB"}
			case "directory privacy":
				require.NoError(t, os.Chmod(config, 0o755))
				required = []string{"private", "directory", "0700"}
			case "file privacy":
				require.NoError(t, os.Chmod(path, 0o644))
				required = []string{"private", "regular", "project-links.json", "0600"}
			case "permission denial":
				require.NoError(t, os.Chmod(path, 0o000))
				t.Cleanup(func() { require.NoError(t, os.Chmod(path, 0o600)) })
				probe, err := os.Open(path)
				if err == nil {
					require.NoError(t, probe.Close())
					t.Skip("current privileges bypass this file's read permissions")
				}
				require.True(t, errors.Is(err, os.ErrPermission), "permission probe: %v", err)
				required = []string{"permission"}
			}
			server, requests := globalFlagsServer(t, globalFlagsPage)
			for _, format := range []string{"text", "json"} {
				for _, command := range [][]string{{"link"}, {"link", "--project=proj_replacement"}, {"files", "list"}} {
					args := append(slices.Clone(command), "--format-error", format)
					got := runProjectLinkMain(t, home, directory, globalFlagsEnv(server), "", args...)
					require.NotZero(t, got.code, "%v", args)
					require.Empty(t, got.stdout)
					message := got.stderr
					if format == "json" {
						message, _ = decodeMainStructuredError(t, format, got.stderr)["message"].(string)
					}
					for _, expected := range required {
						require.Contains(t, strings.ToLower(message), strings.ToLower(expected))
					}
					require.Contains(t, message, "This command did not change saved links.")
					require.NotContains(t, strings.ToLower(message), "invalid settings")
					require.NotContains(t, strings.ToLower(message), "invalid registry")
					require.NotContains(t, message, home)
					require.Empty(t, requests, "registry failure reached the API")
				}
			}
			require.NoError(t, os.Chmod(path, 0o600))
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestMainProjectLinkPlanExpCopiedRecoveryPreservesInvocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this copied-command case executes a native Unix Bash shell")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is unavailable")
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	work := filepath.Join(t.TempDir(), "CLI with spaces & apostrophe's")
	require.NoError(t, os.Mkdir(work, 0o700))
	binary, err := os.Executable()
	require.NoError(t, err)
	executable := filepath.Join(work, "openai")
	// This launcher exercises main(), preserving the invoked openai path in argv.
	launcher := "#!/bin/sh\nOPENAI_CLI_MAIN_DISPATCH_PROCESS=1 exec " + quote(binary) +
		" -test.run='^TestMainDispatchProcess$' -- \"$0\" \"$@\"\n"
	require.NoError(t, os.WriteFile(executable, []byte(launcher), 0o700))
	decoy := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(decoy, "openai"), []byte("#!/bin/sh\nprintf 'wrong executable\\n'\nexit 99\n"), 0o700))
	t.Setenv("PATH", decoy+string(os.PathListSeparator)+os.Getenv("PATH"))
	home, directory := t.TempDir(), t.TempDir()
	registry := projectLinkRegistryPath(home)
	require.NoError(t, os.MkdirAll(filepath.Dir(registry), 0o700))
	require.NoError(t, os.WriteFile(registry, []byte("{"), 0o600))
	server, requests := globalFlagsServer(t, globalFlagsPage)
	shell := nativeShell{name: "bash", executable: bash, args: []string{"--noprofile", "--norc", "-c"}}
	got := runNativeShell(t, shell, directory, home, server.URL,
		"export OPENAI_API_KEY=synthetic-recovery-key; "+quote(executable)+" files list --format=raw")
	require.NotZero(t, got.code)
	require.Empty(t, got.stdout)
	recovery := regexp.MustCompile(`Inspect with (.+? link)\.`).FindStringSubmatch(got.stderr)
	require.Len(t, recovery, 2, "%s", got.stderr)
	require.Equal(t, quote(executable)+" link", recovery[1])
	require.Empty(t, requests)
	// Repair the controlled fixture before following the printed inspection command.
	require.NoError(t, os.WriteFile(registry, []byte("{}\n"), 0o600))
	copied := runNativeShell(t, shell, directory, home, server.URL, recovery[1])
	require.Zero(t, copied.code, "%s", copied.stderr)
	require.Empty(t, copied.stderr)
	require.Contains(t, copied.stdout, "This folder has no saved project link.")
	require.NotContains(t, copied.stdout, "wrong executable")
	require.Empty(t, requests, "recovery inspection reached the API")
}
