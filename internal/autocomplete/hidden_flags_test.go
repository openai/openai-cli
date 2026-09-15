package autocomplete

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestFlagCompletionOmitsHiddenFlags(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Name: "openai",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "visible", Aliases: []string{"v"}},
			&cli.StringFlag{Name: "hidden", Aliases: []string{"h"}, Hidden: true},
		},
	}

	longResult := GetCompletions(CompletionStyleZsh, root, []string{"--"})
	longNames := completionNames(longResult.Completions)
	require.Contains(t, longNames, "--visible")
	require.NotContains(t, longNames, "--hidden")

	shortResult := GetCompletions(CompletionStyleZsh, root, []string{"-"})
	shortNames := completionNames(shortResult.Completions)
	require.Contains(t, shortNames, "-v")
	require.NotContains(t, shortNames, "-h")
}

func completionNames(completions []ShellCompletion) []string {
	names := make([]string, 0, len(completions))
	for _, completion := range completions {
		names = append(names, completion.Name)
	}
	return names
}
