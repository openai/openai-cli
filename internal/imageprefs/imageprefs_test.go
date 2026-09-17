package imageprefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreferencesDefaultAndAtomicReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openai", "image-preferences.json")
	on, err := Load(path)
	require.NoError(t, err)
	require.True(t, on)
	_, err = os.Stat(filepath.Dir(path))
	require.True(t, os.IsNotExist(err), "loading defaults must not create files")
	for _, enabled := range []bool{false, true, false} {
		require.NoError(t, Save(path, enabled))
		on, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, enabled, on)
		entries, err := os.ReadDir(filepath.Dir(path))
		require.NoError(t, err)
		require.Len(t, entries, 1, "temporary preference files must be cleaned up")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		info, err = os.Stat(filepath.Dir(path))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0700), info.Mode().Perm())
	}
}

func TestPreferencesRejectMalformedAndRecover(t *testing.T) {
	for _, data := range []string{"", "not json", `{}`, `{"version":1}`, `{"version":1,"inline":null}`, `{"version":2,"inline":true}`, `{"version":1,"inline":"off"}`, `{"version":1,"inline":false,"unexpected":true}`, `{"version":1,"inline":false} {}`} {
		t.Run(data, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "prefs.json")
			require.NoError(t, os.WriteFile(path, []byte(data), 0600))
			_, err := Load(path)
			require.ErrorContains(t, err, "openai images inline on")
			require.NoError(t, Save(path, false))
			on, err := Load(path)
			require.NoError(t, err)
			require.False(t, on)
		})
	}
}

func TestPreferencesRejectSpecialPaths(t *testing.T) {
	directory := t.TempDir()
	_, err := Load(directory)
	require.Error(t, err)
	require.Error(t, Save(directory, true))
	if runtime.GOOS == "windows" {
		return // Symlink creation can require an elevated Windows account.
	}
	file := filepath.Join(directory, "target.json")
	require.NoError(t, Save(file, false))
	link := filepath.Join(directory, "link.json")
	require.NoError(t, os.Symlink(file, link))
	_, err = Load(link)
	require.Error(t, err)
	require.Error(t, Save(link, true))
	on, err := Load(file)
	require.NoError(t, err)
	require.False(t, on, "a symlink must not redirect preference writes")
	parentLink := filepath.Join(directory, "parent-link")
	require.NoError(t, os.Symlink(directory, parentLink))
	require.Error(t, Save(filepath.Join(parentLink, "another.json"), true))
}
