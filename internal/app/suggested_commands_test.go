package app

import (
	"bytes"
	"context"
	"errors"
	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/openai/openai-cli/internal/commandsuggest"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"io"
	"net/http"
	"testing"
)

type recordedCommand struct {
	output      io.Writer
	shell, text string
}
type commandRecorder []recordedCommand

func (r *commandRecorder) Record(output io.Writer, shell, text string) {
	*r = append(*r, recordedCommand{output, shell, text})
}
func TestSuggestedReadableErrorCommandsKeepOutputModes(t *testing.T) {
	for _, format := range []string{"text", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			root := readableErrorTestCommand(t, "--format-error", format)
			var recorder commandRecorder
			commandsuggest.SetRecorder(root, &recorder)
			apierr := &openai.Error{StatusCode: http.StatusUnauthorized, Message: "openai models delete --model untrusted"}
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(root, apierr, &out))
			if format == "text" {
				require.Equal(t, commandRecorder{{&out, "", "./openai help setup"}}, recorder)
				require.NotContains(t, out.String(), "untrusted")
			} else {
				require.Empty(t, recorder)
			}
		})
	}
}
func TestSuggestedTypoRecoveryIncludesExecutableAndParents(t *testing.T) {
	var out bytes.Buffer
	root := &cli.Command{Name: "openai", Writer: &out, ErrWriter: io.Discard, Suggest: true,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags:          []cli.Flag{&cli.StringFlag{Name: "format-error", Value: "text"}},
		Commands:       []*cli.Command{{Name: "models", Suggest: true, Commands: []*cli.Command{{Name: "list"}}}},
	}
	var recorder commandRecorder
	commandsuggest.SetRecorder(root, &recorder)
	args, _, err := ConfigureHelp(root, []string{"openai", "models", "lsit"})
	require.NoError(t, err)
	failure := root.Run(t.Context(), args)
	require.Error(t, failure)
	out.Reset()
	require.NoError(t, ShowCommandError(root, failure, &out))
	require.Equal(t, "Unknown command. Did you mean: openai help models list?\nTo run it, correct only the command spelling. Keep your original options and arguments.\n", out.String())
	require.Equal(t, commandRecorder{{&out, "", "openai help models list"}}, recorder)
}
func TestSuggestionSetupErrorsKeepSafeGuidance(t *testing.T) {
	for _, message := range []string{
		"--suggestions-double-tab requires --suggestions",
		"--suggestions-click requires --suggestions",
		"click insertion currently supports zsh with a local Kitty adapter",
		"--suggestions-adapter cannot be combined with a shell argument or completion options",
		"the supported suggestion adapter is kitty",
		"command insertion options cannot be combined with persistent picker setup",
		"command insertion currently supports Bash 4.3+, zsh, and fish; PowerShell requires a separate shell integration",
		"a command name is required for suggestion integration",
		"suggestion integration requires a simple command name on PATH",
	} {
		for _, format := range []string{"text", "json"} {
			root := readableErrorTestCommand(t, "--format-error", format)
			var out bytes.Buffer
			require.NoError(t, ShowCommandError(root, errors.New(message), &out))
			require.Contains(t, out.String(), message)
		}
	}
}
func TestSuggestedNewParserGuidanceUsesCurrentCommand(t *testing.T) {
	for _, format := range []string{"text", "json", "yaml"} {
		var output bytes.Buffer
		root := &cli.Command{Name: "openai", Writer: &output, ErrWriter: io.Discard,
			ExitErrHandler: func(context.Context, *cli.Command, error) {},
			Flags:          []cli.Flag{&cli.StringFlag{Name: "format-error", Value: format}},
			Commands:       []*cli.Command{{Name: "widgets", Commands: []*cli.Command{{Name: "inspect", Flags: []cli.Flag{&cli.StringFlag{Name: "widget", Required: true}}}}}},
		}
		args, _, err := ConfigureHelp(root, []string{"openai", "widgets", "inspect"})
		require.NoError(t, err)
		ConfigureCommandErrors(root)
		var recorder commandRecorder
		commandsuggest.SetRecorder(root, &recorder)
		failure := root.Run(t.Context(), args)
		require.Error(t, failure)
		output.Reset()
		require.NoError(t, ShowCommandError(root, failure, &output))
		if format == "text" {
			require.Contains(t, output.String(), "Options and examples: openai widgets inspect --help")
			require.Equal(t, commandRecorder{{&output, "", "openai widgets inspect --help"}}, recorder)
		} else {
			require.Empty(t, recorder)
		}
	}
}
func TestSuggestedParserGuidanceKeepsUnsafeNamesPlain(t *testing.T) {
	for _, name := range []string{"widgets;echo", "widgets space", "$(widgets)", "-widgets"} {
		var output bytes.Buffer
		command := &cli.Command{Name: name, Writer: &output}
		var recorder commandRecorder
		commandsuggest.SetRecorder(command, &recorder)
		text := parserErrorGuidance(command, "Missing required options.", &output)
		require.Contains(t, text, "Options and examples:")
		require.Empty(t, recorder)
	}
}

func TestSuggestedImageErrorKeepsMachineModes(t *testing.T) {
	for _, format := range []string{"text", "json", "yaml"} {
		root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
			ExitErrHandler: func(context.Context, *cli.Command, error) {},
			Flags:          []cli.Flag{&cli.StringFlag{Name: "format-error", Value: format}},
			Commands:       []*cli.Command{{Name: "images"}},
		}
		images.RegisterPreview(root, ImagePreviewOptions())
		failure := root.Run(t.Context(), []string{"openai", "images", "preview"})
		require.Error(t, failure)
		var recorder commandRecorder
		commandsuggest.SetRecorder(root, &recorder)
		var out bytes.Buffer
		require.NoError(t, ShowCommandError(root, failure, &out))
		if format == "text" {
			require.Equal(t, commandRecorder{{&out, "", `openai images preview "path/to/image.png"`}}, recorder)
		} else {
			require.Empty(t, recorder)
		}
	}
}

func TestSuggestedReceiptHonorsSuppressedDiagnostics(t *testing.T) {
	root := readableErrorTestCommand(t)
	var recorder commandRecorder
	commandsuggest.SetRecorder(root, &recorder)
	ctx := context.WithValue(t.Context(), outputPolicyKey{}, outputPolicy{diagnostics: false})
	require.NoError(t, writeSuggestedImageReceipt(ctx, root, "Try openai models list", []string{"openai models list"}))
	require.Empty(t, recorder)
}
