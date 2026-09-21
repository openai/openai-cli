package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/png"
	"math"
	"testing"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	fontsfnt "golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

func TestDefaultFontGeometryBytes(t *testing.T) {
	frames := []Frame{{Image: solid(image.Rect(0, 0, 64, 32), color.NRGBA{R: 19, G: 147, B: 225, A: 128}), Columns: 4, Rows: 1}}
	result, err := Encode(context.Background(), frames, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	// PNG compression can change between supported Go toolchains. Cache
	// compatibility depends on the character grid, metrics and decoded pixels,
	// not on one toolchain's compressed representation of the embedded PNGs.
	wantPreview := Preview{
		Text: "\ue000\ue001\ue002\ue003\n", Columns: 4, Rows: 1,
		WidthPixels: 64, HeightPixels: 32, CodepointStart: '\ue000',
	}
	if len(result.Previews) != 1 || result.Previews[0] != wantPreview {
		t.Fatalf("default preview geometry = %+v, want %+v", result.Previews, wantPreview)
	}
	tables := fontTables(t, result.Data)
	parsed, err := fontsfnt.Parse(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	var buffer fontsfnt.Buffer
	for _, size := range []int{16, 32} {
		metrics, err := parsed.Metrics(&buffer, fixed.I(size), font.HintingNone)
		if err != nil || metrics.Ascent != fixed.I(size*3/4) || metrics.Descent != fixed.I(size/4) || metrics.Height != fixed.I(size) {
			t.Fatalf("default %dpt metrics = %+v, %v", size, metrics, err)
		}
		for r, want := range map[rune]fontsfnt.GlyphIndex{'A': 34, '\ue000': 96, '\ue001': 97, '\ue002': 98, '\ue003': 99} {
			glyph, err := parsed.GlyphIndex(&buffer, r)
			if err != nil || glyph != want {
				t.Fatalf("default character %U maps to glyph %d, want %d: %v", r, glyph, want, err)
			}
			advance, err := parsed.GlyphAdvance(&buffer, glyph, fixed.I(size), font.HintingNone)
			if err != nil || advance != fixed.I(size/2) {
				t.Fatalf("default %dpt character %U advance = %v, %v", size, r, advance, err)
			}
		}
	}
	for i, strike := range readStrikes(t, tables["sbix"], 100) {
		scale := i + 1
		stitched := stitchGeometry(t, strike, 4, 1, 16*scale, 32*scale, 32*scale)
		want := solid(image.Rect(0, 0, 64*scale, 32*scale), color.NRGBA{R: 19, G: 147, B: 225, A: 128})
		if !bytes.Equal(stitched.Pix, want.Pix) {
			t.Fatalf("default %dppem image pixels or alpha changed", 32*scale)
		}
	}
	options := testOptions
	options.TileWidth, options.TileHeight = 16, 32
	explicit, err := Encode(context.Background(), frames, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Data, explicit.Data) || result.Previews[0] != explicit.Previews[0] {
		t.Fatal("explicit default geometry differs from implicit defaults")
	}
}

func TestEncodeAdaptiveTilesStitchAcrossBothStrikes(t *testing.T) {
	for _, test := range []struct {
		name          string
		width, height int
	}{
		{"wider", 24, 32},
		{"narrower", 8, 32},
		{"taller", 16, 48},
		{"shorter", 16, 16},
		{"odd dimensions", 19, 27},
	} {
		t.Run(test.name, func(t *testing.T) {
			const columns, rows = 3, 2
			width, height := columns*test.width, rows*test.height
			src := image.NewNRGBA(image.Rect(7, 9, 7+width, 9+height))
			for y := 0; y < height; y++ {
				for x := 0; x < width; x++ {
					src.SetNRGBA(7+x, 9+y, color.NRGBA{R: uint8(x*17 + y*3), G: uint8(x*5 + y*11), B: uint8(x + y*7), A: 255})
				}
			}
			options := testOptions
			options.TileWidth, options.TileHeight = test.width, test.height
			result, err := Encode(context.Background(), []Frame{{Image: src, Columns: columns, Rows: rows}}, options)
			if err != nil {
				t.Fatal(err)
			}
			preview := result.Previews[0]
			if preview.WidthPixels != width || preview.HeightPixels != height || preview.Columns != columns || preview.Rows != rows {
				t.Fatalf("preview geometry = %+v", preview)
			}
			strikes := readStrikes(t, fontTables(t, result.Data)["sbix"], baseGlyphCount+columns*rows)
			for i, strike := range strikes {
				scale := i + 1
				stitched := stitchGeometry(t, strike, columns, rows, test.width*scale, test.height*scale, 32*scale)
				want := image.NewNRGBA(stitched.Bounds())
				if scale == 1 {
					stddraw.Draw(want, want.Bounds(), src, src.Bounds().Min, stddraw.Src)
				} else {
					// A single continuous resize provides the reference for every
					// decoded tile, including both sides of all glyph boundaries.
					draw.CatmullRom.Scale(want, want.Bounds(), src, src.Bounds(), draw.Src, nil)
				}
				if !bytes.Equal(stitched.Pix, want.Pix) {
					t.Fatalf("%dppem reconstructed image has gaps, overlap, or changed pixels", 32*scale)
				}
			}
		})
	}
}

func TestEncodeAdaptiveGeometryPreservesFixedGridAndTextMetrics(t *testing.T) {
	frame := Frame{Image: solid(image.Rect(0, 0, 32, 32), color.NRGBA{G: 220, A: 255}), Columns: 4, Rows: 2, CodepointStart: '\ue010'}
	baseline, err := Encode(context.Background(), []Frame{frame}, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	baselineTables := fontTables(t, baseline.Data)
	clear(baselineTables["head"][8:12])
	for _, dimensions := range [][2]int{{8, 16}, {24, 48}, {24, 16}, {8, 48}} {
		options := testOptions
		options.TileWidth, options.TileHeight = dimensions[0], dimensions[1]
		result, err := Encode(context.Background(), []Frame{frame}, options)
		if err != nil {
			t.Fatal(err)
		}
		before, after := baseline.Previews[0], result.Previews[0]
		if before.Text != after.Text || before.Columns != after.Columns || before.Rows != after.Rows || before.CodepointStart != after.CodepointStart {
			t.Fatalf("geometry %v moved the existing character grid: %+v", dimensions, after)
		}
		tables := fontTables(t, result.Data)
		clear(tables["head"][8:12]) // The whole-font checksum necessarily changes.
		for tag, want := range baselineTables {
			if tag != "sbix" && !bytes.Equal(tables[tag], want) {
				t.Errorf("geometry %v changed %s; only bitmap pixels should change", dimensions, tag)
			}
		}
	}
}

func TestAdaptiveGeometryAutomaticRows(t *testing.T) {
	for _, test := range []struct {
		name                          string
		width, height, imageW, imageH int
		wantRows                      int
	}{
		{"default", 0, 0, 100, 100, 16},
		{"wider", 24, 32, 100, 100, 24},
		{"narrower", 8, 32, 100, 100, 8},
		{"taller rounds up", 16, 48, 100, 100, 11},
		{"shorter", 16, 16, 100, 100, 32},
		{"landscape", 24, 32, 100, 50, 12},
		{"upper bound", 24, 16, 10, 100, 32},
		{"lower bound", 8, 48, 100, 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames, err := prepareGeometry([]Frame{{Image: dimensionOnly{image.Rect(0, 0, test.imageW, test.imageH)}}}, test.width, test.height)
			if err != nil {
				t.Fatal(err)
			}
			if frames[0].Columns != 32 || frames[0].Rows != test.wantRows {
				t.Fatalf("automatic grid = %dx%d, want 32x%d", frames[0].Columns, frames[0].Rows, test.wantRows)
			}
		})
	}
}

func TestAdaptiveGeometryPreservesAspectAndTransparentPadding(t *testing.T) {
	for _, test := range []struct {
		name                          string
		width, height, imageW, imageH int
	}{
		{"wide cells and portrait", 24, 16, 16, 32},
		{"tall cells and landscape", 8, 48, 32, 16},
		{"fractional fit", 19, 27, 23, 37},
	} {
		t.Run(test.name, func(t *testing.T) {
			const columns, rows = 4, 2
			options := testOptions
			options.TileWidth, options.TileHeight = test.width, test.height
			src := solid(image.Rect(0, 0, test.imageW, test.imageH), color.NRGBA{R: 220, B: 30, A: 128})
			result, err := Encode(context.Background(), []Frame{{Image: src, Columns: columns, Rows: rows}}, options)
			if err != nil {
				t.Fatal(err)
			}
			strikes := readStrikes(t, fontTables(t, result.Data)["sbix"], baseGlyphCount+columns*rows)
			for i, strike := range strikes {
				multiplier := i + 1
				stitched := stitchGeometry(t, strike, columns, rows, test.width*multiplier, test.height*multiplier, 32*multiplier)
				w, h := stitched.Bounds().Dx(), stitched.Bounds().Dy()
				scale := min(float64(w)/float64(test.imageW), float64(h)/float64(test.imageH))
				imageW, imageH := int(math.Round(float64(test.imageW)*scale)), int(math.Round(float64(test.imageH)*scale))
				imageRect := image.Rect((w-imageW)/2, (h-imageH)/2, (w-imageW)/2+imageW, (h-imageH)/2+imageH)
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						wantAlpha := uint8(0)
						if image.Pt(x, y).In(imageRect) {
							wantAlpha = 128
						}
						if got := stitched.NRGBAAt(x, y).A; got != wantAlpha {
							t.Fatalf("%dppem alpha at (%d,%d) = %d, want %d; image bounds %v", 32*multiplier, x, y, got, wantAlpha, imageRect)
						}
					}
				}
			}
		})
	}
}

