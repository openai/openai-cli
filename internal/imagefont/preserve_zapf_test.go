package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"
)

func TestEncodePreservingExtendsZapfGlyphOffsets(t *testing.T) {
	source := preservationSource(t)
	oldCount := int(binary.BigEndian.Uint16(source.Tables["maxp"][4:]))
	oldBody := 8 + 4*oldCount
	var zapf buffer
	zapf.u32(0x10000)
	zapf.u32(uint32(oldBody + 12))
	for i := 0; i < oldCount; i++ {
		zapf.u32(uint32(oldBody))
	}
	zapf.u32(0xffffffff)
	zapf.u32(0xffffffff)
	zapf.u16(0)
	zapf.u16(0)
	source.Tables["Zapf"] = zapf.Bytes()
	before := append([]byte(nil), source.Tables["Zapf"]...)
	encoded, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	tables := fontTables(t, encoded.Data)
	newCount := int(binary.BigEndian.Uint16(tables["maxp"][4:]))
	data := tables["Zapf"]
	if newCount <= oldCount {
		t.Fatal("test did not add image glyphs")
	}
	newBody := 8 + 4*newCount
	for i := 0; i < newCount; i++ {
		if 8+4*i+4 > len(data) {
			t.Fatalf("Zapf offset array truncated at glyph %d", i)
		}
		offset := uint64(binary.BigEndian.Uint32(data[8+4*i:]))
		if offset < uint64(newBody) || offset+12 > uint64(len(data)) {
			t.Fatalf("Zapf glyph %d offset %d is outside glyph metadata after extension", i, offset)
		}
		if !bytes.Equal(data[offset:offset+12], before[oldBody:]) {
			t.Fatalf("Zapf glyph %d metadata changed", i)
		}
	}
	if !bytes.Equal(source.Tables["Zapf"], before) {
		t.Fatal("encoder mutated the source Zapf")
	}
}

func preservedZapfFixture() []byte {
	var out buffer
	out.u32(0x10000)
	out.u32(48) // extraInfo follows both glyph records.
	// Glyphs may share metadata; offsets need not be increasing.
	for _, offset := range []uint32{20, 36, 20} {
		out.u32(offset)
	}
	out.u32(0) // Group offset relative to extraInfo.
	out.u32(8) // Feature offset relative to extraInfo.
	out.u16(1)
	out.u16('A')
	out.u16(0)
	out.u16(0) // Alignment padding.
	out.u32(0xffffffff)
	out.u32(0xffffffff)
	out.u16(0)
	out.u16(0)
	// A group containing glyph 0, then an empty FeatureInfo.
	out.u16(1)
	out.u16(0)
	out.u16(1)
	out.u16(0)
	out.u16(0)
	out.u16(0)
	return out.Bytes()
}

