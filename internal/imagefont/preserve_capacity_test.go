package imagefont

import (
	"bytes"
	"encoding/binary"
	"strconv"
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
				Tables:    map[string][]byte{"CFF ": cff, "maxp": {0, 0, 0x50, 0, 0, 2}},
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
				Tables:    map[string][]byte{"CFF ": tc.cff, "maxp": {0, 0, 0x50, 0, 0, tc.glyphCount}},
				PointSize: 12, CellWidth: 8, CellHeight: 16,
			}
			if _, err := PreservedGlyphCapacity(source); err == nil {
				t.Fatal("invalid CFF capacity accepted")
			}
		})
	}
}
