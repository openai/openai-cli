package images_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/stretchr/testify/require"
)

func TestImagePickerZshStartupChild(t *testing.T) {
	if os.Getenv("OPENAI_TEST_ZSH_CHILD") != "1" {
		t.Skip("native zsh subprocess helper")
	}
	mode := os.Getenv("OPENAI_TEST_ZSH_MODE")
	if mode == "automatic" || mode == "default" && os.Getenv("OPENAI_TEST_ZSH_DEFAULT") != "allowed" {
		t.Cleanup(func() {
			state, err := images.TestImagePickerStatePath()
			require.NoError(t, err)
			choice, err := images.TestImagePickerTabChoicePath("zsh")
			require.NoError(t, err)
			for _, path := range []string{state, choice} {
				_, err = os.Stat(filepath.Dir(path))
				require.ErrorIs(t, err, os.ErrNotExist, "ambiguous setup must not create platform state, scripts, or locks")
			}
		})
	}
	if mode == "automatic" {
		targets, err := images.TestImagePickerShellTarget(t.Context(), "zsh", true, "")
		require.NoError(t, err)
		require.Empty(t, targets, "ambiguous zsh state must skip before automatic setup")
		require.NoError(t, images.TestSetupImagePickerFirstRun(t.Context(), targets))
		output, err := runPickerShellSetup(t, t.Context(), "--install-picker", "--automatic")
		require.NoError(t, err)
		require.Empty(t, output, "skipped automatic setup must not claim saved changes")
		return
	}
	args := []string{"zsh", "--install-picker"}
	if mode == "override" {
		args = append(args, "--profile", os.Getenv("OPENAI_TEST_ZSH_PROFILE"))
	}
	output, err := runPickerShellSetup(t, t.Context(), args...)
	if mode == "default" && os.Getenv("OPENAI_TEST_ZSH_DEFAULT") != "allowed" {
		require.Error(t, err)
		require.Contains(t, err.Error(), "--profile")
		require.Empty(t, output)
		return
	}
	require.NoError(t, err)
	profile, err := os.ReadFile(os.Getenv("OPENAI_TEST_ZSH_PROFILE"))
	require.NoError(t, err)
	require.Contains(t, string(profile), "# >>> openai image picker")
	args[1] = "--uninstall-picker"
	_, err = runPickerShellSetup(t, t.Context(), args...)
	require.NoError(t, err)
}
