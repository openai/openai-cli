//go:build !windows

package main

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func imageModelsInterruptCommand(t *testing.T, ctx context.Context, binary, baseURL string) (*exec.Cmd, func() error) {
	t.Helper()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--",
		"openai", "--format", "json", "images", "models")
	child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=synthetic-models-key", "OPENAI_BASE_URL=" + baseURL, "FORCE_COLOR=0"}
	return child, func() error { return child.Process.Signal(os.Interrupt) }
}
