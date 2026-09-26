package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/image/font/sfnt"
)

type preservationCmapRecord struct {
	platform, encoding uint16
	data               []byte
}

func preservationCmap(records ...preservationCmapRecord) []byte {
	var out buffer
	out.u16(0)
	out.u16(uint16(len(records)))
	offset := 4 + len(records)*8
	for _, record := range records {
		out.u16(record.platform)
		out.u16(record.encoding)
		out.u32(uint32(offset))
		offset += len(record.data)
	}
	for _, record := range records {
		out.Write(record.data)
	}
	return out.Bytes()
}

func preservationCmapSubtable(format uint16, mapping map[uint32]uint32) []byte {
	keys := make([]uint32, 0, len(mapping))
	for cp := range mapping {
		keys = append(keys, cp)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var out buffer
	out.u16(format)
	if format != 4 {
		out.u16(0)
		out.u32(uint32(16 + 12*len(keys)))
		out.u32(0)
		out.u32(uint32(len(keys)))
		for _, cp := range keys {
			out.u32(cp)
			out.u32(cp)
			out.u32(mapping[cp])
		}
		return out.Bytes()
	}
	segments := len(keys) + 1
	searchRange, selector := 1, 0
	for searchRange*2 <= segments {
		searchRange *= 2
		selector++
	}
	out.u16(uint16(16 + segments*8))
	out.u16(0)
	out.u16(uint16(segments * 2))
	out.u16(uint16(searchRange * 2))
	out.u16(uint16(selector))
	out.u16(uint16((segments - searchRange) * 2))
	for _, cp := range keys {
		out.u16(uint16(cp))
	}
	out.u16(0xffff)
	out.u16(0)
	for _, cp := range keys {
		out.u16(uint16(cp))
	}
	out.u16(0xffff)
	for _, cp := range keys {
		out.u16(uint16(mapping[cp]) - uint16(cp))
	}
	out.u16(1)
	out.zeros(segments * 2)
	return out.Bytes()
}

func TestEncodePreservingMergesUnicodeCmapCoverage(t *testing.T) {
	for _, format := range []uint16{4, 12, 13} {
		for _, first := range []bool{false, true} {
			t.Run(fmtCmapCase(format, first), func(t *testing.T) {
				source := preservationSource(t)
				source.LegacyAliases = true
				base, err := readPreservedCmap(source.Tables["cmap"], baseGlyphCount)
				if err != nil {
					t.Fatal(err)
				}
				extra := map[uint32]uint32{uint32(FirstCodepoint): base['A'], 'W': base['W'], 'B': 0}
				encoding := uint16(3)
				if format == 12 {
					encoding = 4
					extra[0x1d400] = base['B']
				} else if format == 13 {
					encoding = 6
					extra[0x1d400] = base['B']
				}
				records := []preservationCmapRecord{
					{3, 10, preservationCmapSubtable(12, base)},
					{0, encoding, preservationCmapSubtable(format, extra)},
				}
				if first {
					records[0], records[1] = records[1], records[0]
				}
				source.Tables["cmap"] = preservationCmap(records...)
				before := bytes.Clone(source.Tables["cmap"])
				result, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
				if err != nil {
					t.Fatal(err)
				}
				parsed, err := sfnt.Parse(result.Data)
				if err != nil {
					t.Fatal(err)
				}
				for cp, gid := range extra {
					if gid != 0 {
						base[cp] = gid
					}
				}
				for cp, want := range base {
					got, err := parsed.GlyphIndex(nil, rune(cp))
					if err != nil || uint32(got) != want {
						t.Fatalf("U+%X changed: got %d, want %d: %v", cp, got, want, err)
					}
				}
				if !bytes.Equal(before, source.Tables["cmap"]) {
					t.Fatal("source cmap changed")
				}
			})
		}
	}
}

func fmtCmapCase(format uint16, first bool) string {
	return fmt.Sprintf("format%d/first=%t", format, first)
}

func TestEncodePreservingRejectsConflictingUnicodeCmaps(t *testing.T) {
	for _, format := range []uint16{4, 12, 13} {
		t.Run(fmtCmapCase(format, false), func(t *testing.T) {
			source := preservationSource(t)
			base, err := readPreservedCmap(source.Tables["cmap"], baseGlyphCount)
			if err != nil {
				t.Fatal(err)
			}
			source.Tables["cmap"] = preservationCmap(
				preservationCmapRecord{3, 10, preservationCmapSubtable(12, base)},
				preservationCmapRecord{0, 4, preservationCmapSubtable(format, map[uint32]uint32{'A': base['B']})},
			)
			if _, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source); err == nil || !strings.Contains(err.Error(), "conflicting Unicode cmap") {
				t.Fatalf("conflicting source mappings accepted: %v", err)
			}
		})
	}
}

