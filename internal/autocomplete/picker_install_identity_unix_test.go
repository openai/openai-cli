//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package autocomplete

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func pickerUnixAliasFixture(t *testing.T) (PickerInstallation, PickerInstallation) {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	realHome := filepath.Join(directory, "real", "home")
	require.NoError(t, os.MkdirAll(realHome, 0700))
	require.NoError(t, os.Symlink(filepath.Join(directory, "real"), filepath.Join(directory, "alias")))
	options := PickerInstallation{Shell: CompletionStyleBash, Directory: filepath.Join(directory, "config", "openai", "shell"), Profile: filepath.Join(realHome, "profile")}
	alias := options
	alias.Profile = filepath.Join(directory, "alias", "home", "profile")
	return options, alias
}

func TestPickerUnixProfileAliasLifecycle(t *testing.T) {
	for _, direction := range []string{"alias first", "real first"} {
		for _, created := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/created=%t", direction, created), func(t *testing.T) {
				real, alias := pickerUnixAliasFixture(t)
				original := []byte("# personal startup\n")
				if !created {
					require.NoError(t, os.WriteFile(real.Profile, original, 0600))
				}
				first, second := alias, real
				if direction == "real first" {
					first, second = real, alias
				}
				installed, err := InstallPicker(t.Context(), first)
				require.NoError(t, err)
				before, err := os.Stat(real.Profile)
				require.NoError(t, err)
				active, err := IsPickerInstalled(t.Context(), second)
				require.NoError(t, err)
				require.True(t, active)
				repeated, err := InstallPicker(t.Context(), second)
				require.NoError(t, err)
				require.False(t, repeated.Changed)
				require.Equal(t, installed.ScriptPath, repeated.ScriptPath)
				after, err := os.Stat(real.Profile)
				require.NoError(t, err)
				require.True(t, os.SameFile(before, after), "equivalent names must not rewrite an intact profile")
				_, err = RemovePicker(t.Context(), second)
				require.NoError(t, err)
				restored, err := os.ReadFile(real.Profile)
				if created {
					require.ErrorIs(t, err, os.ErrNotExist)
				} else {
					require.NoError(t, err)
					require.Equal(t, original, restored)
				}
				_, err = os.Stat(installed.ScriptPath)
				require.ErrorIs(t, err, os.ErrNotExist)
			})
		}
	}
}

func TestPickerUnixProfileDirectParentAlias(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, aliasFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/aliasFirst=%t", shell, aliasFirst), func(t *testing.T) {
				real, alias := pickerUnixAliasFixture(t)
				real.Shell, alias.Shell = shell, shell
				alias.Profile = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(real.Profile))), "direct-home", "profile")
				require.NoError(t, os.Symlink(filepath.Dir(real.Profile), filepath.Dir(alias.Profile)))
				personal := []byte("# personal startup\n")
				require.NoError(t, os.WriteFile(real.Profile, personal, 0600))
				first, second := real, alias
				if aliasFirst {
					first, second = alias, real
				}
				installed, err := InstallPicker(t.Context(), first)
				require.NoError(t, err)
				before, err := os.Stat(real.Profile)
				require.NoError(t, err)
				active, err := IsPickerInstalled(t.Context(), second)
				require.NoError(t, err)
				require.True(t, active)
				again, err := InstallPicker(t.Context(), second)
				require.NoError(t, err)
				require.False(t, again.Changed)
				after, err := os.Stat(real.Profile)
				require.NoError(t, err)
				require.True(t, os.SameFile(before, after))
				_, err = RemovePicker(t.Context(), second)
				require.NoError(t, err)
				restored, err := os.ReadFile(real.Profile)
				require.NoError(t, err)
				require.Equal(t, personal, restored)
				_, err = os.Stat(installed.ScriptPath)
				require.ErrorIs(t, err, os.ErrNotExist)
			})
		}
	}
}

