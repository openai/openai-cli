package releaseworkflow

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

const (
	releaseInputsArtifact  = "openai-release-inputs"
	checkoutAction         = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"
	setupGoAction          = "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"
	uploadArtifactAction   = "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
	downloadArtifactAction = "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c"
)

var releaseInputPaths = []string{
	"completions/openai.bash",
	"completions/openai.zsh",
	"completions/openai.fish",
	"man/man1/openai.1.gz",
}

type workflow struct {
	Permissions map[string]string      `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Environment string            `yaml:"environment"`
	Needs       any               `yaml:"needs"`
	Outputs     map[string]string `yaml:"outputs"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []workflowStep    `yaml:"steps"`
	Timeout     int               `yaml:"timeout-minutes"`
}

type workflowStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	ID   string         `yaml:"id"`
	With map[string]any `yaml:"with"`
	Env  map[string]any `yaml:"env"`
}

func readWorkflow(t *testing.T, name string) workflow {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	var parsed workflow
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return parsed
}

func requireStep(t *testing.T, job workflowJob, name string) (int, workflowStep) {
	t.Helper()
	for index, step := range job.Steps {
		if step.Name == name {
			return index, step
		}
	}
	t.Fatalf("workflow job has no %q step", name)
	return -1, workflowStep{}
}

func TestWorkflowPermissionsRemainLeastPrivileged(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"publish-release.yml", "ci.yml"} {
		parsed := readWorkflow(t, name)
		if parsed.Permissions == nil || len(parsed.Permissions) != 0 {
			t.Errorf("%s must explicitly deny default token permissions: %v", name, parsed.Permissions)
		}
	}

	publishJobs := readWorkflow(t, "publish-release.yml").Jobs
	if !reflect.DeepEqual(publishJobs["attest"].Permissions, map[string]string{
		"attestations": "write",
		"contents":     "read",
		"id-token":     "write",
	}) {
		t.Fatalf("attestation permissions changed: %v", publishJobs["attest"].Permissions)
	}

	ciJobs := readWorkflow(t, "ci.yml").Jobs
	for _, name := range []string{"lint", "build-artifacts", "test"} {
		if !reflect.DeepEqual(ciJobs[name].Permissions, map[string]string{"contents": "read"}) {
			t.Errorf("CI job %s permissions changed: %v", name, ciJobs[name].Permissions)
		}
	}
	if ciJobs["build"].Permissions == nil || len(ciJobs["build"].Permissions) != 0 {
		t.Fatalf("aggregate CI build must not receive repository credentials: %v", ciJobs["build"].Permissions)
	}
}

