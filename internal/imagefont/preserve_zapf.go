package imagefont

import (
	"encoding/binary"
	"fmt"
)

// extendPreservedZapf relocates the v1 per-glyph offsets while retaining the
// original glyph metadata and extra-info payload. Added image glyphs share an
// empty record, with no reverse text mapping, identifiers, groups or features.
func extendPreservedZapf(data []byte, oldCount, newCount int) ([]byte, error) {
	fail := func(reason string) ([]byte, error) {
		return nil, fmt.Errorf("cannot preserve this font: %s", reason)
	}
	if oldCount < 1 || oldCount > newCount || newCount > 65535 {
		return fail("invalid Zapf glyph count")
	}
	if len(data) < 8 {
		return fail("truncated Zapf header")
	}
	if binary.BigEndian.Uint32(data) != 0x10000 {
		return fail("unsupported Zapf version; only version 1 can be extended")
	}
	oldBody := 8 + 4*oldCount
	if oldBody > len(data) {
		return fail("truncated Zapf glyph offsets")
	}
	extraInfo := uint64(binary.BigEndian.Uint32(data[4:]))
	if extraInfo < uint64(oldBody) || extraInfo > uint64(len(data)) {
		return fail("invalid Zapf extra-info offset")
	}
	for i := 0; i < oldCount; i++ {
		offset := uint64(binary.BigEndian.Uint32(data[8+4*i:]))
		// Even an empty v1 GlyphInfo occupies 12 bytes, before extraInfo.
		if offset < uint64(oldBody) || offset+12 > extraInfo {
			return fail("invalid Zapf glyph-info offset")
		}
	}
	if oldCount == newCount {
		return append([]byte(nil), data...), nil
	}
	newBody := 8 + 4*newCount
	delta := newBody - oldBody + 12
	newSize := uint64(len(data)) + uint64(delta)
	if newSize > uint64(^uint32(0)) || newSize > uint64(^uint(0)>>1) {
		return fail("extended Zapf table exceeds offset limits")
	}
	result := make([]byte, newBody+12, int(newSize))
	copy(result, data[:8])
	binary.BigEndian.PutUint32(result[4:], uint32(extraInfo)+uint32(delta))
	for i := 0; i < oldCount; i++ {
		offset := binary.BigEndian.Uint32(data[8+4*i:])
		binary.BigEndian.PutUint32(result[8+4*i:], offset+uint32(delta))
	}
	for i := oldCount; i < newCount; i++ {
		binary.BigEndian.PutUint32(result[8+4*i:], uint32(newBody))
	}
	// The two absent extra-info references are followed by zero Unicode and
	// identifier counts. Moving the old body together leaves relative offsets
	// inside its glyph records and extra-info payload unchanged.
	binary.BigEndian.PutUint32(result[newBody:], 0xffffffff)
	binary.BigEndian.PutUint32(result[newBody+4:], 0xffffffff)
	return append(result, data[oldBody:]...), nil
}
