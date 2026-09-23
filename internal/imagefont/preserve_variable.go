package imagefont

import (
	"encoding/binary"
	"fmt"
)

// These helpers extend only glyph-indexed variation tables. The caller retains
// fvar/avar/cvar/MVAR and the selected variation coordinates unchanged. They do
// not instantiate or flatten a variable font, register it, or alter its axes.
func variableFontError(reason string) error {
	return fmt.Errorf("cannot preserve this variable font: %s", reason)
}

func validVariableCounts(oldCount, newCount, axes int) bool {
	return oldCount > 0 && oldCount <= newCount && newCount <= 65535 && axes > 0 && axes <= 64
}

// extendPreservedGvar preserves every existing glyph's variation bytes and
// shared tuple, appending empty variation ranges for the bitmap-only glyphs.
func extendPreservedGvar(data []byte, oldCount, newCount, axes int) ([]byte, error) {
	if !validVariableCounts(oldCount, newCount, axes) || len(data) < 20 || binary.BigEndian.Uint32(data) != 0x10000 || int(binary.BigEndian.Uint16(data[4:])) != axes || int(binary.BigEndian.Uint16(data[12:])) != oldCount {
		return nil, variableFontError("invalid gvar header or glyph count")
	}
	flags := binary.BigEndian.Uint16(data[14:])
	if flags & ^uint16(1) != 0 {
		return nil, variableFontError("unsupported gvar flags")
	}
	width := 2 + 2*int(flags&1)
	oldBody := 20 + (oldCount+1)*width
	if oldBody > len(data) {
		return nil, variableFontError("truncated gvar glyph offsets")
	}
	sharedCount := int(binary.BigEndian.Uint16(data[6:]))
	sharedOffset := uint64(binary.BigEndian.Uint32(data[8:]))
	glyphOffset := uint64(binary.BigEndian.Uint32(data[16:]))
	if sharedCount > 4096 || glyphOffset < uint64(oldBody) || glyphOffset > uint64(len(data)) {
		return nil, variableFontError("invalid gvar data offset")
	}
	if sharedCount > 0 && (sharedOffset < uint64(oldBody) || sharedOffset > glyphOffset || uint64(sharedCount*axes*2) > glyphOffset-sharedOffset) {
		return nil, variableFontError("invalid gvar shared tuple range")
	}
	offsets := make([]uint32, oldCount+1)
	for i := range offsets {
		if width == 2 {
			offsets[i] = uint32(binary.BigEndian.Uint16(data[20+i*2:])) * 2
		} else {
			offsets[i] = binary.BigEndian.Uint32(data[20+i*4:])
		}
		if uint64(offsets[i]) > uint64(len(data))-glyphOffset || (i > 0 && offsets[i] < offsets[i-1]) {
			return nil, variableFontError("invalid gvar glyph variation range")
		}
	}
	newBody := 20 + (newCount+1)*4
	delta := newBody - oldBody
	result := make([]byte, newBody, len(data)+delta)
	copy(result, data[:20])
	binary.BigEndian.PutUint16(result[12:], uint16(newCount))
	binary.BigEndian.PutUint16(result[14:], 1)
	if sharedOffset != 0 {
		if sharedOffset < uint64(oldBody) || sharedOffset > uint64(len(data)) {
			return nil, variableFontError("invalid gvar shared tuple offset")
		}
		binary.BigEndian.PutUint32(result[8:], uint32(sharedOffset)+uint32(delta))
	}
	binary.BigEndian.PutUint32(result[16:], uint32(glyphOffset)+uint32(delta))
	for i := 0; i <= newCount; i++ {
		binary.BigEndian.PutUint32(result[20+i*4:], offsets[min(i, oldCount)])
	}
	return append(result, data[oldBody:]...), nil
}

