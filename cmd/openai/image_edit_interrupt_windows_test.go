//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func imageEditInterruptCommand(t *testing.T, ctx context.Context, binary, home string, env []string, args ...string) (*exec.Cmd, func() error, func(error, []byte, time.Duration) error) {
	t.Helper()
	return windowsInterruptCommand(t, ctx, binary, home, env, 130, args...)
}

func windowsInterruptCommand(t *testing.T, ctx context.Context, binary, home string, env []string, expectedExit uint32, args ...string) (*exec.Cmd, func() error, func(error, []byte, time.Duration) error) {
	t.Helper()
	command := append([]string{"-test.run=^TestShellFileWindowsConsoleHelper$", "--",
		binary, "-test.run=^TestMainDispatchProcess$", "--"}, args...)
	child := exec.CommandContext(ctx, binary, command...)
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			child.Env = append(child.Env, name+"="+value)
		}
	}
	child.Env = append(child.Env, env...)
	resultPath := filepath.Join(home, "interrupt-console-result.json")
	child.Env = append(child.Env, "APPDATA="+home, "LOCALAPPDATA="+home, "TEMP="+home, "TMP="+home,
		"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_CLI_TEST_CONSOLE_RESULT="+resultPath, "GOMAXPROCS=2")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Cmd.Wait closes this pipe. The existing controller owns its child tree.
	interrupt := func() error {
		_, err := input.Write([]byte{byte(windows.CTRL_C_EVENT)})
		return err
	}
	verify := func(waitErr error, stderr []byte, elapsed time.Duration) error {
		if err := verifyImageEditInterruptCompletion(elapsed); err != nil {
			return err
		}
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) {
			return fmt.Errorf("interrupted command did not return a child exit status: %v", waitErr)
		}
		data, err := os.ReadFile(resultPath)
		if err != nil {
			return fmt.Errorf("read interrupt controller result: %w", err)
		}
		var result windowsConsoleResult
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("decode interrupt controller result: %w", err)
		}
		if err := verifyWindowsInterruptControllerResult(result, exit.ExitCode(), expectedExit); err != nil {
			return fmt.Errorf("%w; result=%q helper_exit=%d stderr=%q", err, data, exit.ExitCode(), stderr)
		}
		t.Logf("CTRL_C event=%d child_exit=%d helper_exit=%d expected_exit=%d elapsed=%s",
			result.Event, *result.ChildExit, exit.ExitCode(), expectedExit, elapsed)
		return nil
	}
	return child, interrupt, verify
}

func verifyWindowsInterruptControllerResult(result windowsConsoleResult, helperExit int, expectedExit uint32) error {
	// A queued event must not validate an unrelated early CLI failure.
	if !result.Started || !result.EventSent || result.Event != windows.CTRL_C_EVENT || result.ChildExit == nil ||
		*result.ChildExit != helperExit || result.TimedOut || result.ForcedKill {
		return fmt.Errorf("controller result does not establish cancellation")
	}
	// Only native termination accepts the signed Windows status representation.
	if *result.ChildExit != int(expectedExit) &&
		!(expectedExit == uint32(windows.STATUS_CONTROL_C_EXIT) && *result.ChildExit == int(int32(expectedExit))) {
		return fmt.Errorf("controller exit %d does not match expected cancellation status %d", *result.ChildExit, expectedExit)
	}
	return nil
}

func TestImageEditInterruptControllerResult(t *testing.T) {
	controlExit := uint32(windows.STATUS_CONTROL_C_EXIT)
	for _, test := range []struct {
		name   string
		change func(*windowsConsoleResult, *int)
		valid  bool
	}{
		{"cooperative CTRL_C exit", nil, true},
		{"legacy CTRL_C exit", func(result *windowsConsoleResult, helper *int) {
			*result.ChildExit, *helper = int(controlExit), int(controlExit)
		}, false},
		{"signed legacy CTRL_C exit", func(result *windowsConsoleResult, helper *int) {
			*result.ChildExit, *helper = int(int32(controlExit)), int(int32(controlExit))
		}, false},
		{"unrelated exit 1", func(result *windowsConsoleResult, helper *int) { *result.ChildExit, *helper = 1, 1 }, false},
		{"unrelated exit 7", func(result *windowsConsoleResult, helper *int) { *result.ChildExit, *helper = 7, 7 }, false},
		{"missing startup", func(result *windowsConsoleResult, _ *int) { result.Started = false }, false},
		{"missing event", func(result *windowsConsoleResult, _ *int) { result.EventSent = false }, false},
		{"wrong event", func(result *windowsConsoleResult, _ *int) { result.Event = windows.CTRL_BREAK_EVENT }, false},
		{"missing child status", func(result *windowsConsoleResult, _ *int) { result.ChildExit = nil }, false},
		{"zero child status", func(result *windowsConsoleResult, helper *int) { *result.ChildExit, *helper = 0, 0 }, false},
		{"mismatched child status", func(_ *windowsConsoleResult, helper *int) { *helper = 8 }, false},
		{"controller timeout", func(result *windowsConsoleResult, _ *int) { result.TimedOut = true }, false},
		{"forced kill", func(result *windowsConsoleResult, _ *int) { result.ForcedKill = true }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			childExit, helperExit := 130, 130
			result := windowsConsoleResult{Started: true, EventSent: true, Event: windows.CTRL_C_EVENT, ChildExit: &childExit}
			if test.change != nil {
				test.change(&result, &helperExit)
			}
			if err := verifyWindowsInterruptControllerResult(result, helperExit, 130); (err == nil) != test.valid {
				t.Fatalf("controller result accepted=%t, want %t: %v", err == nil, test.valid, err)
			}
		})
	}
}
