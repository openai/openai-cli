package admin_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

func adminCredentialsTestCommand(t *testing.T, action cli.ActionFunc) *cli.Command {
	t.Helper()
	for _, name := range []string{
		"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_CUSTOM_HEADERS", "OPENAI_BASE_URL",
		requestconfig.MTLSClientCertFileEnv, requestconfig.MTLSClientKeyFileEnv,
	} {
		t.Setenv(name, "")
	}
	return &cli.Command{
		Name: "openai-test", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "api-key", Sources: cli.EnvVars("OPENAI_API_KEY")},
			&cli.StringFlag{Name: "admin-api-key", Sources: cli.EnvVars("OPENAI_ADMIN_KEY")},
			&cli.StringFlag{Name: "base-url"},
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "format-error", Value: "auto"},
			custom.NewRequestHeaderFlag(),
		},
		Commands: []*cli.Command{{
			Name: "admin:organization:projects", Category: "API RESOURCE",
			Commands: []*cli.Command{{Name: "list", Action: func(ctx context.Context, command *cli.Command) error {
				// Generated actions validate request input before checking credentials.
				if _, err := custom.FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, custom.EmptyBody, false); err != nil {
					return err
				}
				return action(ctx, command)
			}}},
		}},
	}
}

func configureTestServerTrust(t *testing.T, server *httptest.Server) {
	t.Helper()

	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(server.Certificate())
	originalTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: serverRoots},
	}
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
	})
}
