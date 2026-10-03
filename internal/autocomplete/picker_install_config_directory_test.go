package autocomplete

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerInstallConfigDirectoryChanges(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, action := range []string{"remove", "refresh", "modified script"} {
			t.Run(string(shell)+"/"+action, func(t *testing.T) {
				original := pickerInstallFixture(t, shell)
				personal := []byte("# personal startup\n")
				require.NoError(t, os.WriteFile(original.Profile, personal, 0600))
				installed, err := InstallPicker(t.Context(), original)
				require.NoError(t, err)
				if action == "refresh" {
					data, err := os.ReadFile(installed.ScriptPath)
					require.NoError(t, err)
					older := append(data, []byte("\n# earlier CLI script\n")...)
					oldPath := filepath.Join(original.Directory, pickerScriptName(original, older))
					require.NoError(t, os.WriteFile(oldPath, older, 0600))
					require.NoError(t, os.WriteFile(original.Profile, append(bytes.Clone(personal), renderPickerBlock(pickerInstalledBlock{Shell: shell, Script: oldPath})...), 0600))
					require.NoError(t, os.Remove(installed.ScriptPath))
					installed.ScriptPath = oldPath
				} else if action == "modified script" {
					require.NoError(t, os.WriteFile(installed.ScriptPath, []byte("# user modification\n"), 0600))
				}
				changed := original
				changed.Directory = filepath.Join(filepath.Dir(original.Profile), "new-config", "openai", "shell")
				profileBefore, err := os.ReadFile(original.Profile)
				require.NoError(t, err)
				active, err := IsPickerInstalled(t.Context(), changed)
				if action == "modified script" {
					require.Error(t, err)
					_, err = InstallPicker(t.Context(), changed)
					require.Error(t, err)
					_, err = RemovePicker(t.Context(), changed)
					require.Error(t, err)
					after, err := os.ReadFile(original.Profile)
					require.NoError(t, err)
					require.Equal(t, profileBefore, after)
					data, err := os.ReadFile(installed.ScriptPath)
					require.NoError(t, err)
					require.Equal(t, "# user modification\n", string(data))
					return
				}
				require.NoError(t, err)
				require.Equal(t, action != "refresh", active)
				if action == "refresh" {
					updated, err := InstallPicker(t.Context(), changed)
					require.NoError(t, err)
					require.True(t, updated.Changed)
					require.Equal(t, original.Directory, filepath.Dir(updated.ScriptPath))
					_, err = os.Stat(installed.ScriptPath)
					require.ErrorIs(t, err, os.ErrNotExist)
					installed = updated
					active, err = IsPickerInstalled(t.Context(), changed)
					require.NoError(t, err)
					require.True(t, active)
					identity, err := os.Stat(original.Profile)
					require.NoError(t, err)
					again, err := InstallPicker(t.Context(), changed)
					require.NoError(t, err)
					require.False(t, again.Changed)
					after, err := os.Stat(original.Profile)
					require.NoError(t, err)
					require.True(t, os.SameFile(identity, after))
				}
				removed, err := RemovePicker(t.Context(), changed)
				require.NoError(t, err)
				require.True(t, removed.Changed)
				restored, err := os.ReadFile(original.Profile)
				require.NoError(t, err)
				require.Equal(t, personal, restored)
				_, err = os.Stat(installed.ScriptPath)
				require.ErrorIs(t, err, os.ErrNotExist)
				_, err = os.Stat(changed.Directory)
				require.ErrorIs(t, err, os.ErrNotExist)
			})
		}
	}
}