func TestReleaseInputsUseAnIsolatedUnprivilegedJob(t *testing.T) {
	t.Parallel()

	jobs := readWorkflow(t, "publish-release.yml").Jobs
	job, ok := jobs["release-inputs"]
	if !ok {
		t.Fatal("release inputs must be generated in a separate unprivileged job")
	}
	if job.Environment != "" {
		t.Fatalf("unprivileged generation must not use a protected environment: %q", job.Environment)
	}
	if !reflect.DeepEqual(job.Permissions, map[string]string{"contents": "read"}) {
		t.Fatalf("release-input job permissions = %v, want only contents: read", job.Permissions)
	}

	checkoutIndex, checkout := requireStep(t, job, "Checkout")
	if checkout.Uses != checkoutAction || checkout.With["persist-credentials"] != false {
		t.Fatalf("release-input checkout must be pinned and credential-free: %#v", checkout)
	}
	if job.Outputs["tag-sha"] != "${{ steps.trusted-tag.outputs.sha }}" {
		t.Fatal("isolated release inputs must be bound to the independently verified immutable tag SHA")
	}
	tagIndex, tag := requireStep(t, job, "Ensure release tag is on main")
	for _, fragment := range []string{
		`git check-ref-format "refs/tags/$TAG"`,
		`git merge-base --is-ancestor "$tag_sha" origin/main`,
		`git checkout --detach "$tag_sha"`,
	} {
		if !strings.Contains(tag.Run, fragment) {
			t.Errorf("unprivileged tag validation is missing %q", fragment)
		}
	}
	if tag.ID != "trusted-tag" || !strings.Contains(tag.Run, `echo "sha=$tag_sha" >> "$GITHUB_OUTPUT"`) {
		t.Fatal("the validated release tag SHA must be exported before executable dependencies run")
	}
	setupIndex, setup := requireStep(t, job, "Set up Go")
	if setup.Uses != setupGoAction || setup.With["go-version-file"] != ".go-version" || setup.With["check-latest"] != true || setup.With["cache"] != false {
		t.Fatalf("unprivileged Go setup must use the reviewed patched toolchain with cache disabled: %#v", setup)
	}
	generationIndex, generate := requireStep(t, job, "Generate release inputs")
	for _, fragment := range []string{
		"@completion bash > completions/openai.bash",
		"@completion zsh > completions/openai.zsh",
		"@completion fish > completions/openai.fish",
		"@manpages -o man",
	} {
		if !strings.Contains(generate.Run, fragment) {
			t.Errorf("release generation does not preserve %q", fragment)
		}
	}
	if len(generate.Env) != 0 {
		t.Fatalf("executable generator must not receive workflow credentials: %v", generate.Env)
	}
	uploadIndex, upload := requireStep(t, job, "Stage isolated release inputs")
	if upload.Uses != uploadArtifactAction || upload.With["name"] != releaseInputsArtifact {
		t.Fatalf("release inputs must use the pinned, fixed-name artifact: %#v", upload)
	}
	if upload.With["if-no-files-found"] != "error" {
		t.Fatal("missing generated release inputs must fail closed")
	}
	paths, ok := upload.With["path"].(string)
	if !ok || !reflect.DeepEqual(strings.Fields(paths), releaseInputPaths) {
		t.Fatalf("uploaded files must be exactly %v; got %q", releaseInputPaths, paths)
	}
	if !(checkoutIndex < setupIndex && setupIndex < tagIndex && tagIndex < generationIndex && generationIndex < uploadIndex) {
		t.Fatal("trusted checkout and patched cache-disabled Go setup must precede tag checkout, generation, and upload")
	}
	for _, step := range job.Steps {
		if strings.Contains(fmt.Sprint(step.With, step.Env, step.Run), "secrets.") {
			t.Fatalf("unprivileged release-input job references secrets in %q", step.Name)
		}
		if strings.HasPrefix(step.Uses, "./") {
			t.Fatalf("unprivileged asset generator must not run publisher-only local actions: %q", step.Uses)
		}
		if strings.HasPrefix(step.Uses, "actions/cache@") {
			t.Fatal("the unprivileged job must not publish a cache consumed by the privileged job")
		}
	}
}

func TestPublisherVerifiesIsolatedInputsBeforeReceivingSecrets(t *testing.T) {
	t.Parallel()

	job := readWorkflow(t, "publish-release.yml").Jobs["goreleaser"]
	if !strings.Contains(fmt.Sprint(job.Needs), "release-inputs") {
		t.Fatalf("privileged publisher must depend on isolated inputs, got needs=%v", job.Needs)
	}
	if job.Environment != "publish" {
		t.Fatalf("publisher must preserve the protected publish environment, got %q", job.Environment)
	}
	if !reflect.DeepEqual(job.Permissions, map[string]string{"contents": "read"}) {
		t.Fatalf("publisher permissions unexpectedly changed: %v", job.Permissions)
	}
	checkoutIndex, _ := requireStep(t, job, "Checkout")
	verifiedBinaryIndex, verifiedBinary := requireStep(t, job, "Set up verified GoReleaser")
	if verifiedBinary.Uses != "./.github/actions/setup-goreleaser" || verifiedBinary.ID != "goreleaser" {
		t.Fatalf("publisher must expose the reviewed digest-verified GoReleaser executable: %#v", verifiedBinary)
	}
	setupIndex, setup := requireStep(t, job, "Set up Go")
	if setup.Uses != setupGoAction || setup.With["go-version-file"] != ".go-version" || setup.With["check-latest"] != true || setup.With["cache"] != false {
		t.Fatalf("publisher must bootstrap the trusted patched Go toolchain without shared cache: %#v", setup)
	}
	preserveScannerIndex, preserveScanner := requireStep(t, job, "Preserve trusted vulnerability scanner")
	if !strings.Contains(preserveScanner.Run, `install -m 0755 ./scripts/govulncheck "$RUNNER_TEMP/openai-cli-govulncheck"`) {
		t.Fatal("publisher must preserve the trusted vulnerability scanner before checking out historical source")
	}
	tagIndex, tag := requireStep(t, job, "Ensure release tag is on main")
	if !strings.Contains(tag.Run, `git merge-base --is-ancestor "$tag_sha" origin/main`) {
		t.Fatal("publisher must independently verify the trusted release tag")
	}
	if tag.Env["EXPECTED_TAG_SHA"] != "${{ needs.release-inputs.outputs.tag-sha }}" || !strings.Contains(tag.Run, `test "$tag_sha" = "$EXPECTED_TAG_SHA"`) {
		t.Fatal("publisher must reject isolated inputs generated from a different trusted tag revision")
	}
	scanIndex, scan := requireStep(t, job, "Check shipped CLI for reachable vulnerabilities")
	if !strings.Contains(scan.Run, `"$RUNNER_TEMP/openai-cli-govulncheck" "$GITHUB_WORKSPACE"`) {
		t.Fatal("publisher must run the preserved trusted scanner against the validated release source")
	}
	downloadIndex, download := requireStep(t, job, "Download isolated release inputs")
	if download.Uses != downloadArtifactAction || download.With["name"] != releaseInputsArtifact {
		t.Fatalf("publisher must download the fixed current-run artifact with a pinned action: %#v", download)
	}
	if _, suppliedToken := download.With["github-token"]; suppliedToken {
		t.Fatal("artifact download must remain scoped to this workflow run without extra credentials")
	}
	verifyIndex, _ := requireStep(t, job, "Verify isolated release inputs")
	firstCredentialIndex, _ := requireStep(t, job, "Create release app token for this repo")
	if !(checkoutIndex < verifiedBinaryIndex && verifiedBinaryIndex < setupIndex && setupIndex < preserveScannerIndex && preserveScannerIndex < tagIndex && tagIndex < scanIndex && scanIndex < downloadIndex && downloadIndex < verifyIndex && verifyIndex < firstCredentialIndex) {
		t.Fatal("verified tooling, patched Go, preserved scanner, validated tag, vulnerability scan, and isolated artifacts must precede publishing credentials")
	}
	for index, step := range job.Steps[:firstCredentialIndex] {
		if strings.Contains(fmt.Sprint(step.With, step.Env, step.Run), "secrets.") {
			t.Fatalf("step %d (%q) receives privileged secrets before release-input validation", index, step.Name)
		}
	}
}

