package explore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestConfigureExploreReusesDeclaredHelpExamples(t *testing.T) {
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "future", Metadata: map[string]any{"help-content": clihelp.Content{Examples: []clihelp.Example{{Description: "Future example", Command: `future --name "quoted value"`}}}}}}}
	Register(root)
	Register(root)
	require.Len(t, root.Commands, 2)
	candidates := exploreCandidates(root, "openai")
	require.Equal(t, `openai future --name "quoted value"`, candidates[1].Command)
	require.Equal(t, "Future example", candidates[1].Description)
	require.NotNil(t, root.Command("explore"))
}

func TestExploreRejectsUnsupportedOutputAndFlagsWithoutOutput(t *testing.T) {
	for _, args := range [][]string{
		{"--format", "json", "explore"}, {"--format", "explore", "explore"}, {"--transform", "id", "explore"}, {"--raw-output", "explore"},
		{"--format-error", "json", "explore"}, {"--transform-error", "message", "explore"}, {"explore", "--suggestions"}, {"explore", "unexpected"}, {"explore", "--shell", "invalid"},
	} {
		var output bytes.Buffer
		root := &cli.Command{Name: "openai", Writer: &output, ErrWriter: &output, ExitErrHandler: func(context.Context, *cli.Command, error) {}, Flags: []cli.Flag{&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"}, &cli.StringFlag{Name: "format-error"}, &cli.StringFlag{Name: "transform-error"}}}
		Register(root)
		err := root.Run(t.Context(), append([]string{"openai"}, args...))
		require.Error(t, err)
		require.Empty(t, output.String())
	}
}

func TestExploreDiagnosticsPreserveCauseAndHidePrivatePaths(t *testing.T) {
	cause := &os.PathError{Op: "read", Path: "private-path", Err: os.ErrPermission}
	failure := exploreFailure("Could not open the terminal for command search.", 2, cause)
	require.ErrorIs(t, failure, os.ErrPermission)
	var typed *commandExploreError
	require.True(t, errors.As(failure, &typed))
	require.Equal(t, 2, typed.ExitCode())
	require.Equal(t, "Could not open the terminal for command search.", failure.Error())
	require.NotContains(t, failure.Error(), "private-path")
	canceled := exploreFailure("Command selection canceled.", 130, context.Canceled)
	require.Equal(t, "Command selection canceled.", canceled.Error())
	require.ErrorIs(t, canceled, context.Canceled)
}
