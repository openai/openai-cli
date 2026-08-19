//go:build !windows

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

var releaseCredentials = []string{
	"GITHUB_TOKEN",
	"GH_TOKEN",
	"HOMEBREW_TAP_GITHUB_TOKEN",
	"MACOS_SIGN_P12",
	"MACOS_SIGN_PASSWORD",
	"MACOS_NOTARY_KEY",
	"MACOS_NOTARY_KEY_ID",
	"MACOS_NOTARY_ISSUER_ID",
	"RELEASE_APP_PRIVATE_KEY",
	"ACTIONS_ID_TOKEN_REQUEST_TOKEN",
}

func TestGenerateReleaseArtifacts(t *testing.T) {
	root, logPath, command := newReleaseArtifactFixture(t)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generate release artifacts: %v; output:\n%s", err, output)
	}

	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	gotCalls := strings.Split(strings.TrimSpace(string(calls)), "\n")
	wantCalls := []string{
		"run ./cmd/openai/main.go @completion bash",
		"run ./cmd/openai/main.go @completion zsh",
		"run ./cmd/openai/main.go @completion fish",
		"run ./cmd/openai/main.go @manpages -o man",
	}
	if !slices.Equal(gotCalls, wantCalls) {
		t.Errorf("Go calls = %v, want %v", gotCalls, wantCalls)
	}

	for _, shell := range []string{"bash", "zsh", "fish"} {
		contents, readErr := os.ReadFile(filepath.Join(root, "completions", "openai."+shell))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if got, want := string(contents), "completion:"+shell+"\n"; got != want {
			t.Errorf("%s completion = %q, want %q", shell, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "man", "man1", "openai.1.gz")); err != nil {
		t.Errorf("man page was not generated at its existing archive path: %v", err)
	}
}

func TestGenerateReleaseArtifactsRejectsEveryCredential(t *testing.T) {
	for _, credential := range releaseCredentials {
		t.Run(credential, func(t *testing.T) {
			root, logPath, command := newReleaseArtifactFixture(t)
			command.Env = append(command.Env, credential+"=synthetic-test-credential")

			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("generator accepted %s", credential)
			}
			if !strings.Contains(string(output), credential) {
				t.Errorf("error does not identify rejected credential %s: %s", credential, output)
			}
			if _, err := os.Stat(logPath); !os.IsNotExist(err) {
				t.Errorf("application dependency was executed with %s present", credential)
			}
			if _, err := os.Stat(filepath.Join(root, "completions")); !os.IsNotExist(err) {
				t.Errorf("generator created artifacts before rejecting %s", credential)
			}
		})
	}
}

func TestGenerateReleaseArtifactsStopsAfterFailure(t *testing.T) {
	_, logPath, command := newReleaseArtifactFixture(t)
	command.Env = append(command.Env, "RELEASE_GO_FAILURE=run ./cmd/openai/main.go @completion zsh")

	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("generator accepted failed completion generation; output:\n%s", output)
	}

	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	gotCalls := strings.Split(strings.TrimSpace(string(calls)), "\n")
	wantCalls := []string{
		"run ./cmd/openai/main.go @completion bash",
		"run ./cmd/openai/main.go @completion zsh",
	}
	if !slices.Equal(gotCalls, wantCalls) {
		t.Errorf("Go calls after failure = %v, want %v", gotCalls, wantCalls)
	}
}

