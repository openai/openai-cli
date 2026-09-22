package imagegallery

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/openai/openai-cli/internal/imagefont"
	"golang.org/x/image/font/sfnt"
)

func typographySource(t *testing.T) imagefont.PreserveOptions {
	t.Helper()
	font, err := imagefont.Encode(t.Context(), nil, imagefont.Options{Family: "Synthetic Source", PostScript: "SyntheticSource"})
	if err != nil {
		t.Fatal(err)
	}
	tables := make(map[string][]byte)
	for offset := 12; offset < 12+16*int(binary.BigEndian.Uint16(font.Data[4:6])); offset += 16 {
		tag := string(font.Data[offset : offset+4])
		start := binary.BigEndian.Uint32(font.Data[offset+8:])
		length := binary.BigEndian.Uint32(font.Data[offset+12:])
		tables[tag] = bytes.Clone(font.Data[start : start+length])
	}
	delete(tables, "sbix")
	return imagefont.PreserveOptions{Tables: tables, SourcePostScript: "SyntheticSource", PointSize: 13, CellWidth: 8, CellHeight: 17, Baseline: 4}
}

func TestTypographyKeepsScrollbackAndReusesImmutableFonts(t *testing.T) {
	g := initialized(t)
	source := typographySource(t)
	red := fixture(t, t.TempDir(), "red.png", color.NRGBA{R: 200, A: 255})
	first, err := g.Prepare(t.Context(), red, 4)
	if err != nil {
		t.Fatal(err)
	}
	before := g.State()
	firstFont, err := g.FontForTypography(t.Context(), first, source)
	if err != nil {
		t.Fatal(err)
	}
	firstData, err := os.ReadFile(firstFont.FontPath)
	if err != nil {
		t.Fatal(err)
	}
	if firstFont.Existing || g.State() != before {
		t.Fatal("typography was already present or committed pending metadata")
	}
	repeated, err := g.FontForTypography(t.Context(), first, source)
	if err != nil || !repeated.Existing || repeated.FontPath != firstFont.FontPath || repeated.Text != firstFont.Text {
		t.Fatalf("typography identity was not stable: %+v, %v", repeated, err)
	}
	if err := g.Commit(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second, err := g.Prepare(t.Context(), fixture(t, t.TempDir(), "blue.png", color.NRGBA{B: 200, A: 255}), 4)
	if err != nil {
		t.Fatal(err)
	}
	secondFont, err := g.FontForTypography(t.Context(), second, source)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(secondFont.FontPath)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := sfnt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{firstFont.Text, secondFont.Text, "ordinary text"} {
		for _, character := range text {
			if character == '\n' {
				continue
			}
			glyph, err := parsed.GlyphIndex(nil, character)
			if err != nil || glyph == 0 {
				t.Fatalf("lost character U+%X: %v", character, err)
			}
		}
	}
	unchanged, err := os.ReadFile(firstFont.FontPath)
	if err != nil || !bytes.Equal(firstData, unchanged) || firstFont.PostScript == secondFont.PostScript {
		t.Fatal("new image modified an earlier font or reused its identity")
	}
	if err := g.Commit(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	duplicate, err := g.Prepare(t.Context(), red, 32)
	if err != nil {
		t.Fatal(err)
	}
	display, err := g.FontForTypography(t.Context(), duplicate, source)
	if err != nil || !display.Existing || display.FontPath != secondFont.FontPath || display.Text != firstFont.Text {
		t.Fatalf("repeated image lost its original text or cumulative font: %+v, %v", display, err)
	}
}

func TestTypographyRejectsStaleAndUnsafeRevisions(t *testing.T) {
	g := initialized(t)
	source := typographySource(t)
	revision, err := g.Initialize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.FontForTypography(t.Context(), nil, source); err == nil {
		t.Fatal("accepted nil revision")
	}
	other := initialized(t)
	foreign, err := other.Initialize(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.FontForTypography(t.Context(), foreign, source); err == nil {
		t.Fatal("accepted another gallery's revision")
	}
	display, err := g.FontForTypography(t.Context(), revision, source)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		outside := filepath.Join(t.TempDir(), "outside.ttf")
		if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(display.FontPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, display.FontPath); err != nil {
			t.Fatal(err)
		}
		if _, err := g.FontForTypography(t.Context(), revision, source); err == nil {
			t.Fatal("accepted a symlink as a cached font")
		}
		data, err := os.ReadFile(outside)
		if err != nil || string(data) != "unchanged" {
			t.Fatal("modified external font")
		}
	}
	if err := g.Commit(t.Context(), revision); err != nil {
		t.Fatal(err)
	}
	if _, err := g.FontForTypography(t.Context(), revision, source); err == nil {
		t.Fatal("accepted a consumed revision")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := g.FontForTypography(canceled, revision, source); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
