package vectorstores

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

type workspaceRoutingState struct {
	previous, service, run, direct int
	project, organization, url     string
	header                         []string
	output                         bytes.Buffer
}

func workspaceRoutingRoot(terminal bool) (*cli.Command, *workspaceRoutingState) {
	state := &workspaceRoutingState{}
	root := &cli.Command{
		Name: "openai", Reader: strings.NewReader(""), Writer: &state.output, ErrWriter: &state.output,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "format-error", Value: "text"},
			&cli.StringFlag{Name: "transform"},
			&cli.StringFlag{Name: "transform-error"},
			&cli.BoolFlag{Name: "raw-output"},
			&cli.BoolFlag{Name: "quiet"},
			&cli.BoolFlag{Name: "debug"},
			&cli.BoolFlag{Name: "verbose"},
			&cli.StringFlag{Name: "project"},
			&cli.StringFlag{Name: "organization"},
			&cli.StringFlag{Name: "base-url"},
			&cli.StringSliceFlag{Name: "header", Aliases: []string{"H"}},
		},
		Commands: []*cli.Command{{
			Name: "vector-stores",
			Action: func(context.Context, *cli.Command) error {
				state.previous++
				return nil
			},
			Commands: []*cli.Command{{
				Name: "search", Action: func(context.Context, *cli.Command) error {
					state.direct++
					return nil
				},
			}},
		}},
	}
	workspaceCommand{
		options: Options{NewService: func(_ context.Context, command *cli.Command) (Service, error) {
			state.service++
			state.project = command.Root().String("project")
			state.organization = command.String("organization")
			state.url = command.String("base-url")
			state.header = command.StringSlice("header")
			return nil, nil
		}},
		terminal: func(*cli.Command) (*os.File, *os.File, bool) { return nil, nil, terminal },
		run: func(context.Context, Service, func(error) string, *os.File, *os.File) error {
			state.run++
			return nil
		},
	}.configure(root)
	return root, state
}

func TestWorkspaceCommandRouting(t *testing.T) {
	for _, tc := range []struct {
		name             string
		args             []string
		terminal         bool
		run, old, direct int
		wantError        bool
	}{
		{"bare terminal", []string{"vector-stores"}, true, 1, 0, 0, false},
		{"explicit terminal", []string{"vector-stores", "manage"}, true, 1, 0, 0, false},
		{"bare pipe", []string{"vector-stores"}, false, 0, 1, 0, false},
		{"explicit pipe", []string{"vector-stores", "manage"}, false, 0, 0, 0, true},
		{"generated operation", []string{"vector-stores", "search"}, true, 0, 0, 1, false},
		{"bare positional", []string{"vector-stores", "unrecognized"}, true, 0, 1, 0, false},
		{"explicit positional", []string{"vector-stores", "manage", "private-value"}, true, 0, 0, 0, true},
		{"bare help", []string{"vector-stores", "--help"}, true, 0, 0, 0, false},
		{"explicit help", []string{"vector-stores", "manage", "--help"}, true, 0, 0, 0, false},
		{"help route", []string{"help", "vector-stores"}, true, 0, 0, 0, false},
		{"command name as value", []string{"--project", "manage", "vector-stores", "search"}, true, 0, 0, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, state := workspaceRoutingRoot(tc.terminal)
			err := root.Run(t.Context(), append([]string{"openai"}, tc.args...))
			if tc.wantError {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "private-value")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.run, state.run)
			assert.Equal(t, tc.run, state.service)
			assert.Equal(t, tc.old, state.previous)
			assert.Equal(t, tc.direct, state.direct)
		})
	}
}

func TestWorkspaceExplicitOutputChoices(t *testing.T) {
	for _, flags := range [][]string{
		{"--format", "auto"}, {"--format", "text"}, {"--format", "json"}, {"--format", "jsonl"},
		{"--format", "raw"}, {"--format", "yaml"}, {"--format", "explore"},
		{"--format-error", "text"}, {"--format-error", "auto"}, {"--format-error", "json"},
		{"--transform", "id"}, {"--transform", ""}, {"--transform-error", "message"},
		{"--raw-output"}, {"--raw-output=false"}, {"--quiet"}, {"--quiet=false"},
		{"--debug"}, {"--debug=false"}, {"--verbose"}, {"--verbose=false"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			for _, explicit := range []bool{false, true} {
				root, state := workspaceRoutingRoot(true)
				args := append([]string{"openai"}, flags...)
				args = append(args, "vector-stores")
				if explicit {
					args = append(args, "manage")
				}
				err := root.Run(t.Context(), args)
				compatible := explicit && (flags[0] == "--format" || flags[0] == "--format-error") &&
					(flags[1] == "auto" || flags[1] == "text")
				switch {
				case compatible:
					require.NoError(t, err)
					assert.Equal(t, 1, state.run)
				case explicit:
					require.Error(t, err)
					assert.Zero(t, state.run)
				default:
					require.NoError(t, err)
					assert.Equal(t, 1, state.previous)
					assert.Zero(t, state.run)
				}
				assert.Equal(t, state.run, state.service)
				assert.Zero(t, state.direct)
			}
		})
	}
}

