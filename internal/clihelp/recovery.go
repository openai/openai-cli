package clihelp

import (
	"slices"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

// RecoveryMessage uses parsed positions, never the diagnostic text or
// original argv. The parser has already removed recognized flags and values.
func RecoveryMessage(command *cli.Command) string {
	if command != nil {
		command = command.Root()
	}
	for command != nil && command.Args() != nil && command.Args().Present() {
		name := command.Args().First()
		if next := command.Command(name); next != nil {
			command = next
			continue
		}
		helpRequested := false
		if cli.HelpFlag != nil {
			helpRequested = slices.ContainsFunc(cli.HelpFlag.Names(), command.Bool)
		}
		return RecoveryAt(command, name, command.Args().Tail(), helpRequested)
	}
	return RecoveryAt(command, "", nil, false)
}

// RecoveryAt emits help for declared command names. A corrected group
// can retain an exact descendant path, but never an operand or an option value.
// Running the original operation requires keeping its private request context.
func RecoveryAt(command *cli.Command, name string, remaining []string, helpRequested bool) string {
	message := "Unknown command."
	if helpRequested {
		message = "Unknown help topic."
	}
	if command == nil {
		return message + " Run openai help to see commands."
	}
	if command.Suggest {
		candidates := VisibleCommands(command)
		for _, child := range command.Commands {
			compatibility, _ := child.Metadata["command-compatibility-alias"].(bool)
			if child.Hidden && compatibility && (strings.Contains(name, ":") || WithinOneEdit(strings.ToLower(name), strings.ToLower(child.Name))) {
				candidates = append(candidates, child)
			}
		}
		if target := suggestCommandTarget(candidates, name); target != nil {
			path := append(append([]string(nil), command.Path()[1:]...), target.Name)
			for _, token := range remaining {
				if strings.HasPrefix(token, "-") {
					break
				}
				next := target.Command(token)
				if next == nil || !recoveryCommandVisible(next) {
					break
				}
				path = append(path, next.Name)
				target = next
			}
			invocation := DisplayInvocation(command.Root()) + " help"
			if len(path) > 0 {
				invocation += " " + strings.Join(path, " ")
			}
			message += " Did you mean: " + invocation + "?"
			if !helpRequested {
				message += "\nTo run it, correct only the command spelling. Keep your original options and arguments."
			}
			return message
		}
	}
	invocation := DisplayInvocation(command.Root()) + " help"
	if path := command.Path(); len(path) > 1 {
		invocation += " " + strings.Join(path[1:], " ")
	}
	return message + " Run " + invocation + " to see commands."
}

func recoveryCommandVisible(command *cli.Command) bool {
	compatibility, _ := command.Metadata["command-compatibility-alias"].(bool)
	return command.Name != "help" && (!command.Hidden || compatibility)
}

// DisplayInvocation escapes local help metadata for display, using openai when
// the invocation has not been configured. It never reads API response data.
func DisplayInvocation(root *cli.Command) string {
	invocation, _ := root.Metadata["help-invocation"].(string)
	if invocation == "" {
		return "openai"
	}
	return readable.Text(invocation)
}
