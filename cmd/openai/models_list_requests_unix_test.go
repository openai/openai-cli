//go:build !windows

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The test executable dispatches the production main entrypoint using the same
// private terminal driver that can also exercise a standalone CLI build.
func TestMainModelsListRequestPagination(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for Models pagination PTY checks")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	script := filepath.Join("..", "..", "scripts", "check-models-list-requests.py")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, python, "-I", "-S", "-B", script, binary, "--test-binary")
	child.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "LANG=en_US.UTF-8"}
	child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
	child.WaitDelay = 2 * time.Second
	output, err := child.CombinedOutput()
	require.NoError(t, ctx.Err(), "PTY observer timed out: %s", output)
	require.NoError(t, err, "PTY observer failed: %s", output)
	t.Logf("%s", bytes.TrimSpace(output))
}
