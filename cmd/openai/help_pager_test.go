package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestMainHelpPagerTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PTYs do not establish native Windows interaction")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for POSIX PTY checks")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, python, "testdata/help_pager.py", binary)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("public help pager checks failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
