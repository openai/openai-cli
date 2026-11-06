package skills

import (
	"context"
	"errors"
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
