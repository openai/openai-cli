// Package imagefont builds bitmap fonts for terminal image rendering.
// It does not install fonts, change terminal preferences, or write files.
package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"math"
	"sort"
	"strings"

	"golang.org/x/image/draw"
)

const (
	FirstCodepoint = '\ue000'
	LastCodepoint  = '\uf8ff'
	MaxGlyphs      = int(LastCodepoint-FirstCodepoint) + 1
)

// Frame supplies one image and its immutable character range. Reusing its
// range for a different image would change the appearance of existing text.
type Frame struct {
	Image          image.Image
	Columns, Rows  int  // Zero selects 32 columns and an aspect-ratio-derived row count.
	CodepointStart rune // Zero allocates after the preceding frame, starting at U+E000.
}

// Options identifies this font and its bitmap cell geometry. Callers must choose
// a unique identity whenever the contents change because platform font caches
// can retain older glyphs.
type Options struct {
	Family, PostScript string
	// TileWidth and TileHeight are bitmap dimensions at 32ppem. Both zero selects
	// 16×32. Otherwise width must be 8..24 and height 16..48. These adapt image
	// tiles to terminal cell spacing without changing the font's text metrics.
	TileWidth, TileHeight int
}

type Preview struct {
	Text                      string
	Columns, Rows             int
	WidthPixels, HeightPixels int // Dimensions of the 32ppem strike; the other strike doubles both.
	CodepointStart            rune
}

type Font struct {
	Data     []byte
	Previews []Preview // In the same order as the input frames.
}

type preparedFrame struct {
	image image.Image
	Preview
	firstGlyph int
}

// Encode creates a TrueType font containing lossless PNG glyphs at 32 and
// 64ppem. Tiles default to 16×32 pixels at 32ppem. The 750/-250 ascent/descent
// divide evenly at 16pt and 32pt. Custom tile geometry affects only image pixels;
// text advances and line metrics remain fixed to avoid changing the cell layout.
// Image fitting preserves aspect ratio and alpha, with transparent edge padding.
// An empty frame list produces the base monospace ASCII font for initial setup.
func Encode(ctx context.Context, frames []Frame, options Options) (Font, error) {
	if err := ctx.Err(); err != nil {
		return Font{}, err
	}
	if err := validateNames(options); err != nil {
		return Font{}, err
	}
	prepared, err := prepareGeometry(frames, options.TileWidth, options.TileHeight)
	if err != nil {
		return Font{}, err
	}
	outlineGlyphs, err := asciiGlyphs(ctx)
	if err != nil {
		return Font{}, err
	}
	glyphCount := baseGlyphCount
	previews := make([]Preview, len(prepared))
	for i := range prepared {
		prepared[i].firstGlyph = glyphCount
		glyphCount += prepared[i].Columns * prepared[i].Rows
		previews[i] = prepared[i].Preview
	}
	bitmap, err := sbix(ctx, prepared, glyphCount)
	if err != nil {
		return Font{}, err
	}
	tables := map[string][]byte{
		"head": head(outlineGlyphs), "hhea": hhea(glyphCount, outlineGlyphs), "maxp": maxp(glyphCount, outlineGlyphs),
		"OS/2": os2(prepared), "hmtx": hmtx(glyphCount, outlineGlyphs), "cmap": cmap(prepared),
		"name": names(options), "post": post(), "sbix": bitmap,
	}
	var glyphs, locations buffer
	for i := 0; i < glyphCount; i++ {
		locations.u32(uint32(glyphs.Len()))
		if i < len(outlineGlyphs) {
			glyphs.Write(outlineGlyphs[i])
		} else {
			glyphs.zeros(12) // Empty outline: the sbix table owns the glyph pixels.
		}
		if glyphs.Len()%2 != 0 {
			glyphs.WriteByte(0)
		}
	}
	locations.u32(uint32(glyphs.Len()))
	tables["glyf"], tables["loca"] = glyphs.Bytes(), locations.Bytes()
	if err := ctx.Err(); err != nil {
		return Font{}, err
	}
	return Font{Data: assembleFont(tables), Previews: previews}, nil
}

