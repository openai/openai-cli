package imagefont

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
)

// PreserveOptions supplies the installed text face to extend. Its original
// outlines, hinting, layout tables, style, character mappings and text metrics
// remain unchanged. Pixel geometry is measured from the user's Terminal tab.
type PreserveOptions struct {
	Tables           map[string][]byte
	SourcePostScript string
	// SourceName retains an exact bundled full face name when PostScript names
	// are shared by different installed files. Empty uses SourcePostScript.
	SourceName string
	Variations map[string]float64
	// FamilyClass is the native public typographic classification (0..15).
	FamilyClass           uint8
	PointSize             int
	CellWidth, CellHeight int
	Baseline              int
	// TextHeight is the original font's natural line height rounded up, before
	// profile spacing. Monaco needs this to preserve Terminal's special layout.
	TextHeight int
	// LegacyAliases restores old BMP gallery characters during an explicit
	// migration. Off by default so fallback-font private-use icons are retained.
	LegacyAliases bool
}

// EncodePreserving creates a private, uniquely named copy of an outline
// face, with extra bitmap glyphs. Source font files are never modified. Images
// occupy a supplementary private-use range, preserving existing BMP icons.
func EncodePreserving(ctx context.Context, frames []Frame, options Options, source PreserveOptions) (Font, error) {
	if err := ctx.Err(); err != nil {
		return Font{}, err
	}
	if err := validateNames(options); err != nil {
		return Font{}, err
	}
	if source.SourcePostScript == "" || len(source.SourcePostScript) > 255 {
		return Font{}, fmt.Errorf("an exact source font identity is required")
	}
	if source.FamilyClass > 15 {
		return Font{}, fmt.Errorf("invalid source font family classification")
	}
	if source.SourceName != "" {
		if len(source.SourceName) > 255 || strings.HasPrefix(source.SourceName, "OpenAIImages-") {
			return Font{}, fmt.Errorf("an original source font name is required")
		}
		for _, r := range source.SourceName {
			if unicode.IsControl(r) {
				return Font{}, fmt.Errorf("source font name cannot contain control characters")
			}
		}
	}
	tables, oldCount, mapping, err := preserveSource(source.Tables)
	if err != nil {
		return Font{}, err
	}
	if err := compensatePreservedMonaco(tables, source); err != nil {
		return Font{}, err
	}
	namingOptions := options
	var nameOverrides map[uint16]string
	if len(tables["fvar"]) > 0 {
		namingOptions, nameOverrides, err = preservedVariableIdentity(tables, source, options)
		if err != nil {
			return Font{}, err
		}
		metadata, err := preservedVariableMeta(tables["meta"])
		if err != nil {
			return Font{}, err
		}
		if !bytes.Equal(metadata, tables["meta"]) && source.FamilyClass != 0 && len(tables["OS/2"]) >= 32 {
			// CoreText's public family classes share OS/2's high-byte values.
			// Retain that classification when dropping Apple's stale private one.
			old := binary.BigEndian.Uint16(tables["OS/2"][30:])
			subclass := uint16(0)
			if old>>8 == uint16(source.FamilyClass) {
				subclass = old & 255
			}
			binary.BigEndian.PutUint16(tables["OS/2"][30:], uint16(source.FamilyClass)<<8|subclass)
		}
		if len(metadata) > 0 {
			tables["meta"] = metadata
		} else {
			delete(tables, "meta")
		}
	}
	prepared, err := preparePreservingFrames(frames, source.CellWidth, source.CellHeight, source.PointSize)
	if err != nil {
		return Font{}, err
	}
	if source.Baseline < 0 || source.Baseline > source.CellHeight {
		return Font{}, fmt.Errorf("font baseline must fit the measured terminal cell")
	}
	count := oldCount
	previews := make([]Preview, len(prepared))
	for i := range prepared {
		prepared[i].firstGlyph = count
		for k := 0; k < prepared[i].Columns*prepared[i].Rows; k++ {
			cp := uint32(prepared[i].CodepointStart + rune(k))
			if _, exists := mapping[cp]; exists {
				return Font{}, fmt.Errorf("source font already uses image character U+%X", cp)
			}
			mapping[cp] = uint32(count + k)
			// Old gallery scrollback uses BMP private-use characters. Keep those aliases
			// only when they cannot replace an existing user-font icon.
			alias := cp - uint32(FirstSupplementaryCodepoint) + uint32(FirstCodepoint)
			if _, exists := mapping[alias]; source.LegacyAliases && !exists {
				mapping[alias] = uint32(count + k)
			}
		}
		count += prepared[i].Columns * prepared[i].Rows
		previews[i] = prepared[i].Preview
	}
	if count > 65535 {
		return Font{}, fmt.Errorf("source font and image gallery exceed the TrueType glyph limit")
	}
	if zapf, exists := tables["Zapf"]; exists {
		tables["Zapf"], err = extendPreservedZapf(zapf, oldCount, count)
		if err != nil {
			return Font{}, err
		}
	}
	if len(tables["fvar"]) > 0 {
		axisCount := int(binary.BigEndian.Uint16(tables["fvar"][8:]))
		widthGlyph, hasWidth := mapping['W']
		if !hasWidth || widthGlyph == 0 || widthGlyph >= uint32(oldCount) {
			return Font{}, fmt.Errorf("variable image previews require the original font's W glyph")
		}
		tables["gvar"], err = extendPreservedGvar(tables["gvar"], oldCount, count, axisCount)
		if err != nil {
			return Font{}, err
		}
		tables["HVAR"], err = extendPreservedHVAR(tables["HVAR"], oldCount, count, int(widthGlyph), axisCount)
		if err != nil {
			return Font{}, err
		}
	}
	// Retain each original glyph index and outline. Added image glyphs have an
	// empty outline in either CFF Type 2 charstrings or TrueType loca entries.
	if cff, ok := tables["CFF "]; ok {
		tables["CFF "], err = extendPreservedCFF(cff, oldCount, count, options.PostScript)
		if err != nil {
			return Font{}, err
		}
	} else {
		oldLoca := tables["loca"]
		longLoca := binary.BigEndian.Uint16(tables["head"][50:]) == 1
		var loca buffer
		for i := 0; i <= oldCount; i++ {
			if longLoca {
				loca.u32(binary.BigEndian.Uint32(oldLoca[i*4:]))
			} else {
				loca.u32(uint32(binary.BigEndian.Uint16(oldLoca[i*2:])) * 2)
			}
		}
		end := binary.BigEndian.Uint32(loca.Bytes()[oldCount*4:])
		for i := oldCount; i < count; i++ {
			loca.u32(end)
		}
		tables["loca"] = loca.Bytes()
		binary.BigEndian.PutUint16(tables["head"][50:], 1)
	}
	binary.BigEndian.PutUint32(tables["head"][8:], 0)
	binary.BigEndian.PutUint16(tables["maxp"][4:], uint16(count))
	oldHmtx := tables["hmtx"]
	metricsCount := int(binary.BigEndian.Uint16(tables["hhea"][34:]))
	lastAdvance := binary.BigEndian.Uint16(oldHmtx[(metricsCount-1)*4:])
	var hmtx buffer
	hmtx.Write(oldHmtx[:metricsCount*4])
	for i := metricsCount; i < oldCount; i++ {
		hmtx.u16(lastAdvance)
		hmtx.Write(oldHmtx[metricsCount*4+(i-metricsCount)*2 : metricsCount*4+(i-metricsCount)*2+2])
	}
	advance := lastAdvance
	if w, ok := mapping['W']; ok {
		index := min(int(w), metricsCount-1)
		advance = binary.BigEndian.Uint16(oldHmtx[index*4:])
	}
	for i := oldCount; i < count; i++ {
		hmtx.u16(advance)
		hmtx.u16(0)
	}
	tables["hmtx"] = hmtx.Bytes()
	binary.BigEndian.PutUint16(tables["hhea"][34:], uint16(count))
	tables["cmap"] = preservedCmap(mapping, tables["cmap"])
	tables["name"], err = preservedNamesWithOverrides(tables["name"], namingOptions, nameOverrides)
	if err != nil {
		return Font{}, err
	}
	// v3 post retains italic/underline/monospace fields while avoiding a stale
	// glyph-name array whose count no longer matches maxp.
	tables["post"] = append([]byte(nil), tables["post"][:32]...)
	binary.BigEndian.PutUint32(tables["post"], 0x30000)
	tables["sbix"], err = sbixPreserving(ctx, prepared, count, source.PointSize, source.Baseline)
	if err != nil {
		return Font{}, err
	}
	tables["OAIp"], err = json.Marshal(struct {
		Version int    `json:"version"`
		Source  string `json:"source_postscript"`
		Name    string `json:"source_name,omitempty"`
	}{1, source.SourcePostScript, source.SourceName})
	if err != nil {
		return Font{}, err
	}
	delete(tables, "DSIG") // The original signature cannot describe changed tables.
	if err := ctx.Err(); err != nil {
		return Font{}, err
	}
	return Font{Data: assembleFont(tables), Previews: previews}, nil
}

