//go:build darwin || linux

package autocomplete

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerInstallRejectsWritableAncestors(t *testing.T) {
	for _, level := range []string{"config", "higher"} {
		for _, exists := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", level, exists), func(t *testing.T) {
				options := pickerInstallFixture(t, CompletionStyleBash)
				ancestor := filepath.Join(filepath.Dir(options.Profile), "shared")
				config := filepath.Join(ancestor, "config")
				options.Directory = filepath.Join(config, "openai", "shell")
				require.NoError(t, os.MkdirAll(config, 0700))
				if exists {
					require.NoError(t, os.MkdirAll(options.Directory, 0700))
				}
				unsafe := config
				if level == "higher" {
					unsafe = ancestor
				}
				require.NoError(t, os.Chmod(unsafe, 0777))
				_, err := InstallPicker(t.Context(), options)
				require.Error(t, err)
				_, err = os.Lstat(options.Profile)
				require.ErrorIs(t, err, os.ErrNotExist)
				_, err = os.Lstat(filepath.Join(filepath.Dir(options.Profile), ".profile.openai-picker.lock"))
				require.ErrorIs(t, err, os.ErrNotExist, "unsafe script ancestry must fail before profile locks or writes")
				if exists {
					entries, err := os.ReadDir(options.Directory)
					require.NoError(t, err)
					require.Empty(t, entries)
				} else {
					_, err = os.Lstat(filepath.Join(config, "openai"))
					require.ErrorIs(t, err, os.ErrNotExist, "unsafe ancestors must be checked before creating directories")
				}
			})
		}
	}
}

func TestPickerInstallValidatesSymlinkAncestorsAndTargets(t *testing.T) {
	for _, unsafe := range []string{"none", "link parent", "target parent"} {
		t.Run(unsafe, func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleZsh)
			home := filepath.Dir(options.Profile)
			links, target := filepath.Join(home, "aliases"), filepath.Join(home, "target")
			require.NoError(t, os.Mkdir(links, 0700))
			require.NoError(t, os.Mkdir(target, 0700))
			require.NoError(t, os.Symlink(target, filepath.Join(links, "config")))
			options.Directory = filepath.Join(links, "config", "openai", "shell")
			if unsafe == "link parent" {
				require.NoError(t, os.Chmod(links, 0777))
			} else if unsafe == "target parent" {
				require.NoError(t, os.Chmod(target, 0777))
			}
			_, err := InstallPicker(t.Context(), options)
			if unsafe == "none" {
				require.NoError(t, err, "trusted user aliases remain supported")
				_, err = RemovePicker(t.Context(), options)
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				_, err = os.Lstat(filepath.Join(target, "openai"))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestPickerInstallAllowsStickyTemporaryAndUnicodeAncestors(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	ancestor := filepath.Join(filepath.Dir(options.Profile), "sticky-į")
	require.NoError(t, os.Mkdir(ancestor, 0700))
	require.NoError(t, os.Chmod(ancestor, os.ModeSticky|0777))
	options.Directory = filepath.Join(ancestor, "private-į", "openai", "shell")
	_, err := InstallPicker(t.Context(), options)
	require.NoError(t, err)
	_, err = RemovePicker(t.Context(), options)
	require.NoError(t, err)
}
