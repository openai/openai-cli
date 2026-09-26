package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

func TestEncodePreservingRejectsImplicitAATGlyphCoverage(t *testing.T) {
	for _, test := range []struct {
		tag          string
		version      uint32
		format       uint16
		lookupOffset int
	}{
		{"prop", 0x00030000, 1, 8},
		{"bsln", 0x00010000, 1, 72},
		{"bsln", 0x00010000, 3, 74},
		{"morx", 0x00030000, 0, 0},
		{"kerx", 0x00030000, 0, 0},
		{"kerx", 0x00040000, 0, 0},
	} {
		t.Run(fmt.Sprintf("%s/version%X/format%d", test.tag, test.version, test.format), func(t *testing.T) {
			source := preservationSource(t)
			data := make([]byte, max(8, test.lookupOffset+2+baseGlyphCount*2))
			binary.BigEndian.PutUint32(data, test.version)
			binary.BigEndian.PutUint16(data[4:], test.format)
			source.Tables[test.tag] = data
			before := bytes.Clone(data)
			if _, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source); err == nil || !strings.Contains(err.Error(), test.tag) {
				t.Fatalf("unsupported %s glyph coverage accepted: %v", test.tag, err)
			}
			if !bytes.Equal(before, source.Tables[test.tag]) {
				t.Fatal("source changed")
			}
		})
	}
	for _, tag := range []string{"prop", "bsln", "morx", "kerx"} {
		t.Run("truncated/"+tag, func(t *testing.T) {
			source := preservationSource(t)
			source.Tables[tag] = []byte{0}
			if _, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source); err == nil {
				t.Fatal("truncated AAT header accepted")
			}
		})
	}
}

func TestEncodePreservingKeepsBoundedAATGlyphCoverage(t *testing.T) {
	source := preservationSource(t)
	for _, test := range []struct {
		tag     string
		version uint32
		format  uint16
		size    int
	}{
		{"prop", 0x00030000, 0, 8},
		{"bsln", 0x00010000, 0, 72},
		{"morx", 0x00020000, 0, 8},
		{"kerx", 0x00020000, 0, 8},
	} {
		data := make([]byte, test.size)
		binary.BigEndian.PutUint32(data, test.version)
		binary.BigEndian.PutUint16(data[4:], test.format)
		source.Tables[test.tag] = data
	}
	// A trimmed lookup states its own glyph range; added glyphs use the table
	// default instead of reading beyond the existing lookup array.
	for _, tag := range []string{"prop", "bsln"} {
		binary.BigEndian.PutUint16(source.Tables[tag][4:], 1)
		source.Tables[tag] = append(source.Tables[tag], 0, 8, 0, 1, 0, 1, 0, 0)
	}
	result, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	got := fontTables(t, result.Data)
	for _, tag := range []string{"prop", "bsln", "morx", "kerx"} {
		if !bytes.Equal(got[tag], source.Tables[tag]) {
			t.Fatalf("source %s changed", tag)
		}
	}
}
