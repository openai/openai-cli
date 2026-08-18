//go:build windows

package cmd

import (
	"os"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestReplaySnapshotHasProtectedCurrentUserDACL(t *testing.T) {
	snapshot, err := createReplaySnapshotFile()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshot.Close()) })

	securityDescriptor, err := windows.GetSecurityInfo(
		windows.Handle(snapshot.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	require.NoError(t, err)
	control, _, err := securityDescriptor.Control()
	require.NoError(t, err)
	require.NotZero(t, control&windows.SE_DACL_PROTECTED)

	dacl, _, err := securityDescriptor.DACL()
	require.NoError(t, err)
	require.NotNil(t, dacl)
	require.Equal(t, uint16(1), dacl.AceCount)
	var ace *windows.ACCESS_ALLOWED_ACE
	require.NoError(t, windows.GetAce(dacl, 0, &ace))
	require.Equal(t, uint8(windows.ACCESS_ALLOWED_ACE_TYPE), ace.Header.AceType)
	require.Zero(t, ace.Header.AceFlags&windows.INHERITED_ACE)

	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	require.True(t, windows.EqualSid(currentUser.User.Sid, aceSID))

	otherHandle, err := os.Open(snapshot.Name())
	if otherHandle != nil {
		require.NoError(t, otherHandle.Close())
	}
	require.Error(t, err)
}

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
