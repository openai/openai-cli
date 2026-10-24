package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/app"
)

// The PTY driver cancels the caller only after the first page becomes visible.
func TestMainHelpPagerContextProcess(t *testing.T) {
	value := os.Getenv("OPENAI_CLI_HELP_PAGER_CONTEXT_FD")
	if value == "" {
		return
	}
	fd, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	control := os.NewFile(uintptr(fd), "help-pager-context")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		var signal [1]byte
		if _, err := io.ReadFull(control, signal[:]); err != nil {
			os.Exit(2)
		}
		if err := control.Close(); err != nil {
			os.Exit(2)
		}
		cancel()
	}()
	os.Exit(app.Run(ctx, []string{"openai"}, commands.Command))
}

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
