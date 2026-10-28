package autocomplete

import (
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestTypedLegacyHelpFlagCompletion(t *testing.T) {
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{
		{Name: "responses", Commands: []*cli.Command{{Name: "create"}}},
		{Name: "models", Flags: []cli.Flag{&cli.BoolFlag{Name: "all"}}},
	}}
	_, _, err := clihelp.Configure(root, []string{"openai"})
	require.NoError(t, err)
	help := root.Command("help")
	help.Flags = append(help.Flags, &cli.StringFlag{Name: "topic"}, &cli.BoolFlag{Name: "private", Hidden: true})
	for _, flag := range []string{"--all", "-all"} {
		for _, value := range []string{"", "=true", "=false", "=1", "=0", "=TRUE", "=False"} {
			got := GetCompletions(CompletionStyleBash, root, []string{"help", flag + value, "responses", "cr"})
			require.Equal(t, ShellCompletionBehaviorDefault, got.Behavior, flag+value)
			require.Equal(t, []string{"create"}, completionNames(got.Completions), flag+value)
		}
	}
	for _, args := range [][]string{
		{"help", "--all=invalid", "responses", "cr"},
		{"help", "--all=", "responses", "cr"},
		{"help", "-all=invalid", "responses", "cr"},
		{"help", "---all", "responses", "cr"},
		{"help", "--private", "responses", "cr"},
		{"help", "--", "--all", "responses", "cr"},
		{"--all", "responses", "cr"},
	} {
		got := GetCompletions(CompletionStyleBash, root, args)
		require.EqualValues(t, ShellCompletionBehaviorNoComplete, got.Behavior, "%q", args)
		require.Empty(t, got.Completions, "%q", args)
	}
	got := GetCompletions(CompletionStyleBash, root, []string{"help", "--topic", "--all", "responses", "cr"})
	require.Equal(t, []string{"create"}, completionNames(got.Completions))
	for _, args := range [][]string{{"help", "--a"}, {"help", "--all="}} {
		require.Empty(t, GetCompletions(CompletionStyleBash, root, args).Completions)
	}
	require.Contains(t, completionNames(GetCompletions(CompletionStyleBash, root, []string{"models", "--a"}).Completions), "--all")

	// A visible declaration with the same name retains normal value consumption.
	help.Flags = append(help.Flags, &cli.StringFlag{Name: "all"})
	got = GetCompletions(CompletionStyleBash, root, []string{"help", "--all", "ignored", "responses", "cr"})
	require.Equal(t, []string{"create"}, completionNames(got.Completions))
}
