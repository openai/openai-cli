//go:build !windows

package scripts_test

import (
	"archive/tar"
	"compress/gzip"
	"io"
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

func TestLocalReleasePreflight(t *testing.T) {
	goreleaser := verifiedReleaseExecutable(t)

	ignore, err := os.ReadFile(filepath.Join("..", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	generator, err := os.ReadFile("generate-release-artifacts")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := map[string]string{
		".gitignore":                         string(ignore),
		"scripts/generate-release-artifacts": string(generator),
		"go.mod":                             "module example.com/local-release\n\ngo 1.25.0\n",
		"cmd/openai/main.go": `package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	switch os.Args[1] {
	case "@completion":
		fmt.Printf("synthetic %s completion\n", os.Args[2])
	case "@manpages":
		directory := filepath.Join(os.Args[3], "man1")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "openai.1.gz"), []byte("synthetic manual\n"), 0o644); err != nil {
			panic(err)
		}
	}
}
`,
		".goreleaser.yml": `version: 2
project_name: local-release
release:
  disable: true
builds:
  - main: ./cmd/openai/main.go
    goos: [linux]
    goarch: [amd64]
archives:
  - formats: [tar.gz]
    files:
      - completions/*
      - man/*/*
`,
	}
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "scripts", "generate-release-artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}

	runHistoricalFixtureCommand(t, root, "git", "init", "--quiet")
	runHistoricalFixtureCommand(t, root, "git", "remote", "add", "origin", "https://github.com/example/local-release.git")
	runHistoricalFixtureCommand(t, root, "git", "add", ".")
	runHistoricalFixtureCommand(t, root, "git", "-c", "user.name=Release Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "local release")
	runHistoricalFixtureCommand(t, root, "git", "tag", "v1.2.3")

	environment := make([]string, 0, len(os.Environ()))
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if name != "GITHUB_WORKSPACE" && !slices.Contains(releaseCredentials, name) {
			environment = append(environment, variable)
		}
	}
	prepare := exec.Command("./scripts/generate-release-artifacts")
	prepare.Dir = root
	prepare.Env = environment
	output, err := prepare.CombinedOutput()
	if err != nil {
		t.Fatalf("documented local artifact preparation failed: %v; output:\n%s", err, output)
	}
	release := func() *exec.Cmd {
		command := exec.Command(goreleaser, "release", "--clean")
		command.Dir = root
		command.Env = environment
		return command
	}
	output, err = release().CombinedOutput()
	if err != nil {
		t.Fatalf("documented local non-snapshot release rejected prepared artifacts: %v; output:\n%s", err, output)
	}
	assertReleaseArchiveIncludesSupportFiles(t, root, "local")

	for _, unexpected := range []string{
		"completions/unexpected.sh",
		"man/man1/unexpected.1.gz",
		"nested/completions/unrelated.txt",
	} {
		path := filepath.Join(root, filepath.FromSlash(unexpected))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("unrelated dirt\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		output, err = release().CombinedOutput()
		if err == nil || !strings.Contains(string(output), "git is in a dirty state") {
			t.Fatalf("exact release-artifact ignores concealed %s: %v; output:\n%s", unexpected, err, output)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHistoricalReleasePreflight(t *testing.T) {
	goreleaser := verifiedReleaseExecutable(t)

	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/historical-release\n\ngo 1.25.0\n",
		"main.go":    "package main\n\nfunc main() {}\n",
		".gitignore": "dist/\n",
		".goreleaser.yml": `version: 2
project_name: historical-release
before:
  hooks:
    - sh -c "touch legacy-hook-ran"
builds:
  - main: .
    goos: [linux]
    goarch: [amd64]
archives:
  - formats: [tar.gz]
    files:
      - completions/*
      - man/*/*
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runHistoricalFixtureCommand(t, root, "git", "init", "--quiet")
	runHistoricalFixtureCommand(t, root, "git", "remote", "add", "origin", "https://github.com/example/historical-release.git")
	runHistoricalFixtureCommand(t, root, "git", "add", ".")
	runHistoricalFixtureCommand(t, root, "git", "-c", "user.name=Release Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "historical release")
	runHistoricalFixtureCommand(t, root, "git", "tag", "v1.2.3")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("dist/\n/completions/\n/man/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runHistoricalFixtureCommand(t, root, "git", "add", ".gitignore")
	runHistoricalFixtureCommand(t, root, "git", "-c", "user.name=Release Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "later ignore rules")
	runHistoricalFixtureCommand(t, root, "git", "checkout", "--quiet", "--detach", "v1.2.3")

	completions := filepath.Join(root, "completions")
	manpages := filepath.Join(root, "man", "man1")
	for _, directory := range []string{completions, manpages} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		path := filepath.Join(completions, "openai."+shell)
		if err := os.WriteFile(path, []byte("synthetic "+shell+" completion\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manpage, err := os.Create(filepath.Join(manpages, "openai.1.gz"))
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(manpage)
	if _, err := compressed.Write([]byte("synthetic manual\n")); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manpage.Close(); err != nil {
		t.Fatal(err)
	}

	release := func() *exec.Cmd {
		command := exec.Command(goreleaser, "release", "--clean", "--skip=publish,before")
		command.Dir = root
		for _, variable := range os.Environ() {
			name, _, _ := strings.Cut(variable, "=")
			if !slices.Contains(releaseCredentials, name) {
				command.Env = append(command.Env, variable)
			}
		}
		return command
	}
	output, err := release().CombinedOutput()
	if err == nil || !strings.Contains(string(output), "git is in a dirty state") {
		t.Fatalf("historical release did not reject untracked support artifacts: %v; output:\n%s", err, output)
	}

	workflow := readReleaseYAML[releaseWorkflow](t, filepath.Join("..", ".github", "workflows", "publish-release.yml"))
	job := workflow.Jobs["goreleaser"]
	verify := releaseStepIndex(t, job, "Verify isolated release inputs")
	staging := t.TempDir()
	for _, directory := range []string{"completions", "man"} {
		if err := os.Rename(filepath.Join(root, directory), filepath.Join(staging, directory)); err != nil {
			t.Fatal(err)
		}
	}
	verification := exec.Command("bash", "-c", job.Steps[verify].Run)
	verification.Dir = root
	verification.Env = append(os.Environ(), "RELEASE_INPUT_DIR="+staging)
	if output, err := verification.CombinedOutput(); err != nil {
		t.Fatalf("historical release artifact verification failed: %v; output:\n%s", err, output)
	}
	output, err = release().CombinedOutput()
	if err != nil {
		t.Fatalf("historical tagged release rejected downloaded support artifacts: %v; output:\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "legacy-hook-ran")); !os.IsNotExist(err) {
		t.Fatal("historical application-executing hook was not skipped")
	}

	assertReleaseArchiveIncludesSupportFiles(t, root, "historical")
}

func assertReleaseArchiveIncludesSupportFiles(t *testing.T, root, scenario string) {
	t.Helper()

	archives, err := filepath.Glob(filepath.Join(root, "dist", "*.tar.gz"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("%s release archives = %v, error = %v; want one archive", scenario, archives, err)
	}
	archive, err := os.Open(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	decompressed, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer decompressed.Close()
	reader := tar.NewReader(decompressed)
	found := make(map[string]bool)
	for {
		header, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		found[header.Name] = true
	}
	for _, artifact := range []string{
		"completions/openai.bash",
		"completions/openai.zsh",
		"completions/openai.fish",
		"man/man1/openai.1.gz",
	} {
		if !found[artifact] {
			t.Errorf("%s release archive omitted %s", scenario, artifact)
		}
	}
}

func TestCIReleasePreflightUsesVerifiedExecutable(t *testing.T) {
	workflow := readReleaseYAML[releaseWorkflow](t, filepath.Join("..", ".github", "workflows", "ci.yml"))
	job, ok := workflow.Jobs["build-artifacts"]
	if !ok {
		t.Fatal("CI has no release-artifact build job")
	}
	install := releaseStepIndex(t, job, "Set up verified GoReleaser")
	preflight := releaseStepIndex(t, job, "Verify local and historical release preflight")
	generate := releaseStepIndex(t, job, "Generate release inputs")
	if !(install < preflight && preflight < generate) {
		t.Errorf("CI release steps are out of order: install=%d preflight=%d generate=%d", install, preflight, generate)
	}
	if !strings.Contains(job.Steps[preflight].Run, "Test(Local|Historical)ReleasePreflight") {
		t.Errorf("CI does not execute both real local and historical-tag release preflights")
	}
	if got, want := job.Steps[preflight].Env["GORELEASER_EXECUTABLE"], "${{ steps.goreleaser.outputs.executable }}"; got != want {
		t.Errorf("CI release preflights executable = %q, want reviewed verified binary %q", got, want)
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

func verifiedReleaseExecutable(t *testing.T) string {
	t.Helper()

	if executable := os.Getenv("GORELEASER_EXECUTABLE"); executable != "" {
		return executable
	}
	executable, err := exec.LookPath("goreleaser")
	if err != nil {
		t.Skip("the verified GoReleaser is unavailable outside the artifact-build job")
	}
	return executable
}

func runHistoricalFixtureCommand(t *testing.T, directory, name string, args ...string) {
	t.Helper()

	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v; output:\n%s", name, strings.Join(args, " "), err, output)
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

type releaseWorkflow struct {
	Jobs map[string]releaseJob `yaml:"jobs"`
}

type releaseJob struct {
	Steps []releaseStep `yaml:"steps"`
}

type releaseStep struct {
	Name string            `yaml:"name"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
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