func TestPublishWorkflowSeparatesGenerationFromReleaseCredentials(t *testing.T) {
	workflow := readReleaseYAML[releaseWorkflow](t, filepath.Join("..", ".github", "workflows", "publish-release.yml"))
	prepare, ok := workflow.Jobs["prepare-release-artifacts"]
	if !ok {
		t.Fatal("publish workflow has no isolated release-artifact preparation job")
	}
	if prepare.Environment != "" {
		t.Errorf("artifact preparation unexpectedly uses protected environment %q", prepare.Environment)
	}
	if len(prepare.Permissions) != 1 || prepare.Permissions["contents"] != "read" {
		t.Errorf("artifact preparation permissions = %v, want only contents: read", prepare.Permissions)
	}
	for _, step := range prepare.Steps {
		encoded, err := yaml.Marshal(step)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "secrets.") || strings.Contains(step.Uses, "create-github-app-token") {
			t.Errorf("unprivileged step %q requests protected credentials", step.Name)
		}
		for _, credential := range releaseCredentials {
			if _, exists := step.Env[credential]; exists {
				t.Errorf("unprivileged step %q exposes %s", step.Name, credential)
			}
		}
	}

	stage := releaseStepIndex(t, prepare, "Stage trusted release artifact generator")
	checkout := releaseStepIndex(t, prepare, "Ensure release tag is on main")
	generate := releaseStepIndex(t, prepare, "Generate release support artifacts")
	upload := releaseStepIndex(t, prepare, "Upload release support artifacts")
	if !(stage < checkout && checkout < generate && generate < upload) {
		t.Errorf("preparation steps are out of order: stage=%d checkout=%d generate=%d upload=%d", stage, checkout, generate, upload)
	}
	if got := prepare.Steps[generate].Run; got != "$RUNNER_TEMP/generate-release-artifacts" {
		t.Errorf("generator command = %q, want staged trusted generator", got)
	}
	const expectedArtifact = "release-support-artifacts-${{ steps.release-tag.outputs.commit }}"
	if got := prepare.Steps[upload].With["name"]; got != expectedArtifact {
		t.Errorf("support artifact name = %v, want artifacts bound to the verified release commit", got)
	}
	for _, directory := range []string{"completions/", "man/"} {
		if !strings.Contains(stringReleaseInput(t, prepare.Steps[upload], "path"), directory) {
			t.Errorf("uploaded support artifacts omit %s", directory)
		}
	}

	release, ok := workflow.Jobs["goreleaser"]
	if !ok {
		t.Fatal("publish workflow has no GoReleaser job")
	}
	if release.Needs != "prepare-release-artifacts" {
		t.Errorf("GoReleaser dependency = %q, want prepare-release-artifacts", release.Needs)
	}
	if release.Environment != "publish" {
		t.Errorf("GoReleaser environment = %q, want publish", release.Environment)
	}
	if len(release.Permissions) != 1 || release.Permissions["contents"] != "read" {
		t.Errorf("GoReleaser job permissions = %v, want only contents: read", release.Permissions)
	}
	download := releaseStepIndex(t, release, "Download release support artifacts")
	repoToken := releaseStepIndex(t, release, "Create release app token for this repo")
	tapToken := releaseStepIndex(t, release, "Create release app token for homebrew-tools")
	run := releaseStepIndex(t, release, "Run GoReleaser")
	if !(download < repoToken && repoToken < tapToken && tapToken < run) {
		t.Errorf("privileged steps are out of order: download=%d repo=%d tap=%d release=%d", download, repoToken, tapToken, run)
	}
	if !strings.Contains(release.Steps[run].Run, "--skip=before") {
		t.Errorf("privileged GoReleaser can execute hooks restored by a historical release tag")
	}
	if got := release.Steps[download].With["name"]; got != expectedArtifact {
		t.Errorf("downloaded support artifact name = %v, want artifacts bound to the verified release commit", got)
	}
	if got := release.Steps[download].With["path"]; got != "." {
		t.Errorf("support artifact download path = %v, want repository root", got)
	}
	if got := release.Steps[repoToken].With["permission-contents"]; got != "write" {
		t.Errorf("release repository token contents permission = %v, want write", got)
	}
	if got := release.Steps[tapToken].With["repositories"]; got != "homebrew-tools" {
		t.Errorf("Homebrew token repository = %v, want homebrew-tools", got)
	}
	for _, credential := range []string{
		"GITHUB_TOKEN",
		"HOMEBREW_TAP_GITHUB_TOKEN",
		"MACOS_SIGN_P12",
		"MACOS_SIGN_PASSWORD",
		"MACOS_NOTARY_KEY",
		"MACOS_NOTARY_KEY_ID",
		"MACOS_NOTARY_ISSUER_ID",
	} {
		if _, exists := release.Steps[run].Env[credential]; !exists {
			t.Errorf("release step no longer receives required %s", credential)
		}
	}

	attest, ok := workflow.Jobs["attest"]
	if !ok || attest.Needs != "goreleaser" {
		t.Fatal("release artifact attestation no longer follows GoReleaser")
	}
	if attest.Permissions["id-token"] != "write" || attest.Permissions["attestations"] != "write" {
		t.Errorf("attestation permissions = %v, want OIDC and attestation writes", attest.Permissions)
	}
	releaseStepIndex(t, attest, "Attest release artifacts")
	releaseStepIndex(t, attest, "Verify release artifact attestations")
}