// extendPreservedHVAR keeps the original variation store byte-for-byte. The
// new glyphs inherit W's advance deltas because their default hmtx advance also
// inherits W. Their zero-bearing empty outlines have no side-bearing deltas.
// Existing implicit or compressed mappings are expanded without changing any
// original glyph's logical outer/inner variation index.
func extendPreservedHVAR(data []byte, oldCount, newCount, widthGlyph, axes int) ([]byte, error) {
	if !validVariableCounts(oldCount, newCount, axes) || widthGlyph < 0 || widthGlyph >= oldCount || len(data) < 20 || binary.BigEndian.Uint32(data) != 0x10000 {
		return nil, variableFontError("invalid HVAR header or glyph count")
	}
	storeOffset := uint64(binary.BigEndian.Uint32(data[4:]))
	if storeOffset < 20 || storeOffset > uint64(len(data)) {
		return nil, variableFontError("invalid HVAR variation store offset")
	}
	items, err := readPreservedVariationStore(data[int(storeOffset):], axes)
	if err != nil {
		return nil, err
	}
	result := append([]byte(nil), data...)
	for _, field := range []int{8, 12, 16} {
		offset := uint64(binary.BigEndian.Uint32(data[field:]))
		if offset == 0 && field != 8 {
			continue
		}
		var indices []uint32
		if offset == 0 {
			indices = make([]uint32, oldCount)
			for gid := range indices {
				indices[gid] = uint32(gid)
			}
		} else {
			if offset < 20 || offset > uint64(len(data)) {
				return nil, variableFontError("invalid HVAR mapping offset")
			}
			indices, err = readPreservedDeltaMap(data[int(offset):], oldCount)
			if err != nil {
				return nil, err
			}
		}
		for _, index := range indices {
			if index == 0xffffffff {
				continue
			}
			outer, inner := int(index>>16), int(index&65535)
			if outer >= len(items) || (items[outer] >= 0 && inner >= items[outer]) {
				return nil, variableFontError("HVAR mapping refers outside its variation store")
			}
		}
		added := uint32(0xffffffff)
		if field == 8 {
			added = indices[widthGlyph]
		}
		for len(indices) < newCount {
			indices = append(indices, added)
		}
		binary.BigEndian.PutUint32(result[field:], uint32(len(result)))
		result = append(result, encodePreservedDeltaMap(indices)...)
	}
	return result, nil
}

// A negative item count denotes a NULL ItemVariationData offset, which the
// OpenType format defines as having no variation data for any inner index.
func readPreservedVariationStore(data []byte, axes int) ([]int, error) {
	if axes < 1 || axes > 64 || len(data) < 8 || binary.BigEndian.Uint16(data) != 1 {
		return nil, variableFontError("invalid item variation store")
	}
	count := int(binary.BigEndian.Uint16(data[6:]))
	header := 8 + count*4
	if header > len(data) {
		return nil, variableFontError("truncated item variation offsets")
	}
	regionOffset := uint64(binary.BigEndian.Uint32(data[2:]))
	if regionOffset < uint64(header) || regionOffset > uint64(len(data)) || uint64(len(data))-regionOffset < 4 {
		return nil, variableFontError("invalid variation region offset")
	}
	regions := data[int(regionOffset):]
	regionCount := int(binary.BigEndian.Uint16(regions[2:]))
	if int(binary.BigEndian.Uint16(regions)) != axes || regionCount >= 32768 || regionCount > (len(regions)-4)/(axes*6) {
		return nil, variableFontError("invalid variation region list")
	}
	items := make([]int, count)
	for i := range items {
		offset := uint64(binary.BigEndian.Uint32(data[8+i*4:]))
		if offset == 0 {
			items[i] = -1
			continue
		}
		if offset < uint64(header) || offset > uint64(len(data)) || uint64(len(data))-offset < 6 {
			return nil, variableFontError("invalid item variation data offset")
		}
		entry := data[int(offset):]
		itemCount, words, indexCount := int(binary.BigEndian.Uint16(entry)), int(binary.BigEndian.Uint16(entry[2:])), int(binary.BigEndian.Uint16(entry[4:]))
		if words&0x8000 != 0 || words > indexCount || indexCount > (len(entry)-6)/2 {
			return nil, variableFontError("invalid HVAR item variation data")
		}
		for region := 0; region < indexCount; region++ {
			if int(binary.BigEndian.Uint16(entry[6+region*2:])) >= regionCount {
				return nil, variableFontError("invalid item variation region index")
			}
		}
		rowSize := indexCount + words
		if rowSize > 0 && itemCount > (len(entry)-6-indexCount*2)/rowSize {
			return nil, variableFontError("truncated item variation deltas")
		}
		items[i] = itemCount
	}
	return items, nil
}

func readPreservedDeltaMap(data []byte, glyphCount int) ([]uint32, error) {
	if len(data) < 4 || data[0] != 0 || data[1]&0xc0 != 0 {
		return nil, variableFontError("unsupported HVAR delta-set index map")
	}
	count := int(binary.BigEndian.Uint16(data[2:]))
	width := int((data[1]>>4)&3) + 1
	bits := uint(data[1]&15) + 1
	if count == 0 || count > (len(data)-4)/width {
		return nil, variableFontError("truncated HVAR delta-set index map")
	}
	indices := make([]uint32, glyphCount)
	for gid := range indices {
		pos := 4 + min(gid, count-1)*width
		var value uint32
		for _, b := range data[pos : pos+width] {
			value = value<<8 | uint32(b)
		}
		outer, inner := value>>bits, value&((1<<bits)-1)
		if outer > 65535 {
			return nil, variableFontError("delta-set outer index exceeds 16 bits")
		}
		indices[gid] = outer<<16 | inner
	}
	return indices, nil
}

func encodePreservedDeltaMap(indices []uint32) []byte {
	var out buffer
	out.WriteByte(0)
	out.WriteByte(0x3f) // Four bytes, sixteen inner-index bits.
	out.u16(uint16(len(indices)))
	for _, index := range indices {
		out.u32(index)
	}
	return out.Bytes()
}
