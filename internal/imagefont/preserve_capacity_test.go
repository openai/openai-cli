package imagefont

import (
	"encoding/binary"
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
