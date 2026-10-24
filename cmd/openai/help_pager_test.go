package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/app"
)

// The PTY driver limits only this child process to 64 descriptors.
func TestMainHelpPagerStartupFailureProcess(t *testing.T) {
	if os.Getenv("OPENAI_CLI_HELP_PAGER_EXHAUST_FDS") != "1" {
		return
	}
	// Initialize Go's signal pipe before exhausting descriptors so the failure
	// reaches Tea's cancelable reader instead of aborting inside the runtime.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	var files []*os.File
	for {
		file, err := os.Open(os.DevNull)
		if errors.Is(err, syscall.EMFILE) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
		if len(files) > 64 {
			t.Fatal("PTY driver did not bound the descriptor limit")
		}
	}
	code := app.Run(t.Context(), []string{"openai"}, commands.Command)
	for _, file := range files {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	os.Exit(code)
}

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
