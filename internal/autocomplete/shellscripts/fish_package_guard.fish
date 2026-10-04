# Run inside the shared first-prompt handler, after PATH and user settings load.
set -q HOME[1]; and string match --quiet '/*' -- "$HOME"; or return
test "$(type --type openai 2>/dev/null)" = file; or return
command -sq openai; and command test (command -s openai) -ef __OPENAI_PACKAGE_EXECUTABLE__ 2>/dev/null; or return

set -l config "$HOME/.config"
if set -q XDG_CONFIG_HOME[1]; and test -n "$XDG_CONFIG_HOME"
    set config "$XDG_CONFIG_HOME"
end
# Honor shared consent and any unmigrated preference in the current legacy root.
for marker in "$HOME/.openai/shell/image-picker.tab-off-fish" "$config/openai/image-picker.json.tab-off-fish"
    if test -e "$marker"; or test -L "$marker"
        return
    end
end