func validateNames(options Options) error {
	if len(options.Family) == 0 || len(options.Family) > 100 || strings.TrimSpace(options.Family) != options.Family {
		return fmt.Errorf("font family must contain 1 to 100 printable ASCII characters without surrounding spaces")
	}
	for _, r := range options.Family {
		if r < 32 || r > 126 {
			return fmt.Errorf("font family must contain only printable ASCII characters")
		}
	}
	if len(options.PostScript) == 0 || len(options.PostScript) > 63 {
		return fmt.Errorf("PostScript name must contain 1 to 63 ASCII letters, digits, or hyphens")
	}
	for _, r := range options.PostScript {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return fmt.Errorf("PostScript name must contain only ASCII letters, digits, or hyphens")
		}
	}
	return nil
}

func prepare(frames []Frame) ([]preparedFrame, error) {
	return prepareGeometry(frames, 0, 0)
}

func prepareGeometry(frames []Frame, tileWidth, tileHeight int) ([]preparedFrame, error) {
	if tileWidth == 0 && tileHeight == 0 {
		tileWidth, tileHeight = 16, 32
	}
	if tileWidth < 8 || tileWidth > 24 || tileHeight < 16 || tileHeight > 48 {
		return nil, fmt.Errorf("bitmap tile geometry requires width 8 to 24 and height 16 to 48 pixels at 32ppem, or both zero for defaults")
	}
	if len(frames) > MaxGlyphs {
		return nil, fmt.Errorf("a font supports at most %d image frames", MaxGlyphs)
	}
	prepared := make([]preparedFrame, 0, len(frames))
	var used [MaxGlyphs]bool
	next := FirstCodepoint
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
			f.Rows = min(32, max(1, int(math.Ceil(float64(f.Columns*tileWidth)*float64(h)/float64(tileHeight*w)))))
		}
		if f.CodepointStart == 0 {
			f.CodepointStart = next
		}
		count := f.Columns * f.Rows
		if f.CodepointStart < FirstCodepoint || f.CodepointStart > LastCodepoint || count > int(LastCodepoint-f.CodepointStart)+1 {
			return nil, fmt.Errorf("frame %d character range must fit in U+E000 through U+F8FF", i+1)
		}
		var text strings.Builder
		text.Grow(count*3 + f.Rows)
		for glyph := 0; glyph < count; glyph++ {
			codepoint := f.CodepointStart + rune(glyph)
			if used[int(codepoint-FirstCodepoint)] {
				return nil, fmt.Errorf("frame %d overlaps another image's character range", i+1)
			}
			used[int(codepoint-FirstCodepoint)] = true
			text.WriteRune(codepoint)
			if (glyph+1)%f.Columns == 0 {
				text.WriteByte('\n')
			}
		}
		next = f.CodepointStart + rune(count)
		prepared = append(prepared, preparedFrame{image: f.Image, Preview: Preview{
			Text: text.String(), Columns: f.Columns, Rows: f.Rows,
			WidthPixels: f.Columns * tileWidth, HeightPixels: f.Rows * tileHeight, CodepointStart: f.CodepointStart,
		}})
	}
	return prepared, nil
}

func fit(src image.Image, width, height int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	bounds := src.Bounds()
	scale := min(float64(width)/float64(bounds.Dx()), float64(height)/float64(bounds.Dy()))
	w := min(width, max(1, int(math.Round(float64(bounds.Dx())*scale))))
	h := min(height, max(1, int(math.Round(float64(bounds.Dy())*scale))))
	x, y := (width-w)/2, (height-h)/2
	draw.CatmullRom.Scale(dst, image.Rect(x, y, x+w, y+h), src, bounds, draw.Src, nil)
	return dst
}

func sbix(ctx context.Context, frames []preparedFrame, glyphCount int) ([]byte, error) {
	var b buffer
	b.u16(1)
	b.u16(1)
	b.u32(2)
	strikes := make([][]byte, 2)
	offset := 16
	for i, ppem := range []int{32, 64} {
		strike, err := encodeStrike(ctx, frames, glyphCount, ppem)
		if err != nil {
			return nil, err
		}
		strikes[i] = strike
		b.u32(uint32(offset))
		offset += len(strike)
	}
	for _, strike := range strikes {
		b.Write(strike)
	}
	return b.Bytes(), nil
}

