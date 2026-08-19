//go:build !windows

package scripts_test

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseTagCheckoutKeepsCredentialsOutOfGitArguments(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		failedGit   string
		wantFailure bool
	}{
		{name: "authenticated tag checkout"},
		{name: "failed authenticated fetch", failedGit: "fetch", wantFailure: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			remote := filepath.Join(root, "remote.git")
			checkout := filepath.Join(root, "checkout")
			bin := filepath.Join(root, "bin")
			for _, directory := range []string{source, bin} {
				if err := os.Mkdir(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			runHistoricalFixtureCommand(t, source, "git", "init", "--quiet", "--initial-branch=main")
			if err := os.WriteFile(filepath.Join(source, "release.txt"), []byte("synthetic release\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runHistoricalFixtureCommand(t, source, "git", "add", "release.txt")
			runHistoricalFixtureCommand(t, source, "git", "-c", "user.name=Release Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "release")
			runHistoricalFixtureCommand(t, source, "git", "tag", "v1.2.3")
			runHistoricalFixtureCommand(t, root, "git", "init", "--bare", "--quiet", "--initial-branch=main", remote)
			runHistoricalFixtureCommand(t, source, "git", "push", "--quiet", remote, "main", "v1.2.3")
			runHistoricalFixtureCommand(t, root, "git", "clone", "--quiet", remote, checkout)

			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			gitWrapper := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$RELEASE_GIT_ARGUMENT_LOG"

authenticated=0
if [[ "$1" == "--config-env=http.https://github.com/.extraheader=RELEASE_GIT_AUTHORIZATION" ]]; then
  authenticated=1
  if [[ "${RELEASE_GIT_AUTHORIZATION:-}" != "$EXPECTED_GIT_AUTHORIZATION" ]]; then
    printf 'fatal: GitHub HTTP authorization is absent or incorrect\n' >&2
    exit 91
  fi
elif [[ -n "${RELEASE_GIT_AUTHORIZATION:-}" ]]; then
  printf 'fatal: GitHub HTTP authorization leaked into an unrelated Git command\n' >&2
  exit 92
fi

args=()
for argument in "$@"; do
  if [[ "$argument" == *"$GITHUB_TOKEN"* ]]; then
    printf 'fatal: unable to access %s\n' "$argument" >&2
    exit 93
  fi
  if [[ "$argument" == https://github.com/example/private-cli.git ]]; then
    if [[ "$authenticated" != 1 ]]; then
      printf 'fatal: private release repository was accessed without HTTP authorization\n' >&2
      exit 94
    fi
    argument="$RELEASE_GIT_REMOTE"
  fi
  args+=("$argument")
done

if [[ -n "${RELEASE_GIT_FAILURE:-}" && " $* " == *" ${RELEASE_GIT_FAILURE} "* ]]; then
  printf 'fatal: unable to access %s: synthetic network failure\n' "$*" >&2
  exit 95
fi
exec "$REAL_GIT" "${args[@]}"
`
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(gitWrapper), 0o755); err != nil {
				t.Fatal(err)
			}

			action := readReleaseYAML[releaseAction](t, filepath.Join("..", ".github", "actions", "checkout-release-tag", "action.yml"))
			const token = "synthetic-sensitive-github-token-3817231106"
			encodedToken := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
			argumentLog := filepath.Join(root, "git-arguments.log")
			outputPath := filepath.Join(root, "github-output")
			command := exec.Command("bash", "-e", "-o", "pipefail", "-c", action.Runs.Steps[0].Run)
			command.Dir = checkout
			for _, variable := range os.Environ() {
				name, _, _ := strings.Cut(variable, "=")
				if name != "PATH" && name != "GITHUB_TOKEN" {
					command.Env = append(command.Env, variable)
				}
			}
			command.Env = append(command.Env,
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GITHUB_TOKEN="+token,
				"GITHUB_REPOSITORY=example/private-cli",
				"GITHUB_OUTPUT="+outputPath,
				"TAG=v1.2.3",
				"REAL_GIT="+realGit,
				"RELEASE_GIT_REMOTE="+remote,
				"RELEASE_GIT_ARGUMENT_LOG="+argumentLog,
				"RELEASE_GIT_FAILURE="+scenario.failedGit,
				"EXPECTED_GIT_AUTHORIZATION=AUTHORIZATION: basic "+encodedToken,
			)
			output, err := command.CombinedOutput()
			arguments, readErr := os.ReadFile(argumentLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			gitConfig, readErr := os.ReadFile(filepath.Join(checkout, ".git", "config"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, sensitive := range []string{token, encodedToken} {
				if strings.Contains(string(output), sensitive) {
					t.Fatalf("Git command output disclosed a synthetic credential:\n%s", output)
				}
				if strings.Contains(string(arguments), sensitive) {
					t.Fatalf("Git command arguments disclosed a synthetic credential:\n%s", arguments)
				}
				if strings.Contains(string(gitConfig), sensitive) {
					t.Fatalf("local Git configuration persisted a synthetic credential:\n%s", gitConfig)
				}
			}
			if scenario.wantFailure {
				if err == nil || !strings.Contains(string(output), "synthetic network failure") {
					t.Fatalf("Git failure was not propagated safely: %v; output:\n%s", err, output)
				}
				return
			}
			if err != nil {
				t.Fatalf("authenticated release tag checkout failed: %v; output:\n%s", err, output)
			}
			if got := strings.Count(string(arguments), "--config-env=http.https://github.com/.extraheader=RELEASE_GIT_AUTHORIZATION"); got != 3 {
				t.Errorf("authenticated remote Git operations = %d, want 3", got)
			}
			commit, err := exec.Command(realGit, "-C", source, "rev-parse", "HEAD").Output()
			if err != nil {
				t.Fatal(err)
			}
			githubOutput, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(githubOutput), "commit="+string(commit); got != want {
				t.Errorf("release action output = %q, want %q", got, want)
			}
		})
	}
}
