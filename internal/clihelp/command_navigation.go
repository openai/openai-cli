package clihelp

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

// VisibleCommands gives help and completion the same order without changing
// parser definitions. Actions precede groups; equal ranks retain source order.
// Curated commands can preserve their complete declaration order through metadata.
func VisibleCommands(command *cli.Command) []*cli.Command {
	commands := slices.Clone(command.VisibleCommands())
	if preserve, _ := command.Metadata["help-preserve-command-order"].(bool); preserve {
		return commands
	}
	slices.SortStableFunc(commands, func(a, b *cli.Command) int {
		if commandGroup(a) != commandGroup(b) {
			if commandGroup(a) {
				return 1
			}
			return -1
		}
		return cmp.Compare(commandRank(a), commandRank(b))
	})
	return commands
}

func commandRank(command *cli.Command) int {
	if rank, ok := command.Metadata["help-command-rank"].(int); ok {
		return rank
	}
	return int(^uint(0) >> 1)
}

func commandGroup(command *cli.Command) bool {
	return command.Category == "API RESOURCE" || len(command.VisibleCommands()) > 0
}

func commandList(command *cli.Command, width int) string {
	var order []string
	sections := map[string][]*cli.Command{}
	for _, child := range VisibleCommands(command) {
		section, _ := child.Metadata["help-command-section"].(string)
		if section == "" {
			section = "Actions"
			if commandGroup(child) {
				section = "Command groups"
			}
		}
		if _, seen := sections[section]; !seen {
			order = append(order, section)
		}
		sections[section] = append(sections[section], child)
	}
	var out strings.Builder
	for i, section := range order {
		if i > 0 {
			out.WriteByte('\n')
		}
		fmt.Fprintln(&out, strings.ToUpper(section))
		for _, child := range sections[section] {
			writeHelpEntry(&out, child.Name, shortDescription(child.Usage), width)
		}
	}
	return out.String()
}

func configureCommandList(command *cli.Command) {
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	command.Metadata["help-command-list"] = func() string {
		return commandList(command, helpWidth(command))
	}
}
