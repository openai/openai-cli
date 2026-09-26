package imagegallery

import (
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/font/sfnt"
)

func TestGalleryPreparationDefersFontsUntilTypography(t *testing.T) {
	g := initialized(t)
	fontDirectory := filepath.Join(g.directory, "fonts")
	files, err := os.ReadDir(fontDirectory)
	require.NoError(t, err)
	require.Empty(t, files, "initialization only needs revision metadata")
	first, err := g.Prepare(t.Context(), fixture(color.NRGBA{R: 200, A: 255}), 4)
	require.NoError(t, err)
	files, err = os.ReadDir(fontDirectory)
	require.NoError(t, err)
	require.Empty(t, files, "preparation must not generate an unused cumulative font")
	display, err := g.FontForTypography(t.Context(), first, typographySource(t))
	require.NoError(t, err)
	files, err = os.ReadDir(fontDirectory)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, filepath.Base(display.FontPath), files[0].Name())
	data, err := os.ReadFile(display.FontPath)
	require.NoError(t, err)
	font, err := sfnt.Parse(data)
	require.NoError(t, err)
	for _, character := range "ordinary text" + display.Text {
		if character == '\n' {
			continue
		}
		glyph, err := font.GlyphIndex(nil, character)
		require.NoError(t, err)
		require.NotZero(t, glyph)
	}
	require.NoError(t, g.Commit(t.Context(), first))
	_, err = g.Prepare(t.Context(), fixture(color.NRGBA{B: 200, A: 255}), 4)
	require.NoError(t, err)
	files, err = os.ReadDir(fontDirectory)
	require.NoError(t, err)
	require.Len(t, files, 1, "another allocation must retain only the actual display font")
}

func TestGalleryLegacyRevisionKeepsTypographyAndScrollback(t *testing.T) {
	for _, artifact := range []string{"retained", "missing", "symlink"} {
		t.Run(artifact, func(t *testing.T) {
			if artifact == "symlink" && runtime.GOOS == "windows" {
				t.Skip("symlink privileges differ on Windows")
			}
			g := initialized(t)
			red := fixture(color.NRGBA{R: 200, A: 255})
			first, err := g.Prepare(t.Context(), red, 4)
			require.NoError(t, err)
			// Reproduce the version-1 cumulative artifact written by the earlier
			// implementation. Its metadata remains the revision's cache identity.
			legacyState := first.state
			placement := legacyState.Images[0]
			legacy, err := imagefont.Encode(t.Context(), []imagefont.Frame{{Image: red, Columns: placement.Columns, Rows: placement.Rows, CodepointStart: placement.Start}}, imagefont.Options{Family: legacyState.Family, PostScript: legacyState.PostScript})
			require.NoError(t, err)
			legacyPath := filepath.Join(g.directory, "fonts", legacyState.Font)
			require.NoError(t, os.WriteFile(legacyPath, legacy.Data, 0600))
			source := typographySource(t)
			firstFont, err := g.FontForTypography(t.Context(), first, source)
			require.NoError(t, err)
			firstData, err := os.ReadFile(firstFont.FontPath)
			require.NoError(t, err)
			require.NoError(t, g.Commit(t.Context(), first))
			directory := g.directory
			statePath := filepath.Join(directory, "state.json")
			stateData, err := os.ReadFile(statePath)
			require.NoError(t, err)
			require.NoError(t, g.Close())
			if artifact != "retained" {
				require.NoError(t, os.Remove(legacyPath))
			}
			outside := filepath.Join(t.TempDir(), "unrelated-font")
			if artifact == "symlink" {
				require.NoError(t, os.WriteFile(outside, []byte("unrelated bytes"), 0600))
				require.NoError(t, os.Symlink(outside, legacyPath))
			}
			g, err = Open(t.Context(), directory)
			require.NoError(t, err, "unused cumulative artifacts must not prevent reading a gallery")
			defer g.Close()
			require.Equal(t, legacyState, g.state)
			repeated, err := g.Prepare(t.Context(), red, 4)
			require.NoError(t, err)
			display, err := g.FontForTypography(t.Context(), repeated, source)
			require.NoError(t, err)
			require.True(t, display.Existing)
			require.Equal(t, firstFont.FontPath, display.FontPath)
			require.Equal(t, firstFont.PostScript, display.PostScript)
			require.Equal(t, firstFont.Text, display.Text)
			require.NoError(t, g.Commit(t.Context(), repeated))
			unchangedState, err := os.ReadFile(statePath)
			require.NoError(t, err)
			require.Equal(t, stateData, unchangedState, "opening or reusing a legacy gallery needs no migration")
			second, err := g.Prepare(t.Context(), fixture(color.NRGBA{B: 200, A: 255}), 4)
			require.NoError(t, err)
			secondFont, err := g.FontForTypography(t.Context(), second, source)
			require.NoError(t, err)
			secondData, err := os.ReadFile(secondFont.FontPath)
			require.NoError(t, err)
			parsed, err := sfnt.Parse(secondData)
			require.NoError(t, err)
			for _, character := range firstFont.Text + secondFont.Text {
				if character != '\n' {
					glyph, err := parsed.GlyphIndex(nil, character)
					require.NoError(t, err)
					require.NotZero(t, glyph, "legacy scrollback must retain its characters")
				}
			}
			resolved, err := g.LookupFontPS(t.Context(), firstFont.PostScript)
			require.NoError(t, err)
			require.Equal(t, firstFont.FontPath, resolved)
			unchangedFont, err := os.ReadFile(resolved)
			require.NoError(t, err)
			require.Equal(t, firstData, unchangedFont)
			switch artifact {
			case "retained":
				data, err := os.ReadFile(legacyPath)
				require.NoError(t, err)
				require.Equal(t, legacy.Data, data)
			case "missing":
				require.NoFileExists(t, legacyPath, "the unused artifact must not be rebuilt")
			case "symlink":
				data, err := os.ReadFile(outside)
				require.NoError(t, err)
				require.Equal(t, "unrelated bytes", string(data))
			}
		})
	}
}
