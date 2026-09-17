package imagefontmac

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These checks only read installed system fonts; they do not register fonts,
// inspect preferences, or control Terminal.
func TestSourceNativeInstalledFaces(t *testing.T) {
	if !Supported() {
		t.Skip("macOS font bridge unavailable")
	}
	for _, name := range []string{"Menlo-Regular", "Monaco", "SFMono-Regular", "Courier"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			font, err := Source(ctx, name, 16)
			if err != nil && strings.Contains(err.Error(), "not available to this command") {
				t.Skip("optional system face is not installed on this macOS version")
			}
			if err != nil {
				t.Fatal(err)
			}
			if font.PostScript != name || font.Advance <= 0 || font.LineHeight <= 0 || len(font.Tables) < 5 {
				t.Fatalf("invalid source metadata: face=%s tables=%d advance=%f height=%f", font.PostScript, len(font.Tables), font.Advance, font.LineHeight)
			}
			for _, face := range append([]SourceFont{font}, font.Companions...) {
				head := face.Tables["head"]
				if len(head) < 20 || binary.BigEndian.Uint32(head[12:16]) != 0x5f0f3cf5 || binary.BigEndian.Uint16(head[18:20]) == 0 {
					t.Errorf("font %s did not preserve a valid TrueType head table", face.PostScript)
				}
				if len(face.Tables["cmap"]) == 0 || len(face.Tables["hmtx"]) == 0 {
					t.Errorf("font %s lost character mapping or metrics", face.PostScript)
				}
			}
			t.Logf("%s: tables=%d companions=%d ascent=%.6f descent=%.6f advance=%.6f line_height=%.6f", font.PostScript, len(font.Tables), len(font.Companions), font.Ascent, font.Descent, font.Advance, font.LineHeight)
		})
	}
}

func TestSourceNativeRejectsFallback(t *testing.T) {
	if !Supported() {
		t.Skip("macOS font bridge unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := Source(ctx, "OpenAISyntheticMissingFont-NotInstalled", 16); err == nil || !strings.Contains(err.Error(), "no fallback font was selected") {
		t.Fatalf("missing font silently selected a fallback: %v", err)
	}
}

func TestSourceGeneratedFontReadsOnlyLineageJavaScript(t *testing.T) {
	if !Supported() {
		t.Skip("built-in JavaScript interpreter unavailable")
	}
	// All native names are shadowed by plain mock objects in a lexical scope.
	// Any enumeration or atlas request throws, regardless of atlas size.
	logic := `
function run(argv) {
    var calls = [], size = argv[0] === "missing" ? -1 : Number(argv[0]);
    function dollar(value) { return value; }
    dollar.NSFont = {fontWithNameSize: function(name) { return {name: name, fontName: name, displayName: name, familyName: name}; }};
    dollar.CTFontCopyPostScriptName = function(font) { return font.name; };
    dollar.CTFontCopyFullName = function(font) { return font.name; };
    dollar.CTFontCopyTable = function(font, tag) {
        calls.push(tag);
        if (tag !== 0x4f414970) { throw Error("must never copy the large sbix atlas"); }
        if (size < 0) { return null; }
        return {length: size, base64EncodedStringWithOptions: function() { calls.push("encoded lineage"); return "e30="; }};
    };
    dollar.CTFontCopyAvailableTables = function() { throw Error("must not enumerate generated tables"); };
    var objc = {import: function() {}, bindFunction: function() {}, unwrap: function(x) { return x; }, castRefToObject: function(x) { return x; }};
    function mockedBridge(ObjC, $, args) {
` + strings.Replace(sourceBridge, "function run(argv)", "function fontSourceOperation(argv)", 1) + `
        return JSON.parse(fontSourceOperation(args));
    }
    var result = mockedBridge(objc, dollar, ["OpenAIImages-0123abcd-0123456789abcdef0123456789abcdef-Regular", "16"]);
    result.calls = calls;
    return JSON.stringify(result);
}`
	for _, size := range []string{"64", "missing", "4097"} {
		t.Run(size, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			data, err := run(ctx, interpreter, []string{"-l", "JavaScript", "-e", logic, size}, []string{})
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				OK    bool
				Font  SourceFont
				Calls []any
			}
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if size == "64" {
				wantCalls = 2
				if string(result.Font.Tables["OAIp"]) != "{}" {
					t.Fatal("small lineage table was not exported")
				}
			} else if len(result.Font.Tables) != 0 {
				t.Fatal("missing or oversized lineage must remain absent")
			}
			if !result.OK || len(result.Calls) != wantCalls || result.Calls[0] != float64(0x4f414970) {
				t.Fatalf("generated font path enumerated tables or read more than lineage: %+v", result)
			}
		})
	}
}