func TestWorkspaceInheritedContext(t *testing.T) {
	for _, tailFlags := range []bool{false, true} {
		root, state := workspaceRoutingRoot(true)
		flags := []string{"--project", "proj_synthetic", "--organization", "org_synthetic", "--base-url", "http://127.0.0.1:1234/v1", "-H", "X-Synthetic: literal"}
		args := append([]string{"openai"}, flags...)
		args = append(args, "vector-stores", "manage")
		if tailFlags {
			args = append([]string{"openai", "vector-stores", "manage"}, flags...)
		}
		require.NoError(t, root.Run(t.Context(), args))
		assert.Equal(t, 1, state.run)
		assert.Equal(t, "proj_synthetic", state.project)
		assert.Equal(t, "org_synthetic", state.organization)
		assert.Equal(t, "http://127.0.0.1:1234/v1", state.url)
		assert.Equal(t, []string{"X-Synthetic: literal"}, state.header)
	}
}

func TestWorkspaceConfigurationPreservesOwners(t *testing.T) {
	t.Run("idempotent", func(t *testing.T) {
		root, _ := workspaceRoutingRoot(false)
		group := root.Command("vector-stores")
		before := len(group.Commands)
		Configure(root, Options{})
		assert.Len(t, group.Commands, before)
	})
	t.Run("missing group", func(t *testing.T) {
		root := &cli.Command{Name: "openai"}
		Configure(root, Options{})
		assert.Empty(t, root.Commands)
	})
	t.Run("existing manage", func(t *testing.T) {
		existing := &cli.Command{Name: "manage", Usage: "Existing owner"}
		group := &cli.Command{Name: "vector-stores", Commands: []*cli.Command{existing}}
		root := &cli.Command{Name: "openai", Commands: []*cli.Command{group}}
		Configure(root, Options{})
		assert.Same(t, existing, group.Command("manage"))
		assert.Nil(t, group.Action)
	})
	t.Run("bare fallback help", func(t *testing.T) {
		var output bytes.Buffer
		root := &cli.Command{Name: "openai", Writer: &output, ErrWriter: &output,
			Reader: strings.NewReader(""), Commands: []*cli.Command{{Name: "vector-stores"}}}
		Configure(root, Options{})
		require.NoError(t, root.Run(t.Context(), []string{"openai", "vector-stores"}))
		assert.Contains(t, output.String(), "manage")
		assert.Contains(t, output.String(), "Create vector stores")
	})
}

func TestWorkspaceTerminalRejectsPipesWithoutReading(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	input, sender, err := os.Pipe()
	require.NoError(t, err)
	defer input.Close()
	defer sender.Close()
	output, sink, err := os.Pipe()
	require.NoError(t, err)
	defer output.Close()
	defer sink.Close()
	root := &cli.Command{Name: "openai", Reader: input, Writer: sink}
	gotInput, gotOutput, ok := workspaceTerminal(root)
	assert.Same(t, input, gotInput)
	assert.Same(t, sink, gotOutput)
	assert.False(t, ok)
	_, err = sender.Write([]byte("retained"))
	require.NoError(t, err)
	data := make([]byte, len("retained"))
	_, err = input.Read(data)
	require.NoError(t, err)
	assert.Equal(t, "retained", string(data))
}

func TestWorkspaceLaunchFailureRetainsIdentity(t *testing.T) {
	failure := errors.New("synthetic cause")
	called := false
	command := workspaceCommand{
		options: Options{NewService: func(context.Context, *cli.Command) (Service, error) { return nil, failure }},
		run: func(context.Context, Service, func(error) string, *os.File, *os.File) error {
			called = true
			return nil
		},
	}
	err := command.launch(t.Context(), &cli.Command{Name: "manage"}, nil, nil)
	assert.ErrorIs(t, err, failure)
	assert.False(t, called)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, command.launch(ctx, &cli.Command{}, nil, nil), context.Canceled)
}

func TestWorkspaceFailureHidesUnderlyingDiagnostics(t *testing.T) {
	cause := errors.New("synthetic private value")
	err := workspaceFailure("Could not display the workspace.", cause)
	assert.NotContains(t, err.Error(), cause.Error())
	assert.ErrorIs(t, err, cause)
	var local *localcommands.LocalError
	assert.ErrorAs(t, err, &local)
}
