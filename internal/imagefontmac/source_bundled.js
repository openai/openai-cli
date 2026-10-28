// Read font descriptors from Apple's fixed, built-in Terminal bundle. No font
// registration or application access is needed. The caller imports Foundation,
// AppKit, and CoreText. Returned objects are NSFont-compatible CoreText fonts.
function terminalBundledHasTable(font, tag) {
    // CTFontHasTable is absent from some shipped JavaScript bridge metadata.
    // CopyTable is available on all supported macOS versions; these system
    // font outlines are small and no user image atlas is involved here.
    var data = ObjC.castRefToObject($.CTFontCopyTable(font, tag, 0));
    return isFinite(Number(data.length));
}

function terminalBundledCandidates(size) {
    var directory = "/System/Applications/Utilities/Terminal.app/Contents/Resources/Fonts";
    var error = Ref();
    var files = ObjC.deepUnwrap($.NSFileManager.defaultManager.contentsOfDirectoryAtPathError(directory, error));
    if (!Array.isArray(files)) { return []; }
    var candidates = [];
    files.sort();
    function text(value) { return ObjC.unwrap(ObjC.castRefToObject(value)); }
    for (var i = 0; i < files.length; i++) {
        // Only direct font files from this immutable system-owned directory.
        if (!/^[A-Za-z0-9_.-]+\.(ttf|otf|ttc)$/i.test(files[i])) { continue; }
        var url = $.NSURL.fileURLWithPath(directory + "/" + files[i]);
        var descriptors = ObjC.castRefToObject($.CTFontManagerCreateFontDescriptorsFromURL(url));
        var count = Number(descriptors.count);
        if (!isFinite(count)) { continue; }
        for (var index = 0; index < count; index++) {
            var nativeFont = $.CTFontCreateWithFontDescriptor(descriptors.objectAtIndex(index), size, null);
            var name = text($.CTFontCopyPostScriptName(nativeFont));
            if (typeof name !== "string" || !name) { continue; }
            candidates.push({
                font: ObjC.castRefToObject(nativeFont),
                postscript: name,
                full: text($.CTFontCopyFullName(nativeFont)),
                display: text($.CTFontCopyDisplayName(nativeFont)),
                family: text($.CTFontCopyFamilyName(nativeFont)),
                style: text($.CTFontCopyName(nativeFont, $.kCTFontStyleNameKey)),
                flavor: terminalBundledHasTable(nativeFont, 0x43464620) ? "CFF" : (terminalBundledHasTable(nativeFont, 0x676c7966) ? "glyf" : "other")
            });
        }
    }
    return candidates;
}

function terminalBundledAmbiguous() {
    var error = new Error("the bundled font name is ambiguous; choose its exact full face name");
    error.reason = "ambiguous";
    throw error;
}

// Exact PostScript, full, or display names are accepted. A family name is not
// a fallback. Duplicate names across distinct descriptors fail closed.
function terminalBundledFont(requested, size) {
    var candidates = terminalBundledCandidates(size), matches = [];
    for (var i = 0; i < candidates.length; i++) {
        var face = candidates[i];
        if (requested === face.postscript || requested === face.full || requested === face.display) {
            matches.push(face.font);
        }
    }
    if (matches.length > 1) { terminalBundledAmbiguous(); }
    return matches.length === 1 ? matches[0] : null;
}

// Companion names must share both family and outline format with the selected
// face. This separates CFF SF Mono from the variable SF Mono Terminal fonts,
// whose italic PostScript names overlap. Other weights are not guessed from
// their symbolic Bold bit: for example, both Bold and Heavy advertise it.
function terminalBundledCompanions(font, size) {
    function text(value) { return ObjC.unwrap(ObjC.castRefToObject(value)); }
    function style(value) {
        var normalized = String(value).replace(/[ -]/g, "").toLowerCase();
        if (normalized === "regular") { return "Regular"; }
        if (normalized === "italic" || normalized === "regularitalic") { return "Italic"; }
        if (normalized === "bold") { return "Bold"; }
        if (normalized === "bolditalic") { return "BoldItalic"; }
        return null;
    }
    var family = text($.CTFontCopyFamilyName(font));
    var name = text($.CTFontCopyPostScriptName(font));
    var selectedStyle = style(text($.CTFontCopyName(font, $.kCTFontStyleNameKey)));
    if (selectedStyle === null) { return []; }
    var flavor = terminalBundledHasTable(font, 0x43464620) ? "CFF" : (terminalBundledHasTable(font, 0x676c7966) ? "glyf" : "other");
    var candidates = terminalBundledCandidates(size), faces = {};
    for (var i = 0; i < candidates.length; i++) {
        var face = candidates[i], faceStyle = style(face.style);
        if (face.family !== family || face.flavor !== flavor || faceStyle === null || faceStyle === selectedStyle || face.postscript === name) { continue; }
        if (faces[faceStyle]) { terminalBundledAmbiguous(); }
        faces[faceStyle] = face.font;
    }
    var result = [];
    ["Regular", "Bold", "Italic", "BoldItalic"].forEach(function(key) { if (faces[key]) { result.push(faces[key]); } });
    return result;
}
