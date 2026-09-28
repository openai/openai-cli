package custom

import (
	"cmp"
	"slices"

	"github.com/urfave/cli/v3"
)

// Keep image help and completion candidates in the same task-oriented order.
// The command objects retain their generated handlers, flags and aliases.
func orderImageCommands(root *cli.Command) {
	images := root.Command("images")
	if images == nil {
		return
	}
	order := []string{"generate", "edit", "preview", "models", "inline", "create-variation"}
	rank := func(command *cli.Command) int {
		if index := slices.Index(order, command.Name); index >= 0 {
			return index
		}
		return len(order)
	}
	images.Commands = slices.Clone(images.Commands)
	slices.SortStableFunc(images.Commands, func(a, b *cli.Command) int {
		return cmp.Compare(rank(a), rank(b))
	})
}
