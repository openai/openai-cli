package commands

import (
	"context"
	"errors"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/updatenotice"
	"github.com/urfave/cli/v3"
)

// RegisterUpdates adds local controls for the CLI's private release installation.
func RegisterUpdates(root *cli.Command, report func(context.Context, string) error) {
	group := &cli.Command{
		Name:     "updates",
		Usage:    "Choose how this CLI installation receives updates",
		Metadata: map[string]any{LocalUtilityMetadata: true, "help-command-section": "Local tools"},
	}
	for _, option := range []struct{ name, usage string }{
		{"enable", "Allow verified automatic updates for your private CLI installation"},
		{"disable", "Switch your CLI installation to manual updates"},
	} {
		group.Commands = append(group.Commands, &cli.Command{
			Name:  option.name,
			Usage: option.usage,
			Metadata: map[string]any{"help-content": clihelp.Content{
				Examples:          []clihelp.Example{{Command: "updates " + option.name}},
				InheritedFlagHelp: map[string]string{"quiet": "", "format-error": "", "transform-error": ""},
			}},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.Args().Present() {
					return &LocalError{Message: "Run openai updates " + option.name + " without arguments."}
				}
				current := cmd.Root().Version
				enable := option.name == "enable"
				err := updatenotice.SetAutomaticUpdates(ctx, current, enable)
				message := "Updates are manual for this CLI installation."
				if enable {
					message = "Automatic updates enabled for your private CLI installation."
					if errors.Is(err, updatenotice.ErrUnmanagedInstallation) {
						message, err = enableUnmanagedUpdates(ctx, current)
					}
				}
				if err != nil {
					return &LocalError{Message: err.Error(), Cause: err}
				}
				return report(ctx, message)
			},
		})
	}
	root.Commands = append(root.Commands, group)
}
