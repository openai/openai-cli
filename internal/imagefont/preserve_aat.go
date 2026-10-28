package imagefont

import (
	"encoding/binary"
	"fmt"
)

// checkPreservedAATGlyphCoverage declines known AAT layouts whose implicit
// glyph-count arrays would grow into adjacent data when maxp is extended.
// Bounded lookups and default glyph properties keep their original coverage.
func checkPreservedAATGlyphCoverage(tables map[string][]byte) error {
	for _, tag := range []string{"prop", "bsln", "lcar", "opbd", "ankr", "just", "mort", "morx", "kerx"} {
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
		case "ankr":
			if version != 0 || len(data) < 12 {
				return fail()
			}
			offset := uint64(binary.BigEndian.Uint32(data[4:]))
			if offset < 12 || !boundedPreservedAATLookup(data, offset) {
				return fail()
			}
		case "just":
			if version != 0x00010000 || len(data) < 10 || binary.BigEndian.Uint16(data[4:]) != 0 {
				return fail()
			}
			for _, p := range []int{6, 8} {
				offset := int(binary.BigEndian.Uint16(data[p:]))
				if offset == 0 {
					continue
				}
				if offset < 10 || offset+6 > len(data) || !boundedPreservedAATLookup(data, uint64(offset+6)) {
					return fail()
				}
				post := uint64(binary.BigEndian.Uint16(data[offset+4:]))
				if post != 0 && (post < 10 || !boundedPreservedAATLookup(data, post)) {
					return fail()
				}
			}
		case "lcar", "opbd":
			// Their table format selects distances or control points. The
			// separate lookup format immediately follows the six-byte header.
			if version != 0x00010000 || binary.BigEndian.Uint16(data[4:]) > 1 {
				return fail()
			}
			lookup = 6
		case "mort", "morx":
			if !boundedPreservedAATMorphology(data, tag == "morx") {
				return fail()
			}
		case "kerx":
			if version != 0x00020000 || !boundedPreservedKerx(data) {
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
			if !boundedPreservedAATLookup(data, uint64(lookup)) {
				return fail()
			}
		}
	}
	return nil
}

func boundedPreservedAATLookup(data []byte, offset uint64) bool {
	if offset+2 > uint64(len(data)) {
		return false
	}
	lookup := data[int(offset):]
	format := binary.BigEndian.Uint16(lookup)
	switch format {
	case 2, 4, 6:
		if len(lookup) < 12 {
			return false
		}
		unit := 6
		if format == 6 {
			unit = 4
		}
		return int(binary.BigEndian.Uint16(lookup[2:])) == unit && int(binary.BigEndian.Uint16(lookup[4:])) <= (len(lookup)-12)/unit
	case 8:
		return len(lookup) >= 6 && int(binary.BigEndian.Uint16(lookup[4:])) <= (len(lookup)-6)/2
	case 10:
		if len(lookup) < 8 {
			return false
		}
		unit := int(binary.BigEndian.Uint16(lookup[2:]))
		return (unit == 1 || unit == 2 || unit == 4 || unit == 8) && int(binary.BigEndian.Uint16(lookup[6:])) <= (len(lookup)-8)/unit
	}
	// Format 0 has an implicit maxp-sized array. Other supported formats
	// declare their own glyph ranges, with bounded arrays checked above.
	return false
}

// Walk only the container headers needed to find glyph lookups. Contextual
// substitution has additional indirectly indexed lookup arrays; decline it
// until those can be preserved too. Existing text tables are never removed.
func boundedPreservedAATMorphology(data []byte, extended bool) bool {
	version, chainHeader, subHeader := uint32(0x00010000), 12, 8
	if extended {
		version, chainHeader, subHeader = 0x00020000, 16, 12
	}
	// morx version 3 also contains glyph coverage bitfields sized by maxp.
	if len(data) < 8 || binary.BigEndian.Uint32(data) != version {
		return false
	}
	chains := uint64(binary.BigEndian.Uint32(data[4:]))
	p := 8
	if chains > uint64((len(data)-p)/chainHeader) {
		return false
	}
	for i := uint64(0); i < chains; i++ {
		if len(data)-p < chainHeader {
			return false
		}
		length := uint64(binary.BigEndian.Uint32(data[p+4:]))
		if length < uint64(chainHeader) || length > uint64(len(data)-p) {
			return false
		}
		chain := data[p : p+int(length)]
		features := uint64(binary.BigEndian.Uint16(chain[8:]))
		subtables := uint64(binary.BigEndian.Uint16(chain[10:]))
		if extended {
			features = uint64(binary.BigEndian.Uint32(chain[8:]))
			subtables = uint64(binary.BigEndian.Uint32(chain[12:]))
		}
		if features > uint64((len(chain)-chainHeader)/12) {
			return false
		}
		q := chainHeader + int(features)*12
		if subtables > uint64((len(chain)-q)/subHeader) {
			return false
		}
		for j := uint64(0); j < subtables; j++ {
			if len(chain)-q < subHeader {
				return false
			}
			size := uint64(binary.BigEndian.Uint16(chain[q:]))
			kind := uint32(binary.BigEndian.Uint16(chain[q+2:]) & 7)
			if extended {
				size = uint64(binary.BigEndian.Uint32(chain[q:]))
				kind = binary.BigEndian.Uint32(chain[q+4:]) & 255
			}
			if size < uint64(subHeader) || size > uint64(len(chain)-q) {
				return false
			}
			sub := chain[q : q+int(size)]
			switch kind {
			case 4:
				if !boundedPreservedAATLookup(sub, uint64(subHeader)) {
					return false
				}
			case 0, 2, 5:
				if extended {
					minimum := 16
					if kind == 2 {
						minimum = 28
					} else if kind == 5 {
						minimum = 20
					}
					if !boundedPreservedAATStateLookup(sub[subHeader:], minimum) {
						return false
					}
				}
			default:
				return false
			}
			q += int(size)
		}
		p += int(length)
	}
	return true
}

func boundedPreservedAATStateLookup(state []byte, minimum int) bool {
	if len(state) < minimum {
		return false
	}
	offset := uint64(binary.BigEndian.Uint32(state[4:]))
	return offset >= uint64(minimum) && boundedPreservedAATLookup(state, offset)
}

func boundedPreservedKerx(data []byte) bool {
	subtables := uint64(binary.BigEndian.Uint32(data[4:]))
	p := 8
	if subtables > uint64((len(data)-p)/12) {
		return false
	}
	for i := uint64(0); i < subtables; i++ {
		if len(data)-p < 12 {
			return false
		}
		length := uint64(binary.BigEndian.Uint32(data[p:]))
		if length < 12 || length > uint64(len(data)-p) {
			return false
		}
		sub := data[p : p+int(length)]
		switch binary.BigEndian.Uint32(sub[4:]) & 255 {
		case 0:
			// Ordered kerning pairs have an explicit pair count.
		case 1, 4:
			if !boundedPreservedAATStateLookup(sub[12:], 20) {
				return false
			}
		case 2, 6:
			left, right, minimum := 16, 20, 28
			if binary.BigEndian.Uint32(sub[4:])&255 == 6 {
				left, right, minimum = 20, 24, 32
				if binary.BigEndian.Uint32(sub[8:]) != 0 {
					minimum = 36
				}
			}
			if len(sub) < minimum {
				return false
			}
			for _, field := range []int{left, right} {
				offset := uint64(binary.BigEndian.Uint32(sub[field:]))
				if offset < uint64(minimum) || !boundedPreservedAATLookup(sub, offset) {
					return false
				}
			}
		default:
			return false
		}
		p += int(length)
	}
	return true
}
