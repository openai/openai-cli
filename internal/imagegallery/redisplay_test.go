package imagegallery

import (
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/stretchr/testify/require"
)

func TestGalleryRedisplayAtNewWidthKeepsScrollback(t *testing.T) {
	g := initialized(t)
	source := fixture(color.NRGBA{R: 210, G: 40, B: 80, A: 128})
	first, err := g.Prepare(t.Context(), source, 8)
	require.NoError(t, err)
	require.NoError(t, g.Commit(t.Context(), first))
	earlier := g.state.Images[0]
	font, err := os.ReadFile(first.FontPath)
	require.NoError(t, err)
	cached := filepath.Join(g.directory, "images", earlier.Hash+".png")
	originalPNG, err := os.ReadFile(cached)
	require.NoError(t, err)
	resized, err := g.Prepare(t.Context(), source, 4)
	require.NoError(t, err)
	require.False(t, resized.Existing)
	require.Equal(t, 4, resized.Columns)
	require.Equal(t, 1, g.State().ImageCount, "prepare must not publish before font activation")
	require.NotEqual(t, first.FontPath, resized.FontPath)
	require.NotEqual(t, first.Text, resized.Text)
	require.NoError(t, g.Commit(t.Context(), resized))
	require.Equal(t, earlier, g.state.Images[0])
	require.Equal(t, first.Text, textFor(g.state.Images[0]))
	require.Equal(t, earlier.Start+rune(earlier.Columns*earlier.Rows), g.state.Images[1].Start)
	after, err := os.ReadFile(first.FontPath)
	require.NoError(t, err)
	require.Equal(t, font, after)
	after, err = os.ReadFile(cached)
	require.NoError(t, err)
	require.Equal(t, originalPNG, after)
	cacheEntries, err := os.ReadDir(filepath.Dir(cached))
	require.NoError(t, err)
	require.Len(t, cacheEntries, 1)
	state := g.State()
	require.Equal(t, 2, state.ImageCount)
	for _, width := range []int{8, 4} {
		repeated, err := g.Prepare(t.Context(), source, width)
		require.NoError(t, err)
		require.True(t, repeated.Existing)
		require.Equal(t, width, repeated.Columns)
		require.Equal(t, resized.FontPath, repeated.FontPath)
		if width == 8 {
			require.Equal(t, first.Text, repeated.Text)
		} else {
			require.Equal(t, resized.Text, repeated.Text)
		}
		require.NoError(t, g.Commit(t.Context(), repeated))
		require.Equal(t, state, g.State())
	}
	directory := g.directory
	require.NoError(t, g.Close())
	reopened, err := Open(t.Context(), directory)
	require.NoError(t, err)
	defer reopened.Close()
	require.Equal(t, state, reopened.State())
	repeated, err := reopened.Prepare(t.Context(), source, 4)
	require.NoError(t, err)
	require.True(t, repeated.Existing)
	require.Equal(t, resized.Text, repeated.Text)
}

func TestGalleryDuplicateWidthAllocationRejected(t *testing.T) {
	g := initialized(t)
	source := fixture(color.NRGBA{R: 255, A: 255})
	first, err := g.Prepare(t.Context(), source, 4)
	require.NoError(t, err)
	require.NoError(t, g.Commit(t.Context(), first))
	duplicate := g.state.Images[0]
	duplicate.Start += rune(duplicate.Columns * duplicate.Rows)
	g.state.Images = append(g.state.Images, duplicate)
	// A changed row count must not let the same image and width claim a second
	// glyph range; rows are not an independent display setting.
	g.state.Images[1].Rows++
	raw, err := json.Marshal(g.state)
	require.NoError(t, err)
	directory := g.directory
	require.NoError(t, g.Close())
	require.NoError(t, os.WriteFile(filepath.Join(directory, "state.json"), raw, 0o600))
	reopened, err := Open(t.Context(), directory)
	require.Error(t, err)
	if reopened != nil {
		_ = reopened.Close()
	}
}

func TestGalleryResizeAtCapacityKeepsExistingPlacement(t *testing.T) {
	g := initialized(t)
	source := fixture(color.NRGBA{G: 255, A: 255})
	first, err := g.Prepare(t.Context(), source, 4)
	require.NoError(t, err)
	require.NoError(t, g.Commit(t.Context(), first))
	// Fill accounting without generating a 6400-glyph fixture. Existing entries
	// and the committed font remain untouched by a failed new placement.
	remaining := imagefont.MaxGlyphs - g.State().UsedGlyphs
	g.state.Images = append(g.state.Images, entry{Columns: remaining, Rows: 1})
	before := g.State()
	_, err = g.Prepare(t.Context(), source, 2)
	require.ErrorIs(t, err, ErrFull)
	require.Equal(t, before, g.State())
	repeated, err := g.Prepare(t.Context(), source, 4)
	require.NoError(t, err)
	require.True(t, repeated.Existing)
	require.Equal(t, first.Text, repeated.Text)
	require.Equal(t, before, g.State())
}
