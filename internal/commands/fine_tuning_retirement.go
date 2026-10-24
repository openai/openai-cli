package commands

import (
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

// RemoveFineTuningCommands removes both generated aliases and the nested group.
// Run it after feature registration, before help and command indexes are built.
func RemoveFineTuningCommands(root *cli.Command) {
	root.Commands = slices.DeleteFunc(root.Commands, func(command *cli.Command) bool {
		return command.Name == "fine-tuning" || strings.HasPrefix(command.Name, "fine-tuning:")
	})
}
