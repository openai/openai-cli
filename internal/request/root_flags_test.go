package request

import (
	"context"
	"io"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestRootRequestFlagsExplicitAssignmentProvenance(t *testing.T) {
	for _, name := range []string{"api-key", "admin-api-key", "organization", "project", "webhook-secret"} {
		t.Run(name, func(t *testing.T) {
			flag := "--" + name
			for _, tc := range []struct {
				name        string
				environment string
				args        []string
				want        string
				explicit    bool
			}{
				{name: "environment", environment: "synthetic-value", args: []string{"models", "list"}, want: "synthetic-value"},
				{name: "empty environment", args: []string{"models", "list"}},
				{name: "before command", environment: "synthetic-env", args: []string{flag, "synthetic-flag", "models", "list"}, want: "synthetic-flag", explicit: true},
				{name: "between commands", environment: "synthetic-env", args: []string{"models", flag, "synthetic-flag", "list"}, want: "synthetic-flag", explicit: true},
				{name: "after command", environment: "synthetic-env", args: []string{"models", "list", flag, "synthetic-flag"}, want: "synthetic-flag", explicit: true},
				{name: "alias", environment: "synthetic-env", args: []string{"models", "list", "-p", "synthetic-flag"}, want: "synthetic-flag", explicit: true},
				{name: "equal values", environment: "synthetic-value", args: []string{"models", "list", flag + "=synthetic-value"}, want: "synthetic-value", explicit: true},
				{name: "explicit empty", environment: "synthetic-env", args: []string{"models", flag + "=", "list"}, explicit: true},
				{name: "repeated", environment: "synthetic-env", args: []string{flag + "=first", "models", flag + "=second", "list", flag + "=last"}, want: "last", explicit: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Setenv("OPENAI_TEST_ROOT_REQUEST_PROVENANCE", tc.environment)
					called := false
					root := &cli.Command{
						Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
						Flags: []cli.Flag{&requestflag.Flag[string]{
							Name: name, Aliases: []string{"p"}, Sources: cli.EnvVars("OPENAI_TEST_ROOT_REQUEST_PROVENANCE"),
						}},
						Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{
							Name: "list",
							Action: func(_ context.Context, command *cli.Command) error {
								called = true
								require.Equal(t, tc.explicit, ExplicitRootFlag(command.Root(), name))
								require.True(t, command.Root().IsSet(name))
								require.Equal(t, tc.want, command.Root().String(name))
								require.Equal(t, tc.want, command.String(name))
								return nil
							},
						}}}},
					}
					ConfigureRootFlags(root)
					require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, tc.args...)))
					require.True(t, called)
				})
			}
		})
	}
}

func TestRootRequestFlagPreParseResetsOnlyProvenance(t *testing.T) {
	wrapped := &RootRequestFlag{Flag: &requestflag.Flag[string]{Name: "project", Default: "default-project"}}
	require.NoError(t, wrapped.PreParse())
	require.False(t, wrapped.explicitlySet)
	require.False(t, wrapped.IsSet())
	require.NoError(t, wrapped.Set("project", "explicit-project"))
	require.True(t, wrapped.explicitlySet)
	require.Equal(t, 1, wrapped.Count())

	require.NoError(t, wrapped.PreParse())
	require.False(t, wrapped.explicitlySet)
	// Preserve the embedded flag's existing state across repeated parsing.
	require.True(t, wrapped.IsSet())
	require.Equal(t, 1, wrapped.Count())
	require.Equal(t, "default-project", wrapped.Get())
}

func TestRootRequestFlagsResetProvenanceAcrossRuns(t *testing.T) {
	var explicit []bool
	root := &cli.Command{
		Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{&requestflag.Flag[string]{Name: "project"}},
		Action: func(_ context.Context, command *cli.Command) error {
			explicit = append(explicit, ExplicitRootFlag(command, "project"))
			return nil
		},
	}
	ConfigureRootFlags(root)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--project=synthetic-project"}))
	require.NoError(t, root.Run(t.Context(), []string{"openai"}))
	require.Equal(t, []bool{true, false}, explicit)
}
