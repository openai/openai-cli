package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"strconv"
	"strings"
	"testing"
)

func TestPreservedGlyphCapacityUsesFontAndBitmapLimits(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		count, width, height, want int
	}{
		{"private character range", 96, 8, 16, MaxGlyphs},
		{"source glyph boundary", 65535 - 1024, 8, 16, 1024},
		{"source glyph exhausted", 65535, 8, 16, 0},
		{"bitmap boundary", 96, 64, 64, 1024},
		{"bitmap rounds down", 96, 65, 64, 1008},
		{"font wins over bitmap", 65535 - 100, 64, 64, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := preservationSource(t)
			binary.BigEndian.PutUint16(source.Tables["maxp"][4:], uint16(tc.count))
			source.CellWidth, source.CellHeight = tc.width, tc.height
			got, err := PreservedGlyphCapacity(source)
			if err != nil || got != tc.want {
				t.Fatalf("capacity = %d, error = %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestPreservedGlyphCapacityRejectsUnknownSourceBounds(t *testing.T) {
	for _, tc := range []string{"missing maxp", "empty count", "invalid geometry"} {
		t.Run(tc, func(t *testing.T) {
			source := preservationSource(t)
			switch tc {
			case "missing maxp":
				delete(source.Tables, "maxp")
			case "empty count":
				binary.BigEndian.PutUint16(source.Tables["maxp"][4:], 0)
			case "invalid geometry":
				source.CellWidth = 0
			}
			if _, err := PreservedGlyphCapacity(source); err == nil {
				t.Fatal("unknown font capacity accepted")
			}
		})
	}
}

func TestPreservedGlyphCapacityIncludesCFFStrings(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		remaining, cellWidth, cellHeight, want int
	}{
		{"last image glyph names", 4, 8, 16, 4},
		{"progress and final boundary", 1032, 8, 16, 1032},
		{"exhausted image glyph names", 0, 8, 16, 0},
		{"private characters remain limiting", 8000, 8, 16, MaxGlyphs},
		{"bitmap budget remains limiting", 2048, 64, 64, 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			customStrings := make([][]byte, cffCustomStringLimit-tc.remaining)
			for i := range customStrings {
				customStrings[i] = []byte("source metadata " + strconv.Itoa(i))
			}
			cff := cffFixtureWithStrings(customStrings)
			before := bytes.Clone(cff)
			source := PreserveOptions{
				Tables: map[string][]byte{
					"CFF ": cff,
					"maxp": {0, 0, 0x50, 0, 0, 2},
					"cmap": preservationCmap(preservationCmapRecord{3, 10, preservationCmapSubtable(12, map[uint32]uint32{'A': 1})}),
				},
				PointSize: 12, CellWidth: tc.cellWidth, CellHeight: tc.cellHeight,
			}
			capacity, err := PreservedGlyphCapacity(source)
			if err != nil || capacity != tc.want {
				t.Fatalf("capacity = %d, error = %v; want %d", capacity, err, tc.want)
			}
			if !bytes.Equal(cff, before) {
				t.Fatal("capacity inspection changed source font bytes")
			}
			if tc.want == tc.remaining {
				// The advertised last available cell must be encodable, and the
				// next cell must fail the same CFF format limit used for admission.
				extended, err := extendPreservedCFF(cff, 2, 2+capacity, "TestCapacity")
				if err != nil {
					t.Fatalf("declared capacity could not be encoded: %v", err)
				}
				if _, err := readPreservedCFF(extended, 2+capacity); err != nil {
					t.Fatalf("extended font is invalid: %v", err)
				}
				if _, err := extendPreservedCFF(cff, 2, 3+capacity, "TestCapacity"); err == nil {
					t.Fatal("encoder accepted a cell beyond the declared CFF limit")
				}
			}
		})
	}
}

func TestPreservedGlyphCapacityRejectsInvalidCFF(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cff        []byte
		glyphCount byte
	}{
		{"empty table", nil, 2},
		{"truncated index", cffFixture()[:7], 2},
		{"inconsistent glyph count", cffFixture(), 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := PreserveOptions{
				Tables: map[string][]byte{
					"CFF ": tc.cff,
					"maxp": {0, 0, 0x50, 0, 0, tc.glyphCount},
					"cmap": preservationCmap(preservationCmapRecord{3, 10, preservationCmapSubtable(12, map[uint32]uint32{'A': 1})}),
				},
				PointSize: 12, CellWidth: 8, CellHeight: 16,
			}
			if _, err := PreservedGlyphCapacity(source); err == nil {
				t.Fatal("invalid CFF capacity accepted")
			}
		})
	}
}

