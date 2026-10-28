package autocomplete

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerInstallPreservesHardLinkedProfiles(t *testing.T) {
	for _, scenario := range []string{"unmanaged", "managed existing", "managed created", "missing scripts"} {
		t.Run(scenario, func(t *testing.T) {
			options := pickerInstallFixture(t, CompletionStyleBash)
			if scenario != "managed created" {
				require.NoError(t, os.WriteFile(options.Profile, []byte("# shared personal settings\n"), 0600))
			}
			var installed PickerInstallResult
			if scenario != "unmanaged" {
				var err error
				installed, err = InstallPicker(t.Context(), options)
				require.NoError(t, err)
				if scenario == "missing scripts" {
					require.NoError(t, os.RemoveAll(options.Directory))
				}
			}
			original, err := os.ReadFile(options.Profile)
			require.NoError(t, err)
			identity, err := os.Stat(options.Profile)
			require.NoError(t, err)
			peer := filepath.Join(filepath.Dir(options.Profile), "linked-profile")
			require.NoError(t, os.Link(options.Profile, peer))
			for range 2 {
				for _, change := range []func() (PickerInstallResult, error){
					func() (PickerInstallResult, error) { return InstallPicker(t.Context(), options) },
					func() (PickerInstallResult, error) { return RemovePicker(t.Context(), options) },
				} {
					result, err := change()
					require.ErrorContains(t, err, "hard links")
					require.False(t, result.Changed)
					for _, path := range []string{options.Profile, peer} {
						data, err := os.ReadFile(path)
						require.NoError(t, err)
						require.Equal(t, original, data)
						current, err := os.Stat(path)
						require.NoError(t, err)
						require.True(t, os.SameFile(identity, current), "refusal must preserve both links")
					}
					if scenario == "unmanaged" || scenario == "missing scripts" {
						_, err := os.Stat(options.Directory)
						require.ErrorIs(t, err, os.ErrNotExist, "refusal must not stage a script")
					} else {
						_, err := os.Stat(installed.ScriptPath)
						require.NoError(t, err, "a rejected removal must keep its referenced script")
					}
				}
			}
			// Removing the external link restores the ordinary setup lifecycle.
			require.NoError(t, os.Remove(peer))
			_, err = InstallPicker(t.Context(), options)
			require.NoError(t, err)
			_, err = RemovePicker(t.Context(), options)
			require.NoError(t, err)
		})
	}
}

func TestPickerProfileHardLinkAddedAfterSnapshot(t *testing.T) {
	directory := t.TempDir()
	profile := filepath.Join(directory, "profile")
	const original = "# personal profile\n"
	require.NoError(t, os.WriteFile(profile, []byte(original), 0600))
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	snapshot, err := readPickerFile(t.Context(), root, "profile")
	require.NoError(t, err)
	peer := filepath.Join(directory, "linked-profile")
	require.NoError(t, os.Link(profile, peer))
	require.ErrorContains(t, checkPickerSnapshot(t.Context(), root, "profile", snapshot), "hard links")
	require.ErrorContains(t, replacePickerProfile(t.Context(), root, "profile", snapshot, []byte("replacement\n")), "hard links")
	require.ErrorContains(t, removePickerSnapshot(t.Context(), root, "profile", snapshot), "hard links")
	for _, path := range []string{profile, peer} {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, original, string(data))
		current, err := os.Stat(path)
		require.NoError(t, err)
		require.True(t, os.SameFile(snapshot.info, current))
	}
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 2, "failed replacement must clean up its temporary file")
}
