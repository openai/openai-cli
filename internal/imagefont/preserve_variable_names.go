package imagefont

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"unicode/utf16"
)

// preservedVariableIdentity keeps the selected named variation reachable by
// its private PostScript name. Renaming only name ID 6 would instead select the
// variable font's default instance, which can have a different weight.
// The caller supplies already copied tables; source font files are untouched.
func preservedVariableIdentity(tables map[string][]byte, source PreserveOptions, options Options) (Options, map[uint16]string, error) {
	fvar := tables["fvar"]
	if len(fvar) == 0 {
		return options, nil, nil
	}
	fail := func(reason string) (Options, map[uint16]string, error) {
		return Options{}, nil, fmt.Errorf("cannot preserve this font's selected variation: %s", reason)
	}
	if err := validateNames(options); err != nil {
		return Options{}, nil, err
	}
	if len(fvar) < 16 || binary.BigEndian.Uint32(fvar) != 0x00010000 || binary.BigEndian.Uint16(fvar[6:]) != 2 {
		return fail("invalid fvar header")
	}
	axisOffset := int(binary.BigEndian.Uint16(fvar[4:]))
	axisCount := int(binary.BigEndian.Uint16(fvar[8:]))
	axisSize := int(binary.BigEndian.Uint16(fvar[10:]))
	instanceCount := int(binary.BigEndian.Uint16(fvar[12:]))
	instanceSize := int(binary.BigEndian.Uint16(fvar[14:]))
	coordinatesSize := 4 + axisCount*4
	if axisOffset < 16 || axisOffset > len(fvar) || axisCount == 0 || axisSize != 20 || axisCount > (len(fvar)-axisOffset)/axisSize {
		return fail("invalid fvar axes")
	}
	instancesStart := axisOffset + axisCount*axisSize
	if instanceSize != coordinatesSize && instanceSize != coordinatesSize+2 || instanceCount > (len(fvar)-instancesStart)/instanceSize {
		return fail("invalid fvar instances")
	}
	names, err := preservedVariableNameStrings(tables["name"])
	if err != nil {
		return fail(err.Error())
	}
	// Name IDs used as human-readable axis/subfamily labels cannot also be
	// rewritten as PostScript identities without changing the selected style.
	labelIDs := make(map[uint16]bool)
	axisTags := make(map[uint32]bool)
	for a := 0; a < axisCount; a++ {
		p := axisOffset + a*axisSize
		tag := binary.BigEndian.Uint32(fvar[p:])
		low, def, high := int32(binary.BigEndian.Uint32(fvar[p+4:])), int32(binary.BigEndian.Uint32(fvar[p+8:])), int32(binary.BigEndian.Uint32(fvar[p+12:]))
		if axisTags[tag] || low > def || def > high {
			return fail("invalid fvar axis range")
		}
		axisTags[tag] = true
		labelIDs[binary.BigEndian.Uint16(fvar[p+18:])] = true
	}
	for i := 0; i < instanceCount; i++ {
		labelIDs[binary.BigEndian.Uint16(fvar[instancesStart+i*instanceSize:])] = true
	}
	contains := func(id uint16, wanted string) bool {
		for _, name := range names[id] {
			if name == wanted {
				return true
			}
		}
		return false
	}
	digest := sha256.Sum256([]byte(options.PostScript))
	prefix := fmt.Sprintf("OAI%x", digest[:16]) // Alphanumeric, well below the 63-byte PS limit.
	overrides := map[uint16]string{25: prefix}
	selected := -1
	selectedID := uint16(0)
	for i := 0; i < instanceCount; i++ {
		p := instancesStart + i*instanceSize
		for a := 0; a < axisCount; a++ {
			coordinate := int32(binary.BigEndian.Uint32(fvar[p+4+a*4:]))
			axis := axisOffset + a*axisSize
			if coordinate < int32(binary.BigEndian.Uint32(fvar[axis+4:])) || coordinate > int32(binary.BigEndian.Uint32(fvar[axis+12:])) {
				return fail("named instance lies outside its axis range")
			}
		}
		if instanceSize == coordinatesSize {
			continue
		}
		id := binary.BigEndian.Uint16(fvar[p+coordinatesSize:])
		if id == 0xffff {
			continue
		}
		if id != 6 && id < 256 || labelIDs[id] || len(names[id]) == 0 {
			return fail("invalid or conflicting named-instance PostScript name")
		}
		if id != 6 {
			overrides[id] = fmt.Sprintf("%sI%d", prefix, i)
		}
		if contains(id, source.SourcePostScript) {
			if selected >= 0 {
				return fail("ambiguous named instance")
			}
			selected, selectedID = i, id
		}
	}
	if selected < 0 && !contains(6, source.SourcePostScript) {
		return fail("the exact named instance was not found")
	}
	if len(source.Variations) > 0 {
		for key, value := range source.Variations {
			tag, err := strconv.ParseUint(key, 10, 32)
			if err != nil || key != strconv.FormatUint(tag, 10) || !axisTags[uint32(tag)] || math.IsNaN(value) || math.IsInf(value, 0) {
				return fail("unknown or invalid selected variation axis")
			}
		}
		for a := 0; a < axisCount; a++ {
			p := axisOffset + a*axisSize
			def := float64(int32(binary.BigEndian.Uint32(fvar[p+8:]))) / 65536
			expected := def
			if selected >= 0 {
				expected = float64(int32(binary.BigEndian.Uint32(fvar[instancesStart+selected*instanceSize+4+a*4:]))) / 65536
			}
			actual, exists := source.Variations[strconv.FormatUint(uint64(binary.BigEndian.Uint32(fvar[p:])), 10)]
			if !exists {
				actual = def
			}
			// CoreText truncates some named coordinates to four decimal places.
			// This accepts that representation, while retaining exact fvar bytes.
			if math.Abs(actual-expected) > 0.0002 {
				return fail("selected coordinates do not match the named instance")
			}
		}
	}
	baseOptions := options
	newFvar := append([]byte(nil), fvar[:instancesStart]...)
	selectedDefault := selected < 0
	if selected >= 0 {
		selectedDefault = true
		p := instancesStart + selected*instanceSize
		for a := 0; a < axisCount; a++ {
			if binary.BigEndian.Uint32(fvar[p+4+a*4:]) != binary.BigEndian.Uint32(fvar[axisOffset+a*axisSize+8:]) {
				selectedDefault = false
				break
			}
		}
	}
	if selected >= 0 && selectedID != 6 && !selectedDefault {
		// Keep the instance's fixed-point coordinate bytes exactly. CoreText's
		// reported doubles are rounded and cannot safely recreate these values.
		baseOptions.PostScript = prefix + "Base"
		overrides[selectedID] = options.PostScript
		newFvar = append(newFvar, fvar[instancesStart+selected*instanceSize:instancesStart+(selected+1)*instanceSize]...)
		binary.BigEndian.PutUint16(newFvar[12:], 1)
	} else {
		// A source identified by name ID 6 is the default. Selecting that exact
		// private base name needs no named instance, and avoids duplicate faces.
		if selected >= 0 {
			p := instancesStart + selected*instanceSize
			for a := 0; a < axisCount; a++ {
				if binary.BigEndian.Uint32(fvar[p+4+a*4:]) != binary.BigEndian.Uint32(fvar[axisOffset+a*axisSize+8:]) {
					return fail("default PostScript identity has non-default coordinates")
				}
			}
		}
		binary.BigEndian.PutUint16(newFvar[12:], 0)
	}
	tables["fvar"] = newFvar
	return baseOptions, overrides, nil
}

