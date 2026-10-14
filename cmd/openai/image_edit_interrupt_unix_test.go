//go:build !windows

package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func imageEditInterruptCommand(t *testing.T, ctx context.Context, binary, home string, env []string, args ...string) (*exec.Cmd, func() error, func([]byte) error) {
	t.Helper()
	child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--"}, args...)...)
	child.Env = append(env, "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1")
	return child, func() error { return child.Process.Signal(os.Interrupt) }, func([]byte) error { return nil }
}
