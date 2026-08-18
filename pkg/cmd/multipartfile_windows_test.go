//go:build windows

package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplaySnapshotIsDeletedAfterFinalHandleCloses(t *testing.T) {
	snapshot, err := createReplaySnapshotFile()
	require.NoError(t, err)
	path := snapshot.Name()
	_, err = snapshot.Write([]byte("sensitive upload"))
	require.NoError(t, err)

	duplicate, err := duplicateFile(snapshot)
	require.NoError(t, err)
	require.NoError(t, snapshot.Close())
	require.NoError(t, duplicate.Close())

	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}
