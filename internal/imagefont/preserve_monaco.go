package imagefont

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Terminal applies different line metrics to its built-in Monaco family. A
// private font must have a unique family to avoid shadowing the installed face.
// Compensate only Monaco's vertical layout metrics so that the private family's
// normal rounding gives the same cell height and baseline. Its glyph outlines,
// hinting, horizontal advances and the user's point size remain unchanged.
func compensatePreservedMonaco(tables map[string][]byte, source PreserveOptions) error {
	if source.SourcePostScript != "Monaco" {
		return nil
	}
	if source.PointSize < 1 || source.PointSize > 1024 || source.TextHeight < 1 || source.TextHeight > 4096 || source.Baseline < 0 || source.Baseline >= source.TextHeight || len(tables["head"]) < 20 || len(tables["hhea"]) < 10 {
		return fmt.Errorf("cannot preserve Monaco's original line spacing")
	}
	if os2, exists := tables["OS/2"]; exists && len(os2) < 78 {
		return fmt.Errorf("cannot preserve Monaco's original line spacing: truncated OS/2 table")
	}
	upem := int(binary.BigEndian.Uint16(tables["head"][18:20]))
	if upem < 16 || upem > 16384 {
		return fmt.Errorf("cannot preserve Monaco's original line spacing: invalid units per em")
	}
	ascent := int(math.Floor(float64((source.TextHeight-source.Baseline)*upem) / float64(source.PointSize)))
	descent := int(math.Floor(float64(source.Baseline*upem) / float64(source.PointSize)))
	if ascent <= 0 || ascent > 32767 || descent > 32767 {
		return fmt.Errorf("cannot preserve Monaco's original line spacing: metrics exceed font bounds")
	}
	// Flooring at font-unit precision places both components just below their
	// intended integer-point edges. Terminal's ceil then produces TextHeight,
	// and its rounded descent produces the original baseline. Verify before any
	// writes, including unusual source metrics and very large point sizes.
	a := float64(float32(float64(ascent) * float64(source.PointSize) / float64(upem)))
	d := float64(float32(float64(descent) * float64(source.PointSize) / float64(upem)))
	if int(math.Ceil(a)+math.Ceil(d)) != source.TextHeight || int(-math.Floor(float64(float32(-d+0.5)))) != source.Baseline {
		return fmt.Errorf("cannot preserve Monaco's original line spacing at this font size")
	}
	put := func(table string, offset int, value uint16) {
		binary.BigEndian.PutUint16(tables[table][offset:offset+2], value)
	}
	put("hhea", 4, uint16(ascent))
	put("hhea", 6, uint16(int16(-descent)))
	put("hhea", 8, 0)
	if _, exists := tables["OS/2"]; exists {
		put("OS/2", 68, uint16(ascent))
		put("OS/2", 70, uint16(int16(-descent)))
		put("OS/2", 72, 0)
		put("OS/2", 74, uint16(ascent))
		put("OS/2", 76, uint16(descent))
	}
	return nil
}
