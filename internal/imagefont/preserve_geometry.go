package imagefont

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"strings"
)

const (
	FirstSupplementaryCodepoint = '\U000f0000'
	LastSupplementaryCodepoint  = FirstSupplementaryCodepoint + rune(MaxGlyphs) - 1
)

// Preserved fonts use an exact strike for the selected point size. A 2× strike
// has two pixels per logical terminal point; the 4× strike has four. Thus tile
// boundaries and baseline offsets stay integral at every integer font size.
func preparePreservingFrames(frames []Frame, cellWidth, cellHeight, pointSize int) ([]preparedFrame, error) {
	if pointSize < 1 || pointSize > 1024 || cellWidth < 1 || cellWidth > 4096 || cellHeight < 1 || cellHeight > 4096 {
		return nil, fmt.Errorf("invalid preserved font point size or terminal cell geometry")
	}
	if len(frames) > MaxGlyphs {
		return nil, fmt.Errorf("a font supports at most %d image frames", MaxGlyphs)
	}
	var used [MaxGlyphs]bool
	prepared := make([]preparedFrame, 0, len(frames))
	next := FirstSupplementaryCodepoint
	var totalPixels int64
	for i, f := range frames {
		if f.Image == nil {
			return nil, fmt.Errorf("frame %d has no image", i+1)
		}
		bounds := f.Image.Bounds()
		w, h := bounds.Dx(), bounds.Dy()
		if w <= 0 || h <= 0 || w > 16384 || h > 16384 || int64(w)*int64(h) > 32*1024*1024 {
			return nil, fmt.Errorf("frame %d exceeds preview image dimensions", i+1)
		}
		if f.Columns == 0 {
			f.Columns = 32
		}
		if f.Columns < 1 || f.Columns > 64 || f.Rows < 0 || f.Rows > 32 {
			return nil, fmt.Errorf("frame %d requires 1 to 64 columns and 1 to 32 rows", i+1)
		}
		if f.Rows == 0 {
			f.Rows = min(32, max(1, int(math.Ceil(float64(f.Columns*cellWidth)*float64(h)/float64(cellHeight*w)))))
		}
		if f.CodepointStart == 0 {
			f.CodepointStart = next
		}
		count := f.Columns * f.Rows
		if f.CodepointStart < FirstSupplementaryCodepoint || f.CodepointStart > LastSupplementaryCodepoint || count > int(LastSupplementaryCodepoint-f.CodepointStart)+1 {
			return nil, fmt.Errorf("frame %d character range must fit in U+F0000 through U+F18FF", i+1)
		}
		width, height := f.Columns*cellWidth*2, f.Rows*cellHeight*2
		// Check the larger strike before allocating or reading source pixels.
		pixels := int64(width*2) * int64(height*2)
		totalPixels += pixels
		if width*2 > 16384 || height*2 > 16384 || pixels > 32*1024*1024 || totalPixels > 64*1024*1024 {
			return nil, fmt.Errorf("image previews exceed the bitmap budget at this font size; use fewer image cells")
		}
		var text strings.Builder
		text.Grow(count*4 + f.Rows)
		for glyph := 0; glyph < count; glyph++ {
			codepoint := f.CodepointStart + rune(glyph)
			if used[int(codepoint-FirstSupplementaryCodepoint)] {
				return nil, fmt.Errorf("frame %d overlaps another image's character range", i+1)
			}
			used[int(codepoint-FirstSupplementaryCodepoint)] = true
			text.WriteRune(codepoint)
			if (glyph+1)%f.Columns == 0 {
				text.WriteByte('\n')
			}
		}
		next = f.CodepointStart + rune(count)
		prepared = append(prepared, preparedFrame{image: f.Image, Preview: Preview{
			Text: text.String(), Columns: f.Columns, Rows: f.Rows,
			WidthPixels: width, HeightPixels: height, CodepointStart: f.CodepointStart,
		}})
	}
	return prepared, nil
}

func sbixPreserving(ctx context.Context, frames []preparedFrame, glyphCount, pointSize, baseline int) ([]byte, error) {
	if pointSize < 1 || pointSize > 1024 || baseline < -8191 || baseline > 8191 || glyphCount < 1 || glyphCount > 65535 {
		return nil, fmt.Errorf("invalid preserved bitmap font metrics")
	}
	var b buffer
	b.u16(1)
	b.u16(1)
	b.u32(2)
	strikes := make([][]byte, 2)
	offset := 16
	for index, scale := range []int{2, 4} {
		strike, err := encodePreservingStrike(ctx, frames, glyphCount, pointSize, baseline, scale)
		if err != nil {
			return nil, err
		}
		strikes[index] = strike
		b.u32(uint32(offset))
		offset += len(strike)
	}
	for _, strike := range strikes {
		b.Write(strike)
	}
	return b.Bytes(), nil
}

func encodePreservingStrike(ctx context.Context, frames []preparedFrame, glyphCount, pointSize, baseline, scale int) ([]byte, error) {
	var glyphs buffer
	encoder := png.Encoder{BufferPool: &pngBufferPool{}}
	offsets := make([]uint32, glyphCount+1)
	headerLength := 4 + 4*(glyphCount+1)
	for glyph := range offsets {
		offsets[glyph] = uint32(headerLength)
	}
	next := glyphCount
	if len(frames) > 0 {
		next = frames[0].firstGlyph
	}
	for _, frame := range frames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count := frame.Columns * frame.Rows
		if frame.firstGlyph != next || count > glyphCount-next {
			return nil, fmt.Errorf("preserved image glyphs must form a contiguous suffix")
		}
		tileWidth := frame.WidthPixels / frame.Columns * (scale / 2)
		tileHeight := frame.HeightPixels / frame.Rows * (scale / 2)
		img := fit(frame.image, frame.Columns*tileWidth, frame.Rows*tileHeight)
		for tile := 0; tile < count; tile++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			offsets[frame.firstGlyph+tile] = uint32(headerLength + glyphs.Len())
			// One bitmap spans the row. The remaining characters retain their
			// advances but have empty bitmap records. This removes internal
			// glyph edges without overlapping translucent image pixels.
			if tile%frame.Columns != 0 {
				continue
			}
			y := tile / frame.Columns * tileHeight
			glyphs.i16(0)
			glyphs.i16(int16(-baseline * scale))
			glyphs.WriteString("png ")
			if err := encoder.Encode(&glyphs, img.SubImage(image.Rect(0, y, frame.Columns*tileWidth, y+tileHeight))); err != nil {
				return nil, fmt.Errorf("encode preserved image row: %w", err)
			}
		}
		next += count
	}
	if next != glyphCount {
		return nil, fmt.Errorf("preserved image glyph count does not match font")
	}
	offsets[glyphCount] = uint32(headerLength + glyphs.Len())
	var b buffer
	b.u16(uint16(pointSize * scale))
	b.u16(72)
	for _, offset := range offsets {
		b.u32(offset)
	}
	b.Write(glyphs.Bytes())
	return b.Bytes(), nil
}
