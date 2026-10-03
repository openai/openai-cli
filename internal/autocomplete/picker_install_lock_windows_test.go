//go:build windows

package autocomplete

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestPickerWindowsLockCreatedPrivateUnderReadOnlyInheritance(t *testing.T) {
	for _, entry := range []string{
		"(A;OI;FR;;;WD)", "(A;OIIO;FR;;;WD)", "(A;OICI;FR;;;WD)",
		"(A;OICIIO;FR;;;WD)", "(A;OINPIO;FR;;;WD)", "(A;CIIO;FR;;;WD)",
		"(A;OICIIO;FA;;;CO)(A;OIIO;FR;;;WD)",
	} {
		t.Run(entry, func(t *testing.T) {
			options, user := pickerWindowsTestInstallation(t)
			pickerWindowsSetPermissions(t, options.Directory, user, entry)
			root, err := os.OpenRoot(options.Directory)
			require.NoError(t, err)
			defer root.Close()
			if entry == "(A;OIIO;FR;;;WD)" {
				// Establish that the same parent exposes an ordinary 0600
				// creation through inheritance, as the old creation path did.
				control, err := root.OpenFile(".inherited-control", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
				require.NoError(t, err)
				require.ErrorContains(t, checkPickerWindowsPermissions(control, true), "lock must be private")
				require.NoError(t, control.Close())
				require.NoError(t, root.Remove(".inherited-control"))
			}
			// Inspect the immediately returned handle before any lock or
			// postcreation validation could modify its security descriptor.
			file, err := createPickerInstallLock(root, ".new-lock")
			require.NoError(t, err)
			defer file.Close()
			pickerWindowsAssertPrivateLock(t, file)
			_, err = file.WriteString("existing lock sentinel")
			require.NoError(t, err)
			before, err := file.Stat()
			require.NoError(t, err)
			other, err := createPickerInstallLock(root, ".new-lock")
			require.ErrorIs(t, err, os.ErrExist)
			require.Nil(t, other)
			after, err := root.Stat(".new-lock")
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			data, err := root.ReadFile(".new-lock")
			require.NoError(t, err)
			require.Equal(t, "existing lock sentinel", string(data))
		})
	}
}

func TestPickerWindowsReadOnlyInheritanceAllowsInstallation(t *testing.T) {
	for _, flags := range []string{"OI", "OIIO", "OICI", "OICIIO", "OINPIO", "CIIO"} {
		t.Run(flags, func(t *testing.T) {
			options, user := pickerWindowsTestInstallation(t)
			pickerWindowsSetPermissions(t, filepath.Dir(options.Profile), user, "(A;"+flags+";FR;;;WD)")
			require.NoError(t, WithPickerSetupLock(t.Context(), options.Directory, func() error { return nil }))
			installed, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			require.True(t, installed.Changed)
			for _, path := range []string{
				filepath.Join(options.Directory, ".picker-setup.lock"),
				filepath.Join(options.Directory, ".picker-install.lock"),
				filepath.Join(filepath.Dir(options.Profile), "..bashrc.openai-picker.lock"),
			} {
				file, err := os.Open(path)
				require.NoError(t, err)
				pickerWindowsAssertPrivateLock(t, file)
				require.NoError(t, file.Close())
			}
			active, err := IsPickerInstalled(t.Context(), options)
			require.NoError(t, err)
			require.True(t, active)
			repeated, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			require.False(t, repeated.Changed)
			_, err = RemovePicker(t.Context(), options)
			require.NoError(t, err)
			require.NoError(t, WithPickerSetupLock(t.Context(), options.Directory, func() error { return nil }))
		})
	}
}

func TestPickerWindowsPrivateLockCoordinatesContention(t *testing.T) {
	options, user := pickerWindowsTestInstallation(t)
	pickerWindowsSetPermissions(t, options.Directory, user, "(A;OIIO;FR;;;WD)")
	root, err := openPickerDirectory(t.Context(), options.Directory, false)
	require.NoError(t, err)
	defer root.Close()
	first, err := lockPickerInstallation(t.Context(), root, ".lock")
	require.NoError(t, err)
	defer first.Close()
	before, err := first.Stat()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	blocked, err := lockPickerInstallation(ctx, root, ".lock")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, blocked)
	require.NoError(t, first.Close())
	next, err := lockPickerInstallation(t.Context(), root, ".lock")
	require.NoError(t, err)
	defer next.Close()
	after, err := next.Stat()
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after))
	pickerWindowsAssertPrivateLock(t, next)
}

func TestPickerWindowsPrivateLockRejectsNonlocalNames(t *testing.T) {
	options, _ := pickerWindowsTestInstallation(t)
	root, err := os.OpenRoot(options.Directory)
	require.NoError(t, err)
	defer root.Close()
	for _, name := range []string{"", "..", "../outside", `..\outside`, "nested/lock", `nested\lock`, "file:stream", `C:\lock`} {
		file, err := createPickerInstallLock(root, name)
		require.Error(t, err, name)
		require.Nil(t, file, name)
	}
	entries, err := os.ReadDir(options.Directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func pickerWindowsAssertPrivateLock(t *testing.T, file *os.File) {
	t.Helper()
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	control, _, err := descriptor.Control()
	require.NoError(t, err)
	require.NotZero(t, control&windows.SE_DACL_PROTECTED)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	require.NoError(t, validatePickerWindowsPermissions(descriptor, user.User.Sid, true))
}
