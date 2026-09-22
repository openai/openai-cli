package imagegallery

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareImageSharesFileGlyphMapping(t *testing.T) {
	gallery := initialized(t)
	path := fixture(t, t.TempDir(), "red.png", color.NRGBA{R: 200, A: 255})
	fromFile, err := gallery.Prepare(t.Context(), path, 4)
	require.NoError(t, err)
	require.NoError(t, gallery.Commit(t.Context(), fromFile))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	decoded, _, err := image.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	fromImage, err := gallery.PrepareImage(t.Context(), decoded, 32)
	require.NoError(t, err)
	require.True(t, fromImage.Existing)
	require.Equal(t, fromFile.Text, fromImage.Text)
	require.Equal(t, fromFile.Columns, fromImage.Columns)
	require.Equal(t, 1, gallery.State().ImageCount)
}

func TestPrepareImageRejectsEmptyAndCancelled(t *testing.T) {
	gallery := initialized(t)
	for _, img := range []image.Image{nil, image.NewRGBA(image.Rect(0, 0, 0, 0))} {
		_, err := gallery.PrepareImage(t.Context(), img, 4)
		require.ErrorContains(t, err, "empty image")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := gallery.PrepareImage(ctx, nil, 4)
	require.True(t, errors.Is(err, context.Canceled))
}
