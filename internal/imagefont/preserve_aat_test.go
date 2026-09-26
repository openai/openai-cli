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
		{"lcar", 0x00010000, 0, 6},
		{"lcar", 0x00010000, 1, 6},
		{"opbd", 0x00010000, 0, 6},
		{"opbd", 0x00010000, 1, 6},
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
	for _, tag := range []string{"prop", "bsln", "lcar", "opbd", "morx", "kerx"} {
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
	for _, tag := range []string{"lcar", "opbd"} {
		var data buffer
		data.u32(0x00010000)
		data.u16(0)
		data.Write([]byte{0, 8, 0, 1, 0, 1, 0, 14})
		data.zeros(8)
		source.Tables[tag] = data.Bytes()
	}
	result, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	got := fontTables(t, result.Data)
	for _, tag := range []string{"prop", "bsln", "lcar", "opbd", "morx", "kerx"} {
		if !bytes.Equal(got[tag], source.Tables[tag]) {
			t.Fatalf("source %s changed", tag)
		}
	}
}

// These fixtures locate the same standard AAT lookup in each enclosing table.
// Format 0 has one class/value for every original glyph; format 8 carries its
// own firstGlyph and glyphCount and remains bounded when images are appended.
func preservationAATLookup(format uint16) []byte {
	var out buffer
	out.u16(format)
	if format == 0 {
		for i := 0; i < baseGlyphCount; i++ {
			out.u16(1)
		}
	} else {
		out.u16(1)
		out.u16(1)
		out.u16(1)
	}
	return out.Bytes()
}

func preservationNestedAAT(tag string, kind uint32, format uint16, second bool) []byte {
	lookup := preservationAATLookup(format)
	var out buffer
	switch tag {
	case "ankr":
		out.u32(0)
		out.u32(12)
		out.u32(uint32(12 + len(lookup)))
		out.Write(lookup)
		out.u32(0)
	case "just":
		out.u32(0x00010000)
		out.u16(0)
		out.u16(10)
		out.u16(0)
		out.u16(0)
		out.u16(0)
		if second {
			out.u16(24)
			out.Write(preservationAATLookup(8))
		} else {
			out.u16(0)
		}
		out.Write(lookup)
	case "mort", "morx", "kerx":
		var body buffer
		if tag == "mort" || tag == "morx" && kind == 4 {
			body.Write(lookup)
		} else if tag == "kerx" && (kind == 2 || kind == 6) {
			minimum, left, right := 28, 16, 20
			if kind == 6 {
				minimum, left, right = 32, 20, 24
			}
			body.zeros(minimum - 12)
			first := lookup
			if second {
				first = preservationAATLookup(8)
			}
			binary.BigEndian.PutUint32(body.Bytes()[left-12:], uint32(minimum))
			binary.BigEndian.PutUint32(body.Bytes()[right-12:], uint32(minimum+len(first)))
			body.Write(first)
			body.Write(lookup)
		} else {
			header := 20
			if tag == "morx" && kind == 0 {
				header = 16
			} else if tag == "morx" && kind == 2 {
				header = 28
			}
			body.zeros(header)
			binary.BigEndian.PutUint32(body.Bytes(), 4)
			binary.BigEndian.PutUint32(body.Bytes()[4:], uint32(header))
			binary.BigEndian.PutUint32(body.Bytes()[8:], uint32(header+len(lookup)))
			binary.BigEndian.PutUint32(body.Bytes()[12:], uint32(header+len(lookup)+16))
			body.Write(lookup)
			body.zeros(24)
		}
		var sub buffer
		if tag == "mort" {
			sub.u16(uint16(8 + body.Len()))
			sub.u16(uint16(kind))
			sub.u32(0)
			out.u32(0x00010000)
		} else {
			sub.u32(uint32(12 + body.Len()))
			sub.u32(kind)
			sub.u32(0)
			out.u32(0x00020000)
		}
		sub.Write(body.Bytes())
		out.u32(1)
		if tag != "kerx" {
			out.u32(0)
			if tag == "mort" {
				out.u32(uint32(12 + sub.Len()))
				out.u16(0)
				out.u16(1)
			} else {
				out.u32(uint32(16 + sub.Len()))
				out.u32(0)
				out.u32(1)
			}
		}
		out.Write(sub.Bytes())
	}
	return out.Bytes()
}

func TestEncodePreservingNestedAATLookups(t *testing.T) {
	for _, test := range []struct {
		tag    string
		kind   uint32
		second bool
	}{
		{"ankr", 0, false}, {"just", 0, false}, {"just", 0, true},
		{"mort", 4, false},
		{"morx", 0, false}, {"morx", 2, false}, {"morx", 4, false}, {"morx", 5, false},
		{"kerx", 1, false}, {"kerx", 2, false}, {"kerx", 2, true},
		{"kerx", 4, false}, {"kerx", 6, false}, {"kerx", 6, true},
	} {
		for _, format := range []uint16{0, 8} {
			t.Run(fmt.Sprintf("%s/type%d/second=%t/lookup%d", test.tag, test.kind, test.second, format), func(t *testing.T) {
				source := preservationSource(t)
				data := preservationNestedAAT(test.tag, test.kind, format, test.second)
				source.Tables[test.tag] = data
				before := bytes.Clone(data)
				result, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
				if format == 0 {
					if err == nil || !strings.Contains(err.Error(), test.tag) {
						t.Fatalf("implicit glyph array accepted: %v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(fontTables(t, result.Data)[test.tag], before) {
						t.Fatal("bounded source table changed")
					}
				}
				if !bytes.Equal(source.Tables[test.tag], before) {
					t.Fatal("caller source changed")
				}
			})
		}
	}
}

func TestEncodePreservingRejectsMalformedAATContainers(t *testing.T) {
	for _, tag := range []string{"ankr", "just", "mort", "morx", "kerx"} {
		kind := uint32(4)
		if tag == "morx" {
			kind = 2
		}
		for _, test := range []struct {
			name   string
			mutate func([]byte) []byte
		}{
			{"short", func(data []byte) []byte { return data[:7] }},
			{"truncated body", func(data []byte) []byte { return data[:len(data)-8] }},
			{"offset or count overflow", func(data []byte) []byte { binary.BigEndian.PutUint32(data[4:], 0xffffffff); return data }},
		} {
			t.Run(tag+"/"+test.name, func(t *testing.T) {
				source := preservationSource(t)
				source.Tables[tag] = test.mutate(preservationNestedAAT(tag, kind, 8, false))
				if _, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source); err == nil {
					t.Fatal("malformed AAT container accepted")
				}
			})
		}
	}
	for _, tag := range []string{"mort", "morx"} {
		t.Run(tag+"/contextual", func(t *testing.T) {
			source := preservationSource(t)
			source.Tables[tag] = preservationNestedAAT(tag, 1, 8, false)
			if _, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source); err == nil {
				t.Fatal("uninspected contextual substitution accepted")
			}
		})
	}
}
