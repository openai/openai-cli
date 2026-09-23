package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"reflect"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

func cffFixture() []byte {
	integer := func(value int) []byte { var b buffer; b.WriteByte(29); b.u32(uint32(value)); return b.Bytes() }
	name := encodeCFFIndex([][]byte{[]byte("TestCFF")})
	strings := encodeCFFIndex(nil)
	globals := encodeCFFIndex([][]byte{{11}})
	chars := encodeCFFIndex([][]byte{{14}, {139, 139, 21, 14}})
	charset := []byte{0, 0, 34}
	private := append(integer(6), 19)
	local := encodeCFFIndex([][]byte{{11}})
	dict := func(base int) []byte {
		var b buffer
		b.Write(integer(base))
		b.WriteByte(15)
		b.Write(integer(base + len(charset)))
		b.WriteByte(17)
		b.Write(integer(len(private)))
		b.Write(integer(base + len(charset) + len(chars)))
		b.WriteByte(18)
		return b.Bytes()
	}
	base := 4 + len(name) + len(encodeCFFIndex([][]byte{dict(0)})) + len(strings) + len(globals)
	var out []byte
	for _, part := range [][]byte{{1, 0, 4, 4}, name, encodeCFFIndex([][]byte{dict(base)}), strings, globals, charset, chars, private, local} {
		out = append(out, part...)
	}
	return out
}

func TestPreservedCFFRelocationKeepsOriginalPrograms(t *testing.T) {
	original := cffFixture()
	before := append([]byte(nil), original...)
	source, err := readPreservedCFF(original, 2)
	if err != nil {
		t.Fatal(err)
	}
	result, err := extendPreservedCFF(original, 2, 6, "OpenAIImages-CFF-Test")
	if err != nil {
		t.Fatal(err)
	}
	got, err := readPreservedCFF(result, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, before) {
		t.Fatal("source mutated")
	}
	if !bytes.HasPrefix(got.payload, source.payload) {
		t.Fatal("original data section changed")
	}
	if !reflect.DeepEqual(got.globals, source.globals) {
		t.Fatal("global subroutines changed")
	}
	for i, program := range source.charstrings {
		if !bytes.Equal(program, got.charstrings[i]) {
			t.Fatalf("original charstring %d changed", i)
		}
	}
	for _, program := range got.charstrings[2:] {
		if !bytes.Equal(program, []byte{14}) {
			t.Fatal("new image glyph has an outline")
		}
	}
	if !reflect.DeepEqual(got.charset[:len(source.charset)], source.charset) {
		t.Fatal("original glyph identities changed")
	}
	for _, entry := range got.dict {
		if entry.op == 18 {
			private := entry.args[1].integer
			if !bytes.Equal(result[private:private+6], []byte{29, 0, 0, 0, 6, 19}) {
				t.Fatal("private hint dictionary changed")
			}
			local, _, err := readCFFIndex(result, private+6)
			if err != nil || !reflect.DeepEqual(local, [][]byte{{11}}) {
				t.Fatal("local subroutines were not relocated intact")
			}
		}
	}
	names, _, err := readCFFIndex(result, 4)
	if err != nil || string(names[0]) != "OpenAIImages-CFF-Test" {
		t.Fatal("CFF font identity not renamed")
	}
}

func TestPreservedCFFWidensHeaderOffsetsWhenFontGrows(t *testing.T) {
	original := cffFixture()
	if len(original) > 255 {
		t.Fatal("fixture no longer fits one-byte absolute offsets")
	}
	original[3] = 1
	before := append([]byte(nil), original...)
	extended, err := extendPreservedCFF(original, 2, 82, "OpenAIImages-Growing-CFF")
	if err != nil {
		t.Fatal(err)
	}
	if len(extended) <= 255 || extended[3] != 4 {
		t.Fatal("grown CFF still declares one-byte absolute offsets")
	}
	if !bytes.Equal(original, before) {
		t.Fatal("header widening mutated the source")
	}
	if _, err := readPreservedCFF(extended, 82); err != nil {
		t.Fatal(err)
	}
}

