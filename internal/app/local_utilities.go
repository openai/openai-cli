package app

import (
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/commands/audio"
	"github.com/urfave/cli/v3"
)

// IsLocalUtilityCommand identifies a registered utility after root flag parsing.
// Request values and unregistered command names never select the local path.
func IsLocalUtilityCommand(command *cli.Command) bool {
	if audio.IsLocalCommand(command) {
		return true
	}
	root := command.Root()
	selected := root.Command(root.Args().First())
	return selected != nil && selected.Metadata[localcommands.LocalUtilityMetadata] == true
}
