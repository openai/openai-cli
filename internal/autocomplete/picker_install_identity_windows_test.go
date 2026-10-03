//go:build windows

package autocomplete

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func pickerWindowsShortPath(t *testing.T, path string) string {
	t.Helper()
	input, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	buffer := make([]uint16, 32768)
	length, err := windows.GetShortPathName(input, &buffer[0], uint32(len(buffer)))
	require.NoError(t, err)
	require.Less(t, length, uint32(len(buffer)))
	short := windows.UTF16ToString(buffer[:length])
	if strings.EqualFold(short, path) {
		t.Skip("the filesystem did not provide an 8.3 alias")
	}
	return short
}

func TestPickerWindowsProfileIdentity(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "MiXeD Profile Directory")
	require.NoError(t, os.Mkdir(directory, 0700))
	profile := filepath.Join(directory, "StartupProfile.rc")
	require.NoError(t, os.WriteFile(profile, []byte("# personal profile\n"), 0600))
	canonical, err := filepath.EvalSymlinks(profile)
	require.NoError(t, err)
	root, err := openPickerDirectory(t.Context(), directory, false)
	require.NoError(t, err)
	defer root.Close()
	for _, kind := range []string{"case", "short"} {
		t.Run(kind, func(t *testing.T) {
			alias := strings.ToUpper(profile)
			if kind == "short" {
				alias = pickerWindowsShortPath(t, profile)
			}
			actual, err := pickerProfileIdentity(t.Context(), root, alias)
			require.NoError(t, err)
			require.Equal(t, canonical, actual)
		})
	}
	missing := filepath.Join(strings.ToUpper(directory), "NewProfile.rc")
	actual, err := pickerProfileIdentity(t.Context(), root, missing)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(filepath.Dir(canonical), "NewProfile.rc"), actual)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1, "identity inspection must not create a missing profile or locks")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = pickerProfileIdentity(ctx, root, profile)
	require.ErrorIs(t, err, context.Canceled)
}

