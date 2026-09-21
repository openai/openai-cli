package imagefont

import (
	"bytes"
	"encoding/binary"
	"os"
	"reflect"
	"testing"
)

func variableGvarFixture() []byte {
	var out buffer
	out.u16(1)
	out.u16(0)
	out.u16(1)
	out.u16(1)
	out.u32(28)
	out.u16(3)
	out.u16(0)
	out.u32(30)
	for _, offset := range []uint16{0, 1, 1, 3} {
		out.u16(offset)
	}
	out.u16(0x4000)
	out.Write([]byte{1, 2, 3, 4, 5, 6})
	return out.Bytes()
}

func variableHVARFixture(explicit bool) []byte {
	var out buffer
	out.u16(1)
	out.u16(0)
	out.u32(20)
	out.zeros(12)
	// Store: three one-region delta rows, with advance deltas10,20,30.
	out.u16(1)
	out.u32(12)
	out.u16(1)
	out.u32(22)
	out.u16(1)
	out.u16(1)
	out.u16(0)
	out.u16(0x4000)
	out.u16(0x4000)
	out.u16(3)
	out.u16(1)
	out.u16(1)
	out.u16(0)
	out.i16(10)
	out.i16(20)
	out.i16(30)
	data := out.Bytes()
	if explicit {
		// A compact two-entry map exercises last-entry repetition for glyph2.
		offset := len(data)
		data = append(data, []byte{0, 1, 0, 2, 2, 1}...)
		binary.BigEndian.PutUint32(data[8:], uint32(offset))
		binary.BigEndian.PutUint32(data[12:], uint32(offset))
		binary.BigEndian.PutUint32(data[16:], uint32(offset))
	}
	return data
}

func TestPreservedGvarAddsEmptyGlyphsWithoutChangingVariationData(t *testing.T) {
	original := variableGvarFixture()
	before := append([]byte(nil), original...)
	result, err := extendPreservedGvar(original, 3, 6, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, before) {
		t.Fatal("source gvar mutated")
	}
	if binary.BigEndian.Uint16(result[12:]) != 6 || binary.BigEndian.Uint16(result[14:]) != 1 {
		t.Fatal("gvar glyph count or offset format not extended")
	}
	newBody := 20 + 7*4
	if !bytes.Equal(result[newBody:], original[28:]) {
		t.Fatal("original variation payload changed")
	}
	if int(binary.BigEndian.Uint32(result[8:])) != newBody || int(binary.BigEndian.Uint32(result[16:])) != newBody+2 {
		t.Fatal("gvar shared/glyph data offsets not relocated")
	}
	want := []uint32{0, 2, 2, 6, 6, 6, 6}
	for i, offset := range want {
		if binary.BigEndian.Uint32(result[20+i*4:]) != offset {
			t.Fatalf("glyph %d changed variation range", i)
		}
	}
	// The same operation also accepts long offsets on its second invocation.
	again, err := extendPreservedGvar(result, 6, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again[20+9*4:], original[28:]) {
		t.Fatal("long-offset relocation changed payload")
	}
}

func TestPreservedHVARKeepsOriginalIndicesAndNewGlyphMetrics(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		original := variableHVARFixture(explicit)
		before := append([]byte(nil), original...)
		result, err := extendPreservedHVAR(original, 3, 6, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original, before) {
			t.Fatal("source HVAR mutated")
		}
		if !bytes.Equal(result[20:len(original)], original[20:]) {
			t.Fatal("original variation store or map bytes changed")
		}
		advance, err := readPreservedDeltaMap(result[int(binary.BigEndian.Uint32(result[8:])):], 6)
		if err != nil {
			t.Fatal(err)
		}
		want := []uint32{0, 1, 2, 1, 1, 1}
		if explicit {
			want = []uint32{2, 1, 1, 1, 1, 1}
		}
		if !reflect.DeepEqual(advance, want) {
			t.Fatalf("advance delta indices changed: %v", advance)
		}
		for _, field := range []int{12, 16} {
			offset := binary.BigEndian.Uint32(result[field:])
			if !explicit {
				if offset != 0 {
					t.Fatal("invented original side-bearing variations")
				}
				continue
			}
			indices, err := readPreservedDeltaMap(result[int(offset):], 6)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(indices, []uint32{2, 1, 1, 0xffffffff, 0xffffffff, 0xffffffff}) {
				t.Fatalf("side-bearing deltas changed: %v", indices)
			}
		}
	}
}

