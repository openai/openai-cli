//go:build darwin || linux

package autocomplete

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerRecoveryDirectoryRejectsReadableMode(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "readable")
	require.NoError(t, os.Mkdir(directory, 0700))
	file, err := os.Open(directory)
	require.NoError(t, err)
	defer file.Close()
	for _, mode := range []os.FileMode{0755, 0710, 0704} {
		require.NoError(t, os.Chmod(directory, mode))
		require.ErrorContains(t, checkPickerRecoveryDirectory(file), "private")
	}
}
