package autocomplete

import (
	"fmt"
	"strings"
)

// Suggestion scripts only configure the current shell. They do not edit profiles.
func renderSuggestionCompletion(shell CompletionStyle, appName string, doubleTab bool) (string, error) {
	if appName == "" {
		return "", fmt.Errorf("a command name is required for suggestion integration")
	}
	for i, r := range appName {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || i > 0 && r >= '0' && r <= '9') {
			return "", fmt.Errorf("suggestion integration requires a simple command name on PATH")
		}
	}
	files := map[CompletionStyle]string{
		CompletionStyleBash: "bash_command_insertion.bash",
		CompletionStyleZsh:  "zsh_command_insertion.zsh",
		CompletionStyleFish: "fish_command_insertion.fish",
	}
	name, ok := files[shell]
	if !ok {
		return "", fmt.Errorf("command insertion currently supports Bash 4.3+, zsh, and fish; PowerShell requires a separate shell integration")
	}
	script, err := autoCompleteFS.ReadFile("shellscripts/" + name)
	if err != nil {
		return "", err
	}
	key := "0"
	if doubleTab {
		key = "1"
	}
	content := strings.ReplaceAll(string(script), "__DOUBLE_TAB__", key)
	return strings.ReplaceAll(content, "__APPNAME__", appName), nil
}

func renderSuggestionClickCompletion(appName string) (string, error) {
	script, err := autoCompleteFS.ReadFile("shellscripts/zsh_command_insertion_click.zsh")
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(script), "__APPNAME__", appName), nil
}

func renderSuggestionAdapter(name string) (string, error) {
	if name != "kitty" {
		return "", fmt.Errorf("the supported suggestion adapter is kitty")
	}
	script, err := autoCompleteFS.ReadFile("shellscripts/kitty_command_insertion.py")
	return string(script), err
}
