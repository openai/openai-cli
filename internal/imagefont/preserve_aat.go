package imagefont

import (
	"encoding/binary"
	"fmt"
)

// checkPreservedAATGlyphCoverage declines known AAT layouts whose implicit
// glyph-count arrays would grow into adjacent data when maxp is extended.
// Bounded lookups and default glyph properties keep their original coverage.
func checkPreservedAATGlyphCoverage(tables map[string][]byte) error {
	for _, tag := range []string{"prop", "bsln", "morx", "kerx"} {
		data, exists := tables[tag]
		if !exists {
			continue
		}
		fail := func() error {
			return fmt.Errorf("cannot preserve this font: unsupported glyph coverage in %s table", tag)
		}
		if len(data) < 8 {
			return fail()
		}
		version := binary.BigEndian.Uint32(data)
		lookup := 0
		switch tag {
		case "morx", "kerx":
			// Version 3 introduces glyph coverage bitfields sized by maxp.
			if version != 0x00010000 && version != 0x00020000 {
				return fail()
			}
		case "prop":
			format := binary.BigEndian.Uint16(data[4:])
			if version != 0x00010000 && version != 0x00020000 && version != 0x00030000 || format > 1 {
				return fail()
			}
			if format == 1 {
				lookup = 8
			}
		case "bsln":
			format := binary.BigEndian.Uint16(data[4:])
			if version != 0x00010000 || format > 3 {
				return fail()
			}
			end := 72
			if format >= 2 {
				end = 74
			}
			if len(data) < end {
				return fail()
			}
			if format%2 != 0 {
				lookup = end
			}
		}
		if lookup > 0 {
			if len(data) < lookup+2 {
				return fail()
			}
			switch binary.BigEndian.Uint16(data[lookup:]) {
			case 2, 4, 6, 8, 10:
				// These formats declare their own glyph ranges.
			default:
				return fail()
			}
		}
	}
	return nil
}
