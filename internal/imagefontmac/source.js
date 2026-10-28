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

function sourceFontSizeFailure() {
    var error = new Error("font export limit");
    error.reason = "size";
    throw error;
}

function sourceFontText(value) {
    var text = ObjC.unwrap(ObjC.castRefToObject(value));
    if (typeof text !== "string" || text.length > 255) { sourceFontSizeFailure(); }
    return text;
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
        var actual = sourceFontText($.CTFontCopyPostScriptName(font));
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
        // Match the encoder's 64 MiB decoded-table limit for each face. The
        // selected face and its three styles may use 256 MiB together.
        var faceLimit = 64 * 1024 * 1024, familyBytes = 0;
        function exportFont(face) {
            var available = $.CTFontCopyAvailableTables(face, 0), tables = {}, pending = [];
            var count = Number($.CFArrayGetCount(available)), faceBytes = 0;
            if (!isFinite(count) || count < 1 || Math.floor(count) !== count || count > 65535) { sourceFontSizeFailure(); }
            for (var i = 0; i < count; i++) {
                var tag = Number($.CFArrayGetValueAtIndex(available, i));
                var name = String.fromCharCode((tag >>> 24) & 255, (tag >>> 16) & 255, (tag >>> 8) & 255, tag & 255);
                var data = ObjC.castRefToObject($.CTFontCopyTable(face, tag, 0));
                if (!data) { throw new Error("tables"); }
                var length = Number(data.length);
                if (!isFinite(length) || length < 0 || Math.floor(length) !== length || length > faceLimit - faceBytes) { sourceFontSizeFailure(); }
                faceBytes += length;
                pending.push({name: name, data: data});
            }
            if (faceBytes > 4 * faceLimit - familyBytes) { sourceFontSizeFailure(); }
            familyBytes += faceBytes;
            var nativeFont = face; // Already an NSFont object, not a CF Ref.
            var character = Ref("unsigned short"), glyph = Ref("unsigned short");
            character[0] = 87; // Terminal uses the advance of W for its cell width.
            if (!$.CTFontGetGlyphsForCharacters(face, character, glyph, 1)) { throw new Error("glyph"); }
            var exportedFace = {
                postscript: sourceFontText($.CTFontCopyPostScriptName(face)),
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
                var axisCount = Number(axes.count);
                if (!isFinite(axisCount) || Math.floor(axisCount) !== axisCount || axisCount < 0 || axisCount > 65535) { sourceFontSizeFailure(); }
                for (var axis = 0; axis < axisCount; axis++) {
                    var key = axes.objectAtIndex(axis);
                    var tagName = String(ObjC.unwrap(key)), coordinate = Number(ObjC.unwrap(variation.objectForKey(key)));
                    if (!/^[0-9]{1,10}$/.test(tagName) || Number(tagName) > 0xffffffff || !isFinite(coordinate)) { throw new Error("variation"); }
                    coordinates[tagName] = coordinate;
                }
                if (Object.keys(coordinates).length > 0) { exportedFace.variations = coordinates; }
            }
            if (bundled) {
                // Different bundled outlines can share a PostScript name. The
                // full face name identifies the same file/instance next time.
                exportedFace.lookup_name = sourceFontText($.CTFontCopyFullName(face));
            }
            // Check all table lengths and metadata before creating base64 copies.
            pending.forEach(function(table) {
                tables[table.name] = ObjC.unwrap(table.data.base64EncodedStringWithOptions(0));
            });
            return exportedFace;
        }
        var familyFaces = [font];
        if (actual.indexOf("OpenAIImages-") !== 0) {
            var family = ObjC.unwrap(ObjC.castRefToObject($.CTFontCopyFamilyName(font)));
            var seen = {};
            seen[actual] = true;
            var mask = Number($.kCTFontBoldTrait) | Number($.kCTFontItalicTrait);
            if (bundled) {
                var companions = terminalBundledCompanions(font, size);
                if (companions.length > 3) { sourceFontSizeFailure(); }
                companions.forEach(function(face) {
                    familyFaces.push(face);
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
                if (familyFaces.length >= 4) { sourceFontSizeFailure(); }
                familyFaces.push(companion);
            }
        }
        var exported = exportFont(font);
        exported.companions = familyFaces.slice(1).map(exportFont);
        return JSON.stringify({ok: true, matched_name: matchedName, font: exported});
    } catch (error) {
        // Do not return native diagnostics, private font paths, or metadata.
        if (error && error.reason === "ambiguous") { return failure("ambiguous"); }
        if (error && error.reason === "size") { return failure("size"); }
        return failure("native");
    }
}