func TestSourceNativeDisplayAliasesPreserveExactFace(t *testing.T) {
	if !Supported() {
		t.Skip("macOS font bridge unavailable")
	}
	for _, test := range []struct{ name, canonical, style string }{
		{"Menlo Regular", "Menlo-Regular", "Regular"},
		{"Menlo Bold", "Menlo-Bold", "Bold"},
		{"Menlo Italic", "Menlo-Italic", "Italic"},
		{"Menlo Bold Italic", "Menlo-BoldItalic", "BoldItalic"},
		{"Andale Mono", "AndaleMono", "Regular"},
		{"Courier New", "CourierNewPSMT", "Regular"},
		{"Menlo", "Menlo-Regular", "Regular"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			got, err := Source(ctx, test.name, 13)
			if err != nil {
				t.Fatal(err)
			}
			if got.PostScript != test.canonical || got.Style != test.style || len(got.Tables["glyf"]) == 0 {
				t.Fatalf("wrong alias face: got=%s %s want=%s %s", got.PostScript, got.Style, test.canonical, test.style)
			}
			canonical, err := Source(ctx, test.canonical, 13)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, canonical) {
				t.Fatal("alias selected different outlines or metrics than canonical name")
			}
		})
	}
}

func TestSourceNativeBundledFacesKeepExactLookupNames(t *testing.T) {
	if !Supported() {
		t.Skip("macOS font bridge unavailable")
	}
	for _, name := range []string{"SFMono-Regular", "SF Mono Regular Italic", "SF Mono Terminal Regular Italic"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			got, err := Source(ctx, name, 13)
			if err != nil {
				t.Fatal(err)
			}
			if got.LookupName == "" {
				t.Fatal("bundled face lost its exact full name")
			}
			roundtrip, err := Source(ctx, got.LookupName, 13)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, roundtrip) {
				t.Fatal("bundled full-name roundtrip changed font tables or identity")
			}
			for _, face := range got.Companions {
				if face.LookupName == "" {
					t.Fatalf("companion %s lost its lookup name", face.PostScript)
				}
			}
		})
	}
}

func TestSourceNativeVariableFaceKeepsSelectedWeight(t *testing.T) {
	if !Supported() {
		t.Skip("macOS font bridge unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	font, err := Source(ctx, "SFMonoTerminal-Regular", 13)
	if err != nil && strings.Contains(err.Error(), "not available to this command") {
		t.Skip("optional bundled variable font is unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(font.Tables["fvar"]) == 0 {
		t.Skip("this macOS version supplies a static face")
	}
	// The bundled font defaults to Light. Recreating its default descriptor or
	// coercing an NSNumber without unwrapping it loses the selected Regular face.
	if math.Abs(font.Variations["2003265652"]-400) > 0.0002 {
		t.Fatalf("selected Regular weight was lost: %v", font.Variations)
	}
	for _, face := range font.Companions {
		if len(face.Tables["fvar"]) > 0 && len(face.Variations) == 0 {
			t.Fatalf("companion %s lost its selected variation", face.PostScript)
		}
	}
}
