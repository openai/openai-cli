//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func imageEditInterruptCommand(t *testing.T, ctx context.Context, binary, home string, env []string, args ...string) (*exec.Cmd, func() error, func([]byte) error) {
	t.Helper()
	command := append([]string{"-test.run=^TestShellFileWindowsConsoleHelper$", "--",
		binary, "-test.run=^TestMainDispatchProcess$", "--"}, args...)
	// Product JSON distinguishes cancellation from a controller failure.
	command = append(command, "--format-error", "json")
	child := exec.CommandContext(ctx, binary, command...)
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			child.Env = append(child.Env, name+"="+value)
		}
	}
	child.Env = append(child.Env, env...)
	child.Env = append(child.Env, "APPDATA="+home, "LOCALAPPDATA="+home, "TEMP="+home, "TMP="+home,
		"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "GOMAXPROCS=2")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Cmd.Wait closes this pipe. The existing controller owns its child tree.
	interrupt := func() error {
		_, err := input.Write([]byte{byte(windows.CTRL_C_EVENT)})
		return err
	}
	return child, interrupt, verifyImageEditInterruptDiagnostic
}
