package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMainHelpScriptsRunUsesCopyableInvocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/run uses a POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	// A second invocation exercises Go's cached executable, whose path differs
	// from the temporary executable used immediately after linking.
	for attempt := range 2 {
		for _, tc := range []struct {
			args []string
			want []string
		}{
			{[]string{"images", "preview", "--help"}, []string{`  go run ./cmd/openai images preview "photo.png"`, "Full help: go run ./cmd/openai help --all images preview"}},
			{[]string{"help", "--all", "images", "generate"}, []string{`    go run ./cmd/openai images generate --prompt "A tiny cat" --name cat`}},
			{[]string{"help", "--all", "images", "edit"}, []string{`    go run ./cmd/openai images edit --image "photo.png" --prompt "Make the sky purple" --name purple-sky`}},
			{[]string{"help", "--all", "images", "create-variation"}, []string{`    go run ./cmd/openai images create-variation --image "photo.png" --name variation`}},
		} {
			child := exec.CommandContext(ctx, "./scripts/run", tc.args...)
			child.Dir = root
			for _, entry := range os.Environ() {
				name, _, _ := strings.Cut(entry, "=")
				if !strings.HasPrefix(name, "OPENAI_") && name != "BASH_ENV" && name != "ENV" && name != "PATH" && name != "GOTOOLCHAIN" {
					child.Env = append(child.Env, entry)
				}
			}
			child.Env = append(child.Env, "OPENAI_BASE_URL=http://127.0.0.1:1", "GOTOOLCHAIN=local",
				"PATH="+filepath.Join(runtime.GOROOT(), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			if err := child.Run(); err != nil || stderr.Len() != 0 {
				t.Fatalf("scripts/run %v attempt %d: %v; stdout=%q stderr=%q", tc.args, attempt+1, err, stdout.String(), stderr.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("scripts/run %v attempt %d lacks %q:\n%s", tc.args, attempt+1, want, stdout.String())
				}
			}
		}
	}
}
