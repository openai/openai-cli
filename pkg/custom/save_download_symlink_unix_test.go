//go:build !windows

package custom

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestManagedDownloadSpecialDestinationStreamsToDevice(t *testing.T) {
	_, err := WriteBinaryResponse(&http.Response{Body: io.NopCloser(bytes.NewReader([]byte("synthetic bytes")))}, io.Discard, os.DevNull)
	require.NoError(t, err)
}

func TestManagedDownloadSpecialDestinationStreamsToFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.fifo")
	require.NoError(t, unix.Mkfifo(path, 0600))
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	require.NoError(t, err)
	reader := os.NewFile(uintptr(fd), path)
	defer reader.Close()
	// A small payload fits in the pipe while the nonblocking reader waits.
	payload := []byte{0, 255, 254, 's', 'y', 'n', 't', 'h', 'e', 't', 'i', 'c'}
	message, err := WriteBinaryResponse(&http.Response{Body: io.NopCloser(bytes.NewReader(payload))}, io.Discard, path)
	require.NoError(t, err)
	require.Equal(t, "Wrote output to: "+path, message)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, payload, data)
}

func TestManagedDownloadOpenerRefusesSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.Symlink(os.DevNull, path))
	for _, nonblock := range []bool{false, true} {
		file, err := clioutput.OpenDownloadDestination(path, nonblock)
		if file != nil {
			_ = file.Close()
		}
		require.Error(t, err)
	}
}
