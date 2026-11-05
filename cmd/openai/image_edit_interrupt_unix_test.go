//go:build !windows

package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func imageEditInterruptCommand(t *testing.T, ctx context.Context, binary, home string, env []string, args ...string) (*exec.Cmd, func() error, func(error, []byte, time.Duration) error) {
	t.Helper()
	child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--"}, args...)...)
	child.Env = append(env, "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "GOMAXPROCS=2")
	return child, func() error { return child.Process.Signal(os.Interrupt) }, func(error, []byte, time.Duration) error { return nil }
}