func TestEncodeRejectsInvalidTileGeometryBeforeReadingImage(t *testing.T) {
	for _, dimensions := range [][2]int{{0, 32}, {16, 0}, {-1, 32}, {16, -1}, {7, 32}, {25, 32}, {16, 15}, {16, 49}, {math.MaxInt, math.MaxInt}} {
		t.Run(fmt.Sprintf("%dx%d", dimensions[0], dimensions[1]), func(t *testing.T) {
			options := testOptions
			options.TileWidth, options.TileHeight = dimensions[0], dimensions[1]
			frames := []Frame{{Image: dimensionOnly{image.Rect(0, 0, 2, 2)}}}
			if result, err := Encode(context.Background(), frames, options); err == nil || len(result.Data) != 0 {
				t.Fatal("invalid tile geometry produced a font")
			}
			if result, err := Encode(context.Background(), nil, options); err == nil || len(result.Data) != 0 {
				t.Fatal("invalid tile geometry accepted for the base font")
			}
		})
	}
}

func stitchGeometry(t *testing.T, strike [][]byte, columns, rows, tileWidth, tileHeight, ppem int) *image.NRGBA {
	t.Helper()
	stitched := image.NewNRGBA(image.Rect(0, 0, columns*tileWidth, rows*tileHeight))
	for i := 0; i < columns*rows; i++ {
		data := strike[baseGlyphCount+i]
		if len(data) < 8 || string(data[4:8]) != "png " || int16(binary.BigEndian.Uint16(data[:2])) != 0 || int16(binary.BigEndian.Uint16(data[2:4])) != int16(-ppem/4) {
			t.Fatalf("invalid %dppem tile header %d", ppem, i)
		}
		tile, err := png.Decode(bytes.NewReader(data[8:]))
		if err != nil {
			t.Fatal(err)
		}
		if tile.Bounds() != image.Rect(0, 0, tileWidth, tileHeight) {
			t.Fatalf("%dppem tile %d dimensions = %v, want %dx%d", ppem, i, tile.Bounds(), tileWidth, tileHeight)
		}
		x, y := i%columns*tileWidth, i/columns*tileHeight
		stddraw.Draw(stitched, image.Rect(x, y, x+tileWidth, y+tileHeight), tile, image.Point{}, stddraw.Src)
	}
	return stitched
}
