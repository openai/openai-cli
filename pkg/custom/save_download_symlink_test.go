package custom

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteBinaryResponseRejectsSymlinkDestinations(t *testing.T) {
	for _, kind := range []string{"relative", "absolute", "dangling", "chain", "device"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "target.bin")
			path := filepath.Join(directory, "output.bin")
			if kind != "dangling" {
				require.NoError(t, os.WriteFile(target, []byte("keep"), 0600))
			}
			link := filepath.Base(target)
			switch kind {
			case "absolute":
				link = target
			case "chain":
				middle := filepath.Join(directory, "middle.bin")
				if err := os.Symlink(link, middle); err != nil {
					t.Skipf("symlink creation unavailable: %v", err)
				}
				link = filepath.Base(middle)
			case "device":
				link = os.DevNull
			}
			if err := os.Symlink(link, path); err != nil {
				t.Skipf("symlink creation unavailable: %v", err)
			}
			reader := bytes.NewReader([]byte("synthetic download"))
			body := &failedDownloadBody{Reader: reader}
			message, err := WriteBinaryResponse(&http.Response{Body: body}, io.Discard, path)
			require.Error(t, err)
			require.Empty(t, message)
			require.Equal(t, len("synthetic download"), reader.Len(), "rejected destinations must not consume the download")
			require.Equal(t, 1, body.closed)
			actualLink, err := os.Readlink(path)
			require.NoError(t, err)
			require.Equal(t, link, actualLink)
			data, err := os.ReadFile(target)
			if kind == "dangling" {
				require.True(t, os.IsNotExist(err), "%v", err)
			} else {
				require.NoError(t, err)
				require.Equal(t, "keep", string(data))
			}
			requireNoDownloadStages(t, directory)
		})
	}
}

func TestManagedDownloadRejectsSymlinkSwapDuringTransfer(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "target.bin")
			path := filepath.Join(directory, "output.bin")
			require.NoError(t, os.WriteFile(target, []byte("keep"), 0600))
			// Probe symlink support before entering the transfer callback.
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink creation unavailable: %v", err)
			}
			require.NoError(t, os.Remove(path))
			if existing {
				require.NoError(t, os.WriteFile(path, []byte("previous"), 0600))
			}
			body := &inspectingDownloadReader{Reader: bytes.NewReader([]byte("download")), inspect: func() error {
				if existing {
					if err := os.Remove(path); err != nil {
						return err
					}
				}
				return os.Symlink(target, path)
			}}
			message, err := WriteBinaryResponse(&http.Response{Body: io.NopCloser(body)}, io.Discard, path)
			require.Error(t, err)
			require.Empty(t, message)
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "keep", string(data))
			requireNoDownloadStages(t, directory)
		})
	}
}
