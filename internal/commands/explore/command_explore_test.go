package explore

import (
	"bytes"
	"context"
	"errors"
	"io"
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
		{"--format-error", "json", "explore"}, {"--transform-error", "message", "explore"}, {"explore", "--suggestions"}, {"explore", "--shell", "invalid"},
		{"explore", " "}, {"explore", "--shell-integration", "images"}, {"explore", "--suggestions", "images"}, {"explore", "--shell", "bash", "images"},
		{"--format", "json", "explore", "images"}, {"--raw-output", "explore", "images"},
	} {
		var output bytes.Buffer
		root := &cli.Command{Name: "openai", Writer: &output, ErrWriter: &output, ExitErrHandler: func(context.Context, *cli.Command, error) {}, Flags: []cli.Flag{&cli.StringFlag{Name: "format"}, &cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"}, &cli.StringFlag{Name: "format-error"}, &cli.StringFlag{Name: "transform-error"}}}
		Register(root)
		err := root.Run(t.Context(), append([]string{"openai"}, args...))
		require.Error(t, err)
		require.Empty(t, output.String())
	}
}

type unreadSearchInput struct{}

func (unreadSearchInput) Read([]byte) (int, error) { panic("search must not read stdin") }

func TestExploreQueryUsesOnlyVisibleDeclaredCandidates(t *testing.T) {
	t.Setenv("TERM", "dumb")
	for _, query := range [][]string{{"future", "model"}, {"FuTuRe MoDeL"}, {"  future  ", "  model  "}} {
		var output bytes.Buffer
		root := &cli.Command{Name: "openai", Reader: unreadSearchInput{}, Writer: &output, Commands: []*cli.Command{
			{Name: "future", Usage: "Inspect model details.", Action: func(context.Context, *cli.Command) error { t.Fatal("search ran a result"); return nil }},
			{Name: "hidden", Hidden: true, Usage: "Future model"},
		}}
		Register(root)
		require.NoError(t, root.Run(t.Context(), append([]string{"openai", "explore"}, query...)))
		require.Contains(t, output.String(), " future")
		require.Contains(t, output.String(), "Inspect model details.")
		require.NotContains(t, output.String(), "hidden")
		require.NotContains(t, output.String(), "\x1b")
	}
}

type failedSearchOutput struct{ cause error }

func (w failedSearchOutput) Write([]byte) (int, error) { return 0, w.cause }

func TestExploreQueryReportsOutputFailureWithoutPrivateDetails(t *testing.T) {
	for _, cause := range []error{nil, &os.PathError{Op: "write", Path: "private-output", Err: os.ErrPermission}} {
		root := &cli.Command{Name: "openai", Writer: failedSearchOutput{cause}, Reader: unreadSearchInput{}, ExitErrHandler: func(context.Context, *cli.Command, error) {}}
		Register(root)
		err := root.Run(t.Context(), []string{"openai", "explore", "explore"})
		if cause == nil {
			require.ErrorIs(t, err, io.ErrShortWrite)
		} else {
			require.ErrorIs(t, err, os.ErrPermission)
		}
		var failure *commandExploreError
		require.ErrorAs(t, err, &failure)
		require.Equal(t, 1, failure.ExitCode())
		require.NotContains(t, err.Error(), "private-output")
	}
}

func TestExploreQueryCancellationProducesNoResults(t *testing.T) {
	var output bytes.Buffer
	root := &cli.Command{Name: "openai", Writer: &output, Reader: unreadSearchInput{}, ExitErrHandler: func(context.Context, *cli.Command, error) {}}
	Register(root)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := root.Run(ctx, []string{"openai", "explore", "images"})
	require.ErrorIs(t, err, context.Canceled)
	var failure *commandExploreError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, 130, failure.ExitCode())
	require.Empty(t, output.String())
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
