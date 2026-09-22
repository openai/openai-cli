package imagefontmac

import (
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

//go:embed source_bundled.js
var testTerminalBundledBridge string

func requireTerminalFontFiles(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Stat(filepath.Join("/System/Applications/Utilities/Terminal.app/Contents/Resources/Fonts", name)); err != nil {
			t.Skipf("optional Terminal font fixture %s is unavailable on this macOS version", name)
		}
	}
}

// These tests inspect only Apple's installed font files. They neither register
// fonts nor issue AppleEvents to Terminal or any other application.
func TestTerminalBundledExactFontResolution(t *testing.T) {
	if !Supported() {
		t.Skip("macOS JavaScript bridge unavailable")
	}
	requireTerminalFontFiles(t, "SF-Mono-Regular.otf", "SF-Mono-RegularItalic.otf")
	for _, test := range []struct {
		requested, postscript, flavor, reason string
	}{
		{"SFMono-Regular", "SFMono-Regular", "CFF", ""},
		{"SF Mono Regular", "SFMono-Regular", "CFF", ""},
		{"SFMonoTerminal-Regular", "SFMonoTerminal-Regular", "glyf", ""},
		{"SFMono-RegularItalic", "", "", "ambiguous"},
		{"SF Mono Regular Italic", "SFMono-RegularItalic", "CFF", ""},
		{"SF Mono Terminal Regular Italic", "SFMono-RegularItalic", "glyf", ""},
		{"SF Mono Terminal", "", "", "ambiguous"},
		{"SF Mono", "", "", "missing"},
		{"OpenAISyntheticUnknownFont", "", "", "missing"},
	} {
		t.Run(test.requested, func(t *testing.T) {
			if strings.Contains(test.requested, "Terminal") || test.requested == "SFMono-RegularItalic" {
				requireTerminalFontFiles(t, "SFMono-Terminal.ttf", "SFMonoItalic-Terminal.ttf")
			}
			logic := `ObjC.import("Foundation"); ObjC.import("AppKit"); ObjC.import("CoreText");` + testTerminalBundledBridge + `
function run(argv) {
    try {
        var font = terminalBundledFont(argv[0], 13);
        if (font === null) { return JSON.stringify({reason: "missing"}); }
        var character = Ref("unsigned short"), glyph = Ref("unsigned short"); character[0] = 87;
        $.CTFontGetGlyphsForCharacters(font, character, glyph, 1);
        return JSON.stringify({postscript: ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyPostScriptName(font))),
            flavor: terminalBundledHasTable(font, 0x43464620) ? "CFF" : "glyf",
            size: Number(font.pointSize), lineHeight: Number($.NSLayoutManager.alloc.init.defaultLineHeightForFont(font)),
            advance: Number($.CTFontGetAdvancesForGlyphs(font, 0, glyph, null, 1))});
    } catch(error) { return JSON.stringify({reason: error.reason || "native"}); }
}`
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			data, err := run(ctx, interpreter, []string{"-l", "JavaScript", "-e", logic, test.requested}, []string{})
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				PostScript, Flavor, Reason string
				Size, LineHeight, Advance  float64
			}
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if result.PostScript != test.postscript || result.Flavor != test.flavor || result.Reason != test.reason {
				t.Fatalf("font was substituted or ambiguous name accepted: %+v", result)
			}
			if test.reason == "" && (result.Size != 13 || result.LineHeight <= 0 || result.Advance <= 0) {
				t.Fatalf("returned object is not usable as the exact NSFont: %+v", result)
			}
		})
	}
}

func TestTerminalBundledCompanionsStayInSelectedFamilyAndFormat(t *testing.T) {
	if !Supported() {
		t.Skip("macOS JavaScript bridge unavailable")
	}
	requireTerminalFontFiles(t, "SF-Mono-Regular.otf", "SF-Mono-Bold.otf", "SF-Mono-RegularItalic.otf", "SF-Mono-BoldItalic.otf")
	for _, name := range []string{"SFMono-Regular", "SFMono-Bold", "SF Mono Regular Italic", "SFMonoTerminal-Regular"} {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(name, "Terminal") {
				requireTerminalFontFiles(t, "SFMono-Terminal.ttf", "SFMonoItalic-Terminal.ttf")
			}
			logic := `ObjC.import("Foundation"); ObjC.import("AppKit"); ObjC.import("CoreText");` + testTerminalBundledBridge + `
function run(argv) {
    function text(value) { return ObjC.unwrap(ObjC.castRefToObject(value)); }
    var font = terminalBundledFont(argv[0], 16), companions = terminalBundledCompanions(font, 16);
    return JSON.stringify({family: text($.CTFontCopyFamilyName(font)), cff: terminalBundledHasTable(font, 0x43464620),
        companions: companions.map(function(face) { return {name: text($.CTFontCopyPostScriptName(face)), family: text($.CTFontCopyFamilyName(face)),
        cff: terminalBundledHasTable(face, 0x43464620), traits: Number($.CTFontGetSymbolicTraits(face)) & 3}; })});
}`
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			data, err := run(ctx, interpreter, []string{"-l", "JavaScript", "-e", logic, name}, []string{})
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Family     string
				CFF        bool
				Companions []struct {
					Name, Family string
					CFF          bool
					Traits       int
				}
			}
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Companions) != 3 {
				t.Fatalf("missing canonical family faces: %+v", result)
			}
			seen := map[int]bool{}
			for _, face := range result.Companions {
				if face.Family != result.Family || face.CFF != result.CFF || seen[face.Traits] || strings.Contains(face.Name, "Heavy") || strings.Contains(face.Name, "Semibold") {
					t.Fatalf("wrong family, outline format, or substituted weight: %+v", result)
				}
				seen[face.Traits] = true
			}
		})
	}
}

func TestTerminalBundledBridgeCannotRegisterOrControlApplications(t *testing.T) {
	for _, forbidden := range []string{"Application(", "RegisterFonts", "UnregisterFonts", "NSUserDefaults", "writeTo", "CTFontCreateWithName"} {
		if strings.Contains(testTerminalBundledBridge, forbidden) {
			t.Fatalf("bundled lookup contains forbidden capability: %s", forbidden)
		}
	}
}
