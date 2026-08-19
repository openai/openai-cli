package releaseworkflow

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

const (
	releaseInputsArtifact  = "openai-release-inputs"
	checkoutAction         = "actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd"
	setupGoAction          = "actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff"
	uploadArtifactAction   = "actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02"
	downloadArtifactAction = "actions/download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093"
)

var releaseInputPaths = []string{
	"completions/openai.bash",
	"completions/openai.zsh",
	"completions/openai.fish",
	"man/man1/openai.1.gz",
}

type workflow struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Environment string            `yaml:"environment"`
	Needs       any               `yaml:"needs"`
	Outputs     map[string]string `yaml:"outputs"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []workflowStep    `yaml:"steps"`
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
	if setup.Uses != setupGoAction || setup.With["cache"] != false {
		t.Fatalf("unprivileged Go setup must be pinned with cache disabled: %#v", setup)
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
	if !(checkoutIndex < tagIndex && tagIndex < setupIndex && setupIndex < generationIndex && generationIndex < uploadIndex) {
		t.Fatal("checkout, validated tag, cache-disabled Go setup, generation, and upload must run in order")
	}
	for _, step := range job.Steps {
		if strings.Contains(fmt.Sprint(step.With, step.Env, step.Run), "secrets.") {
			t.Fatalf("unprivileged release-input job references secrets in %q", step.Name)
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
	tagIndex, tag := requireStep(t, job, "Ensure release tag is on main")
	if !strings.Contains(tag.Run, `git merge-base --is-ancestor "$tag_sha" origin/main`) {
		t.Fatal("publisher must independently verify the trusted release tag")
	}
	if tag.Env["EXPECTED_TAG_SHA"] != "${{ needs.release-inputs.outputs.tag-sha }}" || !strings.Contains(tag.Run, `test "$tag_sha" = "$EXPECTED_TAG_SHA"`) {
		t.Fatal("publisher must reject isolated inputs generated from a different trusted tag revision")
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
	if !(tagIndex < downloadIndex && downloadIndex < verifyIndex && verifyIndex < firstCredentialIndex) {
		t.Fatal("trusted tag and isolated artifacts must be verified before any publishing credential exists")
	}
	for index, step := range job.Steps[:firstCredentialIndex] {
		if strings.Contains(fmt.Sprint(step.With, step.Env, step.Run), "secrets.") {
			t.Fatalf("step %d (%q) receives privileged secrets before release-input validation", index, step.Name)
		}
	}
}

func TestReleaseInputVerifierRejectsUnsafeArtifacts(t *testing.T) {
	t.Parallel()

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
	if release.With["args"] != "release --clean --skip=before" {
		t.Fatalf("historical tagged release hooks remain executable under publisher credentials: args=%v", release.With["args"])
	}
}

func TestCISnapshotGeneratesInputsBeforeGoReleaser(t *testing.T) {
	t.Parallel()

	job := readWorkflow(t, "ci.yml").Jobs["build-artifacts"]
	generateIndex, generate := requireStep(t, job, "Generate release inputs")
	releaseIndex, release := requireStep(t, job, "Run goreleaser")
	if generateIndex >= releaseIndex {
		t.Fatal("CI snapshot inputs must be generated before the GoReleaser credential exists")
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
			if step.Uses != "" && !pinnedAction.MatchString(step.Uses) {
				t.Errorf("job %s step %q does not pin its action to a full commit SHA: %q", name, step.Name, step.Uses)
			}
		}
	}
}