func preserveSource(source map[string][]byte) (map[string][]byte, int, map[uint32]uint32, error) {
	bad := func(reason string) (map[string][]byte, int, map[uint32]uint32, error) {
		return nil, 0, nil, fmt.Errorf("cannot preserve this font: %s", reason)
	}
	for _, key := range []string{"CFF2", "sbix", "CBDT", "CBLC", "EBDT", "EBLC", "COLR", "SVG "} {
		if len(source[key]) > 0 {
			return bad("this outline or existing bitmap format is not supported")
		}
	}
	// vmtx also has one metric per glyph. Decline these layouts until image
	// glyphs have vertical metrics, rather than copying a now-truncated table
	// or discarding the source font's useful vertical spacing information.
	for _, key := range []string{"vhea", "vmtx"} {
		if _, exists := source[key]; exists {
			return bad("vertical metrics are not supported")
		}
	}
	if err := checkPreservedAATGlyphCoverage(source); err != nil {
		return nil, 0, nil, err
	}
	cff := len(source["CFF "]) > 0
	variable := len(source["fvar"]) > 0
	if variable != (len(source["gvar"]) > 0) || variable && cff {
		return bad("variable TrueType fonts require matching fvar and gvar tables")
	}
	if variable && (len(source["HVAR"]) == 0 || len(source["VVAR"]) > 0) {
		return bad("variable image previews require horizontal advance variations without vertical variations")
	}
	minimum := map[string]int{"head": 54, "hhea": 36, "maxp": 6, "post": 32, "name": 6, "cmap": 4, "hmtx": 4}
	if !cff {
		minimum["maxp"], minimum["loca"] = 32, 4
	}
	for key, n := range minimum {
		if len(source[key]) < n {
			return bad("missing or truncated " + key + " table")
		}
	}
	if data, exists := source["OS/2"]; exists && len(data) < 68 {
		return bad("truncated OS/2 table")
	}
	if _, ok := source["glyf"]; !ok && !cff {
		return bad("missing TrueType outlines")
	} else if ok && cff {
		return bad("both CFF and TrueType outlines are present")
	}
	maxpVersion := uint32(0x10000)
	if cff {
		maxpVersion = 0x5000
	}
	if binary.BigEndian.Uint32(source["maxp"]) != maxpVersion {
		return bad("unsupported maxp version")
	}
	units := binary.BigEndian.Uint16(source["head"][18:])
	if units < 16 || units > 16384 {
		return bad("invalid units per em")
	}
	count := int(binary.BigEndian.Uint16(source["maxp"][4:]))
	if count == 0 {
		return bad("empty glyph table")
	}
	if cff {
		if _, err := readPreservedCFF(source["CFF "], count); err != nil {
			return nil, 0, nil, err
		}
	} else {
		locFormat := binary.BigEndian.Uint16(source["head"][50:])
		if locFormat > 1 {
			return bad("unsupported loca format")
		}
		locaWidth := 2 + 2*int(locFormat)
		if len(source["loca"]) < (count+1)*locaWidth {
			return bad("truncated loca table")
		}
		var prev uint32
		for i := 0; i <= count; i++ {
			var off uint32
			if locFormat == 1 {
				off = binary.BigEndian.Uint32(source["loca"][i*4:])
			} else {
				off = uint32(binary.BigEndian.Uint16(source["loca"][i*2:])) * 2
			}
			if off < prev || uint64(off) > uint64(len(source["glyf"])) {
				return bad("invalid glyph offsets")
			}
			prev = off
		}
	}
	hm := int(binary.BigEndian.Uint16(source["hhea"][34:]))
	if hm < 1 || hm > count || len(source["hmtx"]) < hm*4+(count-hm)*2 {
		return bad("invalid horizontal metrics")
	}
	mapping, err := readPreservedCmap(source["cmap"], count)
	if err != nil {
		return nil, 0, nil, err
	}
	total := 0
	tables := make(map[string][]byte, len(source)+2)
	for tag, data := range source {
		if len(tag) != 4 {
			return bad("invalid table tag")
		}
		total += len(data)
		if total > 64*1024*1024 {
			return bad("source font exceeds 64 MiB")
		}
		// These optional caches contain one entry per original glyph. Let the
		// scaler use the preserved outlines, hinting and hmtx instead of copying
		// arrays that would be too short after appending image glyphs.
		if tag == "LTSH" || tag == "hdmx" {
			continue
		}
		tables[tag] = append([]byte(nil), data...)
	}
	return tables, count, mapping, nil
}

