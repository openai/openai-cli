package request

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"
)

const (
	MTLSClientCertFileFlag = "mtls-client-cert-file"
	MTLSClientCertFileEnv  = "OPENAI_MTLS_CLIENT_CERT_FILE"
	MTLSClientKeyFileFlag  = "mtls-client-key-file"
	MTLSClientKeyFileEnv   = "OPENAI_MTLS_CLIENT_KEY_FILE"
	MTLSHTTPClientMetadata = "mtls-http-client"
)

// MTLSClientFlags returns certificate and key flags with their existing source rules.
func MTLSClientFlags() []cli.Flag {
	return []cli.Flag{
		&MTLSClientFlag{StringFlag: cli.StringFlag{
			Name:        MTLSClientCertFileFlag,
			Usage:       "Path to a PEM client certificate chain for mutual TLS (leaf first, followed by intermediates)",
			Sources:     cli.EnvVars(MTLSClientCertFileEnv),
			TakesFile:   true,
			HideDefault: true,
		}},
		&MTLSClientFlag{StringFlag: cli.StringFlag{
			Name:        MTLSClientKeyFileFlag,
			Usage:       "Path to the PEM private key for the mutual TLS client certificate",
			Sources:     cli.EnvVars(MTLSClientKeyFileEnv),
			TakesFile:   true,
			HideDefault: true,
		}},
	}
}

// ConfigureMTLS stores the root command's configured mTLS client for request consumers.
func ConfigureMTLS(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	if cmd.Metadata == nil {
		cmd.Metadata = map[string]any{}
	}

	certFile, _ := cmd.Value(MTLSClientCertFileFlag).(string)
	keyFile, _ := cmd.Value(MTLSClientKeyFileFlag).(string)
	baseURL := cmd.String("base-url")
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}

	client, err := NewMTLSHTTPClient(
		certFile,
		keyFile,
		baseURL,
	)
	if err != nil {
		delete(cmd.Metadata, MTLSHTTPClientMetadata)
		return ctx, err
	}

	if client == nil {
		delete(cmd.Metadata, MTLSHTTPClientMetadata)
	} else {
		cmd.Metadata[MTLSHTTPClientMetadata] = client
	}
	return ctx, nil
}
