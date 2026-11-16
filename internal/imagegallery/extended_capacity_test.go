package imagegallery

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/imagefont"
	"golang.org/x/image/font/sfnt"
)

func TestPreservedGalleryExtendsLegacyCapacityWithoutReplacingEarlierPreviews(t *testing.T) {
	ctx := context.Background()
	g := initialized(t)
	source := typographySource(t)
	source.PointSize, source.CellWidth, source.CellHeight, source.Baseline = 1, 1, 2, 0
	var oldFont TypographyFont
	for i := 0; i < 12; i++ {
		revision, err := g.Prepare(ctx, fixture(color.NRGBA{R: uint8(i), A: 255}), 32)
		if err != nil {
			t.Fatal(err)
		}
		if i == 11 {
			oldFont, err = g.FontForTypography(ctx, revision, source)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err = g.Commit(ctx, revision); err != nil {
			t.Fatal(err)
		}

	}
	// Cache the earlier immutable font before preparing an allocation past 6400 cells.
	oldBytes, err := os.ReadFile(oldFont.FontPath)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(g.directory + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	img := fixture(color.NRGBA{R: 13, A: 255})
	if _, err = g.Prepare(ctx, img, 32); !errors.Is(err, ErrFull) {
		t.Fatalf("legacy capacity: %v", err)
	}
	revision, err := g.PreparePreservingWithLimit(ctx, img, 32, 13*512)
	if err != nil {
		t.Fatal(err)
	}
	if g.state.Version != 1 || revision.state.Version != 2 {
		t.Fatal("migration did not remain pending")
	}
	font, err := g.FontForTypography(ctx, revision, source)
	if err != nil {
		t.Fatal(err)
	}
	currentState, _ := os.ReadFile(g.directory + "/state.json")
	if !bytes.Equal(currentState, stateBefore) {
		t.Fatal("font preparation committed the migrated state")
	}
	if err = g.Commit(ctx, revision); err != nil {
		t.Fatal(err)
	}
	if g.state.Version != 2 || len(g.state.Images) != 13 {
		t.Fatal("missing committed migration")
	}
	currentOldFont, _ := os.ReadFile(oldFont.FontPath)
	if !bytes.Equal(currentOldFont, oldBytes) {
		t.Fatal("earlier immutable font changed")
	}
	parsedOld, err := sfnt.Parse(oldBytes)
	if err != nil {
		t.Fatal(err)
	}
	newBytes, err := os.ReadFile(font.FontPath)
	if err != nil {
		t.Fatal(err)
	}
	parsedNew, err := sfnt.Parse(newBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []rune{'W', imagefont.FirstSupplementaryCodepoint, imagefont.FirstSupplementaryCodepoint + 6143} {
		before, e1 := parsedOld.GlyphIndex(nil, r)
		after, e2 := parsedNew.GlyphIndex(nil, r)
		if e1 != nil || e2 != nil || before == 0 || before != after {
			t.Fatalf("earlier mapping changed for %U: %d -> %d", r, before, after)
		}
	}
	last := imagefont.FirstSupplementaryCodepoint + 13*512 - 1
	if glyph, err := parsedNew.GlyphIndex(nil, last); err != nil || glyph == 0 {
		t.Fatalf("new supplementary mapping %U missing: %v", last, err)
	}
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, g.directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.state.Version != 2 || len(reopened.state.Images) != 13 {
		t.Fatal("extended gallery did not reopen")
	}
	for i, item := range reopened.state.Images {
		if item.Start != imagefont.FirstCodepoint+rune(i*512) {
			t.Fatal("stored allocation shifted")
		}
	}
}
