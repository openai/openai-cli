//go:build windows

package autocomplete

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestPickerWindowsRecoveryDirectoryCreatedPrivate(t *testing.T) {
	for _, entry := range []string{"(A;OICI;FR;;;WD)", "(A;OICIIO;FR;;;WD)", "(A;CIIO;FR;;;WD)"} {
		t.Run(entry, func(t *testing.T) {
			options, user := pickerWindowsTestInstallation(t)
			pickerWindowsSetPermissions(t, options.Directory, user, entry)
			root, err := os.OpenRoot(options.Directory)
			require.NoError(t, err)
			defer root.Close()
			require.NoError(t, createPickerRecoveryDirectory(root, "recovery"))
			directory, err := root.Open("recovery")
			require.NoError(t, err)
			defer directory.Close()
			require.NoError(t, checkPickerRecoveryDirectory(directory))
			descriptor, err := windows.GetSecurityInfo(windows.Handle(directory.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
			require.NoError(t, err)
			control, _, err := descriptor.Control()
			require.NoError(t, err)
			require.NotZero(t, control&windows.SE_DACL_PROTECTED)
			child, err := root.OpenFile("recovery/journal", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			require.NoError(t, err)
			defer child.Close()
			require.NoError(t, checkPickerWindowsPermissions(child, true), "new journal metadata must also inherit only private grants")
		})
	}
}
