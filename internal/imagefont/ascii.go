package imagefont

import (
	"context"
	"fmt"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

const baseGlyphCount = 96 // .notdef, then printable ASCII.

type outlinePoint struct {
	x, y int16
	on   bool
}

// ASCII uses Go Mono outlines (see GO-FONT-LICENSE.txt), fitted to the same
// half-em advance as the image tiles. System fallback has a different advance
// and causes shell text to overlap in Terminal's fixed-width cell grid.
func asciiGlyphs(ctx context.Context) ([][]byte, error) {
	source, err := sfnt.Parse(gomono.TTF)
	if err != nil {
		return nil, err
	}
	glyphs := make([][]byte, baseGlyphCount)
	glyphs[0] = make([]byte, 12)
	var scratch sfnt.Buffer
	for r := rune(32); r <= 126; r++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		index, err := source.GlyphIndex(&scratch, r)
		if err != nil {
			return nil, err
		}
		advance, err := source.GlyphAdvance(&scratch, index, fixed.I(1000), font.HintingNone)
		if err != nil {
			return nil, err
		}
		segments, err := source.LoadGlyph(&scratch, index, fixed.I(1000), nil)
		if err != nil {
			return nil, err
		}
		var points []outlinePoint
		var ends []uint16
		finish := func() {
			start := 0
			if len(ends) > 0 {
				start = int(ends[len(ends)-1]) + 1
			}
			if len(points) <= start {
				return
			}
			if len(points)-start > 1 && points[len(points)-1] == points[start] {
				points = points[:len(points)-1]
			}
			ends = append(ends, uint16(len(points)-1))
		}
		add := func(p fixed.Point26_6, on bool) {
			// Fit Go Mono's tallest ASCII glyph (783 units) into the shared
			// 750-unit ascent so even accents stay inside Terminal's line box.
			points = append(points, outlinePoint{
				x: int16(math.Round(float64(p.X) * 500 / float64(advance))),
				y: int16(math.Round(-float64(p.Y) * 750 / (64 * 783))), on: on,
			})
		}
		for _, segment := range segments {
			switch segment.Op {
			case sfnt.SegmentOpMoveTo:
				finish()
				add(segment.Args[0], true)
			case sfnt.SegmentOpLineTo:
				add(segment.Args[0], true)
			case sfnt.SegmentOpQuadTo:
				add(segment.Args[0], false)
				add(segment.Args[1], true)
			default:
				return nil, fmt.Errorf("unexpected cubic outline in bundled Go Mono font")
			}
		}
		finish()
		var out buffer
		out.u16(uint16(len(ends)))
		var minX, minY, maxX, maxY int16
		for i, p := range points {
			if i == 0 {
				minX, minY, maxX, maxY = p.x, p.y, p.x, p.y
			}
			minX, minY, maxX, maxY = min(minX, p.x), min(minY, p.y), max(maxX, p.x), max(maxY, p.y)
		}
		for _, n := range []int16{minX, minY, maxX, maxY} {
			out.u16(uint16(n))
		}
		for _, end := range ends {
			out.u16(end)
		}
		out.u16(0) // No TrueType bytecode instructions.
		for _, p := range points {
			if p.on {
				out.WriteByte(1)
			} else {
				out.WriteByte(0)
			}
		}
		var lastX, lastY int16
		for _, p := range points {
			out.u16(uint16(p.x - lastX))
			lastX = p.x
		}
		for _, p := range points {
			out.u16(uint16(p.y - lastY))
			lastY = p.y
		}
		glyphs[int(r)-31] = out.Bytes()
	}
	return glyphs, nil
}
