//go:build darwin

package custom

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/stretchr/testify/require"
)

func TestImagePickerMacOSShellConfigChangesShareDecisionLock(t *testing.T) {
	for _, shell := range []string{"zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			home := pickerShellSetupHome(t)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config-a"))
			first, err := imagePickerShellTarget(t.Context(), shell, false, "")
			require.NoError(t, err)
			acquired, release := make(chan struct{}), make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				finished <- autocomplete.WithPickerSetupLock(t.Context(), first[0].Directory, func() error {
					close(acquired)
					<-release
					return nil
				})
			}()
			select {
			case <-acquired:
			case err := <-finished:
				require.NoError(t, err)
				t.Fatal("setup lock callback did not run")
			}
			defer func() {
				close(release)
				require.NoError(t, <-finished)
			}()
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config-b"))
			second, err := imagePickerShellTarget(t.Context(), shell, false, "")
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			output, err := runPickerShellSetup(t, ctx, shell, "--uninstall-picker")
			require.ErrorIs(t, err, context.DeadlineExceeded, "shared preferences require the same decision lock across XDG roots")
			require.Empty(t, output)
			declined, err := imagePickerTabDeclined(shell)
			require.NoError(t, err)
			require.False(t, declined, "a blocked uninstall must not change the shared preference")
			state, err := imagePickerStatePath()
			require.NoError(t, err)
			require.Equal(t, filepath.Join(filepath.Dir(state), "shell"), first[0].Directory)
			require.Equal(t, first[0].Directory, second[0].Directory)
			if shell == "fish" {
				require.NotEqual(t, first[0].Profile, second[0].Profile, "fish startup selection must still follow XDG")
			}
			for _, target := range append(first, second...) {
				_, err := os.Stat(target.Profile)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestImagePickerMacOSShellSetupRetainsLegacyScriptRoot(t *testing.T) {
	home := pickerShellSetupHome(t)
	legacy, err := imagePickerSelectShellTargets("zsh", "", imagePickerShellPaths{
		home: home, config: filepath.Join(home, "old-xdg"), zdotdir: home,
	})
	require.NoError(t, err)
	personal := []byte("# personal startup\n")
	require.NoError(t, os.WriteFile(legacy[0].Profile, personal, 0600))
	installed, err := autocomplete.InstallPicker(t.Context(), legacy[0])
	require.NoError(t, err)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "new-xdg"))
	current, err := imagePickerShellTarget(t.Context(), "zsh", false, "")
	require.NoError(t, err)
	require.NotEqual(t, legacy[0].Directory, current[0].Directory)
	_, err = runPickerShellSetup(t, t.Context(), "zsh", "--install-picker")
	require.NoError(t, err)
	profile, err := os.ReadFile(legacy[0].Profile)
	require.NoError(t, err)
	require.Contains(t, string(profile), installed.ScriptPath)
	_, err = runPickerShellSetup(t, t.Context(), "zsh", "--uninstall-picker")
	require.NoError(t, err)
	profile, err = os.ReadFile(legacy[0].Profile)
	require.NoError(t, err)
	require.Equal(t, personal, profile)
	_, err = os.Stat(installed.ScriptPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}
