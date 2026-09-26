package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"testing"

	"golang.org/x/image/draw"
)

func TestPreservingStrikesAtOriginalPointSizes(t *testing.T) {
	for _, pointSize := range []int{1, 12, 13, 14, 15, 18, 24, 48, 1024} {
		t.Run(strconv.Itoa(pointSize), func(t *testing.T) {
			const columns, rows, cellWidth, cellHeight, baseline, originalGlyphs = 3, 2, 9, 17, 4, 1700
			src := image.NewNRGBA(image.Rect(0, 0, columns*cellWidth*2, rows*cellHeight*2))
			for y := 0; y < src.Bounds().Dy(); y++ {
				for x := 0; x < src.Bounds().Dx(); x++ {
					alpha := [...]uint8{0, 64, 128, 255}[(x+y)%4]
					src.SetNRGBA(x, y, color.NRGBA{uint8(10 + x*3), uint8(20 + y*2), uint8(255 - x - y), alpha})
				}
			}
			frames, err := preparePreservingFrames([]Frame{{Image: src, Columns: columns, Rows: rows}}, cellWidth, cellHeight, pointSize)
			if err != nil {
				t.Fatal(err)
			}
			frames[0].firstGlyph = originalGlyphs
			glyphCount := originalGlyphs + columns*rows
			data, err := sbixPreserving(context.Background(), frames, glyphCount, pointSize, baseline)
			if err != nil {
				t.Fatal(err)
			}
			for strikeIndex, scale := range []int{2, 4} {
				offset := int(binary.BigEndian.Uint32(data[8+strikeIndex*4:]))
				strike := data[offset:]
				if got := int(binary.BigEndian.Uint16(strike)); got != pointSize*scale {
					t.Fatalf("ppem=%d want %d", got, pointSize*scale)
				}
				readOffset := func(glyph int) int { return int(binary.BigEndian.Uint32(strike[4+glyph*4:])) }
				for glyph := 0; glyph < originalGlyphs; glyph++ {
					if readOffset(glyph) != readOffset(glyph+1) {
						t.Fatalf("original text glyph %d gained a bitmap", glyph)
					}
				}
				expected := image.NewNRGBA(image.Rect(0, 0, columns*cellWidth*scale, rows*cellHeight*scale))
				draw.CatmullRom.Scale(expected, expected.Bounds(), src, src.Bounds(), draw.Src, nil)
				for tile := 0; tile < columns*rows; tile++ {
					glyph := originalGlyphs + tile
					payload := strike[readOffset(glyph):readOffset(glyph+1)]
					if tile%columns != 0 {
						if len(payload) != 0 {
							t.Fatalf("continuation glyph %d paints over the row image", glyph)
						}
						continue
					}
					if len(payload) < 8 || string(payload[4:8]) != "png " {
						t.Fatalf("row glyph %d lacks its PNG", glyph)
					}
					if int16(binary.BigEndian.Uint16(payload)) != 0 || int16(binary.BigEndian.Uint16(payload[2:])) != int16(-baseline*scale) {
						t.Fatalf("wrong row origin")
					}
					decoded, err := png.Decode(bytes.NewReader(payload[8:]))
					if err != nil {
						t.Fatal(err)
					}
					if decoded.Bounds().Dx() != columns*cellWidth*scale || decoded.Bounds().Dy() != cellHeight*scale {
						t.Fatal("row does not cover exactly its original cells")
					}
					for y := 0; y < cellHeight*scale; y++ {
						for x := 0; x < columns*cellWidth*scale; x++ {
							got := color.NRGBAModel.Convert(decoded.At(x, y))
							want := expected.NRGBAAt(x, (tile/columns)*cellHeight*scale+y)
							if got != want {
								t.Fatalf("row %d pixel(%d,%d) changed color or alpha", tile/columns, x, y)
							}
						}
					}
				}
			}
		})
	}
}

func TestPreservingGeometryUsesSupplementaryCharacters(t *testing.T) {
	frames, err := preparePreservingFrames([]Frame{{Image: image.NewNRGBA(image.Rect(0, 0, 27, 34)), Columns: 3, Rows: 2}}, 9, 17, 13)
	if err != nil {
		t.Fatal(err)
	}
	if frames[0].Text != "\U000f0000\U000f0001\U000f0002\n\U000f0003\U000f0004\U000f0005\n" {
		t.Fatalf("unexpected text %q", frames[0].Text)
	}
	if frames[0].WidthPixels != 54 || frames[0].HeightPixels != 68 {
		t.Fatal("incorrect 2x pixel dimensions")
	}
}

func TestPreservingGeometryRejectsUnboundedRasterBeforeRendering(t *testing.T) {
	frame := Frame{Image: image.NewNRGBA(image.Rect(0, 0, 1, 1)), Columns: 64, Rows: 32}
	if _, err := preparePreservingFrames([]Frame{frame}, 4096, 4096, 1024); err == nil {
		t.Fatal("accepted excessive raster dimensions")
	}
	frame.Columns, frame.Rows = 1, 1
	frame.CodepointStart = LastSupplementaryCodepoint
	if _, err := preparePreservingFrames([]Frame{frame, frame}, 8, 16, 16); err == nil {
		t.Fatal("accepted overlapping mappings")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prepared, err := preparePreservingFrames([]Frame{{Image: frame.Image, Columns: 1, Rows: 1}}, 8, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	prepared[0].firstGlyph = 20
	if _, err := sbixPreserving(ctx, prepared, 21, 16, 4); err == nil {
		t.Fatal("ignored cancellation")
	}
}
