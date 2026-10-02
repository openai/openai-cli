package custom

import (
	"context"
	"slices"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/urfave/cli/v3"
)

// IsImagePickerPackageCommand excludes local artifact generation from request
// setup. A matching token passed to an API command is never an exemption.
func IsImagePickerPackageCommand(args []string) bool {
	return isImagePickerCompletionAction(args, "--package-picker")
}

func configureImagePickerPackage(root *cli.Command) {
	completion := root.Command("@completion")
	if completion == nil {
		return
	}
	for _, flag := range completion.Flags {
		if slices.Contains(flag.Names(), "package-picker") {
			return
		}
	}
	completion.Flags = append(completion.Flags, &cli.BoolFlag{Name: "package-picker", Hidden: true})
	next := completion.Action
	completion.Action = func(ctx context.Context, command *cli.Command) error {
		if !command.Bool("package-picker") {
			return next(ctx, command)
		}
		if command.Args().Len() != 1 || command.Bool("picker") ||
			command.Bool("install-picker") || command.Bool("uninstall-picker") ||
			command.Bool("automatic") || command.IsSet("profile") {
			return cli.Exit("Use @completion fish --package-picker without setup options.", 2)
		}
		content, err := autocomplete.PackagePickerScript(autocomplete.CompletionStyle(command.Args().First()))
		if err != nil {
			return err
		}
		_, err = command.Writer.Write(content)
		return err
	}
}
