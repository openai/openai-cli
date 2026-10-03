//go:build windows

package autocomplete

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestPickerWindowsPermissionsDescriptors(t *testing.T) {
	const identity = "S-1-5-21-1000-2000-3000-1001"
	user, err := windows.StringToSid(identity)
	require.NoError(t, err)
	for _, test := range []struct {
		name, permissions string
		accepted          bool
	}{
		{"private", "D:P(A;;FA;;;" + identity + ")", true},
		{"normal profile", "D:AI(A;OICI;FA;;;" + identity + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)", true},
		{"readable by others", "D:(A;;FA;;;" + identity + ")(A;;FRFX;;;WD)", true},
		{"empty DACL", "D:", true},
		{"denied write", "D:(D;;FW;;;WD)(A;;FA;;;" + identity + ")", true},
		{"inherit only", "D:(A;OIIO;FA;;;WD)(A;;FA;;;" + identity + ")", true},
		{"absent DACL", "", false},
		{"null DACL", "D:NO_ACCESS_CONTROL", false},
		{"everyone full control", "D:(A;;FA;;;WD)", false},
		{"users write", "D:(A;;FW;;;BU)", false},
		{"authenticated users write", "D:(A;;GW;;;AU)", false},
		{"other user write", "D:(A;;FW;;;S-1-5-21-1000-2000-3000-1002)", false},
		{"inherited write", "D:(A;ID;FW;;;WD)", false},
		{"deny does not excuse allow", "D:(D;;FW;;;WD)(A;;FW;;;WD)", false},
		{"conditional allow", "D:(XA;;FA;;;WD;(@User.example == 1))", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString("O:" + identity + "G:BA" + test.permissions)
			require.NoError(t, err)
			err = validatePickerWindowsPermissions(descriptor, user, false)
			if test.accepted {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	for _, mask := range []uint32{
		windows.GENERIC_ALL, windows.GENERIC_WRITE, windows.WRITE_DAC, windows.WRITE_OWNER, windows.DELETE,
		windows.FILE_WRITE_DATA, windows.FILE_APPEND_DATA, windows.FILE_WRITE_EA, windows.FILE_WRITE_ATTRIBUTES,
		0x40, // FILE_DELETE_CHILD, even if children have private DACLs.
	} {
		t.Run(fmt.Sprintf("mutation %08x", mask), func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sG:BAD:(A;;0x%x;;;WD)", identity, mask))
			require.NoError(t, err)
			require.ErrorContains(t, validatePickerWindowsPermissions(descriptor, user, false), "writable by other users")
		})
	}
	foreign, err := windows.SecurityDescriptorFromString("O:SYG:BAD:(A;;FA;;;" + identity + ")")
	require.NoError(t, err)
	require.ErrorContains(t, validatePickerWindowsPermissions(foreign, user, false), "owned by the current user")
	require.Error(t, validatePickerWindowsPermissions(nil, user, false))
	require.Error(t, validatePickerWindowsPermissions(foreign, nil, false))
	elevated, err := windows.SecurityDescriptorFromString("O:BAG:BAD:(A;;FA;;;" + identity + ")(A;;FA;;;BA)")
	require.NoError(t, err)
	require.NoError(t, validatePickerWindowsPermissions(elevated, user, false))
}

func TestPickerWindowsInstallationRejectsOtherWriters(t *testing.T) {
	for _, target := range []string{"profile directory", "managed parent", "script directory", "profile", "script", "profile lock", "script lock"} {
		t.Run(target, func(t *testing.T) {
			options, user := pickerWindowsTestInstallation(t)
			ctx := context.Background()
			installed, err := InstallPicker(ctx, options)
			require.NoError(t, err)
			profileBefore, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			scriptBefore, err := os.ReadFile(installed.ScriptPath)
			require.NoError(t, err)
			paths := map[string]string{
				"profile directory": filepath.Dir(options.Profile),
				"managed parent":    filepath.Dir(options.Directory),
				"script directory":  options.Directory,
				"profile":           options.Profile,
				"script":            installed.ScriptPath,
				"profile lock":      filepath.Join(filepath.Dir(options.Profile), ".bashrc.openai-picker.lock"),
				"script lock":       filepath.Join(options.Directory, ".picker-install.lock"),
			}
			pickerWindowsSetPermissions(t, paths[target], user, "(A;;FW;;;WD)")
			_, err = InstallPicker(ctx, options)
			require.ErrorContains(t, err, "writable by other users")
			_, err = RemovePicker(ctx, options)
			require.ErrorContains(t, err, "writable by other users")
			if target != "profile lock" && target != "script lock" {
				active, err := IsPickerInstalled(ctx, options)
				require.ErrorContains(t, err, "writable by other users")
				require.False(t, active)
			}
			profileAfter, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, profileBefore, profileAfter)
			scriptAfter, err := os.ReadFile(installed.ScriptPath)
			require.NoError(t, err)
			require.Equal(t, scriptBefore, scriptAfter)
		})
	}
}

func TestPickerWindowsSetupLockRejectsOtherWriters(t *testing.T) {
	options, user := pickerWindowsTestInstallation(t)
	ctx := context.Background()
	require.NoError(t, WithPickerSetupLock(ctx, options.Directory, func() error { return nil }))
	pickerWindowsSetPermissions(t, filepath.Join(options.Directory, ".picker-setup.lock"), user, "(A;;FW;;;WD)")
	called := false
	err := WithPickerSetupLock(ctx, options.Directory, func() error { called = true; return nil })
	require.ErrorContains(t, err, "writable by other users")
	require.False(t, called)
	pickerWindowsSetPermissions(t, filepath.Join(options.Directory, ".picker-setup.lock"), user, "(A;;FR;;;WD)")
	err = WithPickerSetupLock(ctx, options.Directory, func() error { called = true; return nil })
	require.ErrorContains(t, err, "lock must be private")
	require.False(t, called)
}

func TestPickerWindowsNewFilesRejectInheritedOtherWriters(t *testing.T) {
	for _, target := range []string{"new script", "new profile"} {
		t.Run(target, func(t *testing.T) {
			options, user := pickerWindowsTestInstallation(t)
			directory := filepath.Dir(options.Profile)
			if target == "new script" {
				directory = options.Directory
			}
			// This entry does not grant access to the directory itself. It
			// becomes effective on newly created files despite mode 0600.
			pickerWindowsSetPermissions(t, directory, user, "(A;OIIO;FW;;;WD)")
			root, err := os.OpenRoot(directory)
			require.NoError(t, err)
			defer root.Close()
			if target == "new script" {
				_, err = writePickerScript(context.Background(), root, "picker.bash", []byte("# synthetic script\n"))
			} else {
				err = replacePickerProfile(context.Background(), root, ".bashrc", pickerFileSnapshot{}, []byte("# synthetic profile\n"))
			}
			require.ErrorContains(t, err, "writable by other users")
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			for _, entry := range entries {
				require.True(t, entry.IsDir(), "unsafe staging file left behind: %s", entry.Name())
			}
		})
	}
}

func pickerWindowsTestInstallation(t *testing.T) (PickerInstallation, *windows.SID) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	home := t.TempDir()
	pickerWindowsSetPermissions(t, home, user.User.Sid, "")
	options := PickerInstallation{
		Shell: CompletionStyleBash, Directory: filepath.Join(home, "config", "openai", "shell"), Profile: filepath.Join(home, ".bashrc"),
	}
	require.NoError(t, os.MkdirAll(options.Directory, 0700))
	return options, user.User.Sid
}

func pickerWindowsSetPermissions(t *testing.T, path string, user *windows.SID, extra string) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString("O:" + user.String() + "G:BAD:P(A;OICI;FA;;;" + user.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)" + extra)
	require.NoError(t, err)
	dacl, _, err := descriptor.DACL()
	require.NoError(t, err)
	require.NoError(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, user, nil, dacl, nil))
}
