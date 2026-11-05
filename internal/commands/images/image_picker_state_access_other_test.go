//go:build !windows

package images

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func imagePickerStateSymlink(t *testing.T, target, path string) {
	t.Helper()
	require.NoError(t, os.Symlink(target, path))
}
