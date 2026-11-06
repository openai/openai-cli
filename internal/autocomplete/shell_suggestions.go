package autocomplete

import (
	"fmt"
	"regexp"
	"strings"
)

// RenderShellSuggestions emits an explicitly requested, session-only shell hook.
// The hook examines executable spelling, never request arguments or shell input.
func RenderShellSuggestions(shell CompletionStyle, appName string) (string, error) {
	if len(appName) == 0 || len(appName) > 64 {
		return "", fmt.Errorf("executable suggestions require a simple command name of 1 to 64 ASCII characters")
	}
	for i, char := range appName {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char == '_' || i > 0 && char >= '0' && char <= '9') {
			return "", fmt.Errorf("executable suggestions require a simple command name on PATH")
		}
	}
	files := map[CompletionStyle]string{
		CompletionStyleBash: "bash_suggestions.bash", CompletionStyleZsh: "zsh_suggestions.zsh",
		CompletionStyleFish: "fish_suggestions.fish", CompletionStylePowershell: "pwsh_suggestions.ps1",
	}
	name, ok := files[shell]
	if !ok {
		return "", fmt.Errorf("unsupported executable suggestion shell")
	}
	script, err := autoCompleteFS.ReadFile("shellscripts/" + name)
	if err != nil {
		return "", err
	}
	pattern, expression := shellSuggestionMatchers(appName)
	return strings.NewReplacer("__APPNAME__", appName, "__SUGGESTION_PATTERN__", pattern,
		"__SUGGESTION_REGEX__", expression).Replace(string(script)), nil
}

// Each pattern changes at most one ASCII character. Generated patterns are
// bounded by the executable name, not by input size or the command inventory.
func shellSuggestionMatchers(name string) (string, string) {
	const character = "[A-Za-z0-9_-]"
	var patterns, expressions []string
	seen := make(map[string]bool)
	add := func(left, wildcard, right string) {
		pattern := left + wildcard + right
		if pattern == "" || seen[pattern] {
			return
		}
		seen[pattern] = true
		patterns = append(patterns, pattern)
		expressions = append(expressions, regexp.QuoteMeta(left)+wildcard+regexp.QuoteMeta(right))
	}
	for index := 0; index <= len(name); index++ {
		add(name[:index], character, name[index:])
		if index == len(name) {
			continue
		}
		add(name[:index], character, name[index+1:])
		add(name[:index], "", name[index+1:])
		if index+1 < len(name) {
			add(name[:index]+name[index+1:index+2]+name[index:index+1], "", name[index+2:])
		}
	}
	return strings.Join(patterns, "|"), "^(" + strings.Join(expressions, "|") + ")$"
}
