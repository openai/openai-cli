package custom

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestConfigureCommandComposesBeforeAndRunsOnce(t *testing.T) {
	type contextKey struct{}
	beforeCalls, actionCalls := 0, 0
	root := &cli.Command{
		Name: "openai-test",
		Before: func(ctx context.Context, command *cli.Command) (context.Context, error) {
			beforeCalls++
			command.Root().Metadata[mtlsHTTPClientMetadata] = "stale"
			return context.WithValue(ctx, contextKey{}, "preserved"), nil
		},
		Commands: []*cli.Command{{
			Name: "child",
			Action: func(ctx context.Context, command *cli.Command) error {
				actionCalls++
				require.Equal(t, "preserved", ctx.Value(contextKey{}))
				require.NotContains(t, command.Root().Metadata, mtlsHTTPClientMetadata)
				return nil
			},
		}},
	}
	ConfigureCommand(root)
	ConfigureCommand(root)
	for _, name := range []string{mtlsClientCertFileFlag, mtlsClientKeyFileFlag} {
		count := 0
		for _, flag := range root.Flags {
			if flag.Names()[0] == name {
				count++
			}
		}
		require.Equal(t, 1, count, name)
	}
	require.NoError(t, root.Run(t.Context(), []string{root.Name, "child"}))
	require.Equal(t, 1, beforeCalls)
	require.Equal(t, 1, actionCalls)
}

func TestConfigureCommandPreservesBeforeError(t *testing.T) {
	expected := errors.New("existing hook failed")
	root := &cli.Command{
		Name:   "openai-test",
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) { return ctx, expected },
		Action: func(context.Context, *cli.Command) error { t.Fatal("action must not run"); return nil },
	}
	ConfigureCommand(root)
	require.ErrorIs(t, root.Run(t.Context(), []string{root.Name}), expected)
}

func TestConfigureCommandPreservesMTLSValidation(t *testing.T) {
	root := &cli.Command{
		Name:   "openai-test",
		Action: func(context.Context, *cli.Command) error { t.Fatal("invalid mTLS must stop the action"); return nil },
	}
	ConfigureCommand(root)
	require.EqualError(t, root.Run(t.Context(), []string{root.Name, "--mtls-client-cert-file", "synthetic.pem"}), "mTLS client certificate and key files must be configured together")
}
