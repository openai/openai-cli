//go:build !windows

package output

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestManagedDownloadSpecialDestinationSwapBeforeOpen(t *testing.T) {
	for _, replacement := range []string{"symlink", "regular file"} {
		t.Run(replacement, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "output.fifo")
			target := filepath.Join(directory, "target.bin")
			require.NoError(t, os.WriteFile(target, []byte("keep"), 0600))
			require.NoError(t, unix.Mkfifo(path, 0600))
			destination, err := prepareDownloadDestination(path)
			require.NoError(t, err)
			require.NoError(t, os.Remove(path))
			if replacement == "symlink" {
				require.NoError(t, os.Symlink(target, path))
			} else {
				require.NoError(t, os.WriteFile(path, []byte("replacement"), 0600))
			}
			file, err := destination.open(false)
			if file != nil {
				_ = file.Close()
			}
			require.Error(t, err)
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "keep", string(data))
			if replacement == "regular file" {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, "replacement", string(data))
			}
		})
	}
}
