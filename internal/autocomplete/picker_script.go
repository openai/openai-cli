package autocomplete

import (
	"fmt"
	"strings"
)

// Picker hooks share the completion script distribution, but are opt-in because
// they change an interactive key's behavior. They never edit startup files.
func renderPickerCompletion(shell CompletionStyle, appName string) (string, error) {
	if appName == "" {
		return "", fmt.Errorf("a command name is required for picker integration")
	}
	for i, r := range appName {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || i > 0 && r >= '0' && r <= '9') {
			return "", fmt.Errorf("picker integration requires a simple command name on PATH")
		}
	}
	if shell == CompletionStylePowershell {
		// PSReadLine can prefetch input before invoking a key handler. A child
		// launched by that handler cannot consume it, so it can reach the shell
		// after cancellation. Keep Tab in the line editor and use normal Enter.
		return "", fmt.Errorf("PowerShell uses normal Tab completion. Type %s images generate and press Enter to open the image picker.", appName)
	}
	files := map[CompletionStyle]string{
		CompletionStyleBash: "bash_picker.bash", CompletionStyleZsh: "zsh_picker.zsh",
		CompletionStyleFish: "fish_picker.fish",
	}
	name, ok := files[shell]
	if !ok {
		return "", fmt.Errorf("unsupported picker shell")
	}
	script, err := autoCompleteFS.ReadFile("shellscripts/" + name)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(script), "__APPNAME__", appName), nil
}
