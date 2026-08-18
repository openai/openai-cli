package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBootstrapFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		failure     string
		wantCalls   []string
		wantSuccess bool
	}{
		{
			name:        "downloads and verifies consistent dependencies",
			wantCalls:   []string{"mod tidy -diff", "mod download", "mod verify"},
			wantSuccess: true,
		},
		{
			name:      "rejects inconsistent manifests before downloading",
			failure:   "mod tidy -diff",
			wantCalls: []string{"mod tidy -diff"},
		},
		{
			name:      "propagates dependency resolution failures",
			failure:   "mod download",
			wantCalls: []string{"mod tidy -diff", "mod download"},
		},
		{
			name:      "propagates checksum verification failures",
			failure:   "mod verify",
			wantCalls: []string{"mod tidy -diff", "mod download", "mod verify"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newBootstrapFixture(t)
			binDir := filepath.Join(root, "bin")
			if err := os.Mkdir(binDir, 0o755); err != nil {
				t.Fatal(err)
			}

			fakeGo := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$BOOTSTRAP_GO_LOG"
if [[ "$*" == "${BOOTSTRAP_GO_FAILURE:-}" ]]; then
  exit 23
fi
`
			if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(fakeGo), 0o755); err != nil {
				t.Fatal(err)
			}

			logPath := filepath.Join(root, "go.log")
			command := exec.Command("bash", filepath.Join(root, "scripts", "bootstrap"))
			command.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"BOOTSTRAP_GO_LOG="+logPath,
				"BOOTSTRAP_GO_FAILURE="+test.failure,
			)
			output, err := command.CombinedOutput()
			if (err == nil) != test.wantSuccess {
				t.Fatalf("bootstrap error = %v, want success %t; output:\n%s", err, test.wantSuccess, output)
			}

			calls, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			gotCalls := strings.Split(strings.TrimSpace(string(calls)), "\n")
			if !slices.Equal(gotCalls, test.wantCalls) {
				t.Errorf("Go calls = %v, want %v", gotCalls, test.wantCalls)
			}
		})
	}
}

func TestBootstrapRejectsManifestDriftWithoutMutation(t *testing.T) {
	root := newBootstrapFixture(t)
	dependency := filepath.Join(root, "dependency")
	if err := os.Mkdir(dependency, 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		filepath.Join(dependency, "go.mod"): "module example.com/unused\n\ngo 1.25.0\n",
		filepath.Join(root, "go.mod"): "module example.com/bootstrap-test\n\ngo 1.25.0\n\n" +
			"require example.com/unused v1.0.0\n\nreplace example.com/unused => ./dependency\n",
		filepath.Join(root, "go.sum"):  "example.com/unused v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n",
		filepath.Join(root, "main.go"): "package main\n\nfunc main() {}\n",
	}
	for path, contents := range files {
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	command := exec.Command("bash", filepath.Join(root, "scripts", "bootstrap"))
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("bootstrap accepted inconsistent manifests; output:\n%s", output)
	}
	if !strings.Contains(string(output), "go.mod") || !strings.Contains(string(output), "go.sum") {
		t.Errorf("bootstrap output does not identify both drifting manifests:\n%s", output)
	}

	for _, name := range []string{"go.mod", "go.sum"} {
		path := filepath.Join(root, name)
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(contents) != files[path] {
			t.Errorf("bootstrap mutated %s:\ngot:  %q\nwant: %q", name, contents, files[path])
		}
	}
}

func newBootstrapFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	if err := os.Mkdir(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	bootstrap, err := os.ReadFile("bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "bootstrap"), bootstrap, 0o755); err != nil {
		t.Fatal(err)
	}

	return root
}
