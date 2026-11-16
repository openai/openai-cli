package images

import (
	"slices"

	"github.com/urfave/cli/v3"
)

// ConfigureCompletion keeps the optional current-session hook on the existing completion command.
func ConfigureCompletion(root *cli.Command) {
	completion := root.Command("@completion")
	if completion == nil {
		return
	}
	for _, flag := range completion.Flags {
		if slices.Contains(flag.Names(), "picker") {
			return
		}
	}
	completion.Flags = append(completion.Flags,
		&cli.BoolFlag{Name: "picker", Usage: "Include the optional Tab shortcut for the image picker"},
	)
}
