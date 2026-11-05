package mocktest

import (
	"context"
	"fmt"
	"io"
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
func RunTests(m *testing.M) (code int) {
	var directory string
	defer func() {
		suiteCLI = nil
		if directory == "" {
			return
		}
		if err := os.RemoveAll(directory); err != nil {
			fmt.Fprintf(os.Stderr, "remove test CLI directory: %v\n", err)
			code = 1
		}
	}()
	var once sync.Once
	var binary string
	var buildErr error
	suiteCLI = func(ctx context.Context) (string, error) {
		once.Do(func() {
			directory, buildErr = os.MkdirTemp("", "openai-mock-tests-")
			if buildErr != nil {
				buildErr = fmt.Errorf("create test CLI directory: %w", buildErr)
				return
			}
			binary = filepath.Join(directory, "openai.exe")
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

// CopyCLI gives a test a private executable while RunTests owns compilation.
// The destination must not exist; its parent should belong to the caller.
func CopyCLI(t *testing.T, destination string) {
	t.Helper()
	if suiteCLI == nil {
		t.Fatal("CLI tests must call mocktest.RunTests from TestMain")
	}
	binary, err := suiteCLI(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := copyCLI(binary, destination); err != nil {
		t.Fatalf("copy test CLI: %v", err)
	}
}

func copyCLI(binary, destination string) error {
	source, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
