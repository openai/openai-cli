package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestCommandErrorComposesWithSuccessWrapper(t *testing.T) {
	for _, contextFirst := range []bool{false, true} {
		for _, cause := range []error{nil, context.Canceled, &openai.Error{StatusCode: 400}, cli.Exit("synthetic failure", 7)} {
			t.Run(fmt.Sprintf("contextFirst=%t/cause=%T", contextFirst, cause), func(t *testing.T) {
				calls, successes := 0, 0
				leaf := &cli.Command{Name: "create", Action: func(context.Context, *cli.Command) error {
					calls++
					return cause
				}}
				root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard, Commands: []*cli.Command{{
					Name: "responses", Category: "API RESOURCE", Commands: []*cli.Command{leaf},
				}}}
				successWrapper := func() {
					next := leaf.Action
					leaf.Action = func(ctx context.Context, command *cli.Command) error {
						if err := next(ctx, command); err != nil {
							return fmt.Errorf("wrapped by success handler: %w", err)
						}
						successes++
						return nil
					}
				}
				if contextFirst {
					ConfigureCommandErrors(root)
					successWrapper()
				} else {
					successWrapper()
					ConfigureCommandErrors(root)
				}
				err := root.Run(t.Context(), []string{"openai", "responses", "create"})
				require.Equal(t, 1, calls)
				if cause == nil {
					require.NoError(t, err)
					require.Equal(t, 1, successes)
					return
				}
				require.Zero(t, successes)
				require.ErrorIs(t, err, cause)
				var contextual *commandError
				require.ErrorAs(t, err, &contextual)
				require.Same(t, leaf, contextual.command)
				if _, ok := cause.(cli.ExitCoder); ok {
					var exit cli.ExitCoder
					require.ErrorAs(t, err, &exit)
					require.Equal(t, 7, exit.ExitCode())
				}
				if _, ok := cause.(*openai.Error); ok {
					var apierr *openai.Error
					require.ErrorAs(t, err, &apierr)
					require.Same(t, cause, apierr)
				}
			})
		}
	}
}

func TestCommandErrorUsageHookPreservesHandlerAndSuppressesDefaultOutput(t *testing.T) {
	for _, previousHandler := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		root := &cli.Command{Name: "openai", Writer: &stdout, ErrWriter: &stderr}
		expected := errors.New("existing usage handler")
		if previousHandler {
			root.OnUsageError = func(context.Context, *cli.Command, error, bool) error { return expected }
		}
		ConfigureCommandErrors(root)
		err := root.Run(t.Context(), []string{"openai", "--synthetic-unknown"})
		var contextual *commandError
		require.ErrorAs(t, err, &contextual)
		require.Same(t, root, contextual.command)
		if previousHandler {
			require.ErrorIs(t, err, expected)
		}
		require.Empty(t, stdout.String())
		require.Empty(t, stderr.String())
	}
}

func TestCommandErrorPreservesInnermostContext(t *testing.T) {
	inner, outer := &cli.Command{Name: "inner"}, &cli.Command{Name: "outer"}
	cause := errors.New("synthetic")
	err := withCommandError(inner, cause)
	require.Same(t, err, withCommandError(outer, err))
	require.EqualError(t, err, cause.Error())
	require.Nil(t, withCommandError(inner, nil))
}

func TestCommandErrorsConfigureLateCommandsOnce(t *testing.T) {
	root := &cli.Command{Name: "openai"}
	ConfigureCommandErrors(root)
	calls := 0
	cause := errors.New("synthetic cause")
	late := &cli.Command{Name: "help", Action: func(context.Context, *cli.Command) error { calls++; return cause }}
	root.Commands = append(root.Commands, late)
	ConfigureCommandErrors(root)
	ConfigureCommandErrors(root)
	err := late.Action(t.Context(), late)
	var contextual *commandError
	require.ErrorAs(t, err, &contextual)
	require.Same(t, late, contextual.command)
	require.Same(t, cause, contextual.Unwrap())
	require.Equal(t, 1, calls)
}

func TestRequiredPositionalArgumentShowsHelp(t *testing.T) {
	command := &cli.Command{
		Name: "get", Usage: "Read one item.",
		Arguments: []cli.Argument{&cli.StringArg{Name: "item", Required: true}},
		Action: func(context.Context, *cli.Command) error {
			t.Fatal("the command action ran without its required argument")
			return nil
		},
	}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{command}, Writer: io.Discard, ErrWriter: io.Discard}
	args, _, err := clihelp.Configure(root, []string{"openai", "get"})
	require.NoError(t, err)
	ConfigureCommandErrors(root)
	failure := root.Run(t.Context(), args)
	require.Error(t, failure)
	var out strings.Builder
	require.NoError(t, ShowCommandError(root, failure, &out))
	require.Contains(t, out.String(), "Missing required arguments: item.\n\n")
	require.Contains(t, out.String(), "Read one item.")
}

func TestMinimumPositionalArgumentCountShowsHelp(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		help       bool
		valid      bool
		structured bool
	}{
		{name: "zero values", help: true},
		{name: "one value", args: []string{"1"}, help: true},
		{name: "enough values", args: []string{"1", "2"}, valid: true},
		{name: "invalid value", args: []string{"bad"}, help: false},
		{name: "structured error", args: []string{"1"}, structured: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			command := &cli.Command{
				Name: "compare", Usage: "Compare two record IDs.",
				Arguments: []cli.Argument{&cli.IntArgs{Name: "ids", Min: 2, Max: 2}},
				Action: func(context.Context, *cli.Command) error {
					called = true
					return nil
				},
			}
			root := &cli.Command{
				Name: "openai", Commands: []*cli.Command{command}, Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{&cli.StringFlag{Name: "format-error"}},
			}
			invocation := []string{"openai"}
			if tc.structured {
				invocation = append(invocation, "--format-error", "json")
			}
			invocation = append(append(invocation, "compare"), tc.args...)
			args, _, err := clihelp.Configure(root, invocation)
			require.NoError(t, err)
			ConfigureCommandErrors(root)
			failure := root.Run(t.Context(), args)
			require.Equal(t, tc.valid, called)
			if tc.valid {
				require.NoError(t, failure)
				return
			}
			require.Error(t, failure)
			var out, help strings.Builder
			require.NoError(t, ShowCommandError(root, failure, &out))
			if tc.structured {
				require.JSONEq(t, `{"message":"The command could not be completed. Check your arguments with --help."}`, out.String())
				return
			}
			require.NoError(t, clihelp.WriteCommandHelp(t.Context(), command, &help))
			require.Equal(t, tc.help, strings.HasSuffix(out.String(), "\n\n"+help.String()))
			if tc.help {
				require.True(t, strings.HasPrefix(out.String(), "Provide at least 2 values for ids.\n\n"), out.String())
			}
		})
	}
}
