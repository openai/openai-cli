package autocomplete

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerProfileRecoveryUsesRecordedScriptRoot(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		for _, action := range []string{"refresh", "remove"} {
			t.Run(string(shell)+"/"+action, func(t *testing.T) {
				options := pickerInstallFixture(t, shell)
				personal := []byte("# personal startup\n")
				require.NoError(t, os.WriteFile(options.Profile, personal, 0600))
				installed, err := InstallPicker(t.Context(), options)
				require.NoError(t, err)
				before, err := os.ReadFile(options.Profile)
				require.NoError(t, err)

				// Simulate a process exiting after capturing its live profile.
				// Its managed block still identifies the original script root.
				root, err := openPickerProfileDirectory(t.Context(), filepath.Dir(options.Profile), false)
				require.NoError(t, err)
				defer root.Close()
				name := filepath.Base(options.Profile)
				directory := pickerRecoveryPrefix(name) + "interrupted-config-change"
				require.NoError(t, createPickerRecoveryDirectory(root, directory))
				recovery, err := openPickerRecoveryDirectory(root, directory)
				require.NoError(t, err)
				manifest, err := json.Marshal(pickerRecoveryManifest{Version: 1, Profile: name})
				require.NoError(t, err)
				require.NoError(t, writePickerRecoveryFile(recovery, "manifest.json", manifest))
				require.NoError(t, recovery.Close())
				require.NoError(t, renamePickerFileNoReplace(root, name, filepath.Join(directory, "original")))

				// A different, unusable current root must not prevent recovery
				// of the intact profile and its safe recorded script location.
				obstacle := filepath.Join(filepath.Dir(options.Profile), "new-config")
				require.NoError(t, os.WriteFile(obstacle, []byte("personal file\n"), 0600))
				changed := options
				changed.Directory = filepath.Join(obstacle, "openai", "shell")
				active, err := IsPickerInstalled(t.Context(), changed)
				require.NoError(t, err)
				require.False(t, active)
				if action == "refresh" {
					result, err := InstallPicker(t.Context(), changed)
					require.NoError(t, err)
					require.Equal(t, installed.ScriptPath, result.ScriptPath)
					after, err := os.ReadFile(options.Profile)
					require.NoError(t, err)
					require.Equal(t, before, after)
					active, err = IsPickerInstalled(t.Context(), changed)
					require.NoError(t, err)
					require.True(t, active)
				} else {
					_, err := RemovePicker(t.Context(), changed)
					require.NoError(t, err)
					after, err := os.ReadFile(options.Profile)
					require.NoError(t, err)
					require.Equal(t, personal, after)
					_, err = os.Stat(installed.ScriptPath)
					require.ErrorIs(t, err, os.ErrNotExist)
				}
				untouched, err := os.ReadFile(obstacle)
				require.NoError(t, err)
				require.Equal(t, "personal file\n", string(untouched))
			})
		}
	}
}