func encodeStrike(ctx context.Context, frames []preparedFrame, glyphCount, ppem int) ([]byte, error) {
	var glyphs buffer
	encoder := png.Encoder{BufferPool: &pngBufferPool{}}
	offsets := make([]uint32, glyphCount+1)
	headerLength := 4 + 4*(glyphCount+1)
	for glyph := 0; glyph < baseGlyphCount; glyph++ {
		offsets[glyph] = uint32(headerLength)
	}
	for _, frame := range frames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tileWidth := frame.WidthPixels / frame.Columns * (ppem / 32)
		tileHeight := frame.HeightPixels / frame.Rows * (ppem / 32)
		img := fit(frame.image, frame.Columns*tileWidth, frame.Rows*tileHeight)
		for tile := 0; tile < frame.Columns*frame.Rows; tile++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			offsets[frame.firstGlyph+tile] = uint32(headerLength + glyphs.Len())
			x, y := tile%frame.Columns*tileWidth, tile/frame.Columns*tileHeight
			glyphs.i16(0)
			glyphs.i16(int16(-ppem / 4))
			glyphs.WriteString("png ")
			if err := encoder.Encode(&glyphs, img.SubImage(image.Rect(x, y, x+tileWidth, y+tileHeight))); err != nil {
				return nil, fmt.Errorf("encode image font tile: %w", err)
			}
		}
	}
	offsets[glyphCount] = uint32(headerLength + glyphs.Len())
	var b buffer
	b.u16(uint16(ppem))
	b.u16(72)
	for _, offset := range offsets {
		b.u32(offset)
	}
	b.Write(glyphs.Bytes())
	return b.Bytes(), nil
}

// Hundreds of small tiles share compression scratch space within this encode.
// The pool belongs to one call, so concurrent font builds never share state.
type pngBufferPool struct{ buffer *png.EncoderBuffer }

func (p *pngBufferPool) Get() *png.EncoderBuffer  { return p.buffer }
func (p *pngBufferPool) Put(b *png.EncoderBuffer) { p.buffer = b }

type buffer struct{ bytes.Buffer }

func (b *buffer) u16(v uint16) { _ = binary.Write(&b.Buffer, binary.BigEndian, v) }
func (b *buffer) i16(v int16)  { b.u16(uint16(v)) }
func (b *buffer) u32(v uint32) { _ = binary.Write(&b.Buffer, binary.BigEndian, v) }
func (b *buffer) zeros(n int)  { b.Write(make([]byte, n)) }

func checksum(data []byte) uint32 {
	var sum uint32
	for i := 0; i < len(data); i += 4 {
		var v [4]byte
		copy(v[:], data[i:min(i+4, len(data))])
		sum += binary.BigEndian.Uint32(v[:])
	}
	return sum
}

func assembleFont(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for tag := range tables {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	n := len(tags)
	power, entry := 1, 0
	for power*2 <= n {
		power *= 2
		entry++
	}
	var b buffer
	if _, cff := tables["CFF "]; cff {
		b.WriteString("OTTO")
	} else {
		b.u32(0x00010000)
	}
	b.u16(uint16(n))
	b.u16(uint16(16 * power))
	b.u16(uint16(entry))
	b.u16(uint16(n*16 - 16*power))
	offset := 12 + 16*n
	headOffset := 0
	for _, tag := range tags {
		data := tables[tag]
		b.WriteString(tag)
		b.u32(checksum(data))
		b.u32(uint32(offset))
		b.u32(uint32(len(data)))
		if tag == "head" {
			headOffset = offset
		}
		offset += (len(data) + 3) &^ 3
	}
	for _, tag := range tags {
		b.Write(tables[tag])
		for b.Len()%4 != 0 {
			b.WriteByte(0)
		}
	}
	font := b.Bytes()
	binary.BigEndian.PutUint32(font[headOffset+8:headOffset+12], 0xb1b0afba-checksum(font))
	return font
}
