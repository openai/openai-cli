package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

const (
	mtlsClientCertFileFlag = "mtls-client-cert-file"
	mtlsClientCertFileEnv  = "OPENAI_MTLS_CLIENT_CERT_FILE"
	mtlsClientKeyFileFlag  = "mtls-client-key-file"
	mtlsClientKeyFileEnv   = "OPENAI_MTLS_CLIENT_KEY_FILE"
	mtlsHTTPClientMetadata = "mtls-http-client"

	// Supplying a custom HTTP client bypasses openai-go's default transport
	// construction, so preserve its response-header timeout here.
	openAISDKResponseHeaderTimeout = 10 * time.Minute
)

func mtlsClientFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:      mtlsClientCertFileFlag,
			Usage:     "Path to a PEM client certificate chain for mutual TLS (leaf first, followed by intermediates)",
			Sources:   cli.EnvVars(mtlsClientCertFileEnv),
			TakesFile: true,
			OnlyOnce:  true,
		},
		&cli.StringFlag{
			Name:      mtlsClientKeyFileFlag,
			Usage:     "Path to the PEM private key for the mutual TLS client certificate",
			Sources:   cli.EnvVars(mtlsClientKeyFileEnv),
			TakesFile: true,
			OnlyOnce:  true,
		},
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

	client, err := newMTLSHTTPClient(
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

func newMTLSHTTPClient(certFile, keyFile, baseURL string) (*http.Client, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, errors.New("mTLS client certificate and key files must be configured together")
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") || endpoint.Host == "" {
		return nil, errors.New("mTLS requires an explicit HTTPS base URL")
	}

	certPEM, err := readMTLSFile("certificate", certFile)
	if err != nil {
		return nil, err
	}
	defer clear(certPEM)

	keyPEM, err := readMTLSFile("key", keyFile)
	if err != nil {
		return nil, err
	}
	defer clear(keyPEM)

	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, errors.New("mTLS client certificate and key must be a valid matching PEM pair")
	}

	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("mTLS requires the default HTTP transport to be an *http.Transport")
	}

	transport := baseTransport.Clone()
	transport.ResponseHeaderTimeout = openAISDKResponseHeaderTimeout
	if proxy := transport.Proxy; proxy != nil {
		transport.Proxy = func(request *http.Request) (*url.URL, error) {
			proxyURL, err := proxy(request)
			if err != nil || proxyURL == nil {
				return proxyURL, err
			}
			if strings.EqualFold(proxyURL.Scheme, "https") {
				return nil, errors.New("mTLS does not support HTTPS proxies")
			}
			return proxyURL, nil
		}
	}

	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	// The user explicitly selected this pair, so present it whenever the endpoint
	// requests client authentication instead of filtering it against the server's
	// advertised CA names.
	tlsConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		return &certificate, nil
	}
	transport.TLSClientConfig = tlsConfig

	return &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if !strings.EqualFold(request.URL.Scheme, endpoint.Scheme) ||
				!strings.EqualFold(request.URL.Host, endpoint.Host) {
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}, nil
}

func readMTLSFile(kind, path string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if err == nil {
		return contents, nil
	}

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, errors.New("mTLS client " + kind + " file does not exist")
	case errors.Is(err, fs.ErrPermission):
		return nil, errors.New("mTLS client " + kind + " file is not readable")
	default:
		return nil, errors.New("could not read mTLS client " + kind + " file")
	}
}
