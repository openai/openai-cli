package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// Trigger the change when the walk checks its context, after opening the selected root.
type duringWalk struct {
	context.Context
	once   sync.Once
	change func()
}

func (c *duringWalk) Err() error {
	c.once.Do(c.change)
	return c.Context.Err()
}

func TestFolderChangesDuringPackaging(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "cancel"
		if replace {
			name = "replace root"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "demo")
			require.NoError(t, os.Mkdir(path, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("original"), 0600))
			info, err := os.Stat(path)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			locked := false
			walk := &duringWalk{Context: ctx, change: func() {
				if replace {
					err := os.Rename(path, path+"-old")
					if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(32)) {
						// Windows can pin the directory by denying delete sharing.
						locked = true
						cancel()
						return
					}
					require.NoError(t, err)
					require.NoError(t, os.Mkdir(path, 0700))
				} else {
					cancel()
				}
			}}
			upload, err := ArchiveFolder(walk, path, info)
			require.Error(t, err)
			require.Nil(t, upload.Reader)
			if replace && !locked {
				require.ErrorContains(t, err, "changed during packaging")
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}

func TestFolderOwnsTemporaryZIP(t *testing.T) {
	dir := t.TempDir()
	scratch := filepath.Join(dir, "scratch")
	require.NoError(t, os.Mkdir(scratch, 0700))
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, scratch)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("synthetic"), 0600))
	info, err := os.Stat(dir)
	require.NoError(t, err)
	upload, err := ArchiveFolder(t.Context(), dir, info)
	require.NoError(t, err)
	defer upload.Close()
	data, err := io.ReadAll(upload)
	require.NoError(t, err)
	require.NoError(t, upload.Close())
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	var names []string
	for _, f := range archive.File {
		names = append(names, f.Name)
	}
	require.ElementsMatch(t, []string{filepath.Base(dir) + "/SKILL.md", filepath.Base(dir) + "/scratch/"}, names)
	entries, err := os.ReadDir(scratch)
	require.NoError(t, err)
	require.Empty(t, entries)
}
