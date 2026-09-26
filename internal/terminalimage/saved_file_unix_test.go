//go:build unix

package terminalimage

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestReadSavedFIFODoesNotWaitForWriter(t *testing.T) {
	// Use a child process so a regressed blocking open can be killed rather
	// than leaving a goroutine stuck in the test runner.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.v", "-test.run=^TestReadSavedFIFOProcess$")
	child.Env = append(os.Environ(), "OPENAI_CLI_SAVED_IMAGE_FIFO_TEST="+t.TempDir())
	output, err := child.CombinedOutput()
	require.NoError(t, ctx.Err(), "FIFO preview blocked without a writer: %s", output)
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), "--- PASS: TestReadSavedFIFOProcess")
}

func TestReadSavedFIFOProcess(t *testing.T) {
	directory := os.Getenv("OPENAI_CLI_SAVED_IMAGE_FIFO_TEST")
	if directory == "" {
		t.Skip("subprocess only")
	}
	path := filepath.Join(directory, "saved.png")
	require.NoError(t, unix.Mkfifo(path, 0600))
	link := filepath.Join(directory, "linked.png")
	require.NoError(t, os.Symlink(path, link))
	for _, name := range []string{path, link, os.DevNull} {
		_, err := ReadSaved(t.Context(), name)
		require.ErrorContains(t, err, "not a regular file")
		_, err = ReadSavedMatching(t.Context(), name, [32]byte{})
		require.ErrorContains(t, err, "not a regular file")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := ReadSaved(ctx, link)
	require.ErrorIs(t, err, context.Canceled)
	// This is the same opener used after a pathname could have changed. A FIFO
	// must open promptly and then fail validation of the actual descriptor.
	file, err := openSavedImage(path)
	require.NoError(t, err)
	defer file.Close()
	require.NoError(t, os.Remove(path))
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2, 3))))
	require.NoError(t, os.WriteFile(path, encoded.Bytes(), 0600))
	_, err = readSavedFile(t.Context(), file, nil)
	require.ErrorContains(t, err, "not a regular file", "a replacement regular path must not authorize reading the original FIFO descriptor")
}

func TestReadSavedFileUsesOpenedImageAfterPathReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "saved.png")
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2, 3))))
	require.NoError(t, os.WriteFile(path, encoded.Bytes(), 0600))
	link := filepath.Join(directory, "linked.png")
	require.NoError(t, os.Symlink(path, link))
	decoded, err := ReadSaved(t.Context(), link)
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 2, 3), decoded.Bounds())
	file, err := openSavedImage(link)
	require.NoError(t, err)
	defer file.Close()
	original := filepath.Join(directory, "original.png")
	require.NoError(t, os.Rename(path, original))
	require.NoError(t, unix.Mkfifo(path, 0600))
	decoded, err = readSavedFile(t.Context(), file, nil)
	require.NoError(t, err, "a replacement FIFO path must not replace validation of the opened regular image")
	require.Equal(t, image.Rect(0, 0, 2, 3), decoded.Bounds())
	unchanged, err := os.ReadFile(original)
	require.NoError(t, err)
	require.Equal(t, encoded.Bytes(), unchanged)
}
