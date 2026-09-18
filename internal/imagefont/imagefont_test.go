package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"

	"golang.org/x/image/font"
	fontsfnt "golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

var testOptions = Options{Family: "OpenAI Image Test", PostScript: "OpenAIImageTest-Regular"}

func solid(bounds image.Rectangle, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestEncodeProducesValidFontAndExactLineMetrics(t *testing.T) {
	result, err := Encode(context.Background(), []Frame{{Image: solid(image.Rect(0, 0, 64, 64), color.NRGBA{R: 240, A: 255}), Columns: 4, Rows: 2}}, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	tables := fontTables(t, result.Data)
	for tag, want := range map[string]int{"head": 54, "hhea": 36, "maxp": 32, "OS/2": 96, "hmtx": (baseGlyphCount + 8) * 4, "post": 32, "loca": (baseGlyphCount + 9) * 4} {
		if got := len(tables[tag]); got != want {
			t.Errorf("%s length = %d, want %d", tag, got, want)
		}
	}
	parsed, err := fontsfnt.Parse(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	var buffer fontsfnt.Buffer
	for _, size := range []int{16, 32} {
		metrics, err := parsed.Metrics(&buffer, fixed.I(size), font.HintingNone)
		if err != nil {
			t.Fatal(err)
		}
		if metrics.Ascent != fixed.I(size*3/4) || metrics.Descent != fixed.I(size/4) || metrics.Height != fixed.I(size) {
			t.Errorf("size %d metrics = %+v", size, metrics)
		}
		if got := math.Ceil(float64(metrics.Ascent)/64) + math.Ceil(float64(metrics.Descent)/64); got != float64(size) {
			t.Errorf("size %d rounded line pitch = %v", size, got)
		}
	}
	for _, field := range []struct {
		table  string
		offset int
		want   int16
	}{{"head", 38, -250}, {"head", 42, 750}, {"hhea", 4, 750}, {"hhea", 6, -250}, {"hhea", 8, 0},
		{"OS/2", 68, 750}, {"OS/2", 70, -250}, {"OS/2", 72, 0}} {
		if got := int16(binary.BigEndian.Uint16(tables[field.table][field.offset:])); got != field.want {
			t.Errorf("%s field %d = %d, want %d", field.table, field.offset, got, field.want)
		}
	}
}

func TestEncodeBaseFontContainsReadableMonospaceASCII(t *testing.T) {
	result, err := Encode(context.Background(), nil, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Previews) != 0 {
		t.Fatal("base font unexpectedly contains image previews")
	}
	fontTables(t, result.Data)
	parsed, err := fontsfnt.Parse(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	var buffer fontsfnt.Buffer
	for r := rune(32); r <= 126; r++ {
		glyph, err := parsed.GlyphIndex(&buffer, r)
		if err != nil || int(glyph) != int(r)-31 {
			t.Fatalf("ASCII %q glyph = %d, %v", r, glyph, err)
		}
		advance, err := parsed.GlyphAdvance(&buffer, glyph, fixed.I(32), font.HintingNone)
		if err != nil || advance != fixed.I(16) {
			t.Fatalf("ASCII %q advance = %v, %v", r, advance, err)
		}
		segments, err := parsed.LoadGlyph(&buffer, glyph, fixed.I(32), nil)
		if err != nil || (r != ' ' && len(segments) == 0) {
			t.Fatalf("ASCII %q has no valid outline: %v", r, err)
		}
	}
}

func TestEncodeBitmapPixelsAndRetinaStrike(t *testing.T) {
	src := image.NewNRGBA(image.Rect(7, 9, 71, 73))
	for y := 9; y < 73; y++ {
		for x := 7; x < 71; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8((x - 7) * 4), G: uint8((y - 9) * 4), B: 53, A: 255})
		}
	}
	constant := color.NRGBA{R: 19, G: 147, B: 225, A: 255}
	result, err := Encode(context.Background(), []Frame{
		{Image: src, Columns: 4, Rows: 2},
		{Image: solid(image.Rect(0, 0, 16, 32), constant), Columns: 1, Rows: 1},
	}, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	tables := fontTables(t, result.Data)
	strikes := readStrikes(t, tables["sbix"], baseGlyphCount+9)
	for strikeIndex, strike := range strikes {
		ppem := 32 * (strikeIndex + 1)
		for glyph := 0; glyph < baseGlyphCount+9; glyph++ {
			data := strike[glyph]
			if glyph < baseGlyphCount {
				if len(data) != 0 {
					t.Fatal("blank and missing glyphs must not draw pixels")
				}
				continue
			}
			if len(data) < 8 || string(data[4:8]) != "png " || int16(binary.BigEndian.Uint16(data[:2])) != 0 || int16(binary.BigEndian.Uint16(data[2:4])) != int16(-ppem/4) {
				t.Fatalf("invalid sbix tile header for strike %d glyph %d", ppem, glyph)
			}
			tile, err := png.Decode(bytes.NewReader(data[8:]))
			if err != nil {
				t.Fatal(err)
			}
			if tile.Bounds() != image.Rect(0, 0, ppem/2, ppem) {
				t.Fatalf("glyph %d dimensions = %v", glyph, tile.Bounds())
			}
			for y := 0; y < ppem; y++ {
				for x := 0; x < ppem/2; x++ {
					if glyph == baseGlyphCount+8 {
						assertPixel(t, tile.At(x, y), constant)
					} else if strikeIndex == 0 {
						tx, ty := (glyph-baseGlyphCount)%4*16, (glyph-baseGlyphCount)/4*32
						assertPixel(t, tile.At(x, y), src.At(7+tx+x, 9+ty+y))
					}
				}
			}
		}
	}
}

func TestEncodeCharacterRangesAndImmutableFrames(t *testing.T) {
	first := Frame{Image: solid(image.Rect(0, 0, 32, 32), color.NRGBA{R: 240, A: 255}), Columns: 2, Rows: 1, CodepointStart: '\ue010'}
	second := Frame{Image: solid(image.Rect(0, 0, 32, 32), color.NRGBA{B: 240, A: 255}), Columns: 2, Rows: 1, CodepointStart: FirstCodepoint}
	before, err := Encode(context.Background(), []Frame{first}, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Encode(context.Background(), []Frame{first, second}, Options{Family: "OpenAI Image Test Revision", PostScript: "OpenAIImageTestRevision-Regular"})
	if err != nil {
		t.Fatal(err)
	}
	if before.Previews[0] != after.Previews[0] || after.Previews[0].Text != "\ue010\ue011\n" || after.Previews[1].Text != "\ue000\ue001\n" {
		t.Fatalf("changed frame text: %+v", after.Previews)
	}
	parsed, err := fontsfnt.Parse(after.Data)
	if err != nil {
		t.Fatal(err)
	}
	for r, want := range map[rune]fontsfnt.GlyphIndex{' ': 1, '\ue010': baseGlyphCount, '\ue011': baseGlyphCount + 1, '\ue000': baseGlyphCount + 2, '\ue001': baseGlyphCount + 3, '\ue002': 0, 'A': 34} {
		got, err := parsed.GlyphIndex(nil, r)
		if err != nil || got != want {
			t.Errorf("U+%04X glyph = %d, %v; want %d", r, got, err, want)
		}
	}
	oldStrikes := readStrikes(t, fontTables(t, before.Data)["sbix"], baseGlyphCount+2)
	newStrikes := readStrikes(t, fontTables(t, after.Data)["sbix"], baseGlyphCount+4)
	for i := range oldStrikes {
		for glyph := 0; glyph < baseGlyphCount+2; glyph++ {
			if !bytes.Equal(oldStrikes[i][glyph], newStrikes[i][glyph]) {
				t.Errorf("existing tile changed after appending frame: strike %d glyph %d", i, glyph)
			}
		}
	}
}

func TestEncodePreservesTransparencyAndFitsAspectRatio(t *testing.T) {
	transparent := color.NRGBA{R: 220, G: 20, B: 30, A: 128}
	result, err := Encode(context.Background(), []Frame{{Image: solid(image.Rect(0, 0, 32, 16), transparent), Columns: 2, Rows: 2}}, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	strikes := readStrikes(t, fontTables(t, result.Data)["sbix"], baseGlyphCount+4)
	for i, strike := range strikes {
		ppem := 32 * (i + 1)
		for glyph := baseGlyphCount; glyph < baseGlyphCount+4; glyph++ {
			tile, err := png.Decode(bytes.NewReader(strike[glyph][8:]))
			if err != nil {
				t.Fatal(err)
			}
			for y := 0; y < ppem; y++ {
				absoluteY := (glyph-baseGlyphCount)/2*ppem + y
				for x := 0; x < ppem/2; x++ {
					_, _, _, alpha := tile.At(x, y).RGBA()
					if absoluteY < 3*ppem/4 || absoluteY >= 5*ppem/4 {
						if alpha != 0 {
							t.Fatalf("padding is not transparent: strike %d row %d", ppem, absoluteY)
						}
					} else if alpha != 128*257 {
						t.Fatalf("image alpha changed: %d", alpha)
					}
				}
			}
		}
	}
	prepared, err := prepare([]Frame{{Image: solid(image.Rect(0, 0, 100, 50), transparent)}})
	if err != nil || prepared[0].Columns != 32 || prepared[0].Rows != 8 {
		t.Fatalf("automatic landscape dimensions = %+v, %v", prepared, err)
	}
}

func TestEncodeRejectsInvalidInput(t *testing.T) {
	valid := Frame{Image: solid(image.Rect(0, 0, 2, 2), color.NRGBA{A: 255}), Columns: 1, Rows: 1}
	for _, test := range []struct {
		name    string
		frames  []Frame
		options Options
	}{
		{"nil image", []Frame{{}}, testOptions},
		{"empty image", []Frame{{Image: image.NewNRGBA(image.Rectangle{})}}, testOptions},
		{"large image", []Frame{{Image: dimensionOnly{image.Rect(0, 0, 16385, 2)}}}, testOptions},
		{"pixel limit", []Frame{{Image: dimensionOnly{image.Rect(0, 0, 8192, 8192)}}}, testOptions},
		{"negative columns", []Frame{{Image: valid.Image, Columns: -1}}, testOptions},
		{"many columns", []Frame{{Image: valid.Image, Columns: 65}}, testOptions},
		{"many rows", []Frame{{Image: valid.Image, Rows: 33}}, testOptions},
		{"negative rows", []Frame{{Image: valid.Image, Rows: -1}}, testOptions},
		{"non private codepoint", []Frame{{Image: valid.Image, CodepointStart: 'A'}}, testOptions},
		{"overflow range", []Frame{{Image: valid.Image, CodepointStart: LastCodepoint}}, testOptions},
		{"duplicate range", []Frame{valid, {Image: valid.Image, Columns: 1, Rows: 1, CodepointStart: FirstCodepoint}}, testOptions},
		{"missing family", []Frame{valid}, Options{PostScript: "Test"}},
		{"control in family", []Frame{valid}, Options{Family: "Test\x1b[2J", PostScript: "Test"}},
		{"long family", []Frame{valid}, Options{Family: strings.Repeat("a", 101), PostScript: "Test"}},
		{"missing postscript", []Frame{valid}, Options{Family: "Test"}},
		{"bad postscript", []Frame{valid}, Options{Family: "Test", PostScript: "Test/Name"}},
		{"long postscript", []Frame{valid}, Options{Family: "Test", PostScript: strings.Repeat("a", 64)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if result, err := Encode(context.Background(), test.frames, test.options); err == nil || len(result.Data) != 0 {
				t.Fatal("invalid input produced a font")
			}
		})
	}
}

func TestEncodeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Encode(ctx, nil, testOptions); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled encode = %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	src := cancelImage{Image: solid(image.Rect(0, 0, 64, 64), color.NRGBA{A: 255}), cancel: cancel}
	result, err := Encode(ctx, []Frame{{Image: src, Columns: 2, Rows: 1}}, testOptions)
	if !errors.Is(err, context.Canceled) || len(result.Data) != 0 {
		t.Fatalf("canceled during encoding = %v, %d bytes", err, len(result.Data))
	}
}

func TestCharacterRangeLimits(t *testing.T) {
	img := solid(image.Rect(0, 0, 1, 1), color.NRGBA{A: 255})
	frames := []Frame{
		{Image: img, Columns: 64, Rows: 32},
		{Image: img, Columns: 64, Rows: 32},
		{Image: img, Columns: 64, Rows: 32},
		{Image: img, Columns: 16, Rows: 16},
	}
	prepared, err := prepare(frames)
	if err != nil {
		t.Fatal(err)
	}
	last := prepared[len(prepared)-1]
	if last.CodepointStart+rune(last.Columns*last.Rows)-1 != LastCodepoint {
		t.Fatal("private character allocation ended at the wrong boundary")
	}
	frames = append(frames, Frame{Image: img, Columns: 1, Rows: 1})
	if _, err := prepare(frames); err == nil {
		t.Fatal("allocation passed the private character range")
	}
	if _, err := prepare([]Frame{{Image: img, Columns: 1, Rows: 1, CodepointStart: LastCodepoint}}); err != nil {
		t.Fatalf("last private character must be available: %v", err)
	}
}

type dimensionOnly struct{ rectangle image.Rectangle }

func (i dimensionOnly) Bounds() image.Rectangle { return i.rectangle }
func (dimensionOnly) ColorModel() color.Model   { return color.NRGBAModel }
func (dimensionOnly) At(int, int) color.Color {
	panic("invalid dimensions must be rejected before reading pixels")
}

type cancelImage struct {
	image.Image
	cancel context.CancelFunc
}

func (i cancelImage) At(x, y int) color.Color {
	i.cancel()
	return i.Image.At(x, y)
}

func fontTables(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	if len(data) < 12 || checksum(data) != 0xb1b0afba {
		t.Fatal("invalid font checksum or header")
	}
	count := int(binary.BigEndian.Uint16(data[4:6]))
	if len(data) < 12+count*16 {
		t.Fatal("truncated table directory")
	}
	tables := make(map[string][]byte, count)
	end := uint32(12 + count*16)
	lastTag := ""
	for i := 0; i < count; i++ {
		record := data[12+i*16 : 28+i*16]
		tag := string(record[:4])
		wantChecksum := binary.BigEndian.Uint32(record[4:8])
		offset, length := binary.BigEndian.Uint32(record[8:12]), binary.BigEndian.Uint32(record[12:16])
		if tag <= lastTag || offset < end || offset%4 != 0 || uint64(offset)+uint64(length) > uint64(len(data)) {
			t.Fatalf("invalid table record %q", tag)
		}
		lastTag, end = tag, offset+length
		table := append([]byte(nil), data[offset:offset+length]...)
		tables[tag] = table
		checked := append([]byte(nil), table...)
		if tag == "head" {
			clear(checked[8:12])
		}
		if checksum(checked) != wantChecksum {
			t.Fatalf("%s checksum mismatch", tag)
		}
	}
	return tables
}

func readStrikes(t *testing.T, bitmap []byte, glyphCount int) [][][]byte {
	t.Helper()
	if len(bitmap) < 16 || binary.BigEndian.Uint16(bitmap[:2]) != 1 || binary.BigEndian.Uint32(bitmap[4:8]) != 2 {
		t.Fatal("invalid bitmap header")
	}
	strikes := make([][][]byte, 2)
	for i := range strikes {
		start := int(binary.BigEndian.Uint32(bitmap[8+4*i : 12+4*i]))
		end := len(bitmap)
		if i == 0 {
			end = int(binary.BigEndian.Uint32(bitmap[12:16]))
		}
		if start < 16 || end < start || end > len(bitmap) || end-start < 4+4*(glyphCount+1) {
			t.Fatal("invalid bitmap strike offsets")
		}
		strike := bitmap[start:end]
		if binary.BigEndian.Uint16(strike[:2]) != uint16(32*(i+1)) || binary.BigEndian.Uint16(strike[2:4]) != 72 {
			t.Fatal("invalid strike size or resolution")
		}
		strikes[i] = make([][]byte, glyphCount)
		for glyph := range strikes[i] {
			start := int(binary.BigEndian.Uint32(strike[4+glyph*4 : 8+glyph*4]))
			end := int(binary.BigEndian.Uint32(strike[8+glyph*4 : 12+glyph*4]))
			if start < 4+4*(glyphCount+1) || end < start || end > len(strike) {
				t.Fatal("invalid bitmap glyph offsets")
			}
			strikes[i][glyph] = strike[start:end]
		}
	}
	return strikes
}

func assertPixel(t *testing.T, got, want color.Color) {
	t.Helper()
	r, g, b, a := got.RGBA()
	wr, wg, wb, wa := want.RGBA()
	if r != wr || g != wg || b != wb || a != wa {
		t.Fatalf("pixel = (%d,%d,%d,%d), want (%d,%d,%d,%d)", r, g, b, a, wr, wg, wb, wa)
	}
}
