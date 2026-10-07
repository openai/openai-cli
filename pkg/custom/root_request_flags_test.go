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

func TestRootRequestFlagsPreserveMTLSPlacement(t *testing.T) {
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