func TestPickerUnixProfileParentAliasKeepsTrustBoundaries(t *testing.T) {
	for _, unsafe := range []string{"link parent", "target", "profile link", "script directory link"} {
		t.Run(unsafe, func(t *testing.T) {
			real, alias := pickerUnixAliasFixture(t)
			links := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(real.Profile))), "links")
			require.NoError(t, os.Mkdir(links, 0700))
			alias.Profile = filepath.Join(links, "direct-home", "profile")
			require.NoError(t, os.Symlink(filepath.Dir(real.Profile), filepath.Dir(alias.Profile)))
			personal := []byte("# personal startup\n")
			require.NoError(t, os.WriteFile(real.Profile, personal, 0600))
			switch unsafe {
			case "link parent":
				require.NoError(t, os.Chmod(links, 0777))
			case "target":
				require.NoError(t, os.Chmod(filepath.Dir(real.Profile), 0777))
			case "profile link":
				alias.Profile = filepath.Join(filepath.Dir(alias.Profile), "linked-profile")
				require.NoError(t, os.Symlink(real.Profile, alias.Profile))
			case "script directory link":
				require.NoError(t, os.MkdirAll(real.Directory, 0700))
				alias.Directory = filepath.Join(filepath.Dir(real.Directory), "linked-shell")
				require.NoError(t, os.Symlink(real.Directory, alias.Directory))
			}
			_, err := InstallPicker(t.Context(), alias)
			require.Error(t, err)
			after, err := os.ReadFile(real.Profile)
			require.NoError(t, err)
			require.Equal(t, personal, after)
		})
	}
}

func TestPickerUnixProfileIdentityRejectsChangedParentAndFinalSymlink(t *testing.T) {
	real, alias := pickerUnixAliasFixture(t)
	root, err := openPickerDirectory(t.Context(), filepath.Dir(alias.Profile), false)
	require.NoError(t, err)
	defer root.Close()
	identity, err := pickerProfileIdentity(t.Context(), root, alias.Profile)
	require.NoError(t, err)
	require.Equal(t, real.Profile, identity)
	_, err = os.Stat(real.Profile)
	require.ErrorIs(t, err, os.ErrNotExist, "identity lookup must not create missing profiles")
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(real.Profile), "target"), []byte("# target\n"), 0600))
	require.NoError(t, os.Symlink("target", real.Profile))
	_, err = pickerProfileIdentity(t.Context(), root, alias.Profile)
	require.Error(t, err, "the startup file itself must not be a symbolic link")
	require.NoError(t, os.Remove(real.Profile))
	require.NoError(t, os.Rename(filepath.Dir(real.Profile), filepath.Dir(real.Profile)+"-moved"))
	require.NoError(t, os.Mkdir(filepath.Dir(real.Profile), 0700))
	_, err = pickerProfileIdentity(t.Context(), root, alias.Profile)
	require.ErrorIs(t, err, errPickerInstallChanged)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = pickerProfileIdentity(ctx, root, alias.Profile)
	require.ErrorIs(t, err, context.Canceled)
}

func TestPickerUnixProfileLegacyAliasPreservesUnprovenOwnership(t *testing.T) {
	for _, action := range []string{"refresh", "remove", "missing script", "missing directory"} {
		t.Run(action, func(t *testing.T) {
			real, alias := pickerUnixAliasFixture(t)
			script, err := renderInstalledPicker(real)
			require.NoError(t, err)
			legacyScript := filepath.Join(real.Directory, pickerScriptName(alias, script))
			require.NoError(t, os.MkdirAll(real.Directory, 0700))
			require.NoError(t, os.WriteFile(legacyScript, script, 0600))
			block := renderPickerBlock(pickerInstalledBlock{Shell: real.Shell, Script: legacyScript, ProfileCreated: true})
			require.NoError(t, os.WriteFile(real.Profile, block, 0600))
			peer := real
			peer.Profile = filepath.Join(filepath.Dir(real.Profile), "peer-profile")
			require.NoError(t, os.WriteFile(peer.Profile, block, 0600))
			missing := action == "missing script" || action == "missing directory"
			if action == "missing script" {
				require.NoError(t, os.Remove(legacyScript))
			} else if action == "missing directory" {
				require.NoError(t, os.RemoveAll(real.Directory))
			}
			active, err := IsPickerInstalled(t.Context(), real)
			require.NoError(t, err)
			require.Equal(t, !missing, active)
			if action == "refresh" {
				updated, err := InstallPicker(t.Context(), real)
				require.NoError(t, err)
				require.NotEqual(t, legacyScript, updated.ScriptPath)
				profile, err := os.ReadFile(real.Profile)
				require.NoError(t, err)
				_, _, installed, err := parsePickerBlock(profile, real)
				require.NoError(t, err)
				require.False(t, installed.ProfileCreated)
			}
			_, err = RemovePicker(t.Context(), real)
			require.NoError(t, err)
			profile, err := os.ReadFile(real.Profile)
			require.NoError(t, err, "an unproven creation flag cannot justify deleting the file")
			require.Empty(t, profile)
			peerData, err := os.ReadFile(peer.Profile)
			require.NoError(t, err)
			require.Equal(t, block, peerData)
			if !missing {
				retained, err := os.ReadFile(legacyScript)
				require.NoError(t, err)
				require.Equal(t, script, retained)
				active, err := IsPickerInstalled(t.Context(), peer)
				require.NoError(t, err)
				require.True(t, active)
			}
		})
	}
}

