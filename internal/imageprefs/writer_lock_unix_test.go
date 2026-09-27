//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package imageprefs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestImagePreferenceRejectsFIFOWriterLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image-preferences.json")
	require.NoError(t, unix.Mkfifo(filepath.Join(filepath.Dir(path), ".image-preferences.json.lock"), 0600))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- Save(ctx, path, true) }()
	select {
	case err := <-result:
		require.ErrorContains(t, err, "private regular file")
	case <-ctx.Done():
		t.Fatal("opening a FIFO lock blocked")
	}
}