func readPreservedCmap(data []byte, glyphCount int) (map[uint32]uint32, error) {
	fail := func() (map[uint32]uint32, error) {
		return nil, fmt.Errorf("cannot preserve this font: invalid or unsupported Unicode cmap")
	}
	if len(data) < 4 || binary.BigEndian.Uint16(data) != 0 {
		return fail()
	}
	n := int(binary.BigEndian.Uint16(data[2:]))
	if n > (len(data)-4)/8 {
		return fail()
	}
	// Unicode subtables can cover different characters. Retain their combined
	// coverage only when every mapped character has an unambiguous glyph ID.
	mapping := make(map[uint32]uint32)
	seen := make(map[uint32]bool)
	found := false
	for i := 0; i < n; i++ {
		p := 4 + i*8
		platform := binary.BigEndian.Uint16(data[p:])
		enc := binary.BigEndian.Uint16(data[p+2:])
		if platform != 0 && !(platform == 3 && (enc == 1 || enc == 10)) {
			continue
		}
		off := binary.BigEndian.Uint32(data[p+4:])
		if uint64(off) < uint64(4+n*8) || uint64(off)+2 > uint64(len(data)) {
			return fail()
		}
		if seen[off] {
			continue
		}
		seen[off] = true
		sub := data[int(off):]
		format := binary.BigEndian.Uint16(sub)
		if format == 14 {
			// Variation selectors supplement the base Unicode cmap and are kept
			// verbatim by preservedCmap, since original glyph indices are stable.
			if len(sub) < 10 {
				return fail()
			}
			length := uint64(binary.BigEndian.Uint32(sub[2:]))
			if length < 10 || length > uint64(len(sub)) || uint64(binary.BigEndian.Uint32(sub[6:])) > (length-10)/11 {
				return fail()
			}
			continue
		}
		part, err := readPreservedCmapSubtable(sub, glyphCount)
		if err != nil {
			return nil, err
		}
		for cp, gid := range part {
			if previous, exists := mapping[cp]; exists && previous != gid {
				return nil, fmt.Errorf("cannot preserve this font: conflicting Unicode cmap mappings")
			}
			mapping[cp] = gid
		}
		found = true
	}
	if !found {
		return fail()
	}
	return mapping, nil
}

