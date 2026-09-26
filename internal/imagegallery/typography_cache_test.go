package imagegallery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/font/sfnt"
)

func TestTypographyRebuildsVersionThreeCacheWithoutChangingScrollback(t *testing.T) {
	g := initialized(t)
	img := fixture(color.NRGBA{R: 200, A: 255})
	source := typographySource(t)
	revision, err := g.Prepare(t.Context(), img, 4)
	require.NoError(t, err)
	// Seed the historical v3 identity exactly as published. An already committed
	// image must receive the corrected encoder even when its old cache exists.
	identity, err := json.Marshal(struct {
		Version    int
		Revision   string
		Source     imagefont.PreserveOptions
		Companions []imagefont.PreserveOptions
	}{3, revision.state.PostScript, source, nil})
	require.NoError(t, err)
	digest := sha256.Sum256(identity)
	token := hex.EncodeToString(digest[:16])
	faceDigest := sha256.Sum256([]byte(token + ":" + source.SourcePostScript))
	faceToken := hex.EncodeToString(faceDigest[:16])
	oldPath := filepath.Join(g.directory, "fonts", "revision-"+faceToken+".ttf")
	oldPostScript := "OpenAIImages-" + revision.state.ID[:8] + "-" + faceToken + "-Regular"
	legacy, err := imagefont.EncodePreserving(t.Context(), []imagefont.Frame{{
		Image: img, Columns: revision.Columns, Rows: revision.Rows,
		CodepointStart: imagefont.FirstSupplementaryCodepoint,
	}}, imagefont.Options{Family: "OpenAI Local " + revision.state.ID[:8] + " " + token[:8], PostScript: oldPostScript}, source)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(oldPath, legacy.Data, 0600))
	require.NoError(t, g.Commit(t.Context(), revision))
	state := g.State()
	stateData, err := os.ReadFile(filepath.Join(g.directory, "state.json"))
	require.NoError(t, err)
	repeated, err := g.Prepare(t.Context(), img, 4)
	require.NoError(t, err)
	require.True(t, repeated.Existing)
	display, err := g.FontForTypography(t.Context(), repeated, source)
	require.NoError(t, err)
	require.False(t, display.Existing, "the old encoder cache must be rebuilt")
	require.NotEqual(t, oldPath, display.FontPath)
	require.NotEqual(t, oldPostScript, display.PostScript)
	currentData, err := os.ReadFile(display.FontPath)
	require.NoError(t, err)
	require.False(t, bytes.Equal(legacy.Data, currentData))
	oldFont, err := sfnt.Parse(legacy.Data)
	require.NoError(t, err)
	currentFont, err := sfnt.Parse(currentData)
	require.NoError(t, err)
	for _, character := range "ordinary text" + display.Text {
		if character == '\n' {
			continue
		}
		oldGlyph, err := oldFont.GlyphIndex(nil, character)
		require.NoError(t, err)
		currentGlyph, err := currentFont.GlyphIndex(nil, character)
		require.NoError(t, err)
		require.NotZero(t, oldGlyph)
		require.Equal(t, oldGlyph, currentGlyph)
	}
	resolved, err := g.LookupFontPS(t.Context(), oldPostScript)
	require.NoError(t, err)
	require.Equal(t, oldPath, resolved)
	retained, err := os.ReadFile(oldPath)
	require.NoError(t, err)
	require.Equal(t, legacy.Data, retained)
	reused, err := g.FontForTypography(t.Context(), repeated, source)
	require.NoError(t, err)
	require.True(t, reused.Existing)
	require.Equal(t, display.FontPath, reused.FontPath)
	require.NoError(t, g.Commit(t.Context(), repeated))
	require.Equal(t, state, g.State())
	unchanged, err := os.ReadFile(filepath.Join(g.directory, "state.json"))
	require.NoError(t, err)
	require.Equal(t, stateData, unchanged)
}
