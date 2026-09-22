// Used only by the opt-in CLI workflow. It addresses the caller's exact TTY,
// never the frontmost window. Setup changes only that tab's font;
// the selected profile and all its other appearance settings stay unchanged.
function run(argv) {
    // Terminal gives a tab a temporary copy of a saved profile. Its ID is
    // different, and currentSettings is a live property reference. Snapshot
    // primitive values; never use a saved-profile ID as a postcondition or
    // retain currentSettings as an old profile to restore later.
    function snapshot(settings) {
        return {name: String(settings.name()), font: String(settings.fontName()), size: Number(settings.fontSize())};
    }
    function matches(settings, expected) {
        var actual = snapshot(settings);
        return actual.name === expected.name && actual.font === expected.font && actual.size === expected.size;
    }
    function result(ok, reason, settings) {
        var reply = {ok: ok, reason: reason || ""};
        if (settings) {
            reply.fontName = settings.fontName();
            reply.fontSize = settings.fontSize();
            reply.profileID = Number(settings.id());
            reply.profileName = String(settings.name());
        }
        return JSON.stringify(reply);
    }
    if (argv.length !== 4 && argv.length !== 5) { return result(false, "profile"); }
    var action = argv[0], name = argv[1], tty = argv[2], font = argv[3];
    var match = /^OpenAI Images ([0-9a-f]{8})$/.exec(name);
    if (!match) { return result(false, "profile"); }
    var prefix = "OpenAIImages-" + match[1] + "-";
    if (action !== "check" && action !== "inspect" && action !== "activate" && action !== "unused" && action !== "snapshot" && action !== "preserve") { return result(false, "profile"); }
    if (action !== "unused" && !/^\/dev\/ttys[0-9]+$/.test(tty)) { return result(false, "tab"); }
    if ((action === "activate" || action === "preserve") && (!/^[A-Za-z0-9-]{1,63}$/.test(font) || font.indexOf(prefix) !== 0)) {
        return result(false, "font");
    }
    try {
        var terminal = Application("com.apple.Terminal");
        if (!terminal.running()) { return result(action === "unused", "tab"); }
        var found = null;
        var windows = terminal.windows();
        for (var i = 0; i < windows.length; i++) {
            var tabs;
            try {
                tabs = windows[i].tabs();
            } catch (windowError) {
                var windowCode = Number(windowError.number);
                if (!isFinite(windowCode)) { windowCode = Number(windowError.errorNumber); }
                // Terminal can enumerate Inspector and other windows without
                // tabs. Skip only that missing-object error for an exact-TTY
                // operation. Reset must inspect every window or fail closed.
                if (action !== "unused" && windowCode === -1728) { continue; }
                throw windowError;
            }
            for (var j = 0; j < tabs.length; j++) {
                if (action === "unused") {
                    // Reset is unsafe while any matching tab remains open,
                    // including a renamed profile or a manually changed font.
                    var candidate = tabs[j].currentSettings();
                    if (candidate.name() === name || candidate.fontName().indexOf(prefix) === 0) {
                        return result(false, "in-use");
                    }
                    continue;
                }
                if (tabs[j].tty() === tty) {
                    if (found !== null) { return result(false, "tab"); }
                    found = tabs[j];
                }
            }
        }
        if (action === "unused") { return result(true); }
        if (found === null) { return result(false, "tab"); }
        var settings = found.currentSettings();
        if (action === "snapshot") { return result(true, "", settings); }
        if (action === "preserve") {
            var expected;
            try { expected = JSON.parse(argv[4]); } catch (parseError) { return result(false, "changed"); }
            var captured = snapshot(settings), capturedID = Number(settings.id());
            if (!expected || capturedID !== expected.profileID || captured.name !== expected.profileName || captured.font !== expected.fontName || captured.size !== expected.fontSize) {
                return result(false, "changed");
            }
            if (captured.size < 1 || captured.size > 1024 || captured.size % 1 !== 0) { return result(false, "size"); }
            function sameCapturedTab() {
                var current = found.currentSettings();
                return found.tty() === tty && current.id() === capturedID && current.name() === captured.name;
            }
            function undoPreservedFont() {
                try {
                    if (!sameCapturedTab()) { return true; }
                    var current = found.currentSettings();
                    // A concurrent Inspector change belongs to the user.
                    if (current.fontSize() !== captured.size || (current.fontName() !== font && current.fontName() !== captured.font)) { return true; }
                    if (current.fontName() === font && font !== captured.font) { current.fontName = captured.font; }
                    return matches(found.currentSettings(), captured);
                } catch (undoError) { return false; }
            }
            try {
                if (!sameCapturedTab() || !matches(found.currentSettings(), captured)) { return result(false, "changed"); }
                if (captured.font !== font) { found.currentSettings().fontName = font; }
                var wanted = {name: captured.name, font: font, size: captured.size};
                if (!sameCapturedTab() || !matches(found.currentSettings(), wanted)) {
                    return result(false, undoPreservedFont() ? "changed" : "rollback");
                }
                return result(true, "", found.currentSettings());
            } catch (preserveError) {
                if (!undoPreservedFont()) { return result(false, "rollback"); }
                throw preserveError;
            }
        }
        var ownedFont = settings.fontName().indexOf(prefix) === 0;
        // Inspector labels are not an ownership boundary. Renamed profiles
        // with our registered font still render the same immutable glyphs.
        if (!ownedFont) { return result(false, "profile"); }
        var size = settings.fontSize();
        if (size < 1 || size > 1024 || size % 1 !== 0) { return result(false, "size", settings); }
        if (action === "activate") {
            // Recheck the tab/profile immediately before the only mutation.
            var before = snapshot(settings);
            if (found.tty() !== tty || !matches(found.currentSettings(), before)) {
                return result(false, "changed");
            }
            if (settings.fontName() !== font) { settings.fontName = font; }
            before.font = font;
            if (found.tty() !== tty || !matches(found.currentSettings(), before)) { return result(false, "font"); }
        }
        return result(true, "", settings);
    } catch (error) {
        // -1743 is macOS Automation permission denial. Do not emit arbitrary
        // AppleEvent error text, which can contain paths or other private data.
        var code = Number(error.number);
        if (!isFinite(code)) { code = Number(error.errorNumber); }
        return result(false, code === -1743 ? "permission" : "native");
    }
}