func readPreservedCmapSubtable(chosen []byte, glyphCount int) (map[uint32]uint32, error) {
	fail := func() (map[uint32]uint32, error) {
		return nil, fmt.Errorf("cannot preserve this font: invalid or unsupported Unicode cmap")
	}
	if len(chosen) < 16 {
		return fail()
	}
	format := binary.BigEndian.Uint16(chosen)
	mapping := make(map[uint32]uint32)
	if format == 12 || format == 13 {
		length := uint64(binary.BigEndian.Uint32(chosen[4:]))
		if length < 16 || length > uint64(len(chosen)) {
			return fail()
		}
		chosen = chosen[:int(length)]
		groups := uint64(binary.BigEndian.Uint32(chosen[12:]))
		if groups > uint64((len(chosen)-16)/12) {
			return fail()
		}
		var last uint32
		constant := format == 13
		for i := 0; i < int(groups); i++ {
			p := 16 + i*12
			start, end, gid := binary.BigEndian.Uint32(chosen[p:]), binary.BigEndian.Uint32(chosen[p+4:]), binary.BigEndian.Uint32(chosen[p+8:])
			if start > end || end > 0x10ffff || (i > 0 && start <= last) {
				return fail()
			}
			maxGlyph := uint64(gid)
			if !constant {
				maxGlyph += uint64(end - start)
			}
			if maxGlyph >= uint64(glyphCount) {
				return fail()
			}
			for cp := start; cp <= end; cp++ {
				g := gid
				if !constant {
					g += cp - start
				}
				if g != 0 {
					mapping[cp] = g
				}
			}
			last = end
		}
	} else if format == 4 {
		length := int(binary.BigEndian.Uint16(chosen[2:]))
		if length < 16 || length > len(chosen) {
			return fail()
		}
		chosen = chosen[:length]
		segmentBytes := int(binary.BigEndian.Uint16(chosen[6:]))
		segments := segmentBytes / 2
		if segmentBytes%2 != 0 || segments == 0 || 16+segments*8 > length {
			return fail()
		}
		var previous uint32
		for i := 0; i < segments; i++ {
			end := uint32(binary.BigEndian.Uint16(chosen[14+i*2:]))
			start := uint32(binary.BigEndian.Uint16(chosen[16+segments*2+i*2:]))
			delta := binary.BigEndian.Uint16(chosen[16+segments*4+i*2:])
			rangePos := 16 + segments*6 + i*2
			distance := int(binary.BigEndian.Uint16(chosen[rangePos:]))
			if start > end || (i > 0 && start <= previous) || distance%2 != 0 {
				return fail()
			}
			for cp := start; cp <= end; cp++ {
				if cp == 65535 {
					continue
				}
				var gid uint16
				if distance == 0 {
					gid = uint16(cp) + delta
				} else {
					p := rangePos + distance + int(cp-start)*2
					if p < 16+segments*8 || p+2 > len(chosen) {
						return fail()
					}
					gid = binary.BigEndian.Uint16(chosen[p:])
					if gid != 0 {
						gid += delta
					}
				}
				if int(gid) >= glyphCount {
					return fail()
				}
				if gid != 0 {
					mapping[cp] = uint32(gid)
				}
			}
			previous = end
		}
	} else {
		// Rewriting an unsupported Unicode subtable would discard its coverage.
		return fail()
	}
	return mapping, nil
}

