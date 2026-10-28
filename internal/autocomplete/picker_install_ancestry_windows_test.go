//go:build windows

package autocomplete

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestPickerWindowsAncestorDescriptors(t *testing.T) {
	const identity = "S-1-5-21-1000-2000-3000-1001"
	const installer = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
	user, err := windows.StringToSid(identity)
	require.NoError(t, err)
	for _, test := range []struct {
		name, owner, entries string
		accepted             bool
	}{
		{"current user", identity, "(A;;FA;;;" + identity + ")", true},
		{"administrators", "BA", "(A;;FA;;;BA)(A;;FRFX;;;BU)", true},
		{"system", "SY", "(A;;FA;;;SY)(A;;FA;;;BA)(A;;FRFX;;;BU)", true},
		{"trusted installer", installer, "(A;;FA;;;" + installer + ")(A;;FA;;;SY)(A;;FA;;;BA)(A;;FRFX;;;BU)", true},
		{"normal volume root", installer, "(A;;FA;;;" + installer + ")(A;;FA;;;SY)(A;;FA;;;BA)(A;;FRFX;;;BU)(A;;0x100004;;;AU)(A;OICIIO;FA;;;CO)", true},
		{"foreign owner", "S-1-5-21-1000-2000-3000-1002", "(A;;FA;;;" + identity + ")", false},
		{"other service owner", "S-1-5-80-1-2-3-4-5", "(A;;FA;;;" + identity + ")", false},
		{"delete children", identity, "(A;;0x40;;;WD)", false},
		{"delete this directory", identity, "(A;;SD;;;WD)", false},
		{"change ACL", identity, "(A;;WD;;;WD)", false},
		{"change owner", identity, "(A;;WO;;;WD)", false},
		{"inherited modify", identity, "(A;ID;0x1301bf;;;BU)", false},
		{"create subdirectories", identity, "(A;;0x4;;;WD)", true},
		{"create files can set reparse points", identity, "(A;;0x2;;;WD)", false},
		{"write attributes can set reparse points", identity, "(A;;0x100;;;WD)", false},
		{"unrelated service write", identity, "(A;;FA;;;S-1-5-80-1-2-3-4-5)", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString("O:" + test.owner + "G:BAD:" + test.entries)
			require.NoError(t, err)
			err = validatePickerWindowsPermissionPolicy(descriptor, user, false, true, false)
			if test.accepted {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPickerWindowsAncestryRejectsUntrustedReplacement(t *testing.T) {
	for _, previouslyInstalled := range []bool{false, true} {
		for _, ancestor := range []string{"config root", "higher ancestor"} {
			for _, permission := range []struct{ name, mask string }{
				{"delete children", "0x40"}, {"delete ancestor", "SD"}, {"change ACL", "WD"},
				{"change owner", "WO"}, {"generic write", "GW"}, {"create files", "0x2"}, {"write attributes", "0x100"},
			} {
				t.Run(fmt.Sprintf("installed=%t/%s/%s", previouslyInstalled, ancestor, permission.name), func(t *testing.T) {
					options, user := pickerWindowsTestInstallation(t)
					home := filepath.Dir(options.Profile)
					options.Directory = filepath.Join(home, "shared", "config", "openai", "shell")
					require.NoError(t, os.MkdirAll(options.Directory, 0700))
					require.NoError(t, os.WriteFile(options.Profile, []byte("# personal startup\n"), 0600))
					var installed PickerInstallResult
					var scriptBefore []byte
					var err error
					if previouslyInstalled {
						installed, err = InstallPicker(t.Context(), options)
						require.NoError(t, err)
						scriptBefore, err = os.ReadFile(installed.ScriptPath)
						require.NoError(t, err)
					}
					before, err := os.ReadFile(options.Profile)
					require.NoError(t, err)
					unsafe := filepath.Join(home, "shared")
					if ancestor == "config root" {
						unsafe = filepath.Join(unsafe, "config")
					}
					// This effective-only grant leaves the private child DACLs
					// unchanged; checking openai and shell alone cannot see it.
					pickerWindowsSetPermissions(t, unsafe, user, "(A;;"+permission.mask+";;;WD)")
					_, err = InstallPicker(t.Context(), options)
					require.ErrorContains(t, err, "writable by other users")
					called := false
					err = WithPickerSetupLock(t.Context(), options.Directory, func() error { called = true; return nil })
					require.ErrorContains(t, err, "writable by other users")
					require.False(t, called)
					if previouslyInstalled {
						active, err := IsPickerInstalled(t.Context(), options)
						require.ErrorContains(t, err, "writable by other users")
						require.False(t, active)
						_, err = RemovePicker(t.Context(), options)
						require.ErrorContains(t, err, "writable by other users")
						after, err := os.ReadFile(installed.ScriptPath)
						require.NoError(t, err)
						require.Equal(t, scriptBefore, after)
					} else {
						entries, err := os.ReadDir(options.Directory)
						require.NoError(t, err)
						require.Empty(t, entries, "unsafe ancestry must be rejected before creating scripts or locks")
					}
					after, err := os.ReadFile(options.Profile)
					require.NoError(t, err)
					require.Equal(t, before, after)
				})
			}
		}
	}
}

func TestPickerWindowsAncestryAllowsCreateSubdirectoryOnly(t *testing.T) {
	options, user := pickerWindowsTestInstallation(t)
	config := filepath.Dir(filepath.Dir(options.Directory))
	pickerWindowsSetPermissions(t, config, user, "(A;;0x4;;;WD)")
	installed, err := InstallPicker(t.Context(), options)
	require.NoError(t, err)
	require.True(t, installed.Changed)
	active, err := IsPickerInstalled(t.Context(), options)
	require.NoError(t, err)
	require.True(t, active)
	_, err = RemovePicker(t.Context(), options)
	require.NoError(t, err)
}

func TestPickerWindowsAncestryChecksInheritanceBeforeCreatingChildren(t *testing.T) {
	for _, grant := range []struct{ name, entry string }{
		{"file mutation", "(A;OIIO;FW;;;WD)"},
		{"directory mutation", "(A;CIIO;FA;;;WD)"},
		{"file and directory mutation", "(A;OICIIO;FA;;;WD)"},
		{"one generation directory mutation", "(A;CINPIO;FA;;;WD)"},
		{"inherited file append", "(A;OIIO;0x4;;;WD)"},
		{"inherited subdirectory creation", "(A;CIIO;0x4;;;WD)"},
	} {
		for _, target := range []string{"script ancestry", "profile ancestry"} {
			t.Run(grant.name+"/"+target, func(t *testing.T) {
				options, user := pickerWindowsTestInstallation(t)
				home := filepath.Dir(options.Profile)
				unsafe := home
				if target == "script ancestry" {
					unsafe = filepath.Join(home, "new-config")
					require.NoError(t, os.Mkdir(unsafe, 0700))
					options.Directory = filepath.Join(unsafe, "missing", "openai", "shell")
				} else {
					// Existing script storage has an independent protected DACL.
					// Only creation of the missing profile parent must be denied.
					pickerWindowsSetPermissions(t, filepath.Join(home, "config"), user, "")
					options.Profile = filepath.Join(home, "missing", ".bashrc")
				}
				pickerWindowsSetPermissions(t, unsafe, user, grant.entry)
				before := pickerWindowsTreePaths(t, home)
				var err error
				if target == "script ancestry" {
					called := false
					err = WithPickerSetupLock(t.Context(), options.Directory, func() error { called = true; return nil })
					require.False(t, called)
				} else {
					_, err = InstallPicker(t.Context(), options)
				}
				require.ErrorContains(t, err, "writable by other users")
				require.Equal(t, before, pickerWindowsTreePaths(t, home), "must reject before mkdir, not after creating an exposed child")
			})
		}
	}
}

func TestPickerWindowsAncestryAllowsProtectedExistingChildren(t *testing.T) {
	for _, entry := range []string{"(A;OIIO;FA;;;WD)", "(A;CIIO;FA;;;WD)", "(A;OICIIO;0x4;;;WD)"} {
		t.Run(entry, func(t *testing.T) {
			options, user := pickerWindowsTestInstallation(t)
			managed := filepath.Dir(options.Directory)
			// The existing protected subtree does not inherit this ancestor's
			// grant. Traversing it creates no child below the unsafe template.
			pickerWindowsSetPermissions(t, managed, user, "")
			pickerWindowsSetPermissions(t, filepath.Dir(managed), user, entry)
			require.NoError(t, WithPickerSetupLock(t.Context(), options.Directory, func() error { return nil }))
			installed, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			require.True(t, installed.Changed)
			active, err := IsPickerInstalled(t.Context(), options)
			require.NoError(t, err)
			require.True(t, active)
			_, err = RemovePicker(t.Context(), options)
			require.NoError(t, err)
		})
	}
}

func TestPickerWindowsAncestryAllowsNonpropagatingFileOnlyTemplate(t *testing.T) {
	options, user := pickerWindowsTestInstallation(t)
	config := filepath.Dir(filepath.Dir(options.Directory))
	pickerWindowsSetPermissions(t, config, user, "(A;OINPIO;FA;;;WD)")
	// No files are created directly under config, and the file-only template
	// does not propagate into the new directories or their script/lock files.
	options.Directory = filepath.Join(config, "new", "openai", "shell")
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

func TestPickerWindowsAncestryRejectsJunction(t *testing.T) {
	options, _ := pickerWindowsTestInstallation(t)
	home := filepath.Dir(options.Profile)
	alias := filepath.Join(home, "alias")
	// Junction creation does not need the symbolic-link privilege. Exercise a
	// real native reparse point rather than skipping this contract on Windows.
	output, err := exec.CommandContext(t.Context(), "cmd.exe", "/c", "mklink", "/J", alias, filepath.Join(home, "config")).CombinedOutput()
	require.NoError(t, err, string(output))
	options.Directory = filepath.Join(alias, "openai", "shell")
	_, err = InstallPicker(t.Context(), options)
	require.Error(t, err, "junction ancestry must be refused before setup writes")
	_, err = os.Stat(options.Profile)
	require.ErrorIs(t, err, os.ErrNotExist)
	entries, err := os.ReadDir(options.Directory)
	require.NoError(t, err)
	require.Empty(t, entries)
}