func TestPreservedGlyphCapacityStopsBeforeSourceCharacters(t *testing.T) {
	for _, tc := range []struct {
		name      string
		codepoint uint32
		glyph     uint32
		want      int
	}{
		{"first image character", uint32(FirstSupplementaryCodepoint), 1, 0},
		{"middle image character", uint32(FirstSupplementaryCodepoint) + 768, 1, 768},
		{"last image character", uint32(LastSupplementaryCodepoint), 1, MaxGlyphs - 1},
		{"before image range", uint32(FirstSupplementaryCodepoint) - 1, 1, MaxGlyphs},
		{"after image range", uint32(LastSupplementaryCodepoint) + 1, 1, MaxGlyphs},
		{"missing glyph is not occupied", uint32(FirstSupplementaryCodepoint), 0, MaxGlyphs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := preservationSource(t)
			source.Tables["cmap"] = preservationCmap(preservationCmapRecord{3, 10, preservationCmapSubtable(12, map[uint32]uint32{tc.codepoint: tc.glyph})})
			before := bytes.Clone(source.Tables["cmap"])
			capacity, err := PreservedGlyphCapacity(source)
			if err != nil || capacity != tc.want {
				t.Fatalf("capacity = %d, error = %v; want %d", capacity, err, tc.want)
			}
			if !bytes.Equal(before, source.Tables["cmap"]) {
				t.Fatal("capacity inspection changed source character mappings")
			}
		})
	}
}

func TestPreservedGlyphCapacityIncludesSecondaryUnicodeMappings(t *testing.T) {
	for _, format := range []uint16{12, 13} {
		t.Run(strconv.Itoa(int(format)), func(t *testing.T) {
			source := preservationSource(t)
			source.Tables["cmap"] = preservationCmap(
				preservationCmapRecord{3, 10, preservationCmapSubtable(12, map[uint32]uint32{'A': 1, uint32(FirstSupplementaryCodepoint) + 1024: 1})},
				preservationCmapRecord{0, 4, preservationCmapSubtable(format, map[uint32]uint32{uint32(FirstSupplementaryCodepoint) + 768: 1})},
			)
			capacity, err := PreservedGlyphCapacity(source)
			if err != nil || capacity != 768 {
				t.Fatalf("capacity = %d, error = %v; want 768", capacity, err)
			}
		})
	}
}

func TestPreservedGlyphCapacityMatchesSourceCollisionBoundary(t *testing.T) {
	source := preservationSource(t)
	mapping, err := readPreservedCmap(source.Tables["cmap"], baseGlyphCount)
	if err != nil {
		t.Fatal(err)
	}
	mapping[uint32(FirstSupplementaryCodepoint)+4] = mapping['A']
	source.Tables["cmap"] = preservationCmap(preservationCmapRecord{3, 10, preservationCmapSubtable(12, mapping)})
	capacity, err := PreservedGlyphCapacity(source)
	if err != nil || capacity != 4 {
		t.Fatalf("capacity = %d, error = %v; want 4", capacity, err)
	}
	frames := preservationFrames()
	if _, err := EncodePreserving(context.Background(), frames, testOptions, source); err != nil {
		t.Fatalf("the four unoccupied characters must remain usable: %v", err)
	}
	frames = append(frames, Frame{Image: solid(image.Rect(0, 0, 1, 1), color.NRGBA{A: 255}), Columns: 1, Rows: 1})
	if _, err := EncodePreserving(context.Background(), frames, testOptions, source); err == nil || !strings.Contains(err.Error(), "already uses") {
		t.Fatalf("the next character must retain its original mapping: %v", err)
	}
}

func TestPreservedGlyphCapacityRejectsInvalidCmap(t *testing.T) {
	valid := preservationCmap(preservationCmapRecord{3, 10, preservationCmapSubtable(12, map[uint32]uint32{'A': 1})})
	for _, tc := range []struct {
		name string
		cmap []byte
	}{
		{"missing table", nil},
		{"truncated header", valid[:3]},
		{"truncated subtable", valid[:len(valid)-1]},
		{"invalid glyph index", preservationCmap(preservationCmapRecord{3, 10, preservationCmapSubtable(12, map[uint32]uint32{'A': baseGlyphCount})})},
		{"conflicting secondary mappings", preservationCmap(
			preservationCmapRecord{3, 10, preservationCmapSubtable(12, map[uint32]uint32{uint32(FirstSupplementaryCodepoint): 1})},
			preservationCmapRecord{0, 4, preservationCmapSubtable(12, map[uint32]uint32{uint32(FirstSupplementaryCodepoint): 2})},
		)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := preservationSource(t)
			source.Tables["cmap"] = tc.cmap
			if _, err := PreservedGlyphCapacity(source); err == nil {
				t.Fatal("unknown or conflicting source character coverage was accepted")
			}
		})
	}
}