func TestEncodePreservingRejectsInvalidSecondaryUnicodeCmap(t *testing.T) {
	format4 := preservationCmapSubtable(4, map[uint32]uint32{'A': 1})
	oddSegments := bytes.Clone(format4)
	binary.BigEndian.PutUint16(oddSegments[6:], 5)
	invalidRange := bytes.Clone(format4)
	binary.BigEndian.PutUint16(invalidRange[28:], 0xffff)
	for name, sub := range map[string][]byte{
		"truncated format 4":         format4[:10],
		"truncated format 12":        {0, 12},
		"unsupported format 6":       {0, 6, 0, 10, 0, 0, 0, 0, 0, 0},
		"invalid format 4 glyph":     preservationCmapSubtable(4, map[uint32]uint32{'A': baseGlyphCount}),
		"invalid format 12 glyph":    preservationCmapSubtable(12, map[uint32]uint32{'A': baseGlyphCount}),
		"invalid format 13 glyph":    preservationCmapSubtable(13, map[uint32]uint32{'A': baseGlyphCount}),
		"odd segment count":          oddSegments,
		"invalid glyph array offset": invalidRange,
	} {
		t.Run(name, func(t *testing.T) {
			source := preservationSource(t)
			base, err := readPreservedCmap(source.Tables["cmap"], baseGlyphCount)
			if err != nil {
				t.Fatal(err)
			}
			source.Tables["cmap"] = preservationCmap(
				preservationCmapRecord{3, 10, preservationCmapSubtable(12, base)},
				preservationCmapRecord{0, 4, sub},
			)
			if _, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source); err == nil {
				t.Fatal("invalid secondary mapping accepted")
			}
		})
	}
}

func TestReadPreservedCmapSharedSubtable(t *testing.T) {
	source := preservationSource(t)
	want, err := readPreservedCmap(source.Tables["cmap"], baseGlyphCount)
	if err != nil {
		t.Fatal(err)
	}
	data := preservationCmap(preservationCmapRecord{0, 4, preservationCmapSubtable(12, want)}, preservationCmapRecord{3, 10, nil})
	binary.BigEndian.PutUint32(data[16:], binary.BigEndian.Uint32(data[8:]))
	got, err := readPreservedCmap(data, baseGlyphCount)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("shared mapping changed: %v", err)
	}
}

func TestEncodePreservingRejectsSecondaryImageCharacterCollision(t *testing.T) {
	for _, format := range []uint16{12, 13} {
		t.Run(fmtCmapCase(format, false), func(t *testing.T) {
			source := preservationSource(t)
			base, err := readPreservedCmap(source.Tables["cmap"], baseGlyphCount)
			if err != nil {
				t.Fatal(err)
			}
			source.Tables["cmap"] = preservationCmap(
				preservationCmapRecord{3, 10, preservationCmapSubtable(12, base)},
				preservationCmapRecord{0, 4, preservationCmapSubtable(format, map[uint32]uint32{uint32(FirstSupplementaryCodepoint): base['A']})},
			)
			if _, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source); err == nil || !strings.Contains(err.Error(), "already uses") {
				t.Fatalf("source image character overwritten: %v", err)
			}
		})
	}
}

func TestReadPreservedCmapFormat4GlyphArray(t *testing.T) {
	// The first segment covers A..C, with a missing B. A nonzero idDelta
	// adjusts only nonzero array entries; it must not create a mapping for B.
	sub := preservationCmapSubtable(4, map[uint32]uint32{'A': 1})
	binary.BigEndian.PutUint16(sub[14:], 'C')
	binary.BigEndian.PutUint16(sub[24:], 1)
	binary.BigEndian.PutUint16(sub[28:], 4)
	sub = append(sub, 0, 1, 0, 0, 0, 2)
	binary.BigEndian.PutUint16(sub[2:], uint16(len(sub)))
	want := map[uint32]uint32{'A': 2, 'C': 3}
	got, err := readPreservedCmap(preservationCmap(preservationCmapRecord{3, 1, sub}), baseGlyphCount)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("glyph array changed: got %v, want %v: %v", got, want, err)
	}
	// The offset must point into glyphIdArray, not another idRangeOffset word.
	binary.BigEndian.PutUint16(sub[28:], 2)
	if _, err := readPreservedCmap(preservationCmap(preservationCmapRecord{3, 1, sub}), baseGlyphCount); err == nil {
		t.Fatal("glyph array points into segment metadata")
	}
}

func TestEncodePreservingKeepsAuxiliaryCmaps(t *testing.T) {
	source := preservationSource(t)
	base, err := readPreservedCmap(source.Tables["cmap"], baseGlyphCount)
	if err != nil {
		t.Fatal(err)
	}
	var variation, mac buffer
	variation.u16(14)
	variation.u32(38)
	variation.u32(1)
	variation.Write([]byte{0, 0xfe, 0x0f}) // U+FE0F selector.
	variation.u32(21)
	variation.u32(29)
	variation.u32(1)
	variation.Write([]byte{0, 0, 'A', 0}) // Default mapping for A.
	variation.u32(1)
	variation.Write([]byte{0, 0, 'B'})
	variation.u16(uint16(base['C'])) // Non-default mapping for B.
	mac.u16(0)
	mac.u16(262)
	mac.u16(0)
	mac.zeros(256)
	mac.Bytes()[6+'A'] = byte(base['A'])
	source.Tables["cmap"] = preservationCmap(
		preservationCmapRecord{0, 4, preservationCmapSubtable(12, base)},
		preservationCmapRecord{0, 5, variation.Bytes()},
		preservationCmapRecord{1, 0, mac.Bytes()},
	)
	result, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	got := fontTables(t, result.Data)["cmap"]
	remaining := map[uint32][]byte{5: variation.Bytes(), 1 << 16: mac.Bytes()}
	for i := 0; i < int(binary.BigEndian.Uint16(got[2:])); i++ {
		p := 4 + i*8
		key := binary.BigEndian.Uint32(got[p:])
		if want, exists := remaining[key]; exists {
			off := int(binary.BigEndian.Uint32(got[p+4:]))
			if off+len(want) > len(got) || !bytes.Equal(got[off:off+len(want)], want) {
				t.Fatalf("auxiliary cmap %x changed", key)
			}
			delete(remaining, key)
		}
	}
	if len(remaining) != 0 {
		t.Fatalf("auxiliary cmaps lost: %v", remaining)
	}
}
