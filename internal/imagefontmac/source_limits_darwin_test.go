package imagefontmac

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// All native objects are replaced before evaluation. Synthetic NSData reports
// large lengths without allocating those bytes or reading installed fonts.
func TestSourceBridgeChecksLimitsBeforeEncoding(t *testing.T) {
	if !Supported() {
		t.Skip("built-in JavaScript interpreter unavailable")
	}
	const faceLimit = 64 << 20
	for _, test := range []struct {
		name       string
		options    map[string]any
		ok         bool
		encoded    int
		companions int
	}{
		{"exact face limit", map[string]any{"lengths": []int{faceLimit}}, true, 1, 0},
		{"oversized table", map[string]any{"lengths": []int{faceLimit + 1}}, false, 0, 0},
		{"combined table limit", map[string]any{"lengths": []int{40 << 20, (24 << 20) + 1}}, false, 0, 0},
		{"four full faces", map[string]any{"lengths": []int{faceLimit}, "faces": 4}, true, 4, 3},
		{"oversized companion", map[string]any{"lengths": []int{1}, "faces": 2, "companionLength": faceLimit + 1}, false, 1, 0},
		{"too many companions", map[string]any{"lengths": []int{1}, "faces": 5, "bundled": true}, false, 0, 0},
		{"too many tables", map[string]any{"lengths": []int{1}, "tableCount": 65536}, false, 0, 0},
		{"too many axes", map[string]any{"lengths": []int{1}, "axisCount": 65536}, false, 0, 0},
		{"maximum metadata counts", map[string]any{"lengths": []int{1}, "tableCount": 65535, "axisCount": 65535}, true, 65535, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, err := json.Marshal(test.options)
			require.NoError(t, err)
			logic := strings.NewReplacer("ObjC", "mockObjC", "$.", "mockNative.", "$(", "mockNative(", "Ref(", "mockRef(").Replace(sourceMainBridge)
			logic = strings.Replace(logic, "function run(argv)", "function sourceOperation(argv)", 1)
			logic = "var options = " + string(options) + ";\n" + sourceLimitsMock + logic + `
function run() {
    var result = JSON.parse(sourceOperation([faces[0].name, "13"]));
    return JSON.stringify({ok: result.ok, reason: result.reason || "", encoded: encoded,
        companions: result.font && result.font.companions ? result.font.companions.length : 0});
}`
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			data, err := run(ctx, interpreter, []string{"-l", "JavaScript", "-e", logic}, []string{})
			require.NoError(t, err)
			var result struct {
				OK                  bool
				Reason              string
				Encoded, Companions int
			}
			require.NoError(t, json.Unmarshal(data, &result))
			require.Equal(t, test.ok, result.OK)
			if !test.ok {
				require.Equal(t, "size", result.Reason)
			}
			require.Equal(t, test.encoded, result.Encoded, "reject oversized native data before base64 conversion")
			require.Equal(t, test.companions, result.Companions)
		})
	}
}

const sourceLimitsMock = `
var encoded = 0, faces = [];
for (var i = 0; i < (options.faces || 1); i++) {
    var name = "Synthetic" + i;
    faces.push({name: name, fontName: name, displayName: name, familyName: "Synthetic", style: i,
        lengths: i && options.companionLength ? [options.companionLength] : options.lengths});
}
var mockObjC = {import: function() {}, bindFunction: function() {}, unwrap: function(v) {return v;}, castRefToObject: function(v) {return v;}};
function mockRef() {return [];}
function mockNative(v) {return v;}
mockNative.NSFont = {fontWithNameSize: function(name) {
    if (options.bundled) {return null;}
    for (var i = 0; i < faces.length; i++) {if (faces[i].name === name) {return faces[i];}}
    return null;
}};
mockNative.CTFontCopyPostScriptName = function(face) {return face.name;};
mockNative.CTFontCopyFullName = function(face) {return face.name;};
mockNative.CTFontCopyFamilyName = function(face) {return "Synthetic";};
mockNative.CTFontCopyAvailableTables = function(face) {return face;};
mockNative.CFArrayGetCount = function(face) {return options.tableCount || face.lengths.length;};
mockNative.CFArrayGetValueAtIndex = function(face, index) {return 0x74610000 + index;};
mockNative.CTFontCopyTable = function(face, tag) {return {
    length: face.lengths[tag - 0x74610000] || 1,
    base64EncodedStringWithOptions: function() {encoded++; return "eA==";}
};};
mockNative.CTFontGetGlyphsForCharacters = function() {return true;};
mockNative.CTFontGetSymbolicTraits = function(face) {return face.style;};
mockNative.CTFontGetAscent = function() {return 10;};
mockNative.CTFontGetDescent = function() {return 3;};
mockNative.CTFontGetLeading = function() {return 0;};
mockNative.CTFontGetAdvancesForGlyphs = function() {return 7;};
mockNative.NSLayoutManager = {alloc: {init: {defaultLineHeightForFont: function() {return 14;}}}};
mockNative.CTFontCopyVariation = function() {return options.axisCount ? {
    count: options.axisCount,
    allKeys: {count: options.axisCount, objectAtIndex: function(i) {return i;}},
    objectForKey: function() {return 0;}
} : null;};
mockNative.kCTFontBoldTrait = 2;
mockNative.kCTFontItalicTrait = 1;
mockNative.CTFontCreateCopyWithSymbolicTraits = function(face, size, matrix, traits) {return faces[traits] || null;};
function terminalBundledFont() {return options.bundled ? faces[0] : null;}
function terminalBundledCompanions() {return faces.slice(1);}
`
