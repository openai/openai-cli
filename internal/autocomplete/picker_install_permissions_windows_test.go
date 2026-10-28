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

func TestPickerWindowsChildInheritanceDescriptors(t *testing.T) {
	const identity = "S-1-5-21-1000-2000-3000-1001"
	const installer = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
	user, err := windows.StringToSid(identity)
	require.NoError(t, err)
	for _, test := range []struct {
		name, entry        string
		ancestor, accepted bool
	}{
		{"file inheritance", "(A;OIIO;FA;;;WD)", false, false},
		{"directory inheritance", "(A;CIIO;FA;;;WD)", false, false},
		{"both inheritance", "(A;OICIIO;FA;;;WD)", false, false},
		{"file inheritance one generation", "(A;OINPIO;FA;;;WD)", false, false},
		{"directory inheritance one generation", "(A;CINPIO;FA;;;WD)", false, false},
		{"inherited template", "(A;OICIIOID;FW;;;WD)", false, false},
		{"inherited read only", "(A;OICIIO;FRFX;;;WD)", false, true},
		{"inherit only without inheritance", "(A;IO;FA;;;WD)", false, true},
		{"current creator owner template", "(A;OICIIO;FA;;;CO)", false, true},
		{"creator owner one generation", "(A;OICINPIO;FA;;;CO)", false, true},
		{"effective creator owner", "(A;OICI;FA;;;CO)", false, false},
		{"creator group template", "(A;OICIIO;FA;;;CG)", false, false},
		{"user template", "(A;OICIIO;FA;;;" + identity + ")", false, true},
		{"system template", "(A;OICIIO;FA;;;SY)", false, true},
		{"administrators template", "(A;OICIIO;FA;;;BA)", false, true},
		{"managed directory service template", "(A;OICIIO;FA;;;" + installer + ")", false, false},
		{"ancestor service template", "(A;OICIIO;FA;;;" + installer + ")", true, true},
		{"ancestor create subdirectory only", "(A;;0x4;;;WD)", true, true},
		{"ancestor inherited file append", "(A;OIIO;0x4;;;WD)", true, false},
		{"ancestor inherited subdirectory creation", "(A;CIIO;0x4;;;WD)", true, false},
		{"ancestor file-only template one generation", "(A;OINPIO;FA;;;WD)", true, true},
		{"ancestor inherited directory mutation one generation", "(A;CINPIO;FA;;;WD)", true, false},
		{"ancestor effective mutation despite no propagation", "(A;OINP;FA;;;WD)", true, false},
		{"normal volume root templates", "(A;;0x100004;;;AU)(A;OICIIO;FA;;;CO)", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString("O:" + identity + "G:BAD:(A;;FA;;;" + identity + ")" + test.entry)
			require.NoError(t, err)
			err = validatePickerWindowsPermissionPolicy(descriptor, user, false, test.ancestor, true)
			if test.accepted {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "writable by other users")
			}
		})
	}
}

func TestPickerWindowsCreationOwnerPolicy(t *testing.T) {
	const identity = "S-1-5-21-1000-2000-3000-1001"
	user, err := windows.StringToSid(identity)
	require.NoError(t, err)
	for _, test := range []struct {
		name, identity string
		accepted       bool
	}{
		{"current user", identity, true},
		{"administrators", "S-1-5-32-544", true},
		{"other user", "S-1-5-21-1000-2000-3000-1002", false},
		{"other group", "S-1-5-32-545", false},
		{"system is not current user", "S-1-5-18", false},
		{"creator owner is not resolved", "S-1-3-0", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner, err := windows.StringToSid(test.identity)
			require.NoError(t, err)
			require.Equal(t, test.accepted, pickerWindowsManagedOwner(owner, user))
		})
	}
	require.False(t, pickerWindowsManagedOwner(nil, user))
	require.False(t, pickerWindowsManagedOwner(user, nil))
}

func TestPickerWindowsCurrentTokenCreationOwner(t *testing.T) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	require.NoError(t, err)
	require.NoError(t, checkPickerWindowsCreationOwner(token, user.User.Sid))
	file, err := os.CreateTemp(t.TempDir(), "created-owner-")
	require.NoError(t, err)
	defer file.Close()
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	require.NoError(t, err)
	owner, _, err := descriptor.Owner()
	require.NoError(t, err)
	require.True(t, pickerWindowsManagedOwner(owner, user.User.Sid), "normal and elevated tokens must create trusted owners")
}

func TestPickerWindowsInheritedPermissionsRejectedBeforeWrites(t *testing.T) {
	for _, flags := range []string{"OIIO", "CIIO", "OICIIO", "OINPIO", "CINPIO"} {
		for _, target := range []string{"profile directory", "managed parent", "script directory"} {
			t.Run(flags+"/"+target, func(t *testing.T) {
				options, user := pickerWindowsTestInstallation(t)
				home := filepath.Dir(options.Profile)
				paths := map[string]string{
					"profile directory": home,
					"managed parent":    filepath.Dir(options.Directory),
					"script directory":  options.Directory,
				}
				// Shield existing descendants so the selected directory's
				// inheritance policy is what blocks creation, not a child ACL.
				pickerWindowsSetPermissions(t, filepath.Join(home, "config"), user, "")
				pickerWindowsSetPermissions(t, options.Directory, user, "")
				pickerWindowsSetPermissions(t, paths[target], user, "(A;"+flags+";FA;;;WD)")
				before := pickerWindowsTreePaths(t, home)
				_, err := InstallPicker(t.Context(), options)
				require.ErrorContains(t, err, "writable by other users")
				if target != "profile directory" {
					called := false
					err = WithPickerSetupLock(t.Context(), options.Directory, func() error { called = true; return nil })
					require.ErrorContains(t, err, "writable by other users")
					require.False(t, called)
				}
				require.Equal(t, before, pickerWindowsTreePaths(t, home), "must reject before creating locks or staging files")
			})
		}
	}
}

func TestPickerWindowsCreatorOwnerInheritanceAllowsInstallation(t *testing.T) {
	options, user := pickerWindowsTestInstallation(t)
	home := filepath.Dir(options.Profile)
	pickerWindowsSetPermissions(t, home, user, "(A;OICIIO;FA;;;CO)")
	options.Directory = filepath.Join(home, "new-config", "openai", "shell")
	options.Profile = filepath.Join(home, "new-profile", ".bashrc")
	require.NoError(t, WithPickerSetupLock(t.Context(), options.Directory, func() error { return nil }))
	installed, err := InstallPicker(t.Context(), options)
	require.NoError(t, err)
	require.True(t, installed.Changed)
	active, err := IsPickerInstalled(t.Context(), options)
	require.NoError(t, err)
	require.True(t, active)
	_, err = RemovePicker(t.Context(), options)
	require.NoError(t, err)
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
				"profile lock":      filepath.Join(filepath.Dir(options.Profile), "..bashrc.openai-picker.lock"),
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

func pickerWindowsTreePaths(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	require.NoError(t, filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		paths = append(paths, relative)
		return err
	}))
	return paths
}
