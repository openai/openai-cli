//go:build darwin || linux

package autocomplete

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerInstallPreservesSpecialProfileModes(t *testing.T) {
	for name, special := range map[string]os.FileMode{"setuid": os.ModeSetuid, "setgid": os.ModeSetgid, "sticky": os.ModeSticky} {
		for _, managed := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/unmanaged", true: "/managed"}[managed], func(t *testing.T) {
				options := pickerInstallFixture(t, CompletionStyleBash)
				require.NoError(t, os.WriteFile(options.Profile, []byte("# personal profile\n"), 0600))
				if managed {
					_, err := InstallPicker(t.Context(), options)
					require.NoError(t, err)
				}
				require.NoError(t, os.Chmod(options.Profile, 0600|special))
				before, err := os.Stat(options.Profile)
				require.NoError(t, err)
				require.Equal(t, special, before.Mode()&special)
				data, err := os.ReadFile(options.Profile)
				require.NoError(t, err)
				for _, change := range []func() (PickerInstallResult, error){
					func() (PickerInstallResult, error) { return InstallPicker(t.Context(), options) },
					func() (PickerInstallResult, error) { return RemovePicker(t.Context(), options) },
				} {
					result, err := change()
					require.ErrorContains(t, err, "special permission bits")
					require.False(t, result.Changed)
					after, err := os.Stat(options.Profile)
					require.NoError(t, err)
					require.True(t, os.SameFile(before, after))
					require.Equal(t, before.Mode(), after.Mode())
					current, err := os.ReadFile(options.Profile)
					require.NoError(t, err)
					require.Equal(t, data, current)
				}
			})
		}
	}
}

func TestPickerSpecialProfileModeAddedAfterSnapshot(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	require.NoError(t, os.WriteFile(options.Profile, []byte("# personal profile\n"), 0600))
	root, err := openPickerDirectory(t.Context(), options.Directory, true)
	require.NoError(t, err)
	defer root.Close()
	require.NoError(t, root.WriteFile("profile", []byte("# original\n"), 0600))
	previous, err := readPickerFile(t.Context(), root, "profile")
	require.NoError(t, err)
	require.NoError(t, root.WriteFile("staged", []byte("# replacement\n"), 0600))
	require.NoError(t, os.Chmod(filepath.Join(options.Directory, "profile"), 0600|os.ModeSetuid))
	require.ErrorContains(t, checkPickerSnapshot(t.Context(), root, "profile", previous), "special permission bits")
	require.ErrorContains(t, checkPickerReplacementMetadata(root, "profile", "staged", previous), "special permission bits")
	data, err := root.ReadFile("profile")
	require.NoError(t, err)
	require.Equal(t, "# original\n", string(data))
}
