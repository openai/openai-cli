package autocomplete

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func inheritedTree() (*cli.Command, *cli.Command) {
	leaf := &cli.Command{Name: "leaf", HideHelp: true, Flags: []cli.Flag{&cli.BoolFlag{Name: "override", Aliases: []string{"o"}}}}
	middle := &cli.Command{Name: "middle", HideHelp: true, Flags: []cli.Flag{
		&cli.StringFlag{Name: "near", Aliases: []string{"shared"}, Usage: "near"},
	}, Commands: []*cli.Command{leaf, {Name: "value", HideHelp: true}}}
	root := &cli.Command{Name: "test", HideHelp: true, Writer: io.Discard, ErrWriter: io.Discard,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Aliases: []string{"f"}},
			&cli.StringFlag{Name: "file", TakesFile: true},
			&cli.StringFlag{Name: "private", Hidden: true},
			&cli.StringFlag{Name: "root-only", Local: true},
			&cli.StringFlag{Name: "replaced", Aliases: []string{"o"}},
			&cli.StringFlag{Name: "far", Aliases: []string{"shared"}, Usage: "far"},
		}, Commands: []*cli.Command{middle}}
	return root, leaf
}

func TestInheritedCompletion(t *testing.T) {
	for _, initialized := range []bool{false, true} {
		t.Run(map[bool]string{false: "raw", true: "initialized"}[initialized], func(t *testing.T) {
			root, leaf := inheritedTree()
			if initialized {
				leaf.Action = func(context.Context, *cli.Command) error { return nil }
				require.NoError(t, root.Run(context.Background(), []string{"test", "middle", "leaf"}))
			}
			for _, tc := range []struct {
				args     []string
				names    []string
				behavior ShellCompletionBehavior
			}{
				{[]string{"middle", "leaf", "--fo"}, []string{"--format"}, 0},
				{[]string{"middle", "leaf", "-f"}, []string{"-f"}, 0},
				{[]string{"middle", "leaf", "--format", ""}, nil, 11},
				{[]string{"middle", "leaf", "--format", "--file", "--fo"}, []string{"--format"}, 0},
				{[]string{"middle", "leaf", "--format=synthetic", "--fo"}, []string{"--format"}, 0},
				{[]string{"middle", "leaf", "-f", "--fo"}, nil, 11},
				{[]string{"middle", "leaf", "--file", ""}, nil, 10},
				{[]string{"middle", "leaf", "--private", "--fo"}, nil, 11},
				{[]string{"middle", "leaf", "--pr"}, nil, 0},
				{[]string{"middle", "leaf", "--root-"}, nil, 0},
				{[]string{"middle", "leaf", "--repl"}, nil, 0},
				{[]string{"middle", "leaf", "-o", "--fo"}, []string{"--format"}, 0},
				{[]string{"middle", "--format", "value", "le"}, []string{"leaf"}, 0},
				{[]string{"middle", "leaf", "--"}, []string{"--override", "--near", "--shared", "--format", "--file", "--far"}, 0},
			} {
				t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
					beforeRoot, beforeLeaf := root.Root(), leaf.Root()
					rootFlags, leafFlags := append([]cli.Flag(nil), root.Flags...), append([]cli.Flag(nil), leaf.Flags...)
					got := GetCompletions(CompletionStyleBash, root, tc.args)
					var names []string
					for _, c := range got.Completions {
						names = append(names, c.Name)
					}
					require.Equal(t, tc.names, names)
					require.Equal(t, tc.behavior, got.Behavior)
					require.Same(t, beforeRoot, root.Root())
					require.Same(t, beforeLeaf, leaf.Root())
					require.Equal(t, rootFlags, root.Flags)
					require.Equal(t, leafFlags, leaf.Flags)
				})
			}
		})
	}
}

func TestInheritedFlagShadowing(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		for _, parentName := range []string{"override", "global"} {
			root, leaf := inheritedTree()
			leaf.Flags = []cli.Flag{&cli.BoolFlag{Name: "override", Aliases: []string{"o"}, Hidden: hidden}}
			root.Flags = []cli.Flag{&cli.StringFlag{Name: parentName, Aliases: []string{"o", "other"}}}
			// Even a hidden child declaration suppresses every parent alias.
			got := GetCompletions(CompletionStyleBash, root, []string{"middle", "leaf", "--other"})
			require.Empty(t, got.Completions)
		}
	}
}

// Pin the parser's contract: a child alias suppresses the entire parent flag,
// while overlapping inherited aliases resolve to the nearest ancestor.
func TestInheritedParserContract(t *testing.T) {
	for _, tc := range []struct {
		flag     string
		accepted bool
	}{
		{"--format", true}, {"-f", true}, {"--private", true}, {"--file", true},
		{"--root-only", false}, {"--replaced", false}, {"--near", true}, {"--far", true}, {"--shared", true},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			root, leaf := inheritedTree()
			called := false
			leaf.Action = func(context.Context, *cli.Command) error { called = true; return nil }
			err := root.Run(context.Background(), []string{"test", "middle", "leaf", tc.flag, "synthetic"})
			if tc.accepted {
				require.NoError(t, err)
				require.True(t, called)
			} else {
				require.Error(t, err)
				require.False(t, called)
			}
			if tc.flag == "--shared" {
				require.Equal(t, "synthetic", root.Commands[0].Flags[0].(*cli.StringFlag).Get())
				require.Equal(t, "", root.Flags[5].(*cli.StringFlag).Get())
			}
		})
	}
}
