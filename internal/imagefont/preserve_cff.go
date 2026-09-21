package imagefont

import (
	"encoding/binary"
	"fmt"
	"strconv"
)

// CFF1 SIDs are limited to 0..64999, including its 391 standard strings.
const cffCustomStringLimit = 65000 - 391

// CFF1 relocation keeps the entire original data section intact. Only the
// leading INDEXes and Top DICT offsets are rebuilt; original charstrings,
// global/local subroutines, hinting and Private DICT bytes are never rewritten.
// CID, synthetic, multiple-master and CFF2 fonts require different contracts.
type preservedCFF struct {
	header, payload               []byte
	strings, globals, charstrings [][]byte
	dict                          []cffEntry
	charset                       []uint16
	payloadOffset                 int
}

type cffOperand struct {
	raw       []byte
	integer   int
	isInteger bool
}

type cffEntry struct {
	op   int
	args []cffOperand
}

func cffError(reason string) error {
	return fmt.Errorf("cannot preserve this CFF font: %s", reason)
}

// readCFFIndex returns immutable views of the original objects and the offset
// immediately after their data. Validate every offset before slicing.
func readCFFIndex(data []byte, offset int) ([][]byte, int, error) {
	if offset < 0 || offset > len(data)-2 {
		return nil, 0, cffError("truncated INDEX count")
	}
	count := int(binary.BigEndian.Uint16(data[offset:]))
	if count == 0 {
		return nil, offset + 2, nil
	}
	if offset+2 >= len(data) {
		return nil, 0, cffError("truncated INDEX header")
	}
	width := int(data[offset+2])
	if width < 1 || width > 4 || count+1 > (len(data)-offset-3)/width {
		return nil, 0, cffError("invalid INDEX offset array")
	}
	start := offset + 3 + (count+1)*width
	read := func(index int) uint32 {
		var value uint32
		for _, b := range data[offset+3+index*width : offset+3+(index+1)*width] {
			value = value<<8 | uint32(b)
		}
		return value
	}
	last := read(0)
	if last != 1 {
		return nil, 0, cffError("INDEX data must start at offset one")
	}
	objects := make([][]byte, count)
	for i := 0; i < count; i++ {
		next := read(i + 1)
		if next < last || uint64(next) > uint64(len(data)-start)+1 {
			return nil, 0, cffError("invalid INDEX object offsets")
		}
		objects[i] = data[start+int(last)-1 : start+int(next)-1]
		last = next
	}
	return objects, start + int(last) - 1, nil
}

func encodeCFFIndex(objects [][]byte) []byte {
	var out buffer
	out.u16(uint16(len(objects)))
	if len(objects) == 0 {
		return out.Bytes()
	}
	out.WriteByte(4) // A fixed width keeps relocation independent of its values.
	offset := uint32(1)
	out.u32(offset)
	for _, object := range objects {
		offset += uint32(len(object))
		out.u32(offset)
	}
	for _, object := range objects {
		out.Write(object)
	}
	return out.Bytes()
}

func readCFFDict(data []byte) ([]cffEntry, error) {
	var entries []cffEntry
	var args []cffOperand
	seen := map[int]bool{}
	for i := 0; i < len(data); {
		start := i
		b := data[i]
		i++
		if b <= 21 {
			op := int(b)
			if b == 12 {
				if i == len(data) {
					return nil, cffError("truncated escaped DICT operator")
				}
				op = 1200 + int(data[i])
				i++
			}
			if seen[op] {
				return nil, cffError("duplicate DICT operator")
			}
			seen[op] = true
			entries = append(entries, cffEntry{op: op, args: args})
			args = nil
			continue
		}
		operand := cffOperand{isInteger: true}
		switch {
		case b == 28:
			if len(data)-i < 2 {
				return nil, cffError("truncated DICT short integer")
			}
			operand.integer = int(int16(binary.BigEndian.Uint16(data[i:])))
			i += 2
		case b == 29:
			if len(data)-i < 4 {
				return nil, cffError("truncated DICT long integer")
			}
			operand.integer = int(int32(binary.BigEndian.Uint32(data[i:])))
			i += 4
		case b == 30:
			operand.isInteger = false
			done := false
			for i < len(data) && !done {
				value := data[i]
				i++
				for _, n := range []byte{value >> 4, value & 15} {
					if n == 13 {
						return nil, cffError("invalid DICT real number")
					}
					if n == 15 {
						done = true
						break
					}
				}
			}
			if !done {
				return nil, cffError("unterminated DICT real number")
			}
		case b >= 32 && b <= 246:
			operand.integer = int(b) - 139
		case b >= 247 && b <= 254:
			if i == len(data) {
				return nil, cffError("truncated DICT compact integer")
			}
			if b <= 250 {
				operand.integer = (int(b)-247)*256 + int(data[i]) + 108
			} else {
				operand.integer = -(int(b)-251)*256 - int(data[i]) - 108
			}
			i++
		default:
			return nil, cffError("unsupported DICT operand")
		}
		operand.raw = data[start:i]
		args = append(args, operand)
		if len(args) > 48 {
			return nil, cffError("DICT operand stack exceeds 48 values")
		}
	}
	if len(args) != 0 {
		return nil, cffError("unterminated DICT operands")
	}
	return entries, nil
}