func TestPickerWindowsProfileAliasLifecycle(t *testing.T) {
	for _, kind := range []string{"case", "short"} {
		t.Run(kind, func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleBash)
			options.Profile = filepath.Join(filepath.Dir(options.Profile), "StartupProfile.rc")
			personal := []byte("# shared shell spelling\n")
			require.NoError(t, os.WriteFile(options.Profile, personal, 0600))
			alias := options
			alias.Profile = strings.ToUpper(options.Profile)
			if kind == "short" {
				alias.Profile = pickerWindowsShortPath(t, options.Profile)
			}
			installed, err := InstallPicker(t.Context(), options)
			require.NoError(t, err)
			if kind == "short" {
				// Atomic replacement may change the filesystem's short-name
				// assignment. Only an alias that still exists identifies a file.
				alias.Profile = pickerWindowsShortPath(t, options.Profile)
			}
			before, err := os.Stat(options.Profile)
			require.NoError(t, err)
			active, err := IsPickerInstalled(t.Context(), alias)
			require.NoError(t, err)
			require.True(t, active)
			again, err := InstallPicker(t.Context(), alias)
			require.NoError(t, err)
			require.False(t, again.Changed)
			require.Equal(t, installed.ScriptPath, again.ScriptPath)
			after, err := os.Stat(options.Profile)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after), "alias status/repeated setup must not rewrite the profile")
			removed, err := RemovePicker(t.Context(), alias)
			require.NoError(t, err)
			require.True(t, removed.Changed)
			data, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			require.Equal(t, personal, data)
			_, err = os.Stat(installed.ScriptPath)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestPickerWindowsProfileAliasSharesTransactionLock(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	options.Profile = filepath.Join(filepath.Dir(options.Profile), "StartupProfile.rc")
	require.NoError(t, os.WriteFile(options.Profile, []byte("# profile\n"), 0600))
	root, err := openPickerDirectory(t.Context(), filepath.Dir(options.Profile), false)
	require.NoError(t, err)
	defer root.Close()
	lock, err := lockPickerInstallation(t.Context(), root, ".StartupProfile.rc.openai-picker.lock")
	require.NoError(t, err)
	defer lock.Close()
	alias := options
	alias.Profile = pickerWindowsShortPath(t, options.Profile)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = InstallPicker(ctx, alias)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = os.Stat(options.Directory)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPickerWindowsProfileLegacySpellingPreservesUnprovenOwnership(t *testing.T) {
	for _, action := range []string{"refresh", "remove", "missing script", "missing directory"} {
		t.Run(action, func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleBash)
			options.Profile = filepath.Join(filepath.Dir(options.Profile), "MixedLegacyStartup.rc")
			legacy := options
			legacy.Profile = strings.ToUpper(options.Profile)
			script, err := renderInstalledPicker(options)
			require.NoError(t, err)
			legacyScript := filepath.Join(options.Directory, pickerScriptName(legacy, script))
			require.NotEqual(t, pickerScriptName(options, script), filepath.Base(legacyScript))
			require.NoError(t, os.MkdirAll(options.Directory, 0700))
			require.NoError(t, os.WriteFile(legacyScript, script, 0600))
			block := renderPickerBlock(pickerInstalledBlock{Shell: options.Shell, Script: legacyScript, ProfileCreated: true})
			require.NoError(t, os.WriteFile(options.Profile, block, 0600))
			// The legacy hash cannot prove whether this block came from another
			// profile. Keep a second reference to check that cleanup stays safe.
			peer := options
			peer.Profile = filepath.Join(filepath.Dir(options.Profile), "OtherStartup.rc")
			require.NoError(t, os.WriteFile(peer.Profile, block, 0600))
			missing := action == "missing script" || action == "missing directory"
			if action == "missing script" {
				require.NoError(t, os.Remove(legacyScript))
			} else if action == "missing directory" {
				require.NoError(t, os.RemoveAll(options.Directory))
			}
			active, err := IsPickerInstalled(t.Context(), options)
			require.NoError(t, err)
			require.Equal(t, !missing, active)
			if action == "refresh" {
				updated, err := InstallPicker(t.Context(), options)
				require.NoError(t, err)
				require.True(t, updated.Changed)
				require.NotEqual(t, legacyScript, updated.ScriptPath)
				profile, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				_, _, installed, err := parsePickerBlock(profile, options)
				require.NoError(t, err)
				require.False(t, installed.ProfileCreated, "an unmatched legacy hash cannot prove file creation")
			}
			removed, err := RemovePicker(t.Context(), options)
			require.NoError(t, err)
			require.True(t, removed.Changed)
			profile, err := os.ReadFile(options.Profile)
			require.NoError(t, err, "unproven creation metadata must not delete the profile")
			require.Empty(t, profile)
			again, err := RemovePicker(t.Context(), options)
			require.NoError(t, err)
			require.False(t, again.Changed)
			peerProfile, err := os.ReadFile(peer.Profile)
			require.NoError(t, err)
			require.Equal(t, block, peerProfile)
			if !missing {
				retained, err := os.ReadFile(legacyScript)
				require.NoError(t, err, "an unmatched legacy hash cannot prove exclusive script ownership")
				require.Equal(t, script, retained)
				active, err := IsPickerInstalled(t.Context(), peer)
				require.NoError(t, err)
				require.True(t, active, "another profile's reference must remain usable")
			}
		})
	}
}

func TestPickerWindowsProfileIdentityPreservesCaseSensitiveNames(t *testing.T) {
	directory := t.TempDir()
	path, err := windows.UTF16PtrFromString(directory)
	require.NoError(t, err)
	handle, err := windows.CreateFile(path, windows.FILE_WRITE_ATTRIBUTES|windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	require.NoError(t, err)
	defer windows.CloseHandle(handle)
	flags := uint32(1) // FILE_CS_FLAG_CASE_SENSITIVE_DIR
	err = windows.SetFileInformationByHandle(handle, windows.FileCaseSensitiveInfo, (*byte)(unsafe.Pointer(&flags)), uint32(unsafe.Sizeof(flags)))
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Skip("the filesystem or token does not allow case-sensitive directory opt-in")
	}
	require.NoError(t, err)
	root, err := openPickerDirectory(t.Context(), directory, false)
	require.NoError(t, err)
	defer root.Close()
	var identities []string
	for _, name := range []string{"Profile.rc", "profile.rc"} {
		profile := filepath.Join(directory, name)
		require.NoError(t, os.WriteFile(profile, []byte(name), 0600))
		identity, err := pickerProfileIdentity(t.Context(), root, profile)
		require.NoError(t, err)
		identities = append(identities, identity)
	}
	require.NotEqual(t, identities[0], identities[1])
}
