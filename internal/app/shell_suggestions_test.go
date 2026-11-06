package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/autocomplete"
	"github.com/urfave/cli/v3"
)

func shellSuggestionCommand(action cli.ActionFunc, output *bytes.Buffer) *cli.Command {
	return &cli.Command{Name: "openai", Writer: output, ErrWriter: output,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands: []*cli.Command{{Name: "@completion", Action: action, Flags: []cli.Flag{
			&cli.BoolFlag{Name: "picker"},
			&cli.BoolFlag{Name: "install-picker"},
			&cli.BoolFlag{Name: "uninstall-picker"},
			&cli.BoolFlag{Name: "automatic"},
			&cli.StringFlag{Name: "profile"},
		}}},
	}
}

func TestShellSuggestionsRejectSetupBeforeSideEffects(t *testing.T) {
	for _, options := range [][]string{
		{"--picker"}, {"--install-picker"}, {"--uninstall-picker"}, {"--automatic"},
		{"--profile", "synthetic-private-profile"}, {"--profile="},
	} {
		t.Run(strings.Join(options, "/"), func(t *testing.T) {
			var output bytes.Buffer
			called := false
			root := shellSuggestionCommand(func(context.Context, *cli.Command) error {
				called = true
				return nil
			}, &output)
			configureShellSuggestions(root)
			args := append([]string{"openai", "@completion", "zsh", "--suggest-executable"}, options...)
			err := root.Run(context.Background(), args)
			var exit cli.ExitCoder
			if !errors.As(err, &exit) || exit.ExitCode() != 2 || called || output.Len() != 0 {
				t.Fatalf("mixed setup reached an action or changed output/status: called=%v error=%v output=%q", called, err, output.String())
			}
			if !strings.Contains(err.Error(), "openai '@completion' SHELL --suggest-executable") || strings.Contains(err.Error(), "synthetic-private") {
				t.Fatalf("setup guidance lost its safe next action: %v", err)
			}
		})
	}
}

func TestShellSuggestionsValidateShellWithoutRepeatingInputs(t *testing.T) {
	for _, args := range [][]string{
		{}, {"zsh", "synthetic-private-extra"}, {"synthetic-private-shell\x1b[31m"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var output bytes.Buffer
			called := false
			root := shellSuggestionCommand(func(context.Context, *cli.Command) error { called = true; return nil }, &output)
			configureShellSuggestions(root)
			invocation := append([]string{"openai", "@completion", "--suggest-executable"}, args...)
			err := root.Run(context.Background(), invocation)
			var exit cli.ExitCoder
			if !errors.As(err, &exit) || exit.ExitCode() != 2 || called || output.Len() != 0 {
				t.Fatalf("invalid shell reached output/action: called=%v error=%v output=%q", called, err, output.String())
			}
			if !strings.Contains(err.Error(), "openai '@completion' SHELL --suggest-executable") || strings.Contains(err.Error(), "synthetic-private") || strings.ContainsRune(err.Error(), '\x1b') {
				t.Fatalf("shell guidance leaked input or omitted repair: %v", err)
			}
		})
	}
}

func TestShellSuggestionsPreserveExplicitConsentAndPriorAction(t *testing.T) {
	for _, args := range [][]string{
		{"zsh"}, {"zsh", "--suggest-executable=false", "--install-picker"},
		{"fish", "--suggest-executable=false", "--picker"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var output bytes.Buffer
			original := errors.New("synthetic original action failure")
			calls := 0
			root := shellSuggestionCommand(func(_ context.Context, command *cli.Command) error {
				calls++
				_, _ = command.Writer.Write([]byte("original output"))
				return original
			}, &output)
			configureShellSuggestions(root)
			configureShellSuggestions(root)
			err := root.Run(context.Background(), append([]string{"openai", "@completion"}, args...))
			if !errors.Is(err, original) || calls != 1 || output.String() != "original output" {
				t.Fatalf("prior action changed: calls=%d error=%v output=%q", calls, err, output.String())
			}
		})
	}
}

func TestShellSuggestionsPrintStandaloneHook(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			var output bytes.Buffer
			root := shellSuggestionCommand(func(context.Context, *cli.Command) error {
				t.Fatal("suggestions called ordinary completion or picker setup")
				return nil
			}, &output)
			configureShellSuggestions(root)
			configureShellSuggestions(root)
			err := root.Run(context.Background(), []string{"openai", "@completion", shell, "--suggest-executable",
				"--picker=false", "--install-picker=false", "--uninstall-picker=false", "--automatic=false"})
			want, renderErr := autocomplete.RenderShellSuggestions(autocomplete.CompletionStyle(shell), "openai")
			if err != nil || renderErr != nil || output.String() != want {
				t.Fatalf("standalone output changed: run=%v render=%v output=%q", err, renderErr, output.String())
			}
		})
	}
}

type shellSuggestionFailWriter struct{ err error }

func (w shellSuggestionFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestShellSuggestionsPreserveWriteFailure(t *testing.T) {
	var output bytes.Buffer
	root := shellSuggestionCommand(autocomplete.OutputCompletionScript, &output)
	failure := errors.New("synthetic script write failure")
	root.Writer = shellSuggestionFailWriter{failure}
	configureShellSuggestions(root)
	err := root.Run(context.Background(), []string{"openai", "@completion", "zsh", "--suggest-executable"})
	var exit cli.ExitCoder
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(err.Error(), failure.Error()) {
		t.Fatalf("script write failure lost status or cause: %v", err)
	}
}

func TestShellSuggestionsRenderFailureDoesNotEmitPartialScript(t *testing.T) {
	var output bytes.Buffer
	root := shellSuggestionCommand(autocomplete.OutputCompletionScript, &output)
	root.Name = "synthetic-private;name"
	configureShellSuggestions(root)
	err := root.Run(context.Background(), []string{root.Name, "@completion", "zsh", "--suggest-executable"})
	var exit cli.ExitCoder
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || output.Len() != 0 {
		t.Fatalf("renderer failure emitted a partial script or lost status: error=%v output=%q", err, output.String())
	}
	if strings.Contains(err.Error(), "synthetic-private") {
		t.Fatalf("renderer repeated an unsafe executable name: %v", err)
	}
}
