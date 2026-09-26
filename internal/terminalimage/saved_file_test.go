package terminalimage

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadSavedPreservesSource(t *testing.T) {
	for _, kind := range []string{"png", "jpeg"} {
		t.Run(kind, func(t *testing.T) {
			var encoded bytes.Buffer
			img := image.NewNRGBA(image.Rect(0, 0, 2, 3))
			if kind == "png" {
				require.NoError(t, png.Encode(&encoded, img))
			} else {
				require.NoError(t, jpeg.Encode(&encoded, img, nil))
			}
			file := filepath.Join(t.TempDir(), "source."+kind)
			require.NoError(t, os.WriteFile(file, encoded.Bytes(), 0o600))
			got, err := ReadSaved(t.Context(), file)
			require.NoError(t, err)
			require.Equal(t, img.Bounds(), got.Bounds())
			after, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Equal(t, encoded.Bytes(), after)
		})
	}
}

func TestReadSavedOptionalPreviewLimits(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "image.png")
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 1, 1))))
	header := append([]byte(nil), encoded.Bytes()...)
	binary.BigEndian.PutUint32(header[16:20], 8192)
	binary.BigEndian.PutUint32(header[20:24], 8192)
	binary.BigEndian.PutUint32(header[29:33], crc32.ChecksumIEEE(header[12:29]))
	require.NoError(t, os.WriteFile(file, header, 0o600))
	_, err := ReadSaved(t.Context(), file)
	require.ErrorContains(t, err, "16 megapixel")
	f, err := os.OpenFile(file, os.O_WRONLY, 0)
	require.NoError(t, err)
	require.NoError(t, f.Truncate((64<<20)+1))
	require.NoError(t, f.Close())
	_, err = ReadSaved(t.Context(), file)
	require.ErrorContains(t, err, "64 MiB")
	_, err = ReadSaved(t.Context(), dir)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(file, []byte("not an image"), 0o600))
	_, err = ReadSaved(t.Context(), file)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = ReadSaved(ctx, file)
	require.ErrorIs(t, err, context.Canceled)
}

func TestReadSavedFileRejectsNonRegularDescriptor(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	require.NoError(t, writer.Close())
	_, err = readSavedFile(t.Context(), reader)
	require.ErrorContains(t, err, "not a regular file")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = readSavedFile(ctx, reader)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, reader.Close())
	_, err = readSavedFile(t.Context(), reader)
	require.ErrorIs(t, err, os.ErrClosed)
}
