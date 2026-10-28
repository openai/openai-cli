package autocomplete

import (
	"slices"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestPersistentFlagCompletion(t *testing.T) {
	t.Parallel()
	root := &cli.Command{
		Name: "example",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "header", Aliases: []string{"H"}},
			&cli.StringFlag{Name: "root-only", Local: true},
			&cli.StringFlag{Name: "hidden", Hidden: true},
		},
		Commands: []*cli.Command{
			{Name: "leaf"},
			{
				Name: "group",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "header", Aliases: []string{"H"}, Local: true, Hidden: true},
				},
				Commands: []*cli.Command{{Name: "leaf"}},
			},
			{
				Name: "alias",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "own", Aliases: []string{"H"}, Local: true},
				},
			},
		},
	}
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{name: "inherited", args: []string{"leaf", "--"}, want: []string{"--header"}},
		{name: "hidden local shadow", args: []string{"group", "--"}},
		{name: "local shadow does not propagate", args: []string{"group", "leaf", "--"}, want: []string{"--header"}},
		{name: "alias shadows whole flag", args: []string{"alias", "--"}, want: []string{"--own"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := GetCompletions(CompletionStyleBash, root, tc.args)
			if names := completionNames(got.Completions); !slices.Equal(names, tc.want) {
				t.Errorf("GetCompletions(%q) = %q, want %q", tc.args, names, tc.want)
			}
		})
	}
	for _, flag := range []string{"--header", "-H"} {
		args := []string{"group", "leaf", flag, "literal"}
		got := GetCompletions(CompletionStyleBash, root, args)
		if got.Behavior != ShellCompletionBehaviorNoComplete || len(got.Completions) != 0 {
			t.Errorf("GetCompletions(%q) = %+v, want no value completions", args, got)
		}
	}
}
