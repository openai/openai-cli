// Inventory is read-only and all-or-nothing: a failed tab read must never
// make its gallery appear abandoned. This does not launch or focus Terminal.
function run() {
    try {
        var terminal = Application("com.apple.Terminal"), tabs = [];
        if (!terminal.running()) { return JSON.stringify({ok: true, tabs: []}); }
        var windows = terminal.windows();
        for (var i = 0; i < windows.length; i++) {
            var windowTabs;
            try { windowTabs = windows[i].tabs(); }
            catch (error) {
                var code = Number(error.number);
                if (!isFinite(code)) { code = Number(error.errorNumber); }
                // Inspector windows have no tabs. All other errors invalidate
                // the complete inventory, including permission failures.
                if (code === -1728) { continue; }
                throw error;
            }
            for (var j = 0; j < windowTabs.length; j++) {
                tabs.push({tty: String(windowTabs[j].tty()), fontName: String(windowTabs[j].currentSettings().fontName())});
            }
        }
        return JSON.stringify({ok: true, tabs: tabs});
    } catch (error) {
        // Native diagnostics can include private settings. Do not return them.
        return JSON.stringify({ok: false});
    }
}