func preservedCmap(mapping map[uint32]uint32, old []byte) []byte {
	keys := make([]uint32, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	type group struct{ start, end, glyph uint32 }
	groups := make([]group, 0, len(keys))
	for _, cp := range keys {
		gid := mapping[cp]
		if len(groups) > 0 {
			last := &groups[len(groups)-1]
			if last.end+1 == cp && last.glyph+cp-last.start == gid {
				last.end = cp
				continue
			}
		}
		groups = append(groups, group{cp, cp, gid})
	}
	var sub buffer
	sub.u16(12)
	sub.u16(0)
	sub.u32(uint32(16 + len(groups)*12))
	sub.u32(0)
	sub.u32(uint32(len(groups)))
	for _, g := range groups {
		sub.u32(g.start)
		sub.u32(g.end)
		sub.u32(g.glyph)
	}
	// Preserve variation selectors (format 14) and non-Unicode cmap subtables.
	type extra struct {
		platform, encoding uint16
		data               []byte
		offset             uint32
	}
	var extras []extra
	n := int(binary.BigEndian.Uint16(old[2:]))
	for i := 0; i < n; i++ {
		p := 4 + i*8
		platform, encoding := binary.BigEndian.Uint16(old[p:]), binary.BigEndian.Uint16(old[p+2:])
		off := uint64(binary.BigEndian.Uint32(old[p+4:]))
		if off+2 > uint64(len(old)) {
			continue
		}
		d := old[int(off):]
		format := binary.BigEndian.Uint16(d)
		keep := format == 14 || platform != 0 && !(platform == 3 && (encoding == 1 || encoding == 10))
		if !keep {
			continue
		}
		length := 0
		if format == 14 && len(d) >= 6 {
			length = int(binary.BigEndian.Uint32(d[2:]))
		} else if (format == 8 || format == 10 || format == 12 || format == 13) && len(d) >= 8 {
			length = int(binary.BigEndian.Uint32(d[4:]))
		} else if len(d) >= 4 {
			length = int(binary.BigEndian.Uint16(d[2:]))
		}
		if length >= 4 && length <= len(d) {
			extras = append(extras, extra{platform: platform, encoding: encoding, data: d[:length]})
		}
	}
	var out buffer
	out.u16(0)
	out.u16(uint16(2 + len(extras)))
	offset := 4 + (2+len(extras))*8
	records := []extra{{platform: 0, encoding: 4, offset: uint32(offset)}, {platform: 3, encoding: 10, offset: uint32(offset)}}
	offset += sub.Len()
	for _, e := range extras {
		e.offset = uint32(offset)
		records = append(records, e)
		offset += len(e.data)
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].platform < records[j].platform || records[i].platform == records[j].platform && records[i].encoding < records[j].encoding
	})
	for _, r := range records {
		out.u16(r.platform)
		out.u16(r.encoding)
		out.u32(r.offset)
	}
	out.Write(sub.Bytes())
	for _, e := range extras {
		out.Write(e.data)
	}
	return out.Bytes()
}