func TestPreservedZapfRetainsMetadataAndAddsEmptyGlyphs(t *testing.T) {
	original := preservedZapfFixture()
	before := append([]byte(nil), original...)
	result, err := extendPreservedZapf(original, 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, before) {
		t.Fatal("source Zapf mutated")
	}
	// The old body moves past three new offsets and one empty record.
	const delta = 3*4 + 12
	if len(result) != len(original)+delta || !bytes.Equal(result[20+delta:], original[20:]) {
		t.Fatal("original Zapf payload changed")
	}
	if binary.BigEndian.Uint32(result) != 0x10000 || binary.BigEndian.Uint32(result[4:]) != 48+delta {
		t.Fatal("Zapf header not preserved and relocated")
	}
	for i, offset := range []uint32{20, 36, 20} {
		got := binary.BigEndian.Uint32(result[8+4*i:])
		if got != offset+delta {
			t.Fatalf("glyph %d metadata offset = %d, want %d", i, got, offset+delta)
		}
	}
	empty := []byte{255, 255, 255, 255, 255, 255, 255, 255, 0, 0, 0, 0}
	for i := 3; i < 6; i++ {
		offset := binary.BigEndian.Uint32(result[8+4*i:])
		if offset != 32 || !bytes.Equal(result[offset:offset+12], empty) {
			t.Fatalf("image glyph %d has nonempty metadata", i)
		}
	}
	// Resolve both relative references through the relocated extraInfo base.
	glyph := binary.BigEndian.Uint32(result[8:])
	extra := binary.BigEndian.Uint32(result[4:])
	for _, field := range []uint32{0, 4} {
		relative := binary.BigEndian.Uint32(result[glyph+field:])
		if !bytes.Equal(result[extra+relative:], original[48+relative:]) {
			t.Fatal("relative Zapf metadata reference changed")
		}
	}
	// Re-extending must retain earlier image glyphs' shared empty record too.
	again, err := extendPreservedZapf(result, 6, 7)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		oldOffset := binary.BigEndian.Uint32(result[8+4*i:])
		newOffset := binary.BigEndian.Uint32(again[8+4*i:])
		if newOffset != oldOffset+16 || !bytes.Equal(again[newOffset:newOffset+12], result[oldOffset:oldOffset+12]) {
			t.Fatalf("second extension changed glyph %d", i)
		}
	}
}

func TestPreservedZapfUnchangedCountReturnsIndependentCopy(t *testing.T) {
	original := preservedZapfFixture()
	result, err := extendPreservedZapf(original, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, original) {
		t.Fatal("unchanged glyph count changed Zapf")
	}
	result[len(result)-1] ^= 1
	if bytes.Equal(result, original) {
		t.Fatal("Zapf result aliases source")
	}
}

func TestPreservedZapfRejectsMalformedSources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]byte) []byte
		want   string
	}{
		{"empty", func(d []byte) []byte { return nil }, "truncated Zapf header"},
		{"short header", func(d []byte) []byte { return d[:7] }, "truncated Zapf header"},
		{"version 2", func(d []byte) []byte { binary.BigEndian.PutUint32(d, 0x20000); return d }, "unsupported Zapf version"},
		{"short offset array", func(d []byte) []byte { return d[:19] }, "truncated Zapf glyph offsets"},
		{"extra info inside offsets", func(d []byte) []byte { binary.BigEndian.PutUint32(d[4:], 19); return d }, "invalid Zapf extra-info offset"},
		{"extra info beyond end", func(d []byte) []byte { binary.BigEndian.PutUint32(d[4:], uint32(len(d)+1)); return d }, "invalid Zapf extra-info offset"},
		{"extra info overflow", func(d []byte) []byte { binary.BigEndian.PutUint32(d[4:], 0xffffffff); return d }, "invalid Zapf extra-info offset"},
		{"glyph inside offsets", func(d []byte) []byte { binary.BigEndian.PutUint32(d[8:], 16); return d }, "invalid Zapf glyph-info offset"},
		{"glyph inside extra info", func(d []byte) []byte { binary.BigEndian.PutUint32(d[8:], 48); return d }, "invalid Zapf glyph-info offset"},
		{"short glyph record", func(d []byte) []byte { binary.BigEndian.PutUint32(d[8:], 37); return d }, "invalid Zapf glyph-info offset"},
		{"glyph offset overflow", func(d []byte) []byte { binary.BigEndian.PutUint32(d[8:], 0xffffffff); return d }, "invalid Zapf glyph-info offset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.change(preservedZapfFixture())
			if _, err := extendPreservedZapf(data, 3, 4); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	for _, counts := range [][2]int{{0, 1}, {-1, 1}, {3, 2}, {3, 65536}, {65536, 65536}} {
		if _, err := extendPreservedZapf(preservedZapfFixture(), counts[0], counts[1]); err == nil || !strings.Contains(err.Error(), "invalid Zapf glyph count") {
			t.Fatalf("counts %v: error = %v", counts, err)
		}
	}
}
