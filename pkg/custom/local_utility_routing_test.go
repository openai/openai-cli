package custom

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestLocalUtilityRoutingPreservesPreviousBefore(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "previous hook fails"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("OPENAI_MTLS_CLIENT_CERT_FILE", "/synthetic-private/missing-cert")
			t.Setenv("OPENAI_MTLS_CLIENT_KEY_FILE", "")
			type contextKey struct{}
			failure := errors.New("synthetic previous hook failure")
			beforeCalls, actionCalls := 0, 0
			root := &cli.Command{
				Name: "openai-test", Writer: io.Discard, ErrWriter: io.Discard,
				Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
					beforeCalls++
					if fail {
						return ctx, failure
					}
					return context.WithValue(ctx, contextKey{}, "preserved"), nil
				},
				Commands: []*cli.Command{{
					Name: "synthetic-local", Metadata: map[string]any{localUtilityMetadata: true},
					Action: func(ctx context.Context, _ *cli.Command) error {
						actionCalls++
						require.Equal(t, "preserved", ctx.Value(contextKey{}))
						return nil
					},
				}},
			}
			ConfigureCommand(root)
			ConfigureCommand(root)
			err := root.Run(t.Context(), []string{"openai-test", "synthetic-local"})
			require.Equal(t, 1, beforeCalls)
			if fail {
				require.ErrorIs(t, err, failure)
				require.Zero(t, actionCalls)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, actionCalls)
			}
		})
	}
}

func TestLocalUtilityRoutingPreservesErrorCauseAndExitStatus(t *testing.T) {
	t.Setenv("OPENAI_MTLS_CLIENT_CERT_FILE", "")
	t.Setenv("OPENAI_MTLS_CLIENT_KEY_FILE", "")
	want := &localUtilityError{message: "Synthetic local failure.", cause: cli.Exit("", 7)}
	root := &cli.Command{
		Name: "openai-test", Writer: io.Discard, ErrWriter: io.Discard,
		Commands: []*cli.Command{{
			Name: "synthetic-local", Metadata: map[string]any{localUtilityMetadata: true},
			Action: func(context.Context, *cli.Command) error { return want },
		}},
	}
	ConfigureCommand(root)
	err := root.Run(t.Context(), []string{"openai-test", "synthetic-local"})
	require.ErrorIs(t, err, want)
	var contextual *commandError
	require.ErrorAs(t, err, &contextual)
	var exit cli.ExitCoder
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 7, exit.ExitCode())
	require.Equal(t, "Synthetic local failure.", localErrorMessage(root, err))
}