func preservedNames(old []byte, options Options) ([]byte, error) {
	return preservedNamesWithOverrides(old, options, nil)
}

func preservedNamesWithOverrides(old []byte, options Options, overrides map[uint16]string) ([]byte, error) {
	fail := func() ([]byte, error) { return nil, fmt.Errorf("cannot preserve this font: invalid name table") }
	if len(old) < 6 {
		return fail()
	}
	format := binary.BigEndian.Uint16(old)
	count := int(binary.BigEndian.Uint16(old[2:]))
	storage := int(binary.BigEndian.Uint16(old[4:]))
	if format > 1 || count > (len(old)-6)/12 || storage > len(old) || storage < 6+count*12 {
		return fail()
	}
	var languageTags [][]byte
	if format == 1 {
		p := 6 + count*12
		if p+2 > storage {
			return fail()
		}
		languages := int(binary.BigEndian.Uint16(old[p:]))
		if languages > (storage-p-2)/4 {
			return fail()
		}
		for i := 0; i < languages; i++ {
			q := p + 2 + i*4
			length, offset := int(binary.BigEndian.Uint16(old[q:])), int(binary.BigEndian.Uint16(old[q+2:]))
			if storage+offset+length > len(old) {
				return fail()
			}
			languageTags = append(languageTags, old[storage+offset:storage+offset+length])
		}
	}
	type record struct {
		platform, encoding, language, id uint16
		data                             []byte
	}
	var records []record
	encoded := func(s string, wide bool) []byte {
		if !wide {
			return []byte(s)
		}
		var b buffer
		for _, c := range utf16.Encode([]rune(s)) {
			b.u16(c)
		}
		return b.Bytes()
	}
	seenOverrides := make(map[uint16]bool)
	for i := 0; i < count; i++ {
		p := 6 + i*12
		platform, encoding, language, id := binary.BigEndian.Uint16(old[p:]), binary.BigEndian.Uint16(old[p+2:]), binary.BigEndian.Uint16(old[p+4:]), binary.BigEndian.Uint16(old[p+6:])
		length, off := int(binary.BigEndian.Uint16(old[p+8:])), int(binary.BigEndian.Uint16(old[p+10:]))
		if storage+off+length > len(old) {
			return fail()
		}
		value := append([]byte(nil), old[storage+off:storage+off+length]...)
		replacement := ""
		switch id {
		case 1, 16, 21:
			replacement = options.Family
		case 3, 4, 6, 18:
			replacement = options.PostScript
		}
		if override, ok := overrides[id]; ok {
			replacement = override
		}
		if replacement != "" {
			if platform != 0 && platform != 1 && platform != 3 {
				continue
			}
			value = encoded(replacement, platform != 1)
		}
		records = append(records, record{platform, encoding, language, id, value})
		seenOverrides[id] = true
	}
	for id, value := range overrides {
		if !seenOverrides[id] {
			records = append(records, record{3, 1, 0x409, id, encoded(value, true)})
		}
	}
	if len(overrides) > 0 {
		sort.SliceStable(records, func(i, j int) bool {
			a, b := records[i], records[j]
			if a.platform != b.platform {
				return a.platform < b.platform
			}
			if a.encoding != b.encoding {
				return a.encoding < b.encoding
			}
			if a.language != b.language {
				return a.language < b.language
			}
			return a.id < b.id
		})
	}
	var head, strings buffer
	head.u16(format)
	head.u16(uint16(len(records)))
	newStorage := 6 + len(records)*12
	if format == 1 {
		newStorage += 2 + len(languageTags)*4
	}
	if newStorage > 65535 {
		return fail()
	}
	head.u16(uint16(newStorage))
	for _, r := range records {
		if len(r.data) > 65535 || strings.Len() > 65535 {
			return fail()
		}
		head.u16(r.platform)
		head.u16(r.encoding)
		head.u16(r.language)
		head.u16(r.id)
		head.u16(uint16(len(r.data)))
		head.u16(uint16(strings.Len()))
		strings.Write(r.data)
	}
	if format == 1 {
		head.u16(uint16(len(languageTags)))
		for _, data := range languageTags {
			if len(data) > 65535 || strings.Len() > 65535 {
				return fail()
			}
			head.u16(uint16(len(data)))
			head.u16(uint16(strings.Len()))
			strings.Write(data)
		}
	}
	head.Write(strings.Bytes())
	return head.Bytes(), nil
}
