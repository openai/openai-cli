package custom

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestRootRequestFlagsPreserveOwnershipAndMetadata(t *testing.T) {
	project := &requestflag.Flag[string]{Name: "project", Usage: "Project context", Aliases: []string{"p"}}
	apiKey := &requestflag.Flag[string]{Name: "api-key", Default: "synthetic-secret"}
	endpoint := &requestflag.Flag[string]{Name: "project", BodyPath: "project"}
	unrelated := &requestflag.Flag[string]{Name: "model"}
	format := &cli.StringFlag{Name: "format"}
	root := &cli.Command{
		Flags:    []cli.Flag{project, apiKey, unrelated, format},
		Commands: []*cli.Command{{Name: "create", Flags: []cli.Flag{endpoint}}},
	}
	configureRootRequestFlags(root)
	wrapped := root.Flags[0].(*rootRequestFlag)
	require.Same(t, project, wrapped.RequestFlag())
	require.Equal(t, []string{"project", "p"}, wrapped.Names())
	require.Equal(t, "Project context", wrapped.GetUsage())
	require.False(t, wrapped.IsLocal())
	require.False(t, root.Flags[1].(cli.DocGenerationFlag).IsDefaultVisible())
	require.True(t, endpoint.IsLocal())
	require.Same(t, endpoint, root.Commands[0].Flags[0])
	require.Same(t, unrelated, root.Flags[2])
	require.Same(t, format, root.Flags[3])
	configureRootRequestFlags(root)
	require.Same(t, wrapped, root.Flags[0])
}

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
								wrapped := command.Root().Flags[0].(*rootRequestFlag)
								require.Equal(t, tc.explicit, wrapped.explicitlySet)
								require.True(t, command.Root().IsSet(name))
								require.Equal(t, tc.want, command.Root().String(name))
								require.Equal(t, tc.want, command.String(name))
								return nil
							},
						}}}},
					}
					configureRootRequestFlags(root)
					require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, tc.args...)))
					require.True(t, called)
				})
			}
		})
	}
}

func TestRootRequestFlagPreParseResetsOnlyProvenance(t *testing.T) {
	wrapped := &rootRequestFlag{Flag: &requestflag.Flag[string]{Name: "project", Default: "default-project"}}
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
			explicit = append(explicit, command.Flags[0].(*rootRequestFlag).explicitlySet)
			return nil
		},
	}
	configureRootRequestFlags(root)
	require.NoError(t, root.Run(t.Context(), []string{"openai", "--project=synthetic-project"}))
	require.NoError(t, root.Run(t.Context(), []string{"openai"}))
	require.Equal(t, []bool{true, false}, explicit)
}

func TestRootRequestFlagsPreserveMTLSPlacement(t *testing.T) {
	t.Setenv(mtlsClientCertFileEnv, "/missing/synthetic-env-certificate")
	t.Setenv(mtlsClientKeyFileEnv, "/missing/synthetic-env-key")
	pki := newMTLSTestPKI(t)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(pki.rootPEM))
	requests := make(chan http.Header, 3)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			t.Error("request requires a verified client certificate")
		}
		requests <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	configureTestServerTrust(t, server)
	certFile, keyFile := writeMTLSClientFiles(t, pki.clientChainPEM, pki.clientKeyPEM)
	flags := []string{"--project=proj-mtls", "--api-key=synthetic-mtls-key", "--base-url=" + server.URL,
		"--mtls-client-cert-file=" + certFile, "--mtls-client-key-file=" + keyFile}
	for _, position := range []int{0, 1, 2} {
		root := &cli.Command{
			Name: "openai",
			Flags: []cli.Flag{
				&requestflag.Flag[string]{Name: "project"},
				&requestflag.Flag[string]{Name: "api-key"},
				&cli.StringFlag{Name: "base-url"},
			},
			Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{
				Name: "list",
				Action: func(ctx context.Context, command *cli.Command) error {
					client := openai.NewClient(GetDefaultRequestOptions(command)...)
					_, err := client.Models.List(ctx)
					return err
				},
			}}}},
		}
		configureRootRequestFlags(root)
		ConfigureCommand(root)
		route := []string{"models", "list"}
		args := append([]string{"openai"}, route[:position]...)
		args = append(args, flags...)
		args = append(args, route[position:]...)
		require.NoError(t, root.Run(t.Context(), args), "position %d", position)
		select {
		case headers := <-requests:
			require.Equal(t, "proj-mtls", headers.Get("OpenAI-Project"))
			require.Equal(t, "Bearer synthetic-mtls-key", headers.Get("Authorization"))
		default:
			t.Fatal("mTLS request was not received")
		}
	}
}

func TestRootRequestFlagsRetainSourcesAliasesAndValidation(t *testing.T) {
	t.Setenv("OPENAI_TEST_PROJECT", "env-project")
	rejected := errors.New("rejected project")
	for _, tc := range []struct {
		name string
		args []string
		want string
		err  error
	}{
		{name: "environment", args: []string{"models", "list"}, want: "env-project"},
		{name: "alias", args: []string{"models", "list", "-p", "explicit"}, want: "explicit"},
		{name: "empty", args: []string{"models", "--project=", "list"}, want: ""},
		{name: "duplicate", args: []string{"--project=first", "models", "--project=second", "list", "-p", "last"}, want: "last"},
		{name: "validation", args: []string{"models", "list", "--project=invalid"}, err: rejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			root := &cli.Command{
				Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{&requestflag.Flag[string]{
					Name: "project", Aliases: []string{"p"}, Sources: cli.EnvVars("OPENAI_TEST_PROJECT"),
					Validator: func(value string) error {
						if value == "invalid" {
							return rejected
						}
						return nil
					},
				}},
				Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{
					Name: "list",
					Action: func(_ context.Context, command *cli.Command) error {
						called = true
						require.True(t, command.Root().IsSet("project"))
						require.Equal(t, tc.want, command.String("project"))
						require.Equal(t, tc.want, command.Root().String("project"))
						return nil
					},
				}}}},
			}
			configureRootRequestFlags(root)
			err := root.Run(t.Context(), append([]string{"openai"}, tc.args...))
			if tc.err != nil {
				require.ErrorContains(t, err, tc.err.Error())
				require.False(t, called)
			} else {
				require.NoError(t, err)
				require.True(t, called)
			}
		})
	}
}
