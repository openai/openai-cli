package imagegallery

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/stretchr/testify/require"
)

func TestGalleryLimitKeepsCapacityForFinalAndReplaysExisting(t *testing.T) {
	g := initialized(t)
	makeImage := func(width, height int, shade uint8) image.Image {
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		img.SetNRGBA(0, 0, color.NRGBA{R: shade, A: 255})
		return img
	}
	// Five earlier tall previews consume 5120 cells. The partial can consume
	// exactly 256 more while reserving 1024 for the largest final placement.
	for i := range 5 {
		revision, err := g.Prepare(t.Context(), makeImage(32, 64, uint8(i+1)), 32)
		require.NoError(t, err)
		require.NoError(t, g.Commit(t.Context(), revision))
	}
	const reserve = 32 * imagefont.MaxFrameRows
	limit := imagefont.MaxGlyphs - reserve
	partialImage := makeImage(8, 64, 10)
	partial, err := g.PrepareWithLimit(t.Context(), partialImage, 8, limit)
	require.NoError(t, err)
	require.NoError(t, g.Commit(t.Context(), partial))
	require.Equal(t, limit, g.State().UsedGlyphs)
	before := g.State()
	stateBefore, err := os.ReadFile(filepath.Join(g.directory, "state.json"))
	require.NoError(t, err)
	imagesBefore, err := os.ReadDir(filepath.Join(g.directory, "images"))
	require.NoError(t, err)
	_, err = g.PrepareWithLimit(t.Context(), makeImage(8, 64, 11), 8, limit)
	require.ErrorIs(t, err, ErrFull)
	require.Equal(t, before, g.State())
	stateAfter, err := os.ReadFile(filepath.Join(g.directory, "state.json"))
	require.NoError(t, err)
	require.Equal(t, stateBefore, stateAfter)
	imagesAfter, err := os.ReadDir(filepath.Join(g.directory, "images"))
	require.NoError(t, err)
	require.Len(t, imagesAfter, len(imagesBefore))
	_, err = os.Stat(filepath.Join(g.directory, ".pending.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
	final, err := g.Prepare(t.Context(), makeImage(32, 64, 12), 32)
	require.NoError(t, err)
	require.NoError(t, g.Commit(t.Context(), final))
	require.Equal(t, imagefont.MaxGlyphs, g.State().UsedGlyphs)
	replay, err := g.PrepareWithLimit(t.Context(), partialImage, 8, 0)
	require.NoError(t, err)
	require.True(t, replay.Existing)
	require.Equal(t, partial.Text, replay.Text)
	require.NoError(t, g.Commit(t.Context(), replay))
	require.Equal(t, imagefont.MaxGlyphs, g.State().UsedGlyphs)
}

func TestGalleryLimitRejectsInvalidLimits(t *testing.T) {
	g := initialized(t)
	for _, limit := range []int{-1, imagefont.MaxGlyphs + 1} {
		_, err := g.PrepareWithLimit(t.Context(), fixture(color.NRGBA{R: 255, A: 255}), 4, limit)
		require.Error(t, err)
	}
	require.Zero(t, g.State().ImageCount)
}
