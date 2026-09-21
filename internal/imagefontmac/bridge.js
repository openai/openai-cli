// This bridge calls local font APIs only. It does not send AppleEvents or
// address Terminal. Arguments are data; no caller content is evaluated.
ObjC.import("Foundation");
ObjC.import("CoreText");

function failure(error) {
    var code = error[0] ? Number($.CFErrorGetCode(error[0])) : 0;
    return {ok: false, code: code};
}

function run(argv) {
    if (argv.length !== 2) { return JSON.stringify({ok: false, code: 0}); }
    var action = argv[0];
    var url = $.NSURL.fileURLWithPath(argv[1]);
    var error = Ref();
    var ok = false;
    if (action === "register") {
        ok = $.CTFontManagerRegisterFontsForURL(url, Number($.kCTFontManagerScopeSession), error);
    } else if (action === "unregister") {
        ok = $.CTFontManagerUnregisterFontsForURL(url, Number($.kCTFontManagerScopeSession), error);
        if (!ok && failure(error).code === Number($.kCTFontManagerErrorNotRegistered)) {
            ok = true;
        }
    }
    return JSON.stringify(ok ? {ok: true} : failure(error));
}
