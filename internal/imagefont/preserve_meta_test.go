package imagefont

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func metaFixture() []byte {
	entries := []struct{ tag, data string }{{"dlng", "Latn"}, {"appl", "opaque original font identity"}, {"slng", "Latn, Cyrl"}, {"bild", "\x00\x01\xff"}}
	data := make([]byte, 16+12*len(entries))
	binary.BigEndian.PutUint32(data, 1)
	binary.BigEndian.PutUint32(data[8:], uint32(len(data))) // Apple's historical redundant data offset.
	binary.BigEndian.PutUint32(data[12:], uint32(len(entries)))
	for i, entry := range entries {
		p := 16 + i*12
		copy(data[p:p+4], entry.tag)
		binary.BigEndian.PutUint32(data[p+4:], uint32(len(data)))
		binary.BigEndian.PutUint32(data[p+8:], uint32(len(entry.data)))
		data = append(data, entry.data...)
	}
	return data
}

func TestPreservedVariableMetaKeepsLanguageAndOtherPayloads(t *testing.T) {
	data := metaFixture()
	before := append([]byte(nil), data...)
	got, err := preservedVariableMeta(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, before) || binary.BigEndian.Uint32(got[12:]) != 3 {
		t.Fatal("source changed or metadata records were lost")
	}
	for i, want := range []struct{ tag, data string }{{"dlng", "Latn"}, {"slng", "Latn, Cyrl"}, {"bild", "\x00\x01\xff"}} {
		p := 16 + i*12
		offset, length := int(binary.BigEndian.Uint32(got[p+4:])), int(binary.BigEndian.Uint32(got[p+8:]))
		if string(got[p:p+4]) != want.tag || string(got[offset:offset+length]) != want.data {
			t.Fatalf("metadata %s changed", want.tag)
		}
	}
	if again, err := preservedVariableMeta(got); err != nil || !bytes.Equal(again, got) {
		t.Fatal("metadata without appl should remain byte-identical")
	}
}

func TestPreservedVariableMetaRejectsMalformedRecords(t *testing.T) {
	for _, change := range []func([]byte) []byte{
		func(b []byte) []byte { return b[:15] },
		func(b []byte) []byte { b[3] = 2; return b },
		func(b []byte) []byte { b[7] = 1; return b },
		func(b []byte) []byte { binary.BigEndian.PutUint32(b[12:], 0xffffffff); return b },
		func(b []byte) []byte { binary.BigEndian.PutUint32(b[20:], 0xffffffff); return b },
		func(b []byte) []byte { binary.BigEndian.PutUint32(b[24:], 0xffffffff); return b },
		func(b []byte) []byte { binary.BigEndian.PutUint32(b[20:], 1); return b },
	} {
		if _, err := preservedVariableMeta(change(metaFixture())); err == nil {
			t.Fatal("malformed metadata accepted")
		}
	}
}

func TestPreservedVariableMetaSharedPayloadDoesNotExpand(t *testing.T) {
	const count = 100
	const payloadSize = 4096
	start := 16 + count*12
	data := make([]byte, start+payloadSize)
	binary.BigEndian.PutUint32(data, 1)
	binary.BigEndian.PutUint32(data[12:], count)
	for i := 0; i < count; i++ {
		p := 16 + i*12
		copy(data[p:p+4], "TEST")
		// Overlapping ranges are valid bounded views, not independent payloads.
		binary.BigEndian.PutUint32(data[p+4:], uint32(start+i))
		binary.BigEndian.PutUint32(data[p+8:], uint32(payloadSize-i))
	}
	copy(data[16:20], "appl")
	for i := 0; i < payloadSize; i++ {
		data[start+i] = byte(i)
	}
	before := append([]byte(nil), data...)
	got, err := preservedVariableMeta(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(data)-12 || !bytes.Equal(before, data) {
		t.Fatal("aliased metadata expanded or source was mutated")
	}
	for i := 0; i < count-1; i++ {
		p := 16 + i*12
		offset, length := int(binary.BigEndian.Uint32(got[p+4:])), int(binary.BigEndian.Uint32(got[p+8:]))
		if !bytes.Equal(got[offset:offset+length], data[start+i+1:]) {
			t.Fatal("overlapping metadata payload changed")
		}
	}
	got[len(got)-1] ^= 255
	if !bytes.Equal(before, data) {
		t.Fatal("result aliases source memory")
	}
}

func TestPreservedVariableMetaEmptyAndRemovedRecords(t *testing.T) {
	if got, err := preservedVariableMeta(nil); err != nil || got != nil {
		t.Fatal("absent metadata changed")
	}
	data := make([]byte, 40)
	binary.BigEndian.PutUint32(data, 1)
	binary.BigEndian.PutUint32(data[12:], 2)
	copy(data[16:20], "appl")
	copy(data[28:32], "TEST")
	// A zero-length payload need not point beyond its source record array.
	got, err := preservedVariableMeta(data)
	if err != nil || len(got) != 28 || binary.BigEndian.Uint32(got[20:]) != 28 || binary.BigEndian.Uint32(got[24:]) != 0 {
		t.Fatalf("empty metadata payload was not normalized: %v", err)
	}
	copy(data[28:32], "appl")
	if got, err := preservedVariableMeta(data); err != nil || got != nil {
		t.Fatal("all removed metadata should omit the table")
	}
}
