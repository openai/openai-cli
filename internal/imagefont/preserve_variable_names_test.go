package imagefont

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"strings"
	"testing"
)

func variableIdentityFixture(t *testing.T) (map[string][]byte, PreserveOptions) {
	t.Helper()
	var fvar buffer
	fvar.u32(0x10000)
	fvar.u16(16)
	fvar.u16(2)
	fvar.u16(2)
	fvar.u16(20)
	fvar.u16(3)
	fvar.u16(14)
	for _, a := range []struct {
		tag           string
		min, def, max uint32
		name          uint16
	}{{"wght", 100 << 16, 200 << 16, 900 << 16, 256}, {"YAXS", 0, 300 << 16, 1000 << 16, 257}} {
		fvar.WriteString(a.tag)
		fvar.u32(a.min)
		fvar.u32(a.def)
		fvar.u32(a.max)
		fvar.u16(0)
		fvar.u16(a.name)
	}
	for _, instance := range []struct {
		subfamily  uint16
		wght, yaxs uint32
		ps         uint16
	}{{2, 200 << 16, 300 << 16, 6}, {261, 400 << 16, 21255560, 262}, {263, 700 << 16, 22009397, 264}} {
		fvar.u16(instance.subfamily)
		fvar.u16(0)
		fvar.u32(instance.wght)
		fvar.u32(instance.yaxs)
		fvar.u16(instance.ps)
	}
	name, err := preservedNamesWithOverrides(names(Options{Family: "Source Family", PostScript: "Source-Default"}), Options{Family: "Source Family", PostScript: "Source-Default"}, map[uint16]string{256: "Weight", 257: "Optical", 261: "Regular", 262: "Source-Regular", 263: "Bold", 264: "Source-Bold"})
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"name": name, "fvar": fvar.Bytes()}, PreserveOptions{SourcePostScript: "Source-Regular"}
}

func TestPreservedVariableIdentitySelectsExactNamedInstance(t *testing.T) {
	tables, source := variableIdentityFixture(t)
	original := append([]byte(nil), tables["fvar"]...)
	options := Options{Family: "Private Family", PostScript: strings.Repeat("A", 63)}
	base, overrides, err := preservedVariableIdentity(tables, source, options)
	if err != nil {
		t.Fatal(err)
	}
	if base.Family != options.Family || base.PostScript == options.PostScript || len(base.PostScript) > 63 {
		t.Fatalf("bad base identity %+v", base)
	}
	if overrides[262] != options.PostScript || overrides[264] == "Source-Bold" || overrides[25] == "" || len(overrides[25]) > 63 {
		t.Fatalf("bad overrides %+v", overrides)
	}
	for _, r := range overrides[25] {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			t.Fatal("variation prefix is not alphanumeric")
		}
	}
	if !bytes.Equal(tables["fvar"][16:56], original[16:56]) || binary.BigEndian.Uint16(tables["fvar"][12:]) != 1 || !bytes.Equal(tables["fvar"][56:], original[70:84]) {
		t.Fatal("changed axes or selected coordinate bytes")
	}
	updated, err := preservedNamesWithOverrides(tables["name"], base, overrides)
	if err != nil {
		t.Fatal(err)
	}
	got, err := preservedVariableNameStrings(updated)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[uint16]string{6: base.PostScript, 262: options.PostScript, 25: overrides[25], 261: "Regular", 263: "Bold", 256: "Weight", 257: "Optical"} {
		if len(got[id]) == 0 {
			t.Fatalf("missing name %d", id)
		}
		for _, s := range got[id] {
			if s != want {
				t.Fatalf("name %d = %q, want %q", id, s, want)
			}
		}
	}
}

func TestPreservedVariableIdentityDefault(t *testing.T) {
	tables, source := variableIdentityFixture(t)
	source.SourcePostScript = "Source-Default"
	base, overrides, err := preservedVariableIdentity(tables, source, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	if base != testOptions || binary.BigEndian.Uint16(tables["fvar"][12:]) != 0 || len(tables["fvar"]) != 56 || overrides[6] != "" {
		t.Fatal("default font was not retained as the private base")
	}
}

func TestPreservedVariableIdentityNamedDefaultUsesBaseIdentity(t *testing.T) {
	tables, source := variableIdentityFixture(t)
	// Some fonts give the default coordinates their own named-instance PS ID.
	// CoreText ignores a duplicate named default when resolving its PS name.
	binary.BigEndian.PutUint16(tables["fvar"][68:], 260)
	var err error
	tables["name"], err = preservedNamesWithOverrides(tables["name"], Options{Family: "Source Family", PostScript: "Source-Default"}, map[uint16]string{260: "Source-Light"})
	if err != nil {
		t.Fatal(err)
	}
	source.SourcePostScript = "Source-Light"
	base, overrides, err := preservedVariableIdentity(tables, source, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	if base != testOptions || binary.BigEndian.Uint16(tables["fvar"][12:]) != 0 || overrides[260] == testOptions.PostScript {
		t.Fatal("named default did not use the private base identity")
	}
}

func TestPreservedVariableIdentityValidatesSelectedCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name       string
		variations map[string]float64
		ok         bool
	}{
		{"rounded", map[string]float64{"2003265652": 400, "1497454675": float64(21255560)/65536 - 0.00009}, true},
		{"different weight", map[string]float64{"2003265652": 401, "1497454675": float64(21255560) / 65536}, false},
		{"missing nondefault axis", map[string]float64{"2003265652": 400}, false},
		{"unknown axis", map[string]float64{"1": 1}, false},
		{"invalid axis key", map[string]float64{"wght": 400}, false},
		{"not finite", map[string]float64{"2003265652": math.NaN()}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tables, source := variableIdentityFixture(t)
			source.Variations = tc.variations
			_, _, err := preservedVariableIdentity(tables, source, testOptions)
			if (err == nil) != tc.ok {
				t.Fatalf("error %v, want success %v", err, tc.ok)
			}
		})
	}
}

