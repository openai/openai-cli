package images

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func pickerShellSetupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for name, value := range map[string]string{
		"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": filepath.Join(home, "config"), "APPDATA": filepath.Join(home, "config"), "ZDOTDIR": home,
	} {
		t.Setenv(name, value)
	}
	return home
}

func TestImagePickerTabChoicePrivateIdempotentAndCanceled(t *testing.T) {
	pickerShellSetupHome(t)
	path, err := imagePickerTabChoicePath("zsh")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, declineImagePickerTab(ctx, "zsh"), context.Canceled)
	_, err = os.Lstat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist)
	for range 2 {
		require.NoError(t, declineImagePickerTab(t.Context(), "zsh"))
		declined, err := imagePickerTabDeclined("zsh")
		require.NoError(t, err)
		require.True(t, declined)
	}
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	require.Zero(t, info.Size())
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	for range 2 {
		require.NoError(t, clearImagePickerTabDecline("zsh"))
	}
	declined, err := imagePickerTabDeclined("zsh")
	require.NoError(t, err)
	require.False(t, declined)
}

func TestImagePickerTabChoiceRejectsModifiedOrSymlinkMarker(t *testing.T) {
	for _, kind := range []string{"content", "directory", "public", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			if runtime.GOOS == "windows" && (kind == "public" || kind == "symlink") {
				t.Skip("Unix mode or symlink semantics")
			}
			home := pickerShellSetupHome(t)
			path, err := imagePickerTabChoicePath("zsh")
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			target := filepath.Join(home, "preserved-file")
			require.NoError(t, os.WriteFile(target, []byte("preserve these bytes"), 0600))
			switch kind {
			case "content":
				require.NoError(t, os.WriteFile(path, []byte("external content"), 0600))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0700))
			case "public":
				require.NoError(t, os.WriteFile(path, nil, 0600))
				require.NoError(t, os.Chmod(path, 0644))
			case "symlink":
				require.NoError(t, os.Symlink(target, path))
			}
			before, err := os.Lstat(path)
			require.NoError(t, err)
			_, err = imagePickerTabDeclined("zsh")
			require.Error(t, err)
			require.Error(t, declineImagePickerTab(t.Context(), "zsh"))
			require.Error(t, clearImagePickerTabDecline("zsh"))
			after, err := os.Lstat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "preserve these bytes", string(data))
		})
	}
}