func TestReleaseTagValidationRemainsSharedAndAuthenticated(t *testing.T) {
	action := readReleaseYAML[releaseAction](t, filepath.Join("..", ".github", "actions", "checkout-release-tag", "action.yml"))
	if len(action.Runs.Steps) != 1 {
		t.Fatalf("release tag validation steps = %d, want 1", len(action.Runs.Steps))
	}
	step := action.Runs.Steps[0]
	if step.Env["GITHUB_TOKEN"] != "${{ github.token }}" {
		t.Errorf("tag validation no longer scopes the read-only GitHub token to its own step")
	}
	for _, required := range []string{
		`git check-ref-format "refs/tags/$TAG"`,
		`git ls-remote --exit-code --tags --refs "$authenticated_origin"`,
		`git merge-base --is-ancestor "$tag_sha" origin/main`,
		`git checkout --detach "$tag_sha"`,
		`test "$(git rev-parse HEAD)" = "$tag_sha"`,
		`printf 'commit=%s\n' "$tag_sha" >> "$GITHUB_OUTPUT"`,
	} {
		if !strings.Contains(step.Run, required) {
			t.Errorf("release tag validation no longer performs %s", required)
		}
	}

	workflow := readReleaseYAML[releaseWorkflow](t, filepath.Join("..", ".github", "workflows", "publish-release.yml"))
	for _, jobName := range []string{"prepare-release-artifacts", "goreleaser"} {
		job := workflow.Jobs[jobName]
		index := releaseStepIndex(t, job, "Ensure release tag is on main")
		if got := job.Steps[index].Uses; got != "./.github/actions/checkout-release-tag" {
			t.Errorf("%s validates its tag with %q, want the shared trusted action", jobName, got)
		}
		if got := job.Steps[index].ID; got != "release-tag" {
			t.Errorf("%s release tag validation id = %q, want release-tag", jobName, got)
		}
	}
}

func TestCIReleaseArtifactsAreGeneratedWithoutPublishingToken(t *testing.T) {
	workflow := readReleaseYAML[releaseWorkflow](t, filepath.Join("..", ".github", "workflows", "ci.yml"))
	job, ok := workflow.Jobs["build-artifacts"]
	if !ok {
		t.Fatal("CI has no release-artifact build job")
	}
	generate := releaseStepIndex(t, job, "Generate release support artifacts")
	release := releaseStepIndex(t, job, "Run GoReleaser")
	if generate >= release {
		t.Errorf("CI generates support artifacts after GoReleaser: generate=%d release=%d", generate, release)
	}
	if got := job.Steps[generate].Run; got != "./scripts/generate-release-artifacts" {
		t.Errorf("CI artifact generator = %q, want repository generator", got)
	}
	if !strings.Contains(job.Steps[release].Run, "--skip=publish,before") {
		t.Errorf("CI GoReleaser can execute application hooks or publish artifacts")
	}
	if len(job.Steps[generate].Env) != 0 || len(job.Steps[release].Env) != 0 {
		t.Errorf("CI snapshot generation unexpectedly receives privileged environment variables")
	}
}

func TestGoReleaserPreservesArtifactsWithoutApplicationHooks(t *testing.T) {
	config := readReleaseYAML[goReleaserConfig](t, filepath.Join("..", ".goreleaser.yml"))
	if len(config.Before.Hooks) != 0 || len(config.After.Hooks) != 0 {
		t.Fatal("privileged GoReleaser configuration executes lifecycle hooks")
	}
	if len(config.Builds) != 3 {
		t.Fatalf("release builds = %d, want Linux, macOS, and Windows", len(config.Builds))
	}
	for _, build := range config.Builds {
		if len(build.Hooks.Pre) != 0 || len(build.Hooks.Post) != 0 {
			t.Errorf("release build %q executes privileged hooks", build.ID)
		}
	}
	if len(config.Archives) != 3 {
		t.Fatalf("release archives = %d, want Linux, macOS, and Windows", len(config.Archives))
	}
	for _, archive := range config.Archives {
		if !slices.Equal(archive.Files, []string{"completions/*", "man/*/*"}) {
			t.Errorf("archive %q files = %v, want existing completion and man-page paths", archive.ID, archive.Files)
		}
	}
	if len(config.Notarize.MacOS) != 1 {
		t.Fatal("macOS signing and notarization configuration was removed")
	}
	if len(config.HomebrewCasks) != 1 {
		t.Fatal("Homebrew cask publishing configuration was removed")
	}
	cask := config.HomebrewCasks[0]
	if cask.Repository.Name != "homebrew-tools" || cask.Repository.Token != "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}" {
		t.Errorf("Homebrew repository configuration = %+v", cask.Repository)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		if got, want := cask.Completions[shell], "completions/openai."+shell; got != want {
			t.Errorf("Homebrew %s completion = %q, want %q", shell, got, want)
		}
	}
	if !slices.Equal(cask.Manpages, []string{"man/man1/openai.1.gz"}) {
		t.Errorf("Homebrew man pages = %v, want existing compressed manual", cask.Manpages)
	}
}

