//go:build darwin || linux

package terminal

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestInteractiveImageDiagnosticSessionIsOptIn(t *testing.T) {
	for _, isolated := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		if isolated {
			ctx = DetachedOutputContext(ctx)
		}
		input, feed, err := os.Pipe()
		require.NoError(t, err)
		defer input.Close()
		defer feed.Close()
		command := exec.CommandContext(ctx, "/bin/cat")
		command.Stdin = input
		configureOutputProcess(ctx, command)
		require.NoError(t, command.Start())
		defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
		group, err := syscall.Getpgid(command.Process.Pid)
		require.NoError(t, err)
		session, err := unix.Getsid(command.Process.Pid)
		require.NoError(t, err)
		if isolated {
			require.Equal(t, command.Process.Pid, group)
			require.Equal(t, command.Process.Pid, session)
		} else {
			require.Equal(t, syscall.Getpgrp(), group)
			parentSession, err := unix.Getsid(os.Getpid())
			require.NoError(t, err)
			require.Equal(t, parentSession, session)
		}
		require.NoError(t, feed.Close())
		require.NoError(t, command.Wait())
		require.NotNil(t, command.ProcessState)
	}
}