func TestTrustedTagAuthenticationNeverLeaksCredentials(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("GitHub release tag guards execute with Bash on Ubuntu")
	}

	const token = "synthetic-github-token-never-log"
	const tagSHA = "0123456789abcdef0123456789abcdef01234567"
	encodedToken := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	fakeGit := `#!/usr/bin/env bash
set -euo pipefail
command="$1"
case "$command" in
  check-ref-format) ;;
  ls-remote|fetch)
    remote=""
    for argument in "$@"; do
      case "$argument" in
        https://*) remote="$argument" ;;
      esac
    done
    if [[ "$FAKE_GIT_FAILURE" == "1" ]]; then
      echo "fatal: unable to access '$remote': synthetic transport failure" >&2
      exit 23
    fi
    if [[ "$GIT_CONFIG_COUNT" != "1" ||
          "$GIT_CONFIG_KEY_0" != "http.https://github.com/.extraheader" ||
          "$GIT_CONFIG_VALUE_0" != "AUTHORIZATION: basic $EXPECTED_AUTH_HEADER" ]]; then
      echo "missing step-scoped Git authentication" >&2
      exit 24
    fi
    if [[ "$remote" != "https://github.com/openai/openai-cli.git" ]]; then
      echo "unexpected Git remote: $remote" >&2
      exit 25
    fi
    if [[ "$command" == "ls-remote" ]]; then
      printf '%s\trefs/tags/%s\n' "$FAKE_TAG_SHA" "$TAG"
    fi
    ;;
  for-each-ref) printf 'refs/tags/%s\n' "$TAG" ;;
  rev-parse) printf '%s\n' "$FAKE_TAG_SHA" ;;
  merge-base|checkout) ;;
  *) echo "unexpected git command: $command" >&2; exit 26 ;;
esac
`

	for _, name := range []string{"release-inputs", "goreleaser"} {
		_, tag := requireStep(t, readWorkflow(t, "publish-release.yml").Jobs[name], "Ensure release tag is on main")
		for _, failure := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/fetch_failure=%t", name, failure), func(t *testing.T) {
				t.Parallel()

				bin := t.TempDir()
				if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fakeGit), 0o755); err != nil {
					t.Fatal(err)
				}
				failed := "0"
				if failure {
					failed = "1"
				}
				command := exec.Command("bash", "-e", "-c", tag.Run)
				command.Env = append(os.Environ(),
					"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
					"GITHUB_TOKEN="+token,
					"GITHUB_REPOSITORY=openai/openai-cli",
					"GITHUB_OUTPUT="+filepath.Join(t.TempDir(), "github-output"),
					"TAG=v1.2.3",
					"EXPECTED_TAG_SHA="+tagSHA,
					"FAKE_TAG_SHA="+tagSHA,
					"EXPECTED_AUTH_HEADER="+encodedToken,
					"FAKE_GIT_FAILURE="+failed,
				)
				output, err := command.CombinedOutput()
				if failure && err == nil || !failure && err != nil {
					t.Fatalf("trusted tag command error=%v, failure=%t, output:\n%s", err, failure, output)
				}
				visible := string(output)
				for _, line := range strings.Split(visible, "\n") {
					if mask, ok := strings.CutPrefix(line, "::add-mask::"); ok {
						visible = strings.ReplaceAll(visible, line+"\n", "")
						visible = strings.ReplaceAll(visible, mask, "***")
					}
				}
				if strings.Contains(visible, token) || strings.Contains(visible, encodedToken) {
					t.Fatalf("Git failure exposed a raw or derived credential in workflow logs:\n%s", visible)
				}
			})
		}
	}
}

