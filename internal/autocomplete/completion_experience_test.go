package autocomplete

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestCompletionHelpTopicsFollowCommandTree(t *testing.T) {
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{
		{Name: "responses", Commands: []*cli.Command{{Name: "create"}, {Name: "input-items", Commands: []*cli.Command{{Name: "list"}}}}},
		{Name: "legacy:responses", Hidden: true, Metadata: map[string]any{"command-compatibility-alias": true}, Commands: []*cli.Command{{Name: "create"}}},
		{Name: "internal", Hidden: true},
	}}
	_, _, err := clihelp.Configure(root, []string{"openai"})
	require.NoError(t, err)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"help", ""}, []string{"responses", "setup"}},
		{[]string{"help", "responses", ""}, []string{"create", "input-items"}},
		{[]string{"help", "--all", "responses", "input-items", ""}, []string{"list"}},
		{[]string{"responses", "help", "input-items", ""}, []string{"list"}},
		{[]string{"help", "legacy:responses", ""}, []string{"create"}},
		{[]string{"help", "responses", "--a"}, nil},
	} {
		t.Run(strings.Join(tc.args, "/"), func(t *testing.T) {
			got := GetCompletions(CompletionStyleZsh, root, tc.args)
			require.Equal(t, ShellCompletionBehaviorDefault, got.Behavior)
			var names []string
			for _, candidate := range got.Completions {
				names = append(names, candidate.Name)
			}
			require.Equal(t, tc.want, names)
		})
	}
}

func TestCompletionUnknownParentsStopTraversal(t *testing.T) {
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{Name: "list"}}}}}
	for _, args := range [][]string{{"missing", ""}, {"models", "missing", ""}, {"missing", "models", ""}} {
		got := GetCompletions(CompletionStyleBash, root, args)
		require.Empty(t, got.Completions, "args: %q", args)
		require.EqualValues(t, ShellCompletionBehaviorNoComplete, got.Behavior)
	}
}

func TestCompletionFileInputUsesPathBehavior(t *testing.T) {
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "file", FileInput: true},
		&requestflag.Flag[[]string]{Name: "image", FileInput: true},
		&requestflag.Flag[string]{Name: "prompt"},
	}}
	for _, tc := range []struct {
		name string
		want ShellCompletionBehavior
	}{{"file", ShellCompletionBehaviorFile}, {"image", ShellCompletionBehaviorFile}, {"prompt", ShellCompletionBehaviorNoComplete}} {
		got := GetCompletions(CompletionStyleZsh, root, []string{"--" + tc.name, "candidate"})
		require.Equal(t, tc.want, got.Behavior)
		require.Empty(t, got.Completions)
	}
}

func TestCompletionFlagValuesDoNotBecomeCommands(t *testing.T) {
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{&cli.StringFlag{Name: "project"}}, Commands: []*cli.Command{{Name: "models"}}}
	for _, args := range [][]string{{"--project", "help", "mo"}, {"--project=help", "mo"}, {"--project", "--help", "mo"}} {
		got := GetCompletions(CompletionStyleBash, root, args)
		require.Equal(t, []ShellCompletion{{Name: "models"}}, got.Completions)
	}
}

func TestCompletionDescriptionsEscapeControlsAndRecordDelimiters(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(style, func(t *testing.T) {
			t.Setenv("COMPLETION_STYLE", style)
			var output bytes.Buffer
			root := &cli.Command{
				Name: "openai", SkipFlagParsing: true, Writer: &output,
				Commands: []*cli.Command{
					{Name: "safe:command", Usage: "First\nline\twith: delimiters\r\x1b[31m\x00\x7f\u009b\u202e $(false). Later sentence."},
					{Name: "__complete", Hidden: true, SkipFlagParsing: true, Action: ExecuteShellCompletion},
				},
				ExitErrHandler: func(context.Context, *cli.Command, error) {},
			}
			args := []string{"openai", "__complete"}
			if style != "zsh" {
				args = append(args, "--")
			}
			err := root.Run(context.Background(), append(args, "safe:"))
			var exit cli.ExitCoder
			require.ErrorAs(t, err, &exit)
			require.Zero(t, exit.ExitCode())
			record := output.String()
			require.Equal(t, 1, strings.Count(record, "\n"))
			if style == "fish" {
				require.Equal(t, 1, strings.Count(record, "\t"))
			} else {
				require.NotContains(t, record, "\t")
			}
			for _, char := range record {
				require.False(t, unicode.IsControl(char) && char != '\n' && char != '\t', "raw control %U", char)
			}
			require.NotContains(t, record, "\u202e")
			require.NotContains(t, record, "Later sentence")
			if style == "zsh" || style == "fish" {
				require.Contains(t, record, "First line with: delimiters")
				require.Contains(t, record, `\u001b[31m`)
				require.Contains(t, record, `\u202e $(false).`)
			}
		})
	}
}

func TestCompletionAssignedFileValues(t *testing.T) {
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "file", Aliases: []string{"f"}, FileInput: true},
		&requestflag.Flag[[]string]{Name: "image", FileInput: true},
		&cli.StringFlag{Name: "certificate", TakesFile: true},
		&cli.StringFlag{Name: "prompt"}, &cli.BoolFlag{Name: "verbose"},
	}}
	for _, style := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish, CompletionStylePowershell} {
		for _, tc := range []struct {
			args []string
			want ShellCompletionBehavior
		}{
			{[]string{"--file=assets/lo"}, ShellCompletionBehaviorFile},
			{[]string{"--file="}, ShellCompletionBehaviorFile},
			{[]string{"-f=assets/lo"}, ShellCompletionBehaviorFile},
			{[]string{"--image=assets/a=b"}, ShellCompletionBehaviorFile},
			{[]string{"--certificate=assets/lo"}, ShellCompletionBehaviorFile},
			{[]string{"--prompt=assets/lo"}, ShellCompletionBehaviorNoComplete},
			{[]string{"--verbose=assets/lo"}, ShellCompletionBehaviorNoComplete},
			{[]string{"--unknown=assets/lo"}, ShellCompletionBehaviorNoComplete},
			{[]string{"--", "--file=assets/lo"}, ShellCompletionBehaviorDefault},
			{[]string{"--prompt", "--file=assets/lo"}, ShellCompletionBehaviorNoComplete},
			{[]string{"--file", "="}, ShellCompletionBehaviorFile},
			{[]string{"--file", "=", "assets/lo"}, ShellCompletionBehaviorDefault},
			{[]string{"--file=assets/first", "--image=assets/lo"}, ShellCompletionBehaviorFile},
		} {
			t.Run(string(style)+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
				got := GetCompletions(style, root, tc.args)
				require.Equal(t, tc.want, got.Behavior)
				require.Empty(t, got.Completions)
			})
		}
	}
}
