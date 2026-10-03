//go:build darwin || linux || windows

package autocomplete

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickerRenameNoReplace(t *testing.T) {
	t.Run("open editor continues writing to the retained inode", func(t *testing.T) {
		root, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		defer root.Close()
		writer, err := root.OpenFile("profile", os.O_CREATE|os.O_RDWR, 0600)
		require.NoError(t, err)
		defer writer.Close()
		_, err = writer.WriteString("original\n")
		require.NoError(t, err)
		require.NoError(t, renamePickerFileNoReplace(root, "profile", "recovery"))
		_, err = writer.WriteString("late editor save\n")
		require.NoError(t, err)
		data, err := root.ReadFile("recovery")
		require.NoError(t, err)
		require.Equal(t, "original\nlate editor save\n", string(data))
	})

	t.Run("only one competing destination move can succeed", func(t *testing.T) {
		root, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		defer root.Close()
		const count = 16
		start := make(chan struct{})
		results := make([]error, count)
		var workers sync.WaitGroup
		for index := range count {
			name := fmt.Sprintf("source-%d", index)
			require.NoError(t, root.WriteFile(name, []byte(name), 0600))
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				results[index] = renamePickerFileNoReplace(root, name, "winner")
			}()
		}
		close(start)
		workers.Wait()
		successes := 0
		for index, result := range results {
			name := fmt.Sprintf("source-%d", index)
			path := name
			if result == nil {
				successes++
				path = "winner"
				_, err := root.Lstat(name)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			data, err := root.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, name, string(data), "every source must survive under its original name or the winning destination")
		}
		require.Equal(t, 1, successes)
	})

	t.Run("capture and restore preserve the original inode", func(t *testing.T) {
		directory := t.TempDir()
		root, err := os.OpenRoot(directory)
		require.NoError(t, err)
		defer root.Close()
		require.NoError(t, root.Mkdir("recovery", 0700))
		require.NoError(t, root.WriteFile("profile", []byte("original\n"), 0600))
		previous, err := root.Stat("profile")
		require.NoError(t, err)
		require.NoError(t, renamePickerFileNoReplace(root, "profile", filepath.Join("recovery", "profile")))
		_, err = root.Lstat("profile")
		require.ErrorIs(t, err, os.ErrNotExist)
		captured, err := root.Stat(filepath.Join("recovery", "profile"))
		require.NoError(t, err)
		require.True(t, os.SameFile(previous, captured))
		require.NoError(t, renamePickerFileNoReplace(root, filepath.Join("recovery", "profile"), "profile"))
		restored, err := root.Stat("profile")
		require.NoError(t, err)
		require.True(t, os.SameFile(previous, restored))
		data, err := root.ReadFile("profile")
		require.NoError(t, err)
		require.Equal(t, "original\n", string(data))
	})

	for _, collision := range []string{"file", "directory", "symlink", "dangling symlink"} {
		t.Run("keep destination "+collision, func(t *testing.T) {
			directory := t.TempDir()
			root, err := os.OpenRoot(directory)
			require.NoError(t, err)
			defer root.Close()
			require.NoError(t, root.WriteFile("source", []byte("source\n"), 0600))
			switch collision {
			case "file":
				require.NoError(t, root.WriteFile("target", []byte("editor's newer file\n"), 0600))
			case "directory":
				require.NoError(t, root.Mkdir("target", 0700))
			case "symlink", "dangling symlink":
				if collision == "symlink" {
					require.NoError(t, root.WriteFile("linked", []byte("linked\n"), 0600))
				}
				pickerRenameTestSymlink(t, "linked", filepath.Join(directory, "target"))
			}
			before, err := root.Lstat("target")
			require.NoError(t, err)
			require.Error(t, renamePickerFileNoReplace(root, "source", "target"))
			after, err := root.Lstat("target")
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after), "no existing destination entry may be replaced")
			data, err := root.ReadFile("source")
			require.NoError(t, err)
			require.Equal(t, "source\n", string(data))
			if collision == "file" {
				data, err := root.ReadFile("target")
				require.NoError(t, err)
				require.Equal(t, "editor's newer file\n", string(data))
			}
		})
	}

	t.Run("move the source symlink without following it", func(t *testing.T) {
		directory := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside")
		require.NoError(t, os.WriteFile(outside, []byte("outside\n"), 0600))
		pickerRenameTestSymlink(t, outside, filepath.Join(directory, "profile"))
		root, err := os.OpenRoot(directory)
		require.NoError(t, err)
		defer root.Close()
		require.NoError(t, root.Mkdir("recovery", 0700))
		before, err := root.Lstat("profile")
		require.NoError(t, err)
		require.NoError(t, renamePickerFileNoReplace(root, "profile", filepath.Join("recovery", "profile")))
		after, err := root.Lstat(filepath.Join("recovery", "profile"))
		require.NoError(t, err)
		require.True(t, os.SameFile(before, after))
		require.NotZero(t, after.Mode()&os.ModeSymlink)
		target, err := os.Readlink(filepath.Join(directory, "recovery", "profile"))
		require.NoError(t, err)
		require.Equal(t, outside, target)
		data, err := os.ReadFile(outside)
		require.NoError(t, err)
		require.Equal(t, "outside\n", string(data))
	})

	t.Run("missing source leaves destination absent", func(t *testing.T) {
		root, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		defer root.Close()
		require.ErrorIs(t, renamePickerFileNoReplace(root, "missing", "target"), os.ErrNotExist)
		_, err = root.Lstat("target")
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("reject escape through parent symlink", func(t *testing.T) {
		directory, outside := t.TempDir(), t.TempDir()
		pickerRenameTestSymlink(t, outside, filepath.Join(directory, "escape"))
		root, err := os.OpenRoot(directory)
		require.NoError(t, err)
		defer root.Close()
		require.NoError(t, root.WriteFile("source", []byte("source\n"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(outside, "source"), []byte("outside\n"), 0600))
		require.Error(t, renamePickerFileNoReplace(root, "source", filepath.Join("escape", "target")))
		require.Error(t, renamePickerFileNoReplace(root, filepath.Join("escape", "source"), "target"))
		for path, expected := range map[string]string{filepath.Join(directory, "source"): "source\n", filepath.Join(outside, "source"): "outside\n"} {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, expected, string(data))
		}
	})

	t.Run("reject unsafe names before moving", func(t *testing.T) {
		directory := t.TempDir()
		root, err := os.OpenRoot(directory)
		require.NoError(t, err)
		defer root.Close()
		require.NoError(t, root.WriteFile("source", []byte("source\n"), 0600))
		for _, name := range []string{"", ".", "..", filepath.Join("..", "target"), filepath.Join(directory, "target"), "child" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "target"} {
			require.ErrorIs(t, renamePickerFileNoReplace(root, "source", name), os.ErrInvalid)
			require.ErrorIs(t, renamePickerFileNoReplace(root, name, "target"), os.ErrInvalid)
		}
		data, err := root.ReadFile("source")
		require.NoError(t, err)
		require.Equal(t, "source\n", string(data))
	})
}

func pickerRenameTestSymlink(t *testing.T, target, path string) {
	t.Helper()
	err := os.Symlink(target, path)
	// ERROR_PRIVILEGE_NOT_HELD means the host has not enabled symlink creation
	// for this token. Other failures must still fail the test.
	if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
		t.Skipf("symlink creation is unavailable for this Windows token: %v", err)
	}
	require.NoError(t, err)
}
