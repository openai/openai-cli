package audiohistory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoplayPreferenceSurvivesClear(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "audio")}
	check := func(want bool) {
		t.Helper()
		other := Store{Directory: store.Directory}
		got, err := other.Autoplay()
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	check(false)
	require.NoError(t, store.SetAutoplay(false))
	_, err := os.Stat(store.Directory)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, store.SetAutoplay(true))
	require.NoError(t, store.SetAutoplay(true))
	require.NoError(t, store.Clear(t.Context()))
	check(true)
	require.NoError(t, store.SetAutoplay(false))
	check(false)
	path := filepath.Join(store.Directory, autoplayMarker)
	require.NoError(t, os.WriteFile(path, []byte("unrelated content"), 0600))
	require.ErrorIs(t, store.SetAutoplay(false), ErrInvalid)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "unrelated content", string(data))
}