func TestPreservedCFFEnforcesSIDLimit(t *testing.T) {
	// SID 64999 is valid when its custom string exists; 65000 is reserved even
	// though both values fit in the unsigned 16-bit charset field.
	for _, sid := range []uint16{64999, 65000, 65535} {
		charset := []byte{0, 0, 0, 0, byte(sid >> 8), byte(sid)}
		_, err := readCFFCharset(charset, 3, 2, 65535)
		if (err == nil) != (sid == 64999) {
			t.Fatalf("charset SID %d: %v", sid, err)
		}
		encoding := []byte{0x80, 0, 1, 65, byte(sid >> 8), byte(sid)}
		err = validateCFFEncoding(encoding, 0, 2, 65535)
		if (err == nil) != (sid == 64999) {
			t.Fatalf("encoding SID %d: %v", sid, err)
		}
	}
	// A two-glyph source with no custom strings can add at most 64609 SIDs,
	// even though the sfnt glyph count would allow more.
	if _, err := extendPreservedCFF(cffFixture(), 2, 2+cffCustomStringLimit+1, "Test"); err == nil {
		t.Fatal("extended glyph names exceeded SID 64999")
	}
	valid := cffFixture()
	_, next, _ := readCFFIndex(valid, 4)
	_, stringStart, _ := readCFFIndex(valid, next)
	_, stringEnd, _ := readCFFIndex(valid, stringStart)
	overflow := encodeCFFIndex(make([][]byte, cffCustomStringLimit+1))
	malformed := append([]byte(nil), valid[:stringStart]...)
	malformed = append(malformed, overflow...)
	malformed = append(malformed, valid[stringEnd:]...)
	if _, err := readPreservedCFF(malformed, 2); err == nil {
		t.Fatal("oversized source String INDEX accepted")
	}
}

func TestPreservedCFFRejectsMalformedStructures(t *testing.T) {
	valid := cffFixture()
	for _, tt := range []struct {
		name  string
		data  []byte
		count int
	}{
		{"header", []byte{2, 0, 4, 4}, 2},
		{"truncated-index", valid[:7], 2},
		{"wrong-glyph-count", valid, 3},
		{"truncated-private-subroutines", valid[:len(valid)-1], 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := readPreservedCFF(tt.data, tt.count); err == nil {
				t.Fatal("malformed CFF accepted")
			}
		})
	}
	for _, data := range [][]byte{{0, 1, 0}, {0, 1, 1, 0, 1}, {0, 1, 4, 0, 0, 0, 1, 255, 255, 255, 255}} {
		if _, _, err := readCFFIndex(data, 0); err == nil {
			t.Fatal("invalid INDEX offsets accepted")
		}
	}
	for _, data := range [][]byte{{29, 0}, {30, 0x1d}, {30, 0x12}, {139}, {139, 17, 140, 17}, {255, 0, 0, 0, 0}} {
		if _, err := readCFFDict(data); err == nil {
			t.Fatal("malformed DICT accepted")
		}
	}
	if _, err := readCFFCharset([]byte{0, 0, 0, 0, 0, 34, 0, 34}, 3, 3, 0); err == nil {
		t.Fatal("duplicate SID accepted")
	}
	if _, err := readCFFCharset(nil, 1, 2, 0); err == nil {
		t.Fatal("unsupported Expert charset accepted")
	}
	if err := validateCFFEncoding([]byte{0, 2, 65, 65}, 0, 3, 0); err == nil {
		t.Fatal("duplicate encoding accepted")
	}
	if _, err := extendPreservedCFF(valid, 2, 65536, "Test"); err == nil {
		t.Fatal("glyph overflow accepted")
	}
	// Change the existing escaped-operator-free Top DICT's first operator to
	// ROS, using the same number of bytes, to exercise the explicit CID guard.
	_, next, _ := readCFFIndex(valid, 4)
	top, _, _ := readCFFIndex(valid, next)
	cid := append([]byte(nil), top[0]...)
	cid = append([]byte{139, 139, 139, 12, 30}, cid...)
	_, oldTopEnd, _ := readCFFIndex(valid, next)
	malformed := append([]byte(nil), valid[:next]...)
	malformed = append(malformed, encodeCFFIndex([][]byte{cid})...)
	malformed = append(malformed, valid[oldTopEnd:]...)
	if _, err := readPreservedCFF(malformed, 2); err == nil {
		t.Fatal("CID CFF accepted")
	}
}