func readPreservedCFF(data []byte, glyphCount int) (preservedCFF, error) {
	var result preservedCFF
	if len(data) < 4 || data[0] != 1 || data[1] != 0 || data[2] < 4 || int(data[2]) > len(data) || data[3] < 1 || data[3] > 4 {
		return result, cffError("only a valid static CFF1 header is supported")
	}
	result.header = data[:int(data[2])]
	names, next, err := readCFFIndex(data, len(result.header))
	if err != nil {
		return result, err
	}
	if len(names) != 1 || len(names[0]) == 0 || len(names[0]) > 127 {
		return result, cffError("exactly one named font is required")
	}
	tops, next, err := readCFFIndex(data, next)
	if err != nil {
		return result, err
	}
	if len(tops) != 1 {
		return result, cffError("exactly one Top DICT is required")
	}
	result.strings, next, err = readCFFIndex(data, next)
	if err != nil {
		return result, err
	}
	if len(result.strings) > cffCustomStringLimit {
		return result, cffError("String INDEX exceeds the CFF SID limit")
	}
	result.globals, next, err = readCFFIndex(data, next)
	if err != nil {
		return result, err
	}
	result.payloadOffset = next
	result.payload = data[next:]
	result.dict, err = readCFFDict(tops[0])
	if err != nil {
		return result, err
	}
	charOffset, charsetOffset := -1, 0
	for _, entry := range result.dict {
		integer := func(index int) (int, bool) {
			if index >= len(entry.args) || !entry.args[index].isInteger {
				return 0, false
			}
			return entry.args[index].integer, true
		}
		switch entry.op {
		case 15, 16, 17:
			value, ok := integer(0)
			if !ok || len(entry.args) != 1 || value < 0 {
				return result, cffError("invalid Top DICT offset")
			}
			if entry.op == 15 {
				charsetOffset = value
			}
			if entry.op == 17 {
				charOffset = value
			}
			predefined := (entry.op == 15 && value <= 2) || (entry.op == 16 && value <= 1)
			if !predefined && (value < next || value >= len(data)) {
				return result, cffError("Top DICT offset outside data section")
			}
			if entry.op == 16 && !predefined {
				if err := validateCFFEncoding(data, value, glyphCount, len(result.strings)); err != nil {
					return result, err
				}
			}
		case 18:
			size, ok := integer(0)
			offset, ok2 := integer(1)
			if !ok || !ok2 || len(entry.args) != 2 || size < 0 || offset < next || offset > len(data) || size > len(data)-offset {
				return result, cffError("invalid Private DICT range")
			}
			private, err := readCFFDict(data[offset : offset+size])
			if err != nil {
				return result, err
			}
			for _, item := range private {
				if item.op == 19 {
					if len(item.args) != 1 || !item.args[0].isInteger || item.args[0].integer < size || item.args[0].integer > len(data)-offset {
						return result, cffError("invalid local subroutine offset")
					}
					if _, _, err = readCFFIndex(data, offset+item.args[0].integer); err != nil {
						return result, err
					}
				}
			}
		case 1206:
			value, ok := integer(0)
			if !ok || len(entry.args) != 1 || value != 2 {
				return result, cffError("only Type 2 charstrings are supported")
			}
		case 1220, 1221, 1223, 1224, 1230, 1231, 1232, 1233, 1234, 1235, 1236, 1237, 1238:
			return result, cffError("CID, synthetic and multiple-master fonts are not supported")
		case 0, 1, 2, 3, 4, 5, 13, 14, 1200, 1201, 1202, 1203, 1204, 1205, 1207, 1208, 1213, 1214, 1222:
			// These entries are values/SIDs, never data-section offsets.
		default:
			return result, cffError("unsupported Top DICT operator")
		}
	}
	if charOffset < 0 {
		return result, cffError("missing CharStrings INDEX")
	}
	result.charstrings, _, err = readCFFIndex(data, charOffset)
	if err != nil {
		return result, err
	}
	if len(result.charstrings) != glyphCount {
		return result, cffError("glyph count differs from maxp")
	}
	result.charset, err = readCFFCharset(data, charsetOffset, glyphCount, len(result.strings))
	if err != nil {
		return result, err
	}
	return result, nil
}