func preservedVariableNameStrings(table []byte) (map[uint16][]string, error) {
	bad := func() (map[uint16][]string, error) { return nil, fmt.Errorf("invalid name table") }
	if len(table) < 6 || binary.BigEndian.Uint16(table) > 1 {
		return bad()
	}
	count, storage := int(binary.BigEndian.Uint16(table[2:])), int(binary.BigEndian.Uint16(table[4:]))
	if count > (len(table)-6)/12 || storage < 6+count*12 || storage > len(table) {
		return bad()
	}
	result := make(map[uint16][]string)
	for i := 0; i < count; i++ {
		p := 6 + i*12
		platform, encoding := binary.BigEndian.Uint16(table[p:]), binary.BigEndian.Uint16(table[p+2:])
		id := binary.BigEndian.Uint16(table[p+6:])
		length, offset := int(binary.BigEndian.Uint16(table[p+8:])), int(binary.BigEndian.Uint16(table[p+10:]))
		if length > len(table)-storage || offset > len(table)-storage-length {
			return bad()
		}
		data := table[storage+offset : storage+offset+length]
		if platform == 0 || platform == 3 {
			if len(data)%2 != 0 {
				return bad()
			}
			units := make([]uint16, len(data)/2)
			for k := range units {
				units[k] = binary.BigEndian.Uint16(data[k*2:])
			}
			result[id] = append(result[id], string(utf16.Decode(units)))
		} else if platform == 1 && encoding == 0 {
			// PostScript names are ASCII, so no MacRoman conversion is needed.
			ascii := true
			for _, b := range data {
				if b >= 128 {
					ascii = false
					break
				}
			}
			if ascii {
				result[id] = append(result[id], string(data))
			}
		}
	}
	return result, nil
}
