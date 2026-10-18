package custom

import (
	"context"
	"os"

	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

const (
	mtlsClientCertFileFlag = "mtls-client-cert-file"
	mtlsClientCertFileEnv  = "OPENAI_MTLS_CLIENT_CERT_FILE"
	mtlsClientKeyFileFlag  = "mtls-client-key-file"
	mtlsClientKeyFileEnv   = "OPENAI_MTLS_CLIENT_KEY_FILE"
	mtlsHTTPClientMetadata = "mtls-http-client"
)

func mtlsClientFlags() []cli.Flag {
	return []cli.Flag{
		&mtlsClientFlag{StringFlag: cli.StringFlag{
			Name:        mtlsClientCertFileFlag,
			Usage:       "Path to a PEM client certificate chain for mutual TLS (leaf first, followed by intermediates)",
			Sources:     cli.EnvVars(mtlsClientCertFileEnv),
			TakesFile:   true,
			HideDefault: true,
		}},
		&mtlsClientFlag{StringFlag: cli.StringFlag{
			Name:        mtlsClientKeyFileFlag,
			Usage:       "Path to the PEM private key for the mutual TLS client certificate",
			Sources:     cli.EnvVars(mtlsClientKeyFileEnv),
			TakesFile:   true,
			HideDefault: true,
		}},
	}
}

func configureMTLS(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	if cmd.Metadata == nil {
		cmd.Metadata = map[string]any{}
	}

	certFile, _ := cmd.Value(mtlsClientCertFileFlag).(string)
	keyFile, _ := cmd.Value(mtlsClientKeyFileFlag).(string)
	baseURL := cmd.String("base-url")
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}

	client, err := requestconfig.NewMTLSHTTPClient(
		certFile,
		keyFile,
		baseURL,
	)
	if err != nil {
		delete(cmd.Metadata, mtlsHTTPClientMetadata)
		return ctx, err
	}

	if client == nil {
		delete(cmd.Metadata, mtlsHTTPClientMetadata)
	} else {
		cmd.Metadata[mtlsHTTPClientMetadata] = client
	}
	return ctx, nil
}
