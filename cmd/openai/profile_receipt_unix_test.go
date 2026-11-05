//go:build !windows

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMainProfileCommittedWithClosedOutput(t *testing.T) {
	home := t.TempDir()
	env := profileProcessEnvironment(home)
	binary, err := os.Executable()
	require.NoError(t, err)
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { writer.Close() })
	require.NoError(t, reader.Close())
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--", "openai", "profile", "create", "stored")
	cmd.Env = append(env, "PATH="+os.Getenv("PATH"), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "COMPLETION_STYLE=bash")
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = writer, &stderr
	var exit *exec.ExitError
	require.ErrorAs(t, cmd.Run(), &exit)
	require.Equal(t, 1, exit.ExitCode(), stderr.String())
	require.Contains(t, stderr.String(), "Saved profiles were updated")
	got := runMainDispatchWithEnv(t, "bash", env, "openai", "profile", "get", "stored")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, got.stdout, "Profile: stored")
}
