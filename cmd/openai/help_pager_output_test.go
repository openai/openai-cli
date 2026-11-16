package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestHelpPagerCancelsSaturatedTerminalOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX terminal flow control")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for POSIX PTY checks")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, python, "-I", "testdata/help_pager_output.py", binary).CombinedOutput()
	if err != nil {
		t.Fatalf("saturated terminal: %v\n%s", err, output)
	}
	t.Log(string(output))
}
