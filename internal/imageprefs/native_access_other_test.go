//go:build !windows

package imageprefs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func imagePreferenceSymlink(t *testing.T, target, path string) {
	t.Helper()
	require.NoError(t, os.Symlink(target, path))
}
