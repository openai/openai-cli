package imagefontmac

// Plain objects model temporary tab settings; no Terminal access occurs.
const switchProfileMock = `
var mutations = [], originalName = options.profileName || "Basic";
var originalFont = options.sameFont ? "OpenAIImages-0123abcd-next-Regular" : "Menlo-Regular";
var originalSize = options.size || 14, concurrentDone = false, idReads = 0;
function denied() { var e = new Error("private native text"); e.number = -1743; throw e; }
function theme() { return {background: [11, 22, 33], text: [44, 55, 66], selection: [77, 88, 99],
    ANSIColors: [1, 2, 3, 4, 5, 6, 7, 8], BackgroundBlur: 0.3, BackgroundImage: "synthetic.png",
    FontWidthSpacing: 1.004032258064516, FontHeightSpacing: 1.0, OptionAsMetaKey: true}; }
function profile(id, name, font, size, label) {
    var p = {id: function () { return id; }, name: function () { return name; }, theme: theme()};
    Object.defineProperty(p, "fontName", {
        get: function () { return function () { return font; }; },
        set: function (value) {
            mutations.push(label + " font");
            if (options.fontDenied || (options.rollbackDenied && value === originalFont)) { denied(); }
            if (!options.fontIgnored) { font = value; }
            if (options.fontDeniedAfterWrite && value !== originalFont) { denied(); }
            if (options.concurrentAfterFont && !concurrentDone) { concurrentDone = true; target.externalSelection(); }
            if (options.concurrentFont && !concurrentDone) { concurrentDone = true; mutations.push("external font"); font = "Courier"; }
        }
    });
    Object.defineProperty(p, "fontSize", {
        get: function () { return function () { return size; }; },
        set: function (value) {
            mutations.push(label + " size");
            if (options.sizeDenied) { denied(); }
            if (!options.sizeIgnored) { size = value; }
            if (options.sizeDeniedAfterWrite && value !== originalSize) { denied(); }
        }
    });
    return p;
}
var saved = profile(1, originalName, originalFont, originalSize, "saved");
function tab(tty, id, label) {
    var settings = profile(id, originalName, originalFont, originalSize, label);
    var live = {
        id: function () {
            if (label === "target" && options.concurrentBefore && ++idReads === 2 && !concurrentDone) {
                concurrentDone = true; t.externalSelection();
            }
            return settings.id();
        },
        name: function () { return settings.name(); }
    };
    Object.defineProperty(live, "fontName", {
        get: function () { return function () { return settings.fontName(); }; },
        set: function (value) { settings.fontName = value; }
    });
    Object.defineProperty(live, "fontSize", {
        get: function () { return function () { return settings.fontSize(); }; },
        set: function (value) { settings.fontSize = value; }
    });
    var t = {tty: function () { return tty; }, theme: function () { return settings.theme; },
        externalSelection: function () { mutations.push("external selection"); settings = profile(200, "Novel", "Courier", 18, "external"); }};
    Object.defineProperty(t, "currentSettings", {
        get: function () { return function () { return live; }; },
        set: function () { mutations.push(label + " profile assigned"); throw Error("profile assignment forbidden"); }
    });
    Object.defineProperty(t, "selected", {set: function () { mutations.push(label + " selected"); throw Error("focus forbidden"); }});
    return t;
}
var other = tab(options.duplicateTTY ? "/dev/ttys002" : "/dev/ttys001", 101, "other");
var target = tab(options.missingTTY ? "/dev/ttys999" : "/dev/ttys002", 102, "target");
var initialTheme = JSON.stringify(target.theme()), initialOtherTheme = JSON.stringify(other.theme());
function windows() {
    var result = [{tabs: function () { return [other]; }}, {tabs: function () { return [target]; }}];
    if (options.unreadableWindow) {
        var unreadable = {tabs: function () { var error = new Error("private unrelated window text"); error.number = options.windowError; throw error; }};
        if (options.unreadableWindow === "before") { result.unshift(unreadable); } else { result.push(unreadable); }
    }
    return result;
}
var mockTerminal = {
    running: function () { return true; },
    windows: windows,
    settingsSets: function () { mutations.push("saved profiles read"); throw Error("saved profiles must not be read"); },
    activate: function () { mutations.push("activate"); throw Error("focus forbidden"); }
};
`
