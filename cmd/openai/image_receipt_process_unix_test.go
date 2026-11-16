//go:build !windows

package main

import (
	"context"
	"os/exec"
	"testing"
)

func newImageReceiptProcess(t *testing.T, ctx context.Context, binary string, env []string, args ...string) (*exec.Cmd, func(error) error) {
	t.Helper()
	child := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestMainDispatchProcess$", "--"}, args...)...)
	child.Env = append(env, "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1")
	return child, func(error) error { return nil }
}