func validateCFFEncoding(data []byte, offset, glyphCount, stringCount int) error {
	if offset < 0 || len(data)-offset < 2 {
		return cffError("truncated encoding")
	}
	format, count := data[offset], int(data[offset+1])
	offset += 2
	seen := map[byte]bool{}
	glyphs := 0
	add := func(code byte) bool {
		if seen[code] {
			return false
		}
		seen[code] = true
		glyphs++
		return true
	}
	switch format & 0x7f {
	case 0:
		if count > len(data)-offset {
			return cffError("truncated encoding codes")
		}
		for _, code := range data[offset : offset+count] {
			if !add(code) {
				return cffError("duplicate encoding code")
			}
		}
		offset += count
	case 1:
		if count > (len(data)-offset)/2 {
			return cffError("truncated encoding ranges")
		}
		for i := 0; i < count; i++ {
			first, n := int(data[offset]), int(data[offset+1])
			offset += 2
			if first+n > 255 {
				return cffError("invalid encoding range")
			}
			for c := first; c <= first+n; c++ {
				if !add(byte(c)) {
					return cffError("duplicate encoding code")
				}
			}
		}
	default:
		return cffError("unsupported encoding format")
	}
	if glyphs > glyphCount-1 {
		return cffError("encoding exceeds glyph count")
	}
	if format&0x80 != 0 {
		if offset >= len(data) {
			return cffError("truncated encoding supplement count")
		}
		count = int(data[offset])
		offset++
		if count > (len(data)-offset)/3 {
			return cffError("truncated encoding supplements")
		}
		for i := 0; i < count; i++ {
			code := data[offset]
			sid := int(binary.BigEndian.Uint16(data[offset+1:]))
			offset += 3
			if seen[code] || sid == 0 || sid >= 65000 || sid >= 391+stringCount {
				return cffError("invalid encoding supplement")
			}
			seen[code] = true
		}
	}
	return nil
}

func readCFFCharset(data []byte, offset, glyphCount, stringCount int) ([]uint16, error) {
	if glyphCount < 1 || glyphCount > 65535 {
		return nil, cffError("invalid charset glyph count")
	}
	charset := make([]uint16, 0, glyphCount-1)
	if offset == 0 {
		if glyphCount > 229 {
			return nil, cffError("ISOAdobe charset is too short")
		}
		for gid := 1; gid < glyphCount; gid++ {
			charset = append(charset, uint16(gid))
		}
		return charset, nil
	}
	if offset == 1 || offset == 2 {
		return nil, cffError("predefined Expert charsets are not supported")
	}
	if offset < 0 || offset >= len(data) {
		return nil, cffError("invalid charset offset")
	}
	format := data[offset]
	offset++
	if format > 2 {
		return nil, cffError("unsupported charset format")
	}
	seen := map[uint16]bool{0: true}
	for len(charset) < glyphCount-1 {
		if len(data)-offset < 2 {
			return nil, cffError("truncated charset")
		}
		first := int(binary.BigEndian.Uint16(data[offset:]))
		offset += 2
		count := 1
		if format == 1 {
			if offset == len(data) {
				return nil, cffError("truncated charset range")
			}
			count += int(data[offset])
			offset++
		}
		if format == 2 {
			if len(data)-offset < 2 {
				return nil, cffError("truncated charset range")
			}
			count += int(binary.BigEndian.Uint16(data[offset:]))
			offset += 2
		}
		if count > glyphCount-1-len(charset) || first > 65535-count+1 {
			return nil, cffError("charset range exceeds glyph count")
		}
		for sid := first; sid < first+count; sid++ {
			if sid >= 65000 || sid >= 391+stringCount || seen[uint16(sid)] {
				return nil, cffError("invalid or duplicate glyph SID")
			}
			seen[uint16(sid)] = true
			charset = append(charset, uint16(sid))
		}
	}
	return charset, nil
}

