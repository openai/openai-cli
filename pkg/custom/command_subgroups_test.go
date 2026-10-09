package custom

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestCommandSubgroupsKeepActionsFlagsAndParents(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "compatibility", true: "nested"}[nested], func(t *testing.T) {
			var selected string
			leaf := &cli.Command{Name: "list", Flags: []cli.Flag{&cli.StringFlag{Name: "filter"}}, Action: func(_ context.Context, c *cli.Command) error {
				selected = c.FullName() + "/" + c.String("filter") + "/" + c.String("format")
				return nil
			}}
			original := &cli.Command{Name: "admin:organization:users", Category: "API RESOURCE", Commands: []*cli.Command{leaf}}
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Flags: []cli.Flag{&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "admin-api-key"}}, Commands: []*cli.Command{
				{Name: "admin:organization:users:roles", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "list"}}}, original,
			}}
			ConfigureCommand(root)
			ConfigureCommand(root)
			users := root.Command("admin").Command("organization").Command("users")
			if !original.Hidden || users.Hidden || users.Command("roles") == nil || users.Command("list") == leaf {
				t.Fatal("missing subgroup or shared command node")
			}
			users.Metadata["probe"] = true
			if original.Metadata["probe"] != nil || len(users.Commands) != 2 {
				t.Fatal("metadata was shared or subgroup duplicated")
			}
			path := []string{"admin:organization:users", "list"}
			if nested {
				path = []string{"admin", "organization", "users", "list"}
			}
			args := append([]string{"openai", "--format", "json", "--admin-api-key", "synthetic-admin-key"}, path...)
			if err := root.Run(context.Background(), append(args, "--filter", "admin:organization")); err != nil {
				t.Fatal(err)
			}
			if want := "openai " + strings.Join(path, " ") + "/admin:organization/json"; selected != want {
				t.Fatalf("got %q, want %q", selected, want)
			}
		})
	}
}

func TestCommandSubgroupsNeverReplaceAnExistingAction(t *testing.T) {
	action := &cli.Command{Name: "item", Action: func(context.Context, *cli.Command) error { return nil }}
	legacy := &cli.Command{Name: "files:item:details", Category: "API RESOURCE", Commands: []*cli.Command{{Name: "list"}}}
	root := &cli.Command{Commands: []*cli.Command{{Name: "files", Category: "API RESOURCE", Commands: []*cli.Command{action}}, legacy}}
	configureCommandSubgroups(root)
	if legacy.Hidden || root.Command("files").Command("item") != action || !slices.Contains(root.Commands, legacy) {
		t.Fatal("a command collision replaced an existing route")
	}
}
