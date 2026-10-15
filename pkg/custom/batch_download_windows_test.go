//go:build windows

package custom

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWindowsBatchDownloadPrivateStagingAndPublication(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "result.jsonl")
	payload := []byte("{\"custom_id\":\"synthetic\"}\r\n\x00\xff")
	reader := bytes.NewReader(payload)
	var staged os.FileInfo
	closed := false
	body := batchDownloadReader{
		read: func(p []byte) (int, error) {
			if staged == nil {
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				require.NotEqual(t, filepath.Base(path), entries[0].Name())
				stage, err := os.Open(filepath.Join(directory, entries[0].Name()))
				require.NoError(t, err)
				defer func() { require.NoError(t, stage.Close()) }()
				staged, err = stage.Stat()
				require.NoError(t, err)
				require.True(t, staged.Mode().IsRegular())
				require.Zero(t, staged.Size())
				assertPrivateWindowsDownload(t, stage)
			}
			return reader.Read(p)
		},
		close: func() error {
			closed = true
			return nil
		},
	}
	written, err := writeBatchDownload(t.Context(), path, body)
	require.NoError(t, err)
	require.NotNil(t, staged)
	require.True(t, closed)
	require.Equal(t, int64(len(payload)), written)
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	assertPrivateWindowsDownload(t, file)
	published, err := file.Stat()
	require.NoError(t, err)
	require.True(t, os.SameFile(staged, published))
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, payload, data)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, filepath.Base(path), entries[0].Name())
}
