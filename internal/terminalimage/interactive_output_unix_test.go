//go:build darwin || linux

package terminalimage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestInteractiveImageWorkersOwnSignalGroups(t *testing.T) {
	path, err := os.Executable()
	require.NoError(t, err)
	for _, isolated := range []bool{false, true} {
		ctx := context.WithValue(t.Context(), interactiveImageOutputContextKey{}, isolated)
		out, err := os.Create(filepath.Join(t.TempDir(), "output"))
		require.NoError(t, err)
		defer out.Close()
		session, err := startKittySession(ctx, path, out)
		require.NoError(t, err)
		defer session.Close()
		for _, worker := range session.workers {
			group, err := syscall.Getpgid(worker.command.Process.Pid)
			require.NoError(t, err)
			if isolated {
				require.Equal(t, worker.command.Process.Pid, group)
				require.NotEqual(t, syscall.Getpgrp(), group)
				sessionID, err := unix.Getsid(worker.command.Process.Pid)
				require.NoError(t, err)
				require.Equal(t, worker.command.Process.Pid, sessionID)
			} else {
				require.Equal(t, syscall.Getpgrp(), group)
			}
		}
		_, err = session.writeJob(ctx, 'J', func(writer io.Writer) error {
			_, err := io.WriteString(writer, "screen restoration")
			return err
		})
		require.NoError(t, err)
		require.NoError(t, session.Close())
		for _, worker := range session.workers {
			require.NotNil(t, worker.command.ProcessState, "reap each directly owned helper")
		}
		data, err := os.ReadFile(out.Name())
		require.NoError(t, err)
		require.Equal(t, "screen restoration", string(data))
	}
}

func TestInteractiveImageCancellationKeepsTransportEvidenceSeparate(t *testing.T) {
	transfer := errors.Join(context.Canceled, io.ErrClosedPipe)
	cancelled := &CanceledOutputError{cause: transfer}
	reset := errors.New("terminal reset failed")
	err := errors.Join(cancelled, reset)
	var found *CanceledOutputError
	require.ErrorAs(t, err, &found)
	require.ErrorIs(t, found, context.Canceled)
	require.ErrorIs(t, found, io.ErrClosedPipe)
	require.ErrorIs(t, err, reset)
	require.NotErrorIs(t, found, reset)
}
