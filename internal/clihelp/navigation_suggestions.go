package clihelp

import (
	"strings"
	"unicode"

	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/urfave/cli/v3"
)

// Build targets from the configured invocation and actual command definitions.
// The returned callback registers only rows that the help renderer displays.
func navigationSuggestions(command *cli.Command, invocation, path string) func(*cli.Command) string {
	prefix := strings.TrimSpace(invocation + " " + path)
	return func(child *cli.Command) string {
		if !ShellSafeCommandPath(command) || !safeNavigationName(child.Name) {
			return child.Name
		}
		return commandsuggest.RecordLabel(command, child.Name, prefix+" "+child.Name+" --help")
	}
}

// ShellSafeCommandPath checks whether command names can remain literal shell words.
// Explicitly authored examples and quoted executable invocations use separate paths.
func ShellSafeCommandPath(command *cli.Command) bool {
	if command == nil {
		return false
	}
	for _, ancestor := range command.Lineage() {
		if !safeNavigationName(ancestor.Name) {
			return false
		}
	}
	return true
}

func safeNavigationName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") {
		return false
	}
	for _, char := range name {
		if !unicode.IsLetter(char) && !unicode.IsDigit(char) && !strings.ContainsRune("_-.:@", char) {
			return false
		}
	}
	return true
}
