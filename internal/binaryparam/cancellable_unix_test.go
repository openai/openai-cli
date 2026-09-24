//go:build !windows

package binaryparam

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func cancellablePipe(t *testing.T) (io.ReadCloser, *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	info, err := reader.Stat()
	require.NoError(t, err)
	wrapped, err := CancellableFile(reader, info)
	if err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wrapped.Close() })
	return wrapped, writer
}

func TestCancellableFIFOShortReadsAndEOF(t *testing.T) {
	reader, writer := cancellablePipe(t)
	_, err := writer.Write([]byte("partial"))
	require.NoError(t, err)
	buf := make([]byte, 3)
	n, err := reader.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "par", string(buf[:n]))
	require.NoError(t, writer.Close())
	result := make(chan string, 1)
	go func() {
		data, err := io.ReadAll(reader)
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		result <- string(data)
	}()
	select {
	case rest := <-result:
		require.Equal(t, "tial", rest)
	case <-time.After(3 * time.Second):
		t.Fatal("EOF was not detected after the writer closed")
	}
}

func TestCancellableFIFOCloseReleasesRead(t *testing.T) {
	reader, _ := cancellablePipe(t)
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := reader.Read(make([]byte, 1))
		done <- err
	}()
	<-started
	require.NoError(t, reader.Close())
	select {
	case err := <-done:
		require.ErrorIs(t, err, os.ErrClosed)
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not release the reader")
	}
	require.NoError(t, reader.Close())
}