func TestPreservedVariableTablesRejectMalformedBounds(t *testing.T) {
	gvar := variableGvarFixture()
	for _, change := range []func([]byte){
		func(d []byte) { binary.BigEndian.PutUint16(d[4:], 2) },
		func(d []byte) { binary.BigEndian.PutUint16(d[12:], 4) },
		func(d []byte) { binary.BigEndian.PutUint16(d[14:], 2) },
		func(d []byte) { binary.BigEndian.PutUint32(d[8:], 1) },
		func(d []byte) { binary.BigEndian.PutUint32(d[16:], uint32(len(d)+1)) },
		func(d []byte) { binary.BigEndian.PutUint16(d[24:], 2); binary.BigEndian.PutUint16(d[26:], 1) },
	} {
		bad := append([]byte(nil), gvar...)
		change(bad)
		if _, err := extendPreservedGvar(bad, 3, 4, 1); err == nil {
			t.Fatal("malformed gvar accepted")
		}
	}
	for _, length := range []int{0, 19, 25, 30} {
		if _, err := extendPreservedGvar(gvar[:length], 3, 4, 1); err == nil {
			t.Fatal("truncated gvar accepted")
		}
	}
	hvar := variableHVARFixture(false)
	for _, change := range []func([]byte){
		func(d []byte) { binary.BigEndian.PutUint32(d[4:], 1) },
		func(d []byte) { binary.BigEndian.PutUint32(d[8:], uint32(len(d)+1)) },
		func(d []byte) { binary.BigEndian.PutUint16(d[32:], 2) },
		func(d []byte) { binary.BigEndian.PutUint16(d[42:], 1) },
		func(d []byte) { binary.BigEndian.PutUint16(d[44:], 2) },
	} {
		bad := append([]byte(nil), hvar...)
		change(bad)
		if _, err := extendPreservedHVAR(bad, 3, 4, 1, 1); err == nil {
			t.Fatal("malformed HVAR accepted")
		}
	}
	for _, data := range [][]byte{{1, 0, 0, 1, 0}, {0, 0xc0, 0, 1, 0}, {0, 0, 0, 0}, {0, 0x30, 0, 1, 255, 255, 255, 255}} {
		if _, err := readPreservedDeltaMap(data, 3); err == nil {
			t.Fatal("malformed delta map accepted")
		}
	}
}

func TestPreservedVariableBundledSFMonoTerminal(t *testing.T) {
	path := "/System/Applications/Utilities/Terminal.app/Contents/Resources/Fonts/SFMono-Terminal.ttf"
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("bundled variable SF Mono is unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 12 {
		t.Fatal("truncated font")
	}
	n := int(binary.BigEndian.Uint16(data[4:]))
	if n > (len(data)-12)/16 {
		t.Fatal("truncated directory")
	}
	tables := map[string][]byte{}
	for i := 0; i < n; i++ {
		r := data[12+i*16 : 28+i*16]
		off, length := uint64(binary.BigEndian.Uint32(r[8:])), uint64(binary.BigEndian.Uint32(r[12:]))
		if off+length > uint64(len(data)) {
			t.Fatal("invalid table bounds")
		}
		tables[string(r[:4])] = data[int(off):int(off+length)]
	}
	count := int(binary.BigEndian.Uint16(tables["maxp"][4:]))
	axes := int(binary.BigEndian.Uint16(tables["fvar"][8:]))
	mapping, err := readPreservedCmap(tables["cmap"], count)
	if err != nil {
		t.Fatal(err)
	}
	gvar, err := extendPreservedGvar(tables["gvar"], count, count+512, axes)
	if err != nil {
		t.Fatal(err)
	}
	originalBody := 20 + (count+1)*(2+2*int(binary.BigEndian.Uint16(tables["gvar"][14:])&1))
	if !bytes.Equal(gvar[20+(count+513)*4:], tables["gvar"][originalBody:]) {
		t.Fatal("bundled font variation body changed")
	}
	hvar, err := extendPreservedHVAR(tables["HVAR"], count, count+512, int(mapping['W']), axes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hvar[20:len(tables["HVAR"])], tables["HVAR"][20:]) {
		t.Fatal("bundled font horizontal variation store changed")
	}
	indices, err := readPreservedDeltaMap(hvar[int(binary.BigEndian.Uint32(hvar[8:])):], count+512)
	if err != nil {
		t.Fatal(err)
	}
	for gid := 0; gid < count; gid++ {
		if indices[gid] != uint32(gid) {
			t.Fatal("original implicit variation index changed")
		}
	}
	for _, index := range indices[count:] {
		if index != mapping['W'] {
			t.Fatal("new glyph does not use W advance variations")
		}
	}
}
