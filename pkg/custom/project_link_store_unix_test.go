//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package custom

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestProjectLinkStoreMissingRegistryPreservesExistingConfiguration(t *testing.T) {
	path := projectLinkTestPath(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.Chmod(filepath.Dir(path), 0755))
	links, err := loadProjectLinks(t.Context(), path)
	require.NoError(t, err)
	require.Empty(t, links)
	_, err = updateProjectLink(t.Context(), path, t.TempDir(), "proj_new")
	require.ErrorContains(t, err, "private configuration directory")
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProjectLinkStoreRejectsInsecurePermissions(t *testing.T) {
	for _, target := range []string{"registry", "directory", "lock"} {
		t.Run(target, func(t *testing.T) {
			path := projectLinkTestPath(t)
			writeProjectLinkTestFile(t, path, "{}")
			switch target {
			case "registry":
				require.NoError(t, os.Chmod(path, 0644))
			case "directory":
				require.NoError(t, os.Chmod(filepath.Dir(path), 0755))
			case "lock":
				lockPath := filepath.Join(filepath.Dir(path), ".project-links.json.lock")
				require.NoError(t, os.WriteFile(lockPath, nil, 0600))
				require.NoError(t, os.Chmod(lockPath, 0666))
			}
			if target != "lock" {
				_, err := loadProjectLinks(t.Context(), path)
				require.ErrorContains(t, err, "private")
			}
			_, err := updateProjectLink(t.Context(), path, t.TempDir(), "proj_new")
			require.ErrorContains(t, err, "private")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, "{}", string(data))
		})
	}
}

func TestProjectLinkStoreRejectsFIFOReplacementWithoutBlocking(t *testing.T) {
	path := projectLinkTestPath(t)
	writeProjectLinkTestFile(t, path, "{}")
	root, name, err := openProjectLinkDirectory(path, false)
	require.NoError(t, err)
	defer root.Close()
	info, err := root.Lstat(name)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	require.NoError(t, unix.Mkfifo(path, 0600))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := readProjectLinkSnapshot(ctx, root, name, info)
		result <- err
	}()
	select {
	case err := <-result:
		require.ErrorIs(t, err, errProjectLinksChanged)
	case <-ctx.Done():
		t.Fatal("opening a substituted FIFO blocked")
	}
}

func TestProjectLinkStoreRejectsFIFOLockWithoutBlocking(t *testing.T) {
	path := projectLinkTestPath(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, unix.Mkfifo(filepath.Join(filepath.Dir(path), ".project-links.json.lock"), 0600))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	directory := t.TempDir()
	go func() {
		_, err := updateProjectLink(ctx, path, directory, "proj_new")
		result <- err
	}()
	select {
	case err := <-result:
		require.ErrorContains(t, err, "private regular file")
	case <-ctx.Done():
		t.Fatal("opening a FIFO lock blocked")
	}
}

func TestProjectLinkStoreUnwritableDirectoryKeepsRegistry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses Unix write permissions")
	}
	path := projectLinkTestPath(t)
	writeProjectLinkTestFile(t, path, "{}")
	require.NoError(t, os.Chmod(filepath.Dir(path), 0500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(filepath.Dir(path), 0700)) })
	_, err := updateProjectLink(t.Context(), path, t.TempDir(), "proj_new")
	require.ErrorIs(t, err, os.ErrPermission)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "{}", string(data))
}
