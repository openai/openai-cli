package explore

import (
	"context"
	"slices"

	"github.com/urfave/cli/v3"
)

// ConfigureCompletion exposes generation and managed shell setup.
func ConfigureCompletion(root *cli.Command) {
	completion := root.Command("@completion")
	if completion == nil {
		return
	}
	for _, flag := range completion.Flags {
		if slices.Contains(flag.Names(), "suggestions") {
			return
		}
	}
	completion.Flags = append(completion.Flags,
		&cli.BoolFlag{Name: "suggestions", Usage: "Include command insertion for Bash 4.3+, zsh, or fish; Ctrl-X, then Tab searches suggestions when unbound"},
		&cli.BoolFlag{Name: "suggestions-double-tab", Usage: "Enable double Tab when unbound; use with --suggestions or --install-suggestions. Can delay normal Tab completion"},
		&cli.BoolFlag{Name: "suggestions-click", Usage: "Enable experimental clicking with a configured local Kitty adapter; requires zsh and --suggestions"},
		&cli.StringFlag{Name: "suggestions-adapter", Usage: "Print the Kitty click adapter without changing terminal settings; use kitty without a shell argument"},
		&cli.BoolFlag{Name: "install-suggestions", Usage: "Enable command insertion in future terminals; updates follow the installed CLI"},
		&cli.BoolFlag{Name: "uninstall-suggestions", Usage: "Remove only managed command insertion setup"},
		&cli.BoolFlag{Name: "suggestions-status", Usage: "Inspect command insertion setup without changing files"},
		&cli.BoolFlag{Name: "suggestions-only", Hidden: true, Usage: "Generate only insertion hooks; requires --suggestions"},
	)
	next := completion.Action
	completion.Action = func(ctx context.Context, command *cli.Command) error {
		if command.Bool("suggest-executable") && (command.Bool("suggestions") || command.Bool("suggestions-double-tab") || command.Bool("suggestions-click") || command.IsSet("suggestions-adapter") || command.Bool("suggestions-only") || command.Bool("install-suggestions") || command.Bool("uninstall-suggestions") || command.Bool("suggestions-status")) {
			return insertionSetupFailure("Print executable typo advice separately from command insertion setup.", nil, 2)
		}
		if command.Bool("install-suggestions") || command.Bool("uninstall-suggestions") || command.Bool("suggestions-status") {
			return commandInsertionShellSetup(ctx, command)
		}
		if command.IsSet("suggestions-adapter") || command.Bool("suggestions-click") || command.Bool("suggestions") || command.Bool("suggestions-double-tab") || command.Bool("suggestions-only") {
			if command.IsSet("install-picker") || command.IsSet("uninstall-picker") ||
				command.IsSet("automatic") || command.IsSet("profile") {
				return cli.Exit("command insertion options cannot be combined with persistent picker setup", 2)
			}
		}
		if next != nil {
			return next(ctx, command)
		}
		return nil
	}
}
