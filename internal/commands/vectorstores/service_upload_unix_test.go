//go:build !windows

package vectorstores

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServiceUploadRejectsReplacementFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected.txt")
	require.NoError(t, os.WriteFile(path, []byte("original synthetic file"), 0o600))
	before, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, before.Mode().IsRegular())
	require.NoError(t, os.Remove(path))
	require.NoError(t, syscall.Mkfifo(path, 0o600))

	done := make(chan error, 1)
	go func() {
		upload, err := openUpload(context.Background(), path)
		if err == nil {
			err = upload.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "regular file")
	case <-time.After(2 * time.Second):
		// Release a regressed blocking open before reporting failure.
		writer, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
		require.NoError(t, err)
		defer writer.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("FIFO open remained blocked after a writer connected")
		}
		t.Fatal("opening a replaced FIFO blocked until a writer connected")
	}
}
