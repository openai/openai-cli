package imagefont

import (
	"encoding/binary"
	"fmt"
)

// preservedVariableMeta removes Apple's opaque identity-bound font metadata
// from a renamed variable face. Retaining that record makes CoreText classify
// SF Mono Terminal Regular as Light, even when its selected outlines are exact.
// Language declarations and all other metadata retain their original bytes.
// Table layout: https://learn.microsoft.com/typography/opentype/spec/meta
func preservedVariableMeta(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	bad := func() ([]byte, error) {
		return nil, fmt.Errorf("cannot preserve this font's metadata: invalid meta table")
	}
	if len(data) < 16 || binary.BigEndian.Uint32(data) != 1 || binary.BigEndian.Uint32(data[4:]) != 0 {
		return bad()
	}
	count := uint64(binary.BigEndian.Uint32(data[12:]))
	if count > uint64((len(data)-16)/12) {
		return bad()
	}
	var kept []int
	for i := 0; i < int(count); i++ {
		p := 16 + i*12
		offset := uint64(binary.BigEndian.Uint32(data[p+4:]))
		length := uint64(binary.BigEndian.Uint32(data[p+8:]))
		if offset > uint64(len(data)) || length > uint64(len(data))-offset || length > 0 && offset < 16+count*12 {
			return bad()
		}
		if string(data[p:p+4]) != "appl" {
			kept = append(kept, p)
		}
	}
	if len(kept) == int(count) {
		return append([]byte(nil), data...), nil
	}
	if len(kept) == 0 {
		return nil, nil
	}
	oldPayload := 16 + int(count)*12
	newPayload := 16 + len(kept)*12
	delta := oldPayload - newPayload
	// Keep one copy of the original payload arena. Metadata records may alias
	// or overlap the same bytes; copying each range separately can otherwise
	// amplify a bounded source table into an arbitrarily large allocation.
	// The removed appl payload is unreferenced and has no metadata semantics.
	result := make([]byte, len(data)-delta)
	copy(result[newPayload:], data[oldPayload:])
	binary.BigEndian.PutUint32(result, 1)
	binary.BigEndian.PutUint32(result[12:], uint32(len(kept)))
	for i, old := range kept {
		p := 16 + i*12
		offset, length := int(binary.BigEndian.Uint32(data[old+4:])), int(binary.BigEndian.Uint32(data[old+8:]))
		newOffset := newPayload
		if length > 0 {
			newOffset = offset - delta
		}
		copy(result[p:p+4], data[old:old+4])
		binary.BigEndian.PutUint32(result[p+4:], uint32(newOffset))
		binary.BigEndian.PutUint32(result[p+8:], uint32(length))
	}
	return result, nil
}
