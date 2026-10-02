//go:build windows

package autocomplete

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestPickerFileMetadataPreservesZoneIdentifierAtSnapshotRecheck(t *testing.T) {
	directory := t.TempDir()
	profile := filepath.Join(directory, ".bashrc")
	require.NoError(t, os.WriteFile(profile, []byte("# existing local profile\n"), 0600))
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	snapshot, err := readPickerFile(context.Background(), root, ".bashrc")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(profile+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0600))
	require.ErrorContains(t, checkPickerSnapshot(context.Background(), root, ".bashrc", snapshot), "protected alternate-stream metadata")
	data, err := os.ReadFile(profile + ":Zone.Identifier")
	require.NoError(t, err)
	require.Equal(t, "[ZoneTransfer]\r\nZoneId=3\r\n", string(data))
}

func TestPickerReplacementMetadataKeepsSameInheritedPermissions(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, ".bashrc"), []byte("# existing profile\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "staged.bash"), []byte("# replacement profile\n"), 0600))
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	snapshot, err := readPickerFile(context.Background(), root, ".bashrc")
	require.NoError(t, err)
	require.NoError(t, checkPickerReplacementMetadata(root, ".bashrc", "staged.bash", snapshot))
	require.NoError(t, checkPickerReplacementMetadata(root, "new.bash", "staged.bash", pickerFileSnapshot{}))
	require.NoError(t, root.Rename(".bashrc", "old.bash"))
	require.NoError(t, os.WriteFile(filepath.Join(directory, ".bashrc"), snapshot.data, 0600))
	require.ErrorIs(t, checkPickerReplacementMetadata(root, ".bashrc", "staged.bash", snapshot), errPickerInstallChanged)
}

func TestPickerSecurityDescriptorComparisonPreservesPermissions(t *testing.T) {
	const permissions = "O:SYG:BAD:AI(A;ID;FA;;;SY)(A;ID;FA;;;BA)"
	existing, err := windows.SecurityDescriptorFromString(permissions)
	require.NoError(t, err)
	replacement, err := windows.SecurityDescriptorFromString(permissions)
	require.NoError(t, err)
	require.NoError(t, comparePickerSecurityDescriptors(existing, replacement))
	for name, changed := range map[string]string{
		"owner":       "O:BAG:BAD:AI(A;ID;FA;;;SY)(A;ID;FA;;;BA)",
		"group":       "O:SYG:SYD:AI(A;ID;FA;;;SY)(A;ID;FA;;;BA)",
		"access":      "O:SYG:BAD:AI(A;ID;FR;;;SY)(A;ID;FA;;;BA)",
		"protected":   "O:SYG:BAD:PAI(A;ID;FA;;;SY)(A;ID;FA;;;BA)",
		"inheritance": "O:SYG:BAD:(A;ID;FA;;;SY)(A;ID;FA;;;BA)",
		"absent ACL":  "O:SYG:BA",
	} {
		t.Run(name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString(changed)
			require.NoError(t, err)
			require.Error(t, comparePickerSecurityDescriptors(existing, descriptor))
		})
	}
	require.Error(t, comparePickerSecurityDescriptors(existing, nil))
	require.Error(t, comparePickerSecurityDescriptors(nil, replacement))
}
