package mocktest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var suiteCLI func(context.Context) (string, error)

// RunTests owns one lazily built CLI for a package's tests. Each invocation still
// runs in a fresh process; only compilation is shared. Call it from TestMain.
func RunTests(m *testing.M) int {
	directory, err := os.MkdirTemp("", "openai-mock-tests-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create test CLI directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(directory)
	var once sync.Once
	binary := filepath.Join(directory, "openai.exe")
	var buildErr error
	suiteCLI = func(ctx context.Context) (string, error) {
		once.Do(func() {
			_, filename, _, ok := runtime.Caller(0)
			if !ok {
				buildErr = fmt.Errorf("locate CLI source")
				return
			}
			project := filepath.Join(filepath.Dir(filename), "..", "..", "cmd", "openai")
			build := exec.CommandContext(ctx, "go", "build", "-o", binary, project)
			if output, err := build.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build test CLI: %w\n%s", err, output)
			}
		})
		return binary, buildErr
	}
	return m.Run()
}
