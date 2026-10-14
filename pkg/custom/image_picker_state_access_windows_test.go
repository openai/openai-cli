//go:build windows

package custom

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func imagePickerStateSymlink(t *testing.T, target, path string) {
	t.Helper()
	err := os.Symlink(target, path)
	if os.Getenv("OPENAI_CLI_REQUIRE_IMAGE_STATE_SYMLINKS") != "1" &&
		(errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) || errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_NOT_SUPPORTED)) {
		t.Skipf("temporary symlinks unavailable: %v", err)
	}
	require.NoError(t, err)
}

// This fixture denies access only on a task-owned temporary path. It tests
// failed state operations, not an owner-only DACL policy in the product.
func denyImagePickerStateAccess(t *testing.T, path string, access uint32) func() {
	t.Helper()
	original, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	originalACL, _, err := original.DACL()
	require.NoError(t, err)
	control, _, err := original.Control()
	require.NoError(t, err)
	var information windows.SECURITY_INFORMATION = windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	if control&windows.SE_DACL_PROTECTED != 0 {
		information = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	var once sync.Once
	var restoreErr error
	restore := func() {
		once.Do(func() {
			restoreErr = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, information, nil, nil, originalACL, nil)
		})
		require.NoError(t, restoreErr, "restore temporary picker-state access")
	}
	t.Cleanup(restore)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(D;;0x%x;;;%s)(A;;FA;;;%s)", access, sid, sid))
	require.NoError(t, err)
	acl, _, err := descriptor.DACL()
	require.NoError(t, err)
	require.NoError(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil))
	return restore
}

func TestImagePickerStateWindowsAccessDeniedKeepsPrevious(t *testing.T) {
	for _, operation := range []string{"read", "write"} {
		t.Run(operation, func(t *testing.T) {
			path := pickerStatePath(t)
			before := pickerStateSettings(t)
			before.prompt = "Previous synthetic draft"
			require.NoError(t, saveImagePickerState(t.Context(), path, before))
			bytesBefore, err := os.ReadFile(path)
			require.NoError(t, err)
			identity, err := os.Stat(path)
			require.NoError(t, err)
			after := before
			after.prompt = "Unsaved replacement draft"
			after.quality = "high"
			var restore func()
			if operation == "read" {
				restore = denyImagePickerStateAccess(t, path, windows.FILE_READ_DATA)
				_, err = os.ReadFile(path)
				require.ErrorIs(t, err, os.ErrPermission, "the fixture must actually deny reads")
				_, _, err = loadImagePickerState(t.Context(), path)
				require.ErrorIs(t, err, os.ErrPermission)
			} else {
				// FILE_WRITE_DATA is FILE_ADD_FILE for a directory.
				restore = denyImagePickerStateAccess(t, filepath.Dir(path), windows.FILE_WRITE_DATA)
				probe, err := os.CreateTemp(filepath.Dir(path), "denial-probe-")
				if probe != nil {
					require.NoError(t, probe.Close())
					require.NoError(t, os.Remove(probe.Name()))
				}
				require.ErrorIs(t, err, os.ErrPermission, "the fixture must actually deny file creation")
			}
			require.ErrorIs(t, saveImagePickerState(t.Context(), path, after), os.ErrPermission)
			restore()
			bytesAfter, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, bytesBefore, bytesAfter)
			current, err := os.Stat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(identity, current))
			got, found, err := loadImagePickerState(t.Context(), path)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, before, got)
			entries, err := os.ReadDir(filepath.Dir(path))
			require.NoError(t, err)
			require.Len(t, entries, 1, "a failed state write must not leave temporary files")
			require.NoError(t, saveImagePickerState(t.Context(), path, after))
		})
	}
}