func newReleaseArtifactFixture(t *testing.T) (string, string, *exec.Cmd) {
	t.Helper()

	root := t.TempDir()
	for _, directory := range []string{"scripts", "bin"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	generator, err := os.ReadFile("generate-release-artifacts")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "generate-release-artifacts"), generator, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGo := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$RELEASE_GO_LOG"
if [[ "$*" == "${RELEASE_GO_FAILURE:-}" ]]; then
  exit 23
fi
case "$3" in
  @completion) printf 'completion:%s\n' "$4" ;;
  @manpages)
    mkdir -p "$5/man1"
    printf 'synthetic manual\n' > "$5/man1/openai.1.gz"
    ;;
  *) exit 24 ;;
esac
`
	binDir := filepath.Join(root, "bin")
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(root, "go.log")
	command := exec.Command("bash", filepath.Join(root, "scripts", "generate-release-artifacts"))
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if name == "GITHUB_WORKSPACE" || name == "PATH" || slices.Contains(releaseCredentials, name) {
			continue
		}
		command.Env = append(command.Env, variable)
	}
	command.Env = append(command.Env,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RELEASE_GO_LOG="+logPath,
	)
	return root, logPath, command
}

func readReleaseYAML[T any](t *testing.T, path string) T {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := yaml.Unmarshal(contents, &value); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return value
}

func releaseStepIndex(t *testing.T, job releaseJob, name string) int {
	t.Helper()

	for index, step := range job.Steps {
		if step.Name == name {
			return index
		}
	}
	t.Fatalf("workflow job has no %q step", name)
	return -1
}

func stringReleaseInput(t *testing.T, step releaseStep, name string) string {
	t.Helper()

	value, ok := step.With[name].(string)
	if !ok {
		t.Fatalf("step %q input %q is %T, want string", step.Name, name, step.With[name])
	}
	return value
}

type releaseWorkflow struct {
	Jobs map[string]releaseJob `yaml:"jobs"`
}

type releaseJob struct {
	Needs       any               `yaml:"needs"`
	Environment string            `yaml:"environment"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []releaseStep     `yaml:"steps"`
}

type releaseAction struct {
	Runs struct {
		Steps []releaseStep `yaml:"steps"`
	} `yaml:"runs"`
}

type releaseStep struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]any    `yaml:"with"`
}

type goReleaserConfig struct {
	Before struct {
		Hooks []any `yaml:"hooks"`
	} `yaml:"before"`
	After struct {
		Hooks []any `yaml:"hooks"`
	} `yaml:"after"`
	Builds []struct {
		ID    string `yaml:"id"`
		Hooks struct {
			Pre  []any `yaml:"pre"`
			Post []any `yaml:"post"`
		} `yaml:"hooks"`
	} `yaml:"builds"`
	Archives []struct {
		ID    string   `yaml:"id"`
		Files []string `yaml:"files"`
	} `yaml:"archives"`
	Notarize struct {
		MacOS []any `yaml:"macos"`
	} `yaml:"notarize"`
	HomebrewCasks []struct {
		Repository struct {
			Name  string `yaml:"name"`
			Token string `yaml:"token"`
		} `yaml:"repository"`
		Completions map[string]string `yaml:"completions"`
		Manpages    []string          `yaml:"manpages"`
	} `yaml:"homebrew_casks"`
}
