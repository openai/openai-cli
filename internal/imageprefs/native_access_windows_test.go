//go:build windows

package imageprefs

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

func imagePreferenceSymlink(t *testing.T, target, path string) {
	t.Helper()
	err := os.Symlink(target, path)
	if os.Getenv("OPENAI_CLI_REQUIRE_IMAGE_STATE_SYMLINKS") != "1" &&
		(errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) || errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_NOT_SUPPORTED)) {
		t.Skipf("temporary symlinks unavailable: %v", err)
	}
	require.NoError(t, err)
}

// Deny one operation on an owned temporary path. Restore its original DACL
// before TempDir cleanup, including when a fixture or product assertion fails.
func denyImagePreferenceAccess(t *testing.T, path string, access uint32) func() {
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
		require.NoError(t, restoreErr, "restore temporary preference access")
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

func TestImagePreferenceWindowsAccessDeniedKeepsFile(t *testing.T) {
	for _, operation := range []string{"read", "write"} {
		t.Run(operation, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "image-preferences.json")
			require.NoError(t, Save(t.Context(), path, false))
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			identity, err := os.Stat(path)
			require.NoError(t, err)
			var restore func()
			if operation == "read" {
				restore = denyImagePreferenceAccess(t, path, windows.FILE_READ_DATA)
				_, err = os.ReadFile(path)
				require.ErrorIs(t, err, os.ErrPermission, "the fixture must actually deny reads")
				_, err = Load(t.Context(), path)
				require.ErrorIs(t, err, os.ErrPermission)
			} else {
				// FILE_WRITE_DATA is FILE_ADD_FILE for a directory.
				restore = denyImagePreferenceAccess(t, directory, windows.FILE_WRITE_DATA)
				probe, err := os.CreateTemp(directory, "denial-probe-")
				if probe != nil {
					require.NoError(t, probe.Close())
					require.NoError(t, os.Remove(probe.Name()))
				}
				require.ErrorIs(t, err, os.ErrPermission, "the fixture must actually deny file creation")
			}
			require.ErrorIs(t, Save(t.Context(), path, true), os.ErrPermission)
			restore()
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
			current, err := os.Stat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(identity, current))
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Len(t, entries, 2, "only the previous record and persistent lock may remain")
			require.NoError(t, Save(t.Context(), path, true), "failed saves must release their writer lock")
			mode, err := Load(t.Context(), path)
			require.NoError(t, err)
			require.Equal(t, "on", mode)
		})
	}
}
