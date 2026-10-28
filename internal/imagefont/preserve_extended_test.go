package imagefont

import (
	"context"
	"image"
	"testing"

	"golang.org/x/image/font/sfnt"
)

func TestExtendedPreservedCharactersDoNotCreateLegacyCJKAlias(t *testing.T) {
	source := preservationSource(t)
	source.LegacyAliases = true
	character := FirstSupplementaryCodepoint + rune(MaxGlyphs)
	result, err := EncodePreserving(context.Background(), []Frame{{Image: image.NewNRGBA(image.Rect(0, 0, 1, 1)), Columns: 1, Rows: 1, CodepointStart: character}}, testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := sfnt.Parse(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if glyph, err := parsed.GlyphIndex(nil, character); err != nil || glyph == 0 {
		t.Fatalf("extended mapping missing: %v", err)
	}
	if glyph, err := parsed.GlyphIndex(nil, FirstCodepoint+rune(MaxGlyphs)); err != nil || glyph != 0 {
		t.Fatalf("extended mapping escaped into source text: %d, %v", glyph, err)
	}
}

func TestPreservedCharactersEndBeforePlane15Noncharacters(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	for _, character := range []rune{LastSupplementaryCodepoint, LastSupplementaryCodepoint + 1, LastSupplementaryCodepoint + 2} {
		_, err := preparePreservingFrames([]Frame{{Image: img, Columns: 1, Rows: 1, CodepointStart: character}}, 1, 2, 1)
		if (err == nil) != (character == LastSupplementaryCodepoint) {
			t.Fatalf("character %U: %v", character, err)
		}
	}
}