func TestReleaseInputVerifierRejectsUnsafeArtifacts(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("GitHub release artifact verification executes with Bash on Ubuntu")
	}

	_, verify := requireStep(t, readWorkflow(t, "publish-release.yml").Jobs["goreleaser"], "Verify isolated release inputs")

	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
		valid  bool
	}{
		{name: "exact passive artifacts", valid: true},
		{name: "unexpected workflow file", mutate: func(t *testing.T, root string) {
			writeReleaseTestFile(t, filepath.Join(root, ".goreleaser.yml"), []byte("before: compromised"))
		}},
		{name: "cache payload", mutate: func(t *testing.T, root string) {
			writeReleaseTestFile(t, filepath.Join(root, ".cache", "payload"), []byte("compromised"))
		}},
		{name: "unexpected nested file", mutate: func(t *testing.T, root string) {
			writeReleaseTestFile(t, filepath.Join(root, "man", "man1", "unexpected.1.gz"), []byte("x"))
		}},
		{name: "symlinked asset", mutate: func(t *testing.T, root string) {
			path := filepath.Join(root, releaseInputPaths[0])
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, releaseInputPaths[1]), path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked directory", mutate: func(t *testing.T, root string) {
			man := filepath.Join(root, "man")
			target := filepath.Join(t.TempDir(), "man")
			if err := os.Rename(man, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, man); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing asset", mutate: func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, releaseInputPaths[0])); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "empty asset", mutate: func(t *testing.T, root string) {
			writeReleaseTestFile(t, filepath.Join(root, releaseInputPaths[0]), nil)
		}},
		{name: "oversized asset", mutate: func(t *testing.T, root string) {
			writeReleaseTestFile(t, filepath.Join(root, releaseInputPaths[0]), bytes.Repeat([]byte("x"), 10*1024*1024+1))
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := filepath.Join(t.TempDir(), "inputs")
			for _, path := range releaseInputPaths {
				writeReleaseTestFile(t, filepath.Join(root, path), []byte("safe passive release input"))
			}
			if test.mutate != nil {
				test.mutate(t, root)
			}
			workspace := t.TempDir()
			if output, err := exec.Command("git", "init", "--quiet", workspace).CombinedOutput(); err != nil {
				t.Fatalf("initialize isolated publisher checkout: %v\n%s", err, output)
			}
			command := exec.Command("bash", "-c", verify.Run)
			command.Dir = workspace
			command.Env = append(os.Environ(), "RELEASE_INPUT_DIR="+root)
			output, err := command.CombinedOutput()
			if test.valid && err != nil {
				t.Fatalf("valid release inputs were rejected: %v\n%s", err, output)
			}
			if !test.valid && err == nil {
				t.Fatalf("unsafe release inputs were accepted:\n%s", output)
			}
			if test.valid {
				for _, path := range releaseInputPaths {
					if _, err := os.Stat(filepath.Join(workspace, path)); err != nil {
						t.Errorf("verified release input %s was not staged: %v", path, err)
					}
				}
				statusCommand := exec.Command("git", "-C", workspace, "status", "--porcelain", "--untracked-files=all")
				status, err := statusCommand.Output()
				if err != nil || len(status) != 0 {
					t.Fatalf("exact verified release inputs make GoReleaser's checkout dirty: status=%q err=%v", status, err)
				}
				for _, payload := range []string{".github/workflows/compromised.yml", ".cache/poison"} {
					writeReleaseTestFile(t, filepath.Join(workspace, payload), []byte("unexpected"))
				}
				status, err = exec.Command("git", "-C", workspace, "status", "--porcelain", "--untracked-files=all").Output()
				if err != nil {
					t.Fatal(err)
				}
				for _, payload := range []string{".github/workflows/compromised.yml", ".cache/poison"} {
					if !strings.Contains(string(status), payload) {
						t.Errorf("unexpected %s was hidden from GoReleaser's dirty-check: %s", payload, status)
					}
				}
				for _, path := range releaseInputPaths {
					if strings.Contains(string(status), path) {
						t.Errorf("verified release input %s must remain excluded without hiding unexpected files", path)
					}
				}
			}
		})
	}
}

func writeReleaseTestFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrivilegedGoReleaserDoesNotExecuteGenerationHooks(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Before struct {
			Hooks []any `yaml:"hooks"`
		} `yaml:"before"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Before.Hooks) != 0 {
		t.Fatalf("privileged GoReleaser must not execute release-generation hooks: %v", config.Before.Hooks)
	}
}

func TestHistoricalTagCannotRunPrivilegedBeforeHooks(t *testing.T) {
	t.Parallel()

	legacyReleaseConfig := []byte("before:\n  hooks:\n    - go run ./cmd/openai/main.go @completion bash\n")
	var legacy struct {
		Before struct {
			Hooks []string `yaml:"hooks"`
		} `yaml:"before"`
	}
	if err := yaml.Unmarshal(legacyReleaseConfig, &legacy); err != nil || len(legacy.Before.Hooks) != 1 {
		t.Fatalf("invalid historical-tag regression fixture: hooks=%v err=%v", legacy.Before.Hooks, err)
	}
	_, release := requireStep(t, readWorkflow(t, "publish-release.yml").Jobs["goreleaser"], "Run GoReleaser")
	if strings.TrimSpace(release.Run) != `"$GORELEASER_EXECUTABLE" release --clean --skip=before` || release.Uses != "" || release.Env["GORELEASER_EXECUTABLE"] != "${{ steps.goreleaser.outputs.executable }}" {
		t.Fatalf("publisher must run only the verified immutable executable with legacy hooks disabled: %#v", release)
	}
}

func TestCISnapshotGeneratesInputsBeforeGoReleaser(t *testing.T) {
	t.Parallel()

	job := readWorkflow(t, "ci.yml").Jobs["build-artifacts"]
	if job.Timeout != 30 {
		t.Fatalf("CI artifact build timeout = %d minutes, want 30", job.Timeout)
	}
	generateIndex, generate := requireStep(t, job, "Generate release inputs")
	installerTestIndex, _ := requireStep(t, job, "Test verified GoReleaser installer")
	verifiedBinaryIndex, verifiedBinary := requireStep(t, job, "Set up verified GoReleaser")
	releaseIndex, release := requireStep(t, job, "Run GoReleaser")
	if verifiedBinary.Uses != "./.github/actions/setup-goreleaser" || verifiedBinary.ID != "goreleaser" {
		t.Fatal("CI must expose the reviewed digest-verified GoReleaser executable")
	}
	if strings.TrimSpace(release.Run) != `"$GORELEASER_EXECUTABLE" release --snapshot --clean --skip=publish` || release.Env["GORELEASER_EXECUTABLE"] != "${{ steps.goreleaser.outputs.executable }}" {
		t.Fatalf("CI must preserve the existing verified immutable snapshot invocation, got %#v", release)
	}
	if !(installerTestIndex < verifiedBinaryIndex && verifiedBinaryIndex < generateIndex && generateIndex < releaseIndex) {
		t.Fatal("CI must test and verify GoReleaser, then generate inputs before its credential exists")
	}
	if len(generate.Env) != 0 || release.Env["GITHUB_TOKEN"] == nil {
		t.Fatal("CI generation must remain credential-free while preserving existing GoReleaser authentication")
	}
	for _, path := range releaseInputPaths[:3] {
		if !strings.Contains(generate.Run, path) {
			t.Errorf("CI snapshot is missing generated input %q", path)
		}
	}
	if !strings.Contains(generate.Run, "@manpages -o man") {
		t.Fatal("CI snapshot no longer generates release manpages")
	}
	for _, path := range releaseInputPaths {
		if !strings.Contains(generate.Run, "'/"+path+"'") {
			t.Errorf("CI must ignore only exact validated generated input %q in its ephemeral checkout", path)
		}
	}
}

func TestReleaseWorkflowActionsRemainPinned(t *testing.T) {
	t.Parallel()

	pinnedAction := regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
	for name, job := range readWorkflow(t, "publish-release.yml").Jobs {
		for _, step := range job.Steps {
			if step.Uses == "./.github/actions/setup-goreleaser" {
				continue
			}
			if step.Uses != "" && !pinnedAction.MatchString(step.Uses) {
				t.Errorf("job %s step %q does not pin its action to a full commit SHA: %q", name, step.Name, step.Uses)
			}
		}
	}
}
