// Read installed font tables and metrics only. This bridge never registers a
// font, reads application preferences, sends AppleEvents, or opens Terminal.
ObjC.import("Foundation");
ObjC.import("AppKit");
ObjC.import("CoreText");

// CTFontCopyAvailableTables returns a CFArray of raw integer table tags, not
// CFNumber objects. Preserve their pointer-sized integer values at the ABI.
ObjC.bindFunction("CFArrayGetValueAtIndex", ["unsigned long", ["void *", "long"]]);

// Positive, exact alias matching keeps display/family names usable without
// accepting a silently substituted font. This also works for file descriptors.
function sourceFontMatchesName(font, requested) {
    if (!font) { return false; }
    var aliases = [ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyPostScriptName(font))),
        ObjC.unwrap(font.fontName), ObjC.unwrap(font.displayName), ObjC.unwrap(font.familyName),
        ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyFullName(font)))];
    return aliases.indexOf(requested) !== -1;
}

function resolveInstalledSourceFont(requested, size) {
    var font = $.NSFont.fontWithNameSize($(requested), size);
    return sourceFontMatchesName(font, requested) ? font : null;
}

function run(argv) {
    function failure(reason) { return JSON.stringify({ok: false, reason: reason}); }
    if (argv.length !== 2) { return failure("arguments"); }
    var requested = argv[0], size = Number(argv[1]);
    if (!requested || !isFinite(size) || size <= 0 || Math.floor(size) !== size) {
        return failure("arguments");
    }
    try {
        // AppKit accepts the actual face names used by Terminal (including
        // display names) and returns nil for missing fonts. CoreText's name
        // constructor instead silently substitutes Helvetica for a miss.
        var font = resolveInstalledSourceFont(requested, size);
        var bundled = false;
        if (!font) {
            font = terminalBundledFont(requested, size);
            bundled = Boolean(font);
        }
        if (!font) { return failure("missing"); }
        var actual = ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyPostScriptName(font)));
        // Use this same NSFont object for table and metric reads. Recreating it
        // through a PostScript-name lookup can lose private system UI faces.
        var matchedName = actual === requested ? "" : requested;
        if (actual.indexOf("OpenAIImages-") === 0) {
            // Only lineage is needed to resolve our original text face. Never
            // enumerate or copy the potentially large cached sbix image atlas.
            var lineage = ObjC.castRefToObject($.CTFontCopyTable(font, 0x4f414970, 0)); // OAIp
            var originTables = {};
            if (lineage && Number(lineage.length) <= 4096) {
                originTables.OAIp = ObjC.unwrap(lineage.base64EncodedStringWithOptions(0));
            }
            return JSON.stringify({ok: true, matched_name: matchedName, font: {postscript: actual, tables: originTables}});
        }
        function exportFont(face) {
            var available = $.CTFontCopyAvailableTables(face, 0), tables = {};
            for (var i = 0; i < Number($.CFArrayGetCount(available)); i++) {
                var tag = Number($.CFArrayGetValueAtIndex(available, i));
                var name = String.fromCharCode((tag >>> 24) & 255, (tag >>> 16) & 255, (tag >>> 8) & 255, tag & 255);
                var data = ObjC.castRefToObject($.CTFontCopyTable(face, tag, 0));
                if (!data) { throw new Error("tables"); }
                tables[name] = ObjC.unwrap(data.base64EncodedStringWithOptions(0));
            }
            var nativeFont = face; // Already an NSFont object, not a CF Ref.
            var character = Ref("unsigned short"), glyph = Ref("unsigned short");
            character[0] = 87; // Terminal uses the advance of W for its cell width.
            if (!$.CTFontGetGlyphsForCharacters(face, character, glyph, 1)) { throw new Error("glyph"); }
            var exportedFace = {
                postscript: ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyPostScriptName(face))),
                style: ["Regular", "Italic", "Bold", "BoldItalic"][Number($.CTFontGetSymbolicTraits(face)) & 3],
                family_class: Number($.CTFontGetSymbolicTraits(face)) >>> 28,
                tables: tables,
                ascent: Number($.CTFontGetAscent(face)),
                descent: Number($.CTFontGetDescent(face)),
                leading: Number($.CTFontGetLeading(face)),
                advance: Number($.CTFontGetAdvancesForGlyphs(face, 0, glyph, null, 1)),
                line_height: Number($.NSLayoutManager.alloc.init.defaultLineHeightForFont(nativeFont))
            };
            var variation = ObjC.castRefToObject($.CTFontCopyVariation(face));
            if (variation && Number(variation.count) > 0) {
                var axes = variation.allKeys, coordinates = {};
                for (var axis = 0; axis < Number(axes.count); axis++) {
                    var key = axes.objectAtIndex(axis);
                    coordinates[String(ObjC.unwrap(key))] = Number(ObjC.unwrap(variation.objectForKey(key)));
                }
                if (Object.keys(coordinates).length > 0) { exportedFace.variations = coordinates; }
            }
            if (bundled) {
                // Different bundled outlines can share a PostScript name. The
                // full face name identifies the same file/instance next time.
                exportedFace.lookup_name = ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyFullName(face)));
            }
            return exportedFace;
        }
        var exported = exportFont(font);
        if (actual.indexOf("OpenAIImages-") !== 0) {
            var family = ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyFamilyName(font)));
            var seen = {};
            seen[actual] = true;
            exported.companions = [];
            var mask = Number($.kCTFontBoldTrait) | Number($.kCTFontItalicTrait);
            if (bundled) {
                terminalBundledCompanions(font, size).forEach(function(face) {
                    exported.companions.push(exportFont(face));
                });
            }
            for (var traits = 0; !bundled && traits <= mask; traits++) {
                var candidate = $.CTFontCreateCopyWithSymbolicTraits(font, size, null, traits, mask);
                if (!ObjC.castRefToObject(candidate)) { continue; }
                var companionName = ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyPostScriptName(candidate)));
                if (seen[companionName]) { continue; }
                // Resolve the face by its exact installed name to exclude
                // synthetic bold/italic and unrelated fallback families.
                var companion = $.NSFont.fontWithNameSize($(companionName), size);
                if (!companion) { continue; }
                if (ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyPostScriptName(companion))) !== companionName ||
                    ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyFamilyName(companion))) !== family ||
                    (Number($.CTFontGetSymbolicTraits(companion)) & mask) !== traits) { continue; }
                seen[companionName] = true;
                exported.companions.push(exportFont(companion));
            }
        }
        return JSON.stringify({ok: true, matched_name: matchedName, font: exported});
    } catch (error) {
        // Do not return native diagnostics, private font paths, or metadata.
        if (error && error.reason === "ambiguous") { return failure("ambiguous"); }
        return failure("native");
    }
}
