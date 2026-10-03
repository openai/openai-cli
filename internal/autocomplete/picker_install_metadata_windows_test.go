//go:build windows

package autocomplete

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestPickerWindowsProfileStagingRejectsDifferentPermissionsBeforeCopy(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "remove"}[remove], func(t *testing.T) {
			options, user := pickerWindowsTestInstallation(t)
			home := filepath.Dir(options.Profile)
			personal := []byte("# private synthetic configuration\n")
			require.NoError(t, os.WriteFile(options.Profile, personal, 0600))
			if remove {
				_, err := InstallPicker(t.Context(), options)
				require.NoError(t, err)
			}
			before, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			identity, err := os.Stat(options.Profile)
			require.NoError(t, err)
			// Keep the existing profile private and shield scripts from this
			// profile-directory fixture's permissions.
			pickerWindowsSetPermissions(t, options.Profile, user, "")
			pickerWindowsSetPermissions(t, filepath.Join(home, "config"), user, "")
			// Deny deletion so the temporary file survives deferred cleanup.
			// This exposes whether content was copied before ACL comparison,
			// without racing a reader or adding a production test callback.
			descriptor, err := windows.SecurityDescriptorFromString("O:" + user.String() + "G:BAD:P(D;;0x40;;;WD)(D;OIIO;SD;;;WD)(A;OICI;FA;;;" + user.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OIIO;FR;;;WD)")
			require.NoError(t, err)
			dacl, _, err := descriptor.DACL()
			require.NoError(t, err)
			t.Cleanup(func() {
				pickerWindowsSetPermissions(t, home, user, "")
				entries, err := os.ReadDir(home)
				require.NoError(t, err)
				for _, entry := range entries {
					if !entry.IsDir() {
						pickerWindowsSetPermissions(t, filepath.Join(home, entry.Name()), user, "")
					}
				}
			})
			require.NoError(t, windows.SetNamedSecurityInfo(home, windows.SE_FILE_OBJECT,
				windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil))
			if remove {
				_, err = RemovePicker(t.Context(), options)
			} else {
				_, err = InstallPicker(t.Context(), options)
			}
			require.ErrorContains(t, err, "different permissions")
			entries, err := os.ReadDir(home)
			require.NoError(t, err)
			var temporary []string
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".openai-picker-") {
					temporary = append(temporary, filepath.Join(home, entry.Name()))
				}
			}
			require.Len(t, temporary, 1, "fixture must retain the refused staging file")
			staged, err := os.ReadFile(temporary[0])
			require.NoError(t, err)
			require.Empty(t, staged, "no private profile bytes may reach incompatible staging permissions")
			require.Error(t, os.Remove(temporary[0]), "fixture must deny staging cleanup")
			after, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, before, after)
			current, err := os.Stat(options.Profile)
			require.NoError(t, err)
			require.True(t, os.SameFile(identity, current))
		})
	}
}

func TestPickerWindowsEmptyStagingMetadataRejectsPrivateProfile(t *testing.T) {
	options, user := pickerWindowsTestInstallation(t)
	home := filepath.Dir(options.Profile)
	personal := []byte("# private synthetic configuration\n")
	require.NoError(t, os.WriteFile(options.Profile, personal, 0600))
	pickerWindowsSetPermissions(t, options.Profile, user, "")
	pickerWindowsSetPermissions(t, home, user, "(A;OIIO;FR;;;WD)")
	root, err := os.OpenRoot(home)
	require.NoError(t, err)
	defer root.Close()
	snapshot, err := readPickerFile(t.Context(), root, filepath.Base(options.Profile))
	require.NoError(t, err)
	file, err := root.OpenFile(".empty-stage", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	defer file.Close()
	require.ErrorContains(t, checkPickerReplacementMetadata(root, filepath.Base(options.Profile), ".empty-stage", snapshot), "different permissions")
	staged, err := file.Stat()
	require.NoError(t, err)
	require.Zero(t, staged.Size())
}

func TestPickerWindowsProfileStagingKeepsMatchingInheritedPermissions(t *testing.T) {
	for _, flags := range []string{"OIIO", "OICIIO"} {
		t.Run(flags, func(t *testing.T) {
			options, user := pickerWindowsTestInstallation(t)
			pickerWindowsSetPermissions(t, filepath.Dir(options.Profile), user, "(A;"+flags+";FR;;;WD)")
			personal := []byte("# existing synthetic shared profile\n")
			require.NoError(t, os.WriteFile(options.Profile, personal, 0600))
			const information = windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION
			before, err := windows.GetNamedSecurityInfo(options.Profile, windows.SE_FILE_OBJECT, information)
			require.NoError(t, err)
			first, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			installed, err := windows.GetNamedSecurityInfo(options.Profile, windows.SE_FILE_OBJECT, information)
			require.NoError(t, err)
			require.NoError(t, comparePickerSecurityDescriptors(before, installed))
			// Exercise replacement during refresh as well as initial setup and
			// removal, retaining an ordinary inherited profile descriptor.
			script, err := os.ReadFile(first.ScriptPath)
			require.NoError(t, err)
			older := append(script, []byte("\n# earlier CLI script\n")...)
			oldPath := filepath.Join(options.Directory, pickerScriptName(options, older))
			require.NoError(t, os.WriteFile(oldPath, older, 0600))
			require.NoError(t, os.WriteFile(options.Profile, append(append([]byte(nil), personal...), renderPickerBlock(pickerInstalledBlock{Shell: options.Shell, Script: oldPath})...), 0600))
			require.NoError(t, os.Remove(first.ScriptPath))
			refreshed, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			require.True(t, refreshed.Changed)
			updated, err := windows.GetNamedSecurityInfo(options.Profile, windows.SE_FILE_OBJECT, information)
			require.NoError(t, err)
			require.NoError(t, comparePickerSecurityDescriptors(before, updated))
			_, err = RemovePicker(t.Context(), options)
			require.NoError(t, err)
			after, err := windows.GetNamedSecurityInfo(options.Profile, windows.SE_FILE_OBJECT, information)
			require.NoError(t, err)
			require.NoError(t, comparePickerSecurityDescriptors(before, after))
			data, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, personal, data)
		})
	}
}
