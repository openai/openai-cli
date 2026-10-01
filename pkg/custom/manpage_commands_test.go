package custom

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	docs "github.com/urfave/cli-docs/v3"
	"github.com/urfave/cli/v3"
)

func TestManpageCommandsPreserveRuntimeTree(t *testing.T) {
	flag := &cli.StringFlag{Name: "filter", Usage: "Filter documented items"}
	leaf := &cli.Command{Name: "list", Aliases: []string{"ls"}, Usage: "List documented items", Flags: []cli.Flag{flag}}
	group := &cli.Command{Name: "resources", Commands: []*cli.Command{leaf}}
	hidden := &cli.Command{Name: "internal", Hidden: true, Commands: []*cli.Command{{Name: "secret"}}}
	root := &cli.Command{Name: "openai"}
	wantErr := errors.New("synthetic writer failure")
	var fail bool
	manpages := &cli.Command{Name: "@manpages", Hidden: true, Action: func(context.Context, *cli.Command) error {
		text, err := docs.ToManWithSection(root, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{".SH resources list, resources ls", "List documented items", "Filter documented items"} {
			if !strings.Contains(text, want) {
				t.Errorf("manpage lost %q", want)
			}
		}
		if strings.Contains(text, "internal") || strings.Contains(text, "secret") {
			t.Error("hidden subtree exposed")
		}
		if fail {
			return wantErr
		}
		return nil
	}}
	original := []*cli.Command{group, hidden, manpages}
	root.Commands = original
	configureManpageCommands(root)
	for _, fail = range []bool{false, true, false} {
		err := manpages.Action(context.Background(), manpages)
		if (fail && !errors.Is(err, wantErr)) || (!fail && err != nil) {
			t.Fatalf("writer result changed: %v", err)
		}
		if !slices.Equal(root.Commands, original) || group.Commands[0] != leaf || leaf.Name != "list" || !slices.Equal(leaf.Aliases, []string{"ls"}) || leaf.Flags[0] != flag {
			t.Fatal("documentation generation changed the runtime command tree")
		}
	}
}
