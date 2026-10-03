package custom

import (
	"context"

	"github.com/urfave/cli/v3"
)

func configureManpageCommands(root *cli.Command) {
	command := root.Command("@manpages")
	if command == nil || command.Action == nil {
		return
	}
	next := command.Action
	command.Action = func(ctx context.Context, command *cli.Command) error {
		// cli-docs increases Markdown heading depth for each command level.
		// Deep subgroups exceed Markdown's six levels. Give the existing
		// generated renderer/writer a flat, full-path documentation view.
		// Its action reads the root directly, so restore the runtime tree on
		// every return, including output errors.
		original := root.Commands
		root.Commands = manpageCommands(original, "")
		defer func() { root.Commands = original }()
		return next(ctx, command)
	}
}

func manpageCommands(commands []*cli.Command, prefix string) []*cli.Command {
	var result []*cli.Command
	for _, command := range commands {
		if command.Hidden {
			continue
		}
		copy := *command
		copy.Name = prefix + command.Name
		copy.Aliases = make([]string, len(command.Aliases))
		for i, alias := range command.Aliases {
			copy.Aliases[i] = prefix + alias
		}
		copy.Commands = nil
		result = append(result, &copy)
		result = append(result, manpageCommands(command.Commands, copy.Name+" ")...)
	}
	return result
}
