package autocomplete

import "errors"

// PackagePickerScript is shipped as a root-owned, world-readable package file.
// It runs only when this package supplies the openai selected by the shell, and
// treats any opt-out marker (including a broken symlink) as a request to stay off.
func PackagePickerScript(shell CompletionStyle) ([]byte, error) {
	return packagePickerScript(shell, "/usr/bin/openai")
}

func packagePickerScript(shell CompletionStyle, executable string) ([]byte, error) {
	if shell != CompletionStyleFish {
		return nil, errors.New("unsupported Linux package picker shell")
	}
	binary := quotePickerPath(shell, executable)
	guard := "status is-interactive; or return 0\n" +
		"# Vendor configuration precedes user PATH, opt-out settings and bindings.\n" +
		"function __openai_package_picker_first_prompt --on-event fish_prompt\n" +
		"functions --erase __openai_package_picker_first_prompt\n" +
		"set -q HOME; and command -sq openai; and test (command -s openai) -ef " + binary + "; or return 0\n" +
		"begin\n    set -l config $HOME/.config\n    if set -q XDG_CONFIG_HOME[1]; and test -n \"$XDG_CONFIG_HOME\"; set config $XDG_CONFIG_HOME; end\n" +
		"    if test -e \"$config/openai/image-picker.json.tab-off-fish\"; or test -L \"$config/openai/image-picker.json.tab-off-fish\"; return 0; end\nend\n"
	content, err := renderInstalledPicker(PickerInstallation{Shell: shell})
	return append(append([]byte(guard), content...), "\nend\n"...), err
}
