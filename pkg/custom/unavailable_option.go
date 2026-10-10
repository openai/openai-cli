package custom

import (
	"slices"

	"github.com/urfave/cli/v3"
)

// unavailableParserFlag recognizes an exact public declaration outside the
// current parser scope. It does not guess which other command the user intended.
func unavailableParserFlag(command *cli.Command, provided string) string {
	if command == nil || provided == "" {
		return ""
	}
	for _, flag := range parserErrorFlags(command) {
		if slices.Contains(flag.Names(), provided) {
			return ""
		}
	}
	var find func(*cli.Command) string
	find = func(current *cli.Command) string {
		for _, flag := range current.VisibleFlags() {
			for _, name := range flag.Names() {
				if name == provided {
					if len(name) == 1 {
						return "-" + name
					}
					return "--" + name
				}
			}
		}
		for _, child := range current.VisibleCommands() {
			if name := find(child); name != "" {
				return name
			}
		}
		return ""
	}
	return find(command.Root())
}
