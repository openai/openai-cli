package imagegallery

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Cancel on a known checkpoint to exercise encoding without timer races.
type cancelAfterChecks struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (ctx *cancelAfterChecks) Err() error {
	ctx.checks--
	if ctx.checks == 0 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

type cancelNormalizationImage struct {
	image.Image
	cancel  context.CancelFunc
	samples int
}

func (img *cancelNormalizationImage) At(x, y int) color.Color {
	img.samples++
	if img.samples == 1 {
		img.cancel()
	}
	return img.Image.At(x, y)
}

func TestGalleryNormalizationStopsDuringPixelProcessing(t *testing.T) {
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	img := &cancelNormalizationImage{Image: image.NewNRGBA(image.Rect(0, 0, 512, 512)), cancel: cancel}
	_, data, err := normalize(base, img)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, data)
	require.LessOrEqual(t, img.samples, 2048, "cancellation must stop the pixel scan early")
}

func TestGalleryEncodingCancellationLeavesCacheUnchanged(t *testing.T) {
	g := initialized(t)
	state := g.State()
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	img := &cancelNormalizationImage{Image: image.NewNRGBA(image.Rect(0, 0, 512, 512)), cancel: cancel}
	_, err := g.Prepare(base, img, 4)
	require.ErrorIs(t, err, context.Canceled)
	require.LessOrEqual(t, img.samples, 2048)
	require.Equal(t, state, g.State())
	for _, name := range []string{"images", "fonts"} {
		files, err := os.ReadDir(filepath.Join(g.directory, name))
		require.NoError(t, err)
		require.Empty(t, files, "cancelled encoding must not publish cache artifacts")
	}
}

func TestGalleryCacheWriteCancellation(t *testing.T) {
	for _, check := range []int{1, 2, 3} {
		base, cancel := context.WithCancel(t.Context())
		ctx := &cancelAfterChecks{base, cancel, check}
		path := filepath.Join(t.TempDir(), "cache")
		err := writeNew(ctx, path, []byte("synthetic cache bytes"))
		cancel()
		require.ErrorIs(t, err, context.Canceled)
		require.NoFileExists(t, path, "cancelled writes must not retain a partial artifact")
	}
	path := filepath.Join(t.TempDir(), "existing")
	require.NoError(t, os.WriteFile(path, []byte("retain active font"), 0600))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, writeNew(ctx, path, []byte("replacement")), context.Canceled)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "retain active font", string(data))
}
