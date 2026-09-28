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
		child := exec.CommandContext(ctx, "./scripts/run", "images", "preview", "--help")
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
			t.Fatalf("scripts/run attempt %d: %v; stdout=%q stderr=%q", attempt+1, err, stdout.String(), stderr.String())
		}
		for _, want := range []string{
			`  go run ./cmd/openai images preview "photo.png"`,
			"Full help: go run ./cmd/openai help --all images preview",
		} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("scripts/run attempt %d lacks %q:\n%s", attempt+1, want, stdout.String())
			}
		}
	}
}
