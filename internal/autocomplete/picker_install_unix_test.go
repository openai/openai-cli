//go:build darwin || linux

package autocomplete

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestPickerInstallKeepsFIFOReplacements(t *testing.T) {
	options := pickerInstallFixture(t, CompletionStyleBash)
	require.NoError(t, os.WriteFile(options.Profile, []byte("# original profile\n"), 0600))
	root, err := openPickerDirectory(t.Context(), filepath.Dir(options.Profile), false)
	require.NoError(t, err)
	defer root.Close()
	before, err := readPickerFile(t.Context(), root, filepath.Base(options.Profile))
	require.NoError(t, err)
	require.NoError(t, os.Remove(options.Profile))
	require.NoError(t, unix.Mkfifo(options.Profile, 0600))
	// A profile replaced after inspection must not block on a FIFO or replace
	// it with either the prepared install or removal content.
	require.Error(t, replacePickerProfile(t.Context(), root, filepath.Base(options.Profile), before, []byte("replacement\n")))
	_, err = InstallPicker(t.Context(), options)
	require.Error(t, err)
	_, err = RemovePicker(t.Context(), options)
	require.Error(t, err)
	info, err := os.Lstat(options.Profile)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeNamedPipe)
	_, err = os.Stat(options.Directory)
	require.ErrorIs(t, err, os.ErrNotExist)
}