func TestPreservedVariableIdentityRejectsMalformedSourceWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string][]byte, *PreserveOptions)
	}{
		{"short header", func(m map[string][]byte, s *PreserveOptions) { m["fvar"] = m["fvar"][:15] }},
		{"bad version", func(m map[string][]byte, s *PreserveOptions) { m["fvar"][1] = 2 }},
		{"bad axis offset", func(m map[string][]byte, s *PreserveOptions) { binary.BigEndian.PutUint16(m["fvar"][4:], 65535) }},
		{"bad axis count", func(m map[string][]byte, s *PreserveOptions) { binary.BigEndian.PutUint16(m["fvar"][8:], 65535) }},
		{"bad instance size", func(m map[string][]byte, s *PreserveOptions) { binary.BigEndian.PutUint16(m["fvar"][14:], 1) }},
		{"truncated instances", func(m map[string][]byte, s *PreserveOptions) { m["fvar"] = m["fvar"][:90] }},
		{"missing instance", func(m map[string][]byte, s *PreserveOptions) { s.SourcePostScript = "Missing-Regular" }},
		{"ambiguous instance", func(m map[string][]byte, s *PreserveOptions) { binary.BigEndian.PutUint16(m["fvar"][96:], 262) }},
		{"name label collision", func(m map[string][]byte, s *PreserveOptions) { binary.BigEndian.PutUint16(m["fvar"][70:], 262) }},
		{"missing PS name", func(m map[string][]byte, s *PreserveOptions) { binary.BigEndian.PutUint16(m["fvar"][82:], 300) }},
		{"invalid name storage", func(m map[string][]byte, s *PreserveOptions) { binary.BigEndian.PutUint16(m["name"][4:], 65535) }},
		{"out of range instance", func(m map[string][]byte, s *PreserveOptions) { binary.BigEndian.PutUint32(m["fvar"][74:], 1000<<16) }},
		{"duplicate axes", func(m map[string][]byte, s *PreserveOptions) { copy(m["fvar"][36:40], m["fvar"][16:20]) }},
		{"invalid default", func(m map[string][]byte, s *PreserveOptions) { binary.BigEndian.PutUint32(m["fvar"][24:], 1000<<16) }},
		{"nondefault base identity", func(m map[string][]byte, s *PreserveOptions) {
			s.SourcePostScript = "Source-Default"
			binary.BigEndian.PutUint32(m["fvar"][60:], 201<<16)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tables, source := variableIdentityFixture(t)
			tc.change(tables, &source)
			before := make(map[string][]byte)
			for key, data := range tables {
				before[key] = append([]byte(nil), data...)
			}
			if _, _, err := preservedVariableIdentity(tables, source, testOptions); err == nil {
				t.Fatal("malformed source accepted")
			}
			if !reflect.DeepEqual(before, tables) {
				t.Fatal("failure changed source tables")
			}
		})
	}
}

func TestPreservedVariableIdentityMissingOptionalInstancePS(t *testing.T) {
	tables, source := variableIdentityFixture(t)
	fvar := append([]byte(nil), tables["fvar"][:56]...)
	for i := 0; i < 3; i++ {
		fvar = append(fvar, tables["fvar"][56+i*14:68+i*14]...)
	}
	binary.BigEndian.PutUint16(fvar[14:], 12)
	tables["fvar"] = fvar
	if _, _, err := preservedVariableIdentity(tables, source, testOptions); err == nil {
		t.Fatal("nondefault instance without exact PS identity accepted")
	}
	source.SourcePostScript = "Source-Default"
	if _, _, err := preservedVariableIdentity(tables, source, testOptions); err != nil {
		t.Fatal(err)
	}
}

func TestPreservedVariableIdentityStaticNoOp(t *testing.T) {
	tables := map[string][]byte{"name": {1, 2, 3}}
	base, overrides, err := preservedVariableIdentity(tables, PreserveOptions{}, testOptions)
	if err != nil || base != testOptions || overrides != nil {
		t.Fatalf("static result %v %v %v", base, overrides, err)
	}
}
