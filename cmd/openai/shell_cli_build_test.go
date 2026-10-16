package main

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
	"time"
)

var shellTestCLI func() (string, error)

// Own the lazy build for the entire suite, beyond any one test's TempDir.
func runShellTests(m *testing.M) (code int) {
	var directory string
	defer func() {
		if directory != "" {
			if err := os.RemoveAll(directory); err != nil {
				fmt.Fprintf(os.Stderr, "remove shell test CLI directory: %v\n", err)
				code = 1
			}
		}
	}()
	shellTestCLI = sync.OnceValues(func() (string, error) {
		var err error
		directory, err = os.MkdirTemp("", "openai-shell-tests-")
		if err != nil {
			return "", fmt.Errorf("create shell test CLI directory: %w", err)
		}
		binary := filepath.Join(directory, "openai.exe")
		goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
		if runtime.GOOS == "windows" {
			goBinary += ".exe"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		build := exec.CommandContext(ctx, goBinary, "build", "-o", binary, ".")
		if output, err := build.CombinedOutput(); err != nil {
			return "", fmt.Errorf("build shell test CLI: %w\n%s", err, output)
		}
		return binary, nil
	})
	return m.Run()
}

// Copy rather than link: tests retain independent executable paths and files,
// including directories with shell metacharacters and executable substitutions.
func copyShellTestCLI(t *testing.T, destination string) {
	t.Helper()
	binary, err := shellTestCLI()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(binary)
	if err != nil {
		t.Fatalf("open built shell test CLI: %v", err)
	}
	defer source.Close()
	target, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatalf("create shell test CLI copy: %v", err)
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		t.Fatalf("copy shell test CLI: %v", copyErr)
	}
	if closeErr != nil {
		t.Fatalf("close shell test CLI copy: %v", closeErr)
	}
}
