package imageprefs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImagePreferenceDefaultAndReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openai", "image-preferences.json")
	mode, err := Load(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, "auto", mode)
	_, err = os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist)
	for _, enabled := range []bool{false, true, false} {
		require.NoError(t, Save(t.Context(), path, enabled))
		mode, err = Load(t.Context(), path)
		require.NoError(t, err)
		want := "off"
		if enabled {
			want = "on"
		}
		require.Equal(t, want, mode)
		entries, err := os.ReadDir(filepath.Dir(path))
		require.NoError(t, err)
		require.Len(t, entries, 2, "only preferences and the persistent writer lock should remain")
	}
	if runtime.GOOS != "windows" {
		file, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), file.Mode().Perm())
		dir, err := os.Stat(filepath.Dir(path))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0700), dir.Mode().Perm())
	}
}

func TestImagePreferenceInvalidSettingsRemainUntouched(t *testing.T) {
	for _, data := range []string{"", "private-not-json", `{}`, `{"version":1}`, `{"version":1,"inline":null}`, `{"version":2,"inline":false}`, `{"version":1,"inline":"off"}`, `{"version":1,"inline":false,"future":true}`, `{"version":1,"inline":false} {}`, `{"version":1,"inline":false,"inline":true}`, strings.Repeat(" ", 4097)} {
		t.Run(data[:min(len(data), 60)], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "image-preferences.json")
			require.NoError(t, os.WriteFile(path, []byte(data), 0600))
			_, err := Load(t.Context(), path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-not-json")
			require.Error(t, Save(t.Context(), path, true))
			unchanged, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, data, string(unchanged))
		})
	}
}

func TestImagePreferenceConcurrentWritesAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openai", "image-preferences.json")
	require.NoError(t, Save(t.Context(), path, false))
	var writers sync.WaitGroup
	errors := make(chan error, 32)
	for index := range 32 {
		writers.Add(1)
		go func() { defer writers.Done(); errors <- Save(t.Context(), path, index%2 == 0) }()
	}
	writers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	mode, err := Load(t.Context(), path)
	require.NoError(t, err)
	require.Contains(t, []string{"on", "off"}, mode)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 2, "only preferences and the persistent writer lock should remain")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, Save(ctx, path, true), context.Canceled)
	_, err = Load(ctx, path)
	require.ErrorIs(t, err, context.Canceled)
	after, err := Load(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, mode, after)
}

func TestImagePreferenceRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	require.Error(t, Save(t.Context(), dir, true))
	if runtime.GOOS == "windows" {
		t.Skip("native Windows symlinks require additional privileges")
	}
	path := filepath.Join(dir, "image-preferences.json")
	target := filepath.Join(t.TempDir(), "target.json")
	require.NoError(t, Save(t.Context(), target, false))
	require.NoError(t, os.Symlink(target, path))
	_, err := Load(t.Context(), path)
	require.Error(t, err)
	require.Error(t, Save(t.Context(), path, true))
	mode, err := Load(t.Context(), target)
	require.NoError(t, err)
	require.Equal(t, "off", mode)
	parent := filepath.Join(t.TempDir(), "linked-config")
	require.NoError(t, os.Symlink(dir, parent))
	require.Error(t, Save(t.Context(), filepath.Join(parent, "new.json"), true))
	_, err = Load(t.Context(), filepath.Join(parent, "missing.json"))
	require.Error(t, err)
}

func TestImagePreferencePermissionFailuresKeepFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs differ from Unix mode bits")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "image-preferences.json")
	require.NoError(t, Save(t.Context(), path, false))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0))
	defer os.Chmod(path, 0600)
	_, err = Load(t.Context(), path)
	require.ErrorIs(t, err, os.ErrPermission)
	require.Error(t, Save(t.Context(), path, true))
	require.NoError(t, os.Chmod(path, 0600))
	require.NoError(t, os.Chmod(dir, 0500))
	defer os.Chmod(dir, 0700)
	require.ErrorIs(t, Save(t.Context(), path, true), os.ErrPermission)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