func extendPreservedCFF(data []byte, oldCount, newCount int, postScript string) ([]byte, error) {
	source, err := readPreservedCFF(data, oldCount)
	if err != nil {
		return nil, err
	}
	added := newCount - oldCount
	if added < 0 || newCount > 65535 || len(source.strings)+added > cffCustomStringLimit {
		return nil, cffError("added glyphs exceed CFF limits")
	}
	strings := append([][]byte(nil), source.strings...)
	charstrings := append([][]byte(nil), source.charstrings...)
	charset := append([]uint16(nil), source.charset...)
	usedNames := map[string]bool{}
	for _, name := range strings {
		usedNames[string(name)] = true
	}
	for i := 0; i < added; i++ {
		name := "OpenAIImage" + strconv.Itoa(i)
		for usedNames[name] {
			name = "_" + name
		}
		if len(name) > 127 {
			return nil, cffError("cannot allocate a unique image glyph name")
		}
		usedNames[name] = true
		charset = append(charset, uint16(391+len(strings)))
		strings = append(strings, []byte(name))
		charstrings = append(charstrings, []byte{14}) // Type 2 endchar: an empty outline.
	}
	var charsetData buffer
	charsetData.WriteByte(0)
	for _, sid := range charset {
		charsetData.u16(sid)
	}
	charData := encodeCFFIndex(charstrings)
	nameIndex := encodeCFFIndex([][]byte{[]byte(postScript)})
	stringIndex := encodeCFFIndex(strings)
	globalIndex := encodeCFFIndex(source.globals)
	// Fixed-size DICT integers make the prefix length stable after relocation.
	makeDict := func(delta, charsetOffset, charOffset int) []byte {
		var out buffer
		hasCharset := false
		integer := func(value int) { out.WriteByte(29); out.u32(uint32(int32(value))) }
		for _, entry := range source.dict {
			switch entry.op {
			case 15:
				integer(charsetOffset)
				hasCharset = true
			case 17:
				integer(charOffset)
			case 16:
				value := entry.args[0].integer
				if value > 1 {
					value += delta
				}
				integer(value)
			case 18:
				out.Write(entry.args[0].raw)
				integer(entry.args[1].integer + delta)
			default:
				for _, arg := range entry.args {
					out.Write(arg.raw)
				}
			}
			if entry.op >= 1200 {
				out.WriteByte(12)
				out.WriteByte(byte(entry.op - 1200))
			} else {
				out.WriteByte(byte(entry.op))
			}
		}
		if !hasCharset {
			integer(charsetOffset)
			out.WriteByte(15)
		}
		return out.Bytes()
	}
	placeholder := encodeCFFIndex([][]byte{makeDict(0, 0, 0)})
	prefixLength := len(source.header) + len(nameIndex) + len(placeholder) + len(stringIndex) + len(globalIndex)
	delta := prefixLength - source.payloadOffset
	charsetOffset := prefixLength + len(source.payload)
	charOffset := charsetOffset + charsetData.Len()
	topIndex := encodeCFFIndex([][]byte{makeDict(delta, charsetOffset, charOffset)})
	if len(topIndex) != len(placeholder) {
		return nil, cffError("unstable Top DICT relocation")
	}
	result := make([]byte, 0, charOffset+len(charData))
	header := append([]byte(nil), source.header...)
	// The grown font may need offsets wider than the source Header's offSize.
	// Keep its declared absolute-offset capacity consistent with our 32-bit
	// rebuilt INDEXes and DICT offsets, without mutating the source header.
	header[3] = 4
	for _, part := range [][]byte{header, nameIndex, topIndex, stringIndex, globalIndex, source.payload, charsetData.Bytes(), charData} {
		result = append(result, part...)
	}
	return result, nil
}
