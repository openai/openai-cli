package commands

import (
	"slices"
	"strings"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// RemoveFineTuningCommands removes both generated aliases and the nested group.
// Run it after feature registration, before help and command indexes are built.
func RemoveFineTuningCommands(root *cli.Command) {
	root.Commands = slices.DeleteFunc(root.Commands, func(command *cli.Command) bool {
		return command.Name == "fine-tuning" || strings.HasPrefix(command.Name, "fine-tuning:")
	})
	if models := root.Command("models"); models != nil {
		models.Commands = slices.DeleteFunc(models.Commands, func(command *cli.Command) bool {
			return command.Name == "delete"
		})
	}
	removeTrainingHelp(root, "")
}

// Update the flags themselves so help, manpages, and shell completions agree.
func removeTrainingHelp(command *cli.Command, path string) {
	for _, child := range command.Commands {
		childPath := strings.TrimPrefix(path+":"+child.Name, ":")
		for _, flag := range child.Flags {
			switch typed := flag.(type) {
			case *requestflag.Flag[string]:
				if typed.Name == "purpose" && (childPath == "files:create" || childPath == "files:upload" || childPath == "uploads:create") {
					typed.Usage = "The intended purpose of the uploaded file: assistants, batch, vision, user_data, or evals. Use vision for image inputs."
				}
			case *requestflag.Flag[*bool]:
				if typed.Name == "store" && childPath == "chat:completions:create" {
					typed.Usage = "Whether to store this chat completion for later retrieval and evals. Supports text and image inputs. Image inputs over 8MB will be dropped."
				}
			}
		}
		removeTrainingHelp(child, childPath)
	}
}
