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

func imageModelsInterruptCommand(t *testing.T, ctx context.Context, binary, baseURL string) (*exec.Cmd, func() error) {
	t.Helper()
	// The existing controller creates a kill-on-close job before starting main.
	// Its new console confines the Ctrl+C event to the owned process tree.
	child := exec.CommandContext(ctx, binary, "-test.run=^TestShellFileWindowsConsoleHelper$", "--",
		binary, "-test.run=^TestMainDispatchProcess$", "--",
		"openai", "--format", "json", "images", "models")
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			child.Env = append(child.Env, name+"="+value)
		}
	}
	home := t.TempDir()
	child.Env = append(child.Env,
		"HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home, "TEMP="+home, "TMP="+home,
		"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=synthetic-models-key",
		"OPENAI_BASE_URL="+baseURL, "FORCE_COLOR=0", "GOMAXPROCS=2")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Cmd.Wait closes this owned pipe. The controller reads exactly one event.
	return child, func() error {
		_, err := input.Write([]byte{byte(windows.CTRL_C_EVENT)})
		return err
	}
}
