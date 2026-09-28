package custom

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestOrderImageCommandsPreservesCommands(t *testing.T) {
	var called string
	names := []string{"future-b", "create-variation", "edit", "generate", "models", "future-a", "preview", "inline"}
	original := make([]*cli.Command, len(names))
	byName := map[string]*cli.Command{}
	for i, name := range names {
		command := &cli.Command{Name: name, Aliases: []string{name + "-alias"},
			Flags:  []cli.Flag{&cli.StringFlag{Name: "original-flag"}},
			Action: func(context.Context, *cli.Command) error { called = name; return nil },
		}
		original[i], byName[name] = command, command
	}
	other := &cli.Command{Name: "other", Commands: original}
	images := &cli.Command{Name: "images", Commands: original}
	root := &cli.Command{Commands: []*cli.Command{other, images}}
	for range 2 { // Repeated help configuration must be idempotent.
		orderImageCommands(root)
		want := []string{"generate", "edit", "preview", "models", "inline", "create-variation", "future-b", "future-a"}
		for i, name := range want {
			command := images.Commands[i]
			require.Same(t, byName[name], command)
			require.Same(t, command, images.Command(name+"-alias"))
			require.Equal(t, []string{"original-flag"}, command.Flags[0].Names())
			require.NoError(t, command.Action(context.Background(), command))
			require.Equal(t, name, called)
		}
		// A shared original slice and all other resources stay untouched.
		for i, command := range original {
			require.Equal(t, names[i], command.Name)
			require.Same(t, command, other.Commands[i])
		}
	}
}

func TestOrderImageCommandsWithoutImages(t *testing.T) {
	root := &cli.Command{Commands: []*cli.Command{{Name: "models"}}}
	orderImageCommands(root)
	require.Equal(t, "models", root.Commands[0].Name)
}
