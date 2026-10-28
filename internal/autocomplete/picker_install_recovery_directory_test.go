//go:build darwin || linux || windows

package autocomplete

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerRecoveryDirectory(t *testing.T) {
	t.Run("create private directory and preserve collision", func(t *testing.T) {
		root, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		defer root.Close()
		require.NoError(t, createPickerRecoveryDirectory(root, "recovery"))
		directory, err := root.Open("recovery")
		require.NoError(t, err)
		defer directory.Close()
		require.NoError(t, checkPickerRecoveryDirectory(directory))
		before, err := directory.Stat()
		require.NoError(t, err)
		require.NoError(t, root.WriteFile(filepath.Join("recovery", "sentinel"), []byte("keep\n"), 0600))
		require.ErrorIs(t, createPickerRecoveryDirectory(root, "recovery"), os.ErrExist)
		after, err := root.Stat("recovery")
		require.NoError(t, err)
		require.True(t, os.SameFile(before, after))
		data, err := root.ReadFile(filepath.Join("recovery", "sentinel"))
		require.NoError(t, err)
		require.Equal(t, "keep\n", string(data))
	})
	t.Run("reject unsafe directory names", func(t *testing.T) {
		root, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		defer root.Close()
		for _, name := range []string{"", ".", "..", filepath.Join("..", "outside"), filepath.Join("nested", "recovery")} {
			require.ErrorIs(t, createPickerRecoveryDirectory(root, name), os.ErrInvalid)
		}
	})
	t.Run("reject a regular file", func(t *testing.T) {
		file, err := os.CreateTemp(t.TempDir(), "regular")
		require.NoError(t, err)
		defer file.Close()
		require.Error(t, checkPickerRecoveryDirectory(file))
	})
}
