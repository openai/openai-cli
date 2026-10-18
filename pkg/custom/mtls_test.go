package custom

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mtlstest "github.com/openai/openai-cli/internal/testutil/mtls"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestMTLSClientFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		envName string
	}{
		{name: mtlsClientCertFileFlag, envName: mtlsClientCertFileEnv},
		{name: mtlsClientKeyFileFlag, envName: mtlsClientKeyFileEnv},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flag := findCommandFlag(t, tt.name)
			wrapped, ok := flag.(interface{ CLIStringFlag() *cli.StringFlag })
			require.True(t, ok)
			stringFlag := wrapped.CLIStringFlag()
			assert.True(t, stringFlag.TakesFile)
			assert.Equal(t, []string{tt.envName}, stringFlag.GetEnvVars())
		})
	}
}

func TestCLIUsesMTLSClientCertificateChain(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-api-key")
	pki := mtlstest.NewPKI(t)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(pki.RootPEM))

	type requestDetails struct {
		authorization string
		chainLength   int
	}
	requests := make(chan requestDetails, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chainLength := 0
		if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 {
			chainLength = len(r.TLS.VerifiedChains[0])
		}
		requests <- requestDetails{
			authorization: r.Header.Get("Authorization"),
			chainLength:   chainLength,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAs,
		MinVersion: tls.VersionTLS12,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	configureTestServerTrust(t, server)

	certFile, keyFile := mtlstest.WriteClientFiles(t, pki.ClientChainPEM, pki.ClientKeyPEM)
	require.NoError(t, runMTLSTestCommand(t, server.URL, certFile, keyFile))

	select {
	case request := <-requests:
		assert.Equal(t, "Bearer test-api-key", request.authorization)
		assert.Equal(t, 3, request.chainLength)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the CLI request")
	}
}

func TestCLIUsesMTLSClientCertificateWithUnrecognizedCAHint(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-api-key")
	pki := mtlstest.NewPKI(t)

	requests := make(chan int, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peerCertificateCount := 0
		if r.TLS != nil {
			peerCertificateCount = len(r.TLS.PeerCertificates)
		}
		requests <- peerCertificateCount
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{
		ClientAuth: tls.RequireAnyClientCert,
		MinVersion: tls.VersionTLS12,
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	// Advertise an unrelated acceptable CA name, as some mTLS endpoints do.
	// The explicitly configured certificate must still be presented.
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(server.Certificate())
	server.TLS.ClientCAs = clientCAs
	configureTestServerTrust(t, server)

	certFile, keyFile := mtlstest.WriteClientFiles(t, pki.ClientChainPEM, pki.ClientKeyPEM)
	require.NoError(t, runMTLSTestCommand(t, server.URL, certFile, keyFile))

	select {
	case peerCertificateCount := <-requests:
		assert.Equal(t, 2, peerCertificateCount)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the CLI request")
	}
}

func TestCLIUsesMTLSFilesFromEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-api-key")
	pki := mtlstest.NewPKI(t)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(pki.RootPEM))

	requests := make(chan int, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peerCertificateCount := 0
		if r.TLS != nil {
			peerCertificateCount = len(r.TLS.PeerCertificates)
		}
		requests <- peerCertificateCount
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAs,
		MinVersion: tls.VersionTLS12,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	configureTestServerTrust(t, server)

	certFile, keyFile := mtlstest.WriteClientFiles(t, pki.ClientChainPEM, pki.ClientKeyPEM)
	t.Setenv(mtlsClientCertFileEnv, certFile)
	t.Setenv(mtlsClientKeyFileEnv, keyFile)
	require.NoError(t, runMTLSTestCommandWithArgs(t, server.URL))

	select {
	case peerCertificateCount := <-requests:
		assert.Equal(t, 2, peerCertificateCount)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the CLI request")
	}
}

func TestCLIRejectsIncompleteMTLSClientChain(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-api-key")
	pki := mtlstest.NewPKI(t)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(pki.RootPEM))

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server handler must not run when the client certificate chain cannot be verified")
		w.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCAs,
		MinVersion: tls.VersionTLS12,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	configureTestServerTrust(t, server)

	certFile, keyFile := mtlstest.WriteClientFiles(t, pki.ClientLeafPEM, pki.ClientKeyPEM)
	require.Error(t, runMTLSTestCommand(t, server.URL, certFile, keyFile))
}

func findCommandFlag(t *testing.T, name string) cli.Flag {
	t.Helper()

	for _, flag := range mtlsClientFlags() {
		if flag.Names()[0] == name {
			return flag
		}
	}

	t.Fatalf("flag %q was not found", name)
	return nil
}

func runMTLSTestCommand(t *testing.T, baseURL, certFile, keyFile string) error {
	t.Helper()

	return runMTLSTestCommandWithArgs(
		t,
		baseURL,
		"--mtls-client-cert-file", certFile,
		"--mtls-client-key-file", keyFile,
	)
}

func runMTLSTestCommandWithArgs(t *testing.T, baseURL string, args ...string) error {
	t.Helper()

	command := &cli.Command{
		Name: "openai-test",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "base-url"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			client := openai.NewClient(GetDefaultRequestOptions(cmd)...)
			_, err := client.Models.List(ctx)
			return err
		},
	}
	ConfigureCommand(command)
	commandArgs := append(
		[]string{command.Name, "--base-url", baseURL},
		args...,
	)
	return command.Run(context.Background(), commandArgs)
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
