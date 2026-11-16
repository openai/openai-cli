package app

import (
	"context"
	"slices"

	"github.com/openai/openai-cli/internal/autocomplete"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/urfave/cli/v3"
)

// Executable suggestions have independent consent from completion and picker setup.
func configureShellSuggestions(root *cli.Command) {
	completion := root.Command("@completion")
	if completion == nil || completion.Action == nil {
		return
	}
	for _, flag := range completion.Flags {
		if slices.Contains(flag.Names(), "suggest-executable") {
			return
		}
	}
	completion.Flags = append(completion.Flags, &cli.BoolFlag{
		Name:  "suggest-executable",
		Usage: "Print optional shell advice for executable typos; never execute corrections",
	})
	next := completion.Action
	completion.Action = func(ctx context.Context, command *cli.Command) error {
		if command.Bool("suggest-executable") {
			message := ""
			switch {
			case command.Bool("picker") || command.Bool("install-picker") || command.Bool("uninstall-picker") || command.Bool("automatic") || command.IsSet("profile"):
				message = "Print executable suggestions separately from picker setup. Use openai '@completion' SHELL --suggest-executable."
			case command.Args().Len() != 1:
				message = "Choose one shell: openai '@completion' SHELL --suggest-executable. Supported shells: bash, zsh, fish, pwsh."
			default:
				switch command.Args().First() {
				case "bash", "zsh", "fish", "pwsh":
				default:
					message = "Executable suggestions support bash, zsh, fish, and pwsh. Use openai '@completion' SHELL --suggest-executable."
				}
			}
			if message != "" {
				return &localcommands.LocalError{Message: message, Cause: cli.Exit(message, 2)}
			}
			script, err := autocomplete.RenderShellSuggestions(autocomplete.CompletionStyle(command.Args().First()), command.Root().Name)
			if err != nil {
				return cli.Exit(err, 1)
			}
			if _, err := command.Writer.Write([]byte(script)); err != nil {
				return cli.Exit(err, 1)
			}
			return nil
		}
		return next(ctx, command)
	}
}
