package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

func preservationSource(t *testing.T) PreserveOptions {
	t.Helper()
	original, err := Encode(context.Background(), nil, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	tables := fontTables(t, original.Data)
	delete(tables, "sbix")
	// Unchanged arbitrary layout tables must survive, including hinting/shaping.
	tables["cvt "] = []byte{0, 1, 0, 2}
	tables["prep"] = []byte{0}
	tables["fpgm"] = []byte{0}
	return PreserveOptions{Tables: tables, SourcePostScript: "GoMono", PointSize: 13, CellWidth: 8, CellHeight: 17, Baseline: 4}
}
func preservationFrames() []Frame {
	return []Frame{{Image: solid(image.Rect(0, 0, 16, 16), color.NRGBA{R: 240, G: 32, A: 255}), Columns: 2, Rows: 2}}
}

func TestEncodePreservingKeepsTextAndSourceTables(t *testing.T) {
	source := preservationSource(t)
	before := make(map[string][]byte)
	for tag, data := range source.Tables {
		before[tag] = append([]byte(nil), data...)
	}
	result, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	got := fontTables(t, result.Data)
	for _, tag := range []string{"glyf", "OS/2", "cvt ", "prep", "fpgm"} {
		if !bytes.Equal(got[tag], before[tag]) {
			t.Errorf("%s changed", tag)
		}
	}
	if !bytes.Equal(got["hhea"][:34], before["hhea"][:34]) {
		t.Error("line metrics changed")
	}
	if !bytes.Equal(got["post"][4:32], before["post"][4:32]) {
		t.Error("style/underline fields changed")
	}
	if !reflect.DeepEqual(before, source.Tables) {
		t.Error("mutated caller source tables")
	}
	original, err := sfnt.Parse(assembleFont(before))
	if err != nil {
		t.Fatal(err)
	}
	extended, err := sfnt.Parse(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{9, 13, 17, 29, 48} {
		a, err := original.Metrics(nil, fixed.I(size), font.HintingNone)
		if err != nil {
			t.Fatal(err)
		}
		b, err := extended.Metrics(nil, fixed.I(size), font.HintingNone)
		if err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Fatalf("size %d metrics changed", size)
		}
		for cp := rune(32); cp <= 126; cp++ {
			ga, _ := original.GlyphIndex(nil, cp)
			gb, _ := extended.GlyphIndex(nil, cp)
			if ga != gb {
				t.Fatalf("glyph %q changed", cp)
			}
			a, _ := original.GlyphAdvance(nil, ga, fixed.I(size), font.HintingNone)
			b, _ := extended.GlyphAdvance(nil, gb, fixed.I(size), font.HintingNone)
			if a != b {
				t.Fatalf("advance %q changed", cp)
			}
			pa, _ := original.LoadGlyph(nil, ga, fixed.I(size), nil)
			pb, _ := extended.LoadGlyph(nil, gb, fixed.I(size), nil)
			if !reflect.DeepEqual(pa, pb) {
				t.Fatalf("outline %q changed", cp)
			}
		}
	}
	first, _ := extended.GlyphIndex(nil, FirstSupplementaryCodepoint)
	if int(first) != baseGlyphCount {
		t.Fatalf("first bitmap index %d", first)
	}
	if !strings.HasPrefix(result.Previews[0].Text, string(FirstSupplementaryCodepoint)) {
		t.Fatal("preview does not use supplementary private characters")
	}
	alias, _ := extended.GlyphIndex(nil, FirstCodepoint)
	if alias != 0 {
		t.Fatal("default encoding replaced BMP fallback characters")
	}
	var lineage struct {
		Version int    `json:"version"`
		Source  string `json:"source_postscript"`
	}
	if err := json.Unmarshal(got["OAIp"], &lineage); err != nil || lineage.Version != 1 || lineage.Source != source.SourcePostScript {
		t.Fatalf("lineage %+v %v", lineage, err)
	}
}

func TestEncodePreservingExistingIconsAndSupplementaryCharacters(t *testing.T) {
	source := preservationSource(t)
	source.LegacyAliases = true
	mapping, err := readPreservedCmap(source.Tables["cmap"], baseGlyphCount)
	if err != nil {
		t.Fatal(err)
	}
	mapping[uint32(FirstCodepoint)] = mapping['A']
	mapping[0x1d400] = mapping['B']
	source.Tables["cmap"] = preservedCmap(mapping, source.Tables["cmap"])
	result, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := sfnt.Parse(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range []rune{FirstCodepoint, 0x1d400} {
		gid, err := parsed.GlyphIndex(nil, cp)
		if err != nil || uint32(gid) != mapping[uint32(cp)] {
			t.Fatalf("original U+%X lost: %d %v", cp, gid, err)
		}
	}
	mapping[uint32(FirstSupplementaryCodepoint)] = mapping['A']
	source.Tables["cmap"] = preservedCmap(mapping, source.Tables["cmap"])
	if _, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source); err == nil || !strings.Contains(err.Error(), "already uses") {
		t.Fatalf("conflicting supplementary icon overwritten: %v", err)
	}
}

func TestEncodePreservingCompressedMetricsAndShortLoca(t *testing.T) {
	source := preservationSource(t)
	var loca buffer
	for i := 0; i <= baseGlyphCount; i++ {
		offset := binary.BigEndian.Uint32(source.Tables["loca"][i*4:])
		if offset%2 != 0 || offset > 131070 {
			t.Fatal("invalid test source")
		}
		loca.u16(uint16(offset / 2))
	}
	source.Tables["loca"] = loca.Bytes()
	binary.BigEndian.PutUint16(source.Tables["head"][50:], 0)
	original := source.Tables["hmtx"]
	var hmtx buffer
	hmtx.Write(original[:4])
	for i := 1; i < baseGlyphCount; i++ {
		hmtx.Write(original[i*4+2 : i*4+4])
	}
	source.Tables["hmtx"] = hmtx.Bytes()
	binary.BigEndian.PutUint16(source.Tables["hhea"][34:], 1)
	result, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := sfnt.Parse(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range []rune{'A', 'W', FirstSupplementaryCodepoint} {
		gid, _ := parsed.GlyphIndex(nil, cp)
		advance, err := parsed.GlyphAdvance(nil, gid, fixed.I(13), font.HintingNone)
		if err != nil || advance != fixed.I(13)/2 {
			t.Fatalf("%q advance %v: %v", cp, advance, err)
		}
	}
}

func TestEncodePreservingEmptyFontAndCancellation(t *testing.T) {
	source := preservationSource(t)
	result, err := EncodePreserving(context.Background(), nil, testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	got := fontTables(t, result.Data)
	if binary.BigEndian.Uint16(got["maxp"][4:]) != baseGlyphCount || len(result.Previews) != 0 {
		t.Fatal("empty setup changed glyph count")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EncodePreserving(ctx, nil, testOptions, source); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestEncodePreservingOmitsGlyphCountDependentCaches(t *testing.T) {
	source := preservationSource(t)
	// These optional caches have one entry per original glyph. Retaining them
	// after increasing maxp.numGlyphs leaves them truncated for the new glyphs.
	var ltsh, hdmx buffer
	ltsh.u16(0)
	ltsh.u16(baseGlyphCount)
	ltsh.Write(bytes.Repeat([]byte{1}, baseGlyphCount))
	recordSize := (2 + baseGlyphCount + 3) &^ 3
	hdmx.u16(0)
	hdmx.u16(2)
	hdmx.u32(uint32(recordSize))
	for _, size := range []byte{12, 16} {
		hdmx.Write([]byte{size, size / 2})
		hdmx.Write(bytes.Repeat([]byte{size / 2}, baseGlyphCount))
		hdmx.zeros(recordSize - 2 - baseGlyphCount)
	}
	source.Tables["LTSH"], source.Tables["hdmx"] = ltsh.Bytes(), hdmx.Bytes()
	result, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	got := fontTables(t, result.Data)
	for _, tag := range []string{"LTSH", "hdmx"} {
		if _, exists := got[tag]; exists {
			t.Errorf("retained stale %s cache", tag)
		}
		if len(source.Tables[tag]) == 0 {
			t.Errorf("removed caller's %s table", tag)
		}
	}
	if !bytes.Equal(got["hmtx"][:len(source.Tables["hmtx"])], source.Tables["hmtx"]) {
		t.Fatal("original horizontal metrics changed")
	}
}

func TestEncodePreservingRejectsVerticalMetrics(t *testing.T) {
	var header, metrics buffer
	header.u32(0x00010000)
	header.zeros(30)
	header.u16(1)
	metrics.u16(1000)
	metrics.u16(20)
	metrics.zeros((baseGlyphCount - 1) * 2)
	for name, tables := range map[string]map[string][]byte{
		"complete":        {"vhea": header.Bytes(), "vmtx": metrics.Bytes()},
		"missing header":  {"vmtx": metrics.Bytes()},
		"missing metrics": {"vhea": header.Bytes()},
		"truncated":       {"vhea": {0}, "vmtx": {0}},
		"empty":           {"vhea": nil, "vmtx": nil},
	} {
		t.Run(name, func(t *testing.T) {
			source := preservationSource(t)
			for tag, data := range tables {
				source.Tables[tag] = bytes.Clone(data)
			}
			if _, err := EncodePreserving(context.Background(), preservationFrames(), testOptions, source); err == nil || !strings.Contains(err.Error(), "vertical metrics") {
				t.Fatalf("unsupported vertical metrics accepted: %v", err)
			}
			for tag, want := range tables {
				if got, exists := source.Tables[tag]; !exists || !bytes.Equal(got, want) {
					t.Fatalf("source %s changed", tag)
				}
			}
		})
	}
}

func TestEncodePreservingRejectsUnsupportedAndMalformedSource(t *testing.T) {
	for _, tag := range []string{"CFF ", "CFF2", "fvar", "gvar", "sbix", "COLR"} {
		t.Run(tag, func(t *testing.T) {
			source := preservationSource(t)
			source.Tables[tag] = []byte{1}
			if _, err := EncodePreserving(context.Background(), nil, testOptions, source); err == nil {
				t.Fatal("unsupported font accepted")
			}
		})
	}
	for _, tag := range []string{"head", "hhea", "maxp", "OS/2", "post", "name", "cmap", "loca", "hmtx"} {
		t.Run("truncated-"+tag, func(t *testing.T) {
			source := preservationSource(t)
			source.Tables[tag] = source.Tables[tag][:1]
			if _, err := EncodePreserving(context.Background(), nil, testOptions, source); err == nil {
				t.Fatal("truncated font accepted")
			}
		})
	}
	for _, mutate := range []func(*PreserveOptions){
		func(s *PreserveOptions) { s.SourcePostScript = "" },
		func(s *PreserveOptions) { s.Baseline = s.CellHeight + 1 },
		func(s *PreserveOptions) { binary.BigEndian.PutUint16(s.Tables["hhea"][34:], 0) },
		func(s *PreserveOptions) { binary.BigEndian.PutUint32(s.Tables["loca"][4:], 0xffffffff) },
		func(s *PreserveOptions) { binary.BigEndian.PutUint32(s.Tables["cmap"][8:], 0xffffffff) },
		func(s *PreserveOptions) { binary.BigEndian.PutUint16(s.Tables["name"][4:], 0xffff) },
	} {
		source := preservationSource(t)
		mutate(&source)
		if _, err := EncodePreserving(context.Background(), nil, testOptions, source); err == nil {
			t.Fatal("malformed font accepted")
		}
	}
}

func TestPreservedCmapRecordsRemainSortedWithMacSubtable(t *testing.T) {
	source := preservationSource(t)
	old := source.Tables["cmap"]
	mapping, err := readPreservedCmap(old, baseGlyphCount)
	if err != nil {
		t.Fatal(err)
	}
	n := int(binary.BigEndian.Uint16(old[2:]))
	headerEnd := 4 + n*8
	var extended buffer
	extended.u16(0)
	extended.u16(uint16(n + 1))
	for i := 0; i < n; i++ {
		p := 4 + i*8
		extended.Write(old[p : p+4])
		extended.u32(binary.BigEndian.Uint32(old[p+4:]) + 8)
	}
	extended.u16(1)
	extended.u16(0)
	extended.u32(uint32(len(old) + 8))
	extended.Write(old[headerEnd:])
	var mac buffer
	mac.u16(0)
	mac.u16(262)
	mac.u16(0)
	mac.zeros(256)
	extended.Write(mac.Bytes())
	got := preservedCmap(mapping, extended.Bytes())
	records := int(binary.BigEndian.Uint16(got[2:]))
	if records != 3 {
		t.Fatal(records)
	}
	var last uint32
	for i := 0; i < records; i++ {
		p := 4 + i*8
		key := binary.BigEndian.Uint32(got[p:])
		if i > 0 && key < last {
			t.Fatal("cmap records are not sorted")
		}
		last = key
	}
	if key := binary.BigEndian.Uint32(got[12:]); key != 0x00010000 {
		t.Fatalf("Mac record missing: %x", key)
	}
}

func TestPreservedNamesKeepsFormatOneLanguageTags(t *testing.T) {
	source := preservationSource(t)
	old := source.Tables["name"]
	count := int(binary.BigEndian.Uint16(old[2:]))
	storage := int(binary.BigEndian.Uint16(old[4:]))
	var versionOne buffer
	versionOne.u16(1)
	versionOne.u16(uint16(count))
	versionOne.u16(uint16(storage + 6))
	versionOne.Write(old[6:storage])
	versionOne.u16(1)
	versionOne.u16(4)
	versionOne.u16(uint16(len(old) - storage))
	versionOne.Write(old[storage:])
	versionOne.Write([]byte{0, 'e', 0, 'n'})
	raw := versionOne.Bytes()
	binary.BigEndian.PutUint16(raw[10:], 0x8000)
	result, err := preservedNames(raw, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(result) != 1 || binary.BigEndian.Uint16(result[10:]) != 0x8000 {
		t.Fatal("language format/reference was changed")
	}
	p := 6 + count*12
	newStorage := int(binary.BigEndian.Uint16(result[4:]))
	off := int(binary.BigEndian.Uint16(result[p+4:]))
	if !bytes.Equal(result[newStorage+off:newStorage+off+4], []byte{0, 'e', 0, 'n'}) {
		t.Fatal("language tag lost")
	}
}

func TestEncodePreservingRecordsExactSourceFullName(t *testing.T) {
	source := preservationSource(t)
	source.SourceName = "Original Mono Regular Italic"
	result, err := EncodePreserving(context.Background(), nil, testOptions, source)
	if err != nil {
		t.Fatal(err)
	}
	var lineage struct {
		Name       string `json:"source_name"`
		PostScript string `json:"source_postscript"`
	}
	if err := json.Unmarshal(fontTables(t, result.Data)["OAIp"], &lineage); err != nil || lineage.Name != source.SourceName || lineage.PostScript != source.SourcePostScript {
		t.Fatalf("source name not retained: %+v %v", lineage, err)
	}
	for _, name := range []string{"bad\nface", strings.Repeat("x", 256), "OpenAIImages-other"} {
		source.SourceName = name
		if _, err := EncodePreserving(context.Background(), nil, testOptions, source); err == nil {
			t.Fatalf("invalid source name accepted: %q", name)
		}
	}
}