func TestPickerDarwinProfileVarAlias(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin /var alias only")
	}
	real, _ := pickerUnixAliasFixture(t)
	if !strings.HasPrefix(real.Profile, "/private/var/") {
		t.Skip("temporary directory is outside /private/var")
	}
	alias := real
	alias.Profile = strings.TrimPrefix(real.Profile, "/private")
	installed, err := InstallPicker(t.Context(), alias)
	require.NoError(t, err)
	active, err := IsPickerInstalled(t.Context(), real)
	require.NoError(t, err)
	require.True(t, active)
	_, err = RemovePicker(t.Context(), real)
	require.NoError(t, err)
	_, err = os.Stat(installed.ScriptPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPickerDarwinProfileCaseAlias(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin filesystem casing only")
	}
	real, _ := pickerUnixAliasFixture(t)
	real.Profile = filepath.Join(filepath.Dir(real.Profile), "MiXeDProfile.rc")
	require.NoError(t, os.WriteFile(real.Profile, []byte("# personal\n"), 0600))
	alias := real
	alias.Profile = filepath.Join(filepath.Dir(filepath.Dir(real.Profile)), "HOME", "mixedprofile.rc")
	before, err := os.Stat(real.Profile)
	require.NoError(t, err)
	other, err := os.Stat(alias.Profile)
	if os.IsNotExist(err) {
		t.Skip("temporary filesystem is case sensitive")
	}
	require.NoError(t, err)
	require.True(t, os.SameFile(before, other))
	installed, err := InstallPicker(t.Context(), real)
	require.NoError(t, err)
	identity, err := os.Stat(real.Profile)
	require.NoError(t, err)
	active, err := IsPickerInstalled(t.Context(), alias)
	require.NoError(t, err)
	require.True(t, active)
	for range 3 {
		repeated, err := InstallPicker(t.Context(), alias)
		require.NoError(t, err)
		require.False(t, repeated.Changed)
		require.Equal(t, installed.ScriptPath, repeated.ScriptPath)
	}
	after, err := os.Stat(real.Profile)
	require.NoError(t, err)
	require.True(t, os.SameFile(identity, after))
	_, err = RemovePicker(t.Context(), alias)
	require.NoError(t, err)
	_, err = os.Stat(installed.ScriptPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestPickerUnixProfileIdentityKeepsDistinctCaseNames(t *testing.T) {
	real, _ := pickerUnixAliasFixture(t)
	directory := filepath.Dir(real.Profile)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "Profile"), []byte("# first\n"), 0600))
	second, err := os.OpenFile(filepath.Join(directory, "profile"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		t.Skip("temporary filesystem is case insensitive")
	}
	require.NoError(t, err)
	require.NoError(t, second.Close())
	root, err := openPickerDirectory(t.Context(), directory, false)
	require.NoError(t, err)
	defer root.Close()
	upper, err := pickerProfileIdentity(t.Context(), root, filepath.Join(directory, "Profile"))
	require.NoError(t, err)
	lower, err := pickerProfileIdentity(t.Context(), root, filepath.Join(directory, "profile"))
	require.NoError(t, err)
	require.NotEqual(t, upper, lower)
}