func TestPreservedCFFBundledSFMono(t *testing.T) {
	path := "/System/Applications/Utilities/Terminal.app/Contents/Resources/Fonts/SF-Mono-Regular.otf"
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("bundled macOS SF Mono is unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	// Apple's bundled source has no valid whole-file checksum adjustment. Read
	// its bounded directory directly; generated output is checked by fontTables.
	if len(data) < 12 {
		t.Fatal("truncated source sfnt")
	}
	tableCount := int(binary.BigEndian.Uint16(data[4:6]))
	if tableCount > (len(data)-12)/16 {
		t.Fatal("truncated source directory")
	}
	tables := map[string][]byte{}
	for i := 0; i < tableCount; i++ {
		record := data[12+i*16 : 28+i*16]
		off, length := int(binary.BigEndian.Uint32(record[8:])), int(binary.BigEndian.Uint32(record[12:]))
		if off > len(data) || length > len(data)-off {
			t.Fatal("invalid source table bounds")
		}
		tables[string(record[:4])] = data[off : off+length]
	}
	count := int(binary.BigEndian.Uint16(tables["maxp"][4:]))
	original, err := readPreservedCFF(tables["CFF "], count)
	if err != nil {
		t.Fatal(err)
	}
	source := PreserveOptions{Tables: tables, SourcePostScript: "SFMono-Regular", PointSize: 13, CellWidth: 8, CellHeight: 16, Baseline: 3}
	result, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data[:4]) != "OTTO" {
		t.Fatal("CFF lost OTTO container")
	}
	out := fontTables(t, result.Data)
	if binary.BigEndian.Uint32(out["maxp"]) != 0x5000 || len(out["maxp"]) != 6 {
		t.Fatal("CFF maxp0.5 changed")
	}
	extended, err := readPreservedCFF(out["CFF "], count+4)
	if err != nil {
		t.Fatal(err)
	}
	for gid, program := range original.charstrings {
		if !bytes.Equal(program, extended.charstrings[gid]) {
			t.Fatalf("original glyph %d program changed", gid)
		}
	}
	if !bytes.HasPrefix(extended.payload, original.payload) {
		t.Fatal("original CFF payload changed")
	}
	a, err := sfnt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sfnt.Parse(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	for cp := rune(32); cp < 127; cp++ {
		ga, _ := a.GlyphIndex(nil, cp)
		gb, _ := b.GlyphIndex(nil, cp)
		if ga != gb {
			t.Fatal("original glyph mapping changed")
		}
		ma, err := a.GlyphAdvance(nil, ga, fixed.I(13), font.HintingNone)
		if err != nil {
			t.Fatal(err)
		}
		mb, err := b.GlyphAdvance(nil, gb, fixed.I(13), font.HintingNone)
		if err != nil {
			t.Fatal(err)
		}
		if ma != mb {
			t.Fatalf("glyph %q advance changed", cp)
		}
		pa, err := a.LoadGlyph(nil, ga, fixed.I(13), nil)
		if err != nil {
			t.Fatal(err)
		}
		pb, err := b.LoadGlyph(nil, gb, fixed.I(13), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pa, pb) {
			t.Fatalf("glyph %q outline changed", cp)
		}
	}
}
