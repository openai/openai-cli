package autocomplete

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestSuggestionGenerationOnlyPreservesCompletionBoundary(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish} {
		t.Run(string(shell), func(t *testing.T) {
			var output bytes.Buffer
			root := suggestionGenerationCommand(&output)
			require.NoError(t, root.Run(t.Context(), []string{"openai", "@completion", string(shell), "--suggestions", "--suggestions-only"}))
			hooks, err := renderSuggestionCompletion(shell, "openai", false)
			require.NoError(t, err)
			require.Equal(t, "\n"+hooks+"\n# openai-cli-suggestions-complete v1\n", output.String())
			require.NotContains(t, output.String(), "COMPLETION_STYLE=")
			require.True(t, strings.HasSuffix(output.String(), "# openai-cli-suggestions-complete v1\n"))
		})
	}
}

func TestSuggestionGenerationOnlyRejectsConflictsWithoutOutput(t *testing.T) {
	for _, args := range [][]string{
		{"zsh", "--suggestions-only"},
		{"zsh", "--suggestions", "--suggestions-only", "--picker"},
		{"--suggestions-adapter", "kitty", "--suggestions-only"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var output bytes.Buffer
			root := suggestionGenerationCommand(&output)
			require.Error(t, root.Run(t.Context(), append([]string{"openai", "@completion"}, args...)))
			require.Empty(t, output.String())
		})
	}
}

func suggestionGenerationCommand(output *bytes.Buffer) *cli.Command {
	return &cli.Command{Name: "openai", Writer: output, ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands: []*cli.Command{{Name: "@completion", Action: OutputCompletionScript, Flags: []cli.Flag{
			&cli.BoolFlag{Name: "suggestions"}, &cli.BoolFlag{Name: "suggestions-only"}, &cli.BoolFlag{Name: "picker"},
			&cli.StringFlag{Name: "suggestions-adapter"},
		}}},
	}
}
