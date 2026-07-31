package cmd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

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
			stringFlag, ok := flag.(*cli.StringFlag)
			require.True(t, ok)
			assert.True(t, stringFlag.TakesFile)
			assert.Equal(t, []string{tt.envName}, stringFlag.GetEnvVars())
		})
	}
}

func TestNewMTLSHTTPClient(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		t.Parallel()

		client, err := newMTLSHTTPClient("", "", "")
		require.NoError(t, err)
		assert.Nil(t, client)
	})

	t.Run("requires certificate and key together", func(t *testing.T) {
		t.Parallel()

		_, err := newMTLSHTTPClient("certificate.pem", "", "")
		require.EqualError(t, err, "mTLS client certificate and key files must be configured together")

		_, err = newMTLSHTTPClient("", "key.pem", "")
		require.EqualError(t, err, "mTLS client certificate and key files must be configured together")
	})

	t.Run("requires an explicit HTTPS base URL", func(t *testing.T) {
		t.Parallel()

		_, err := newMTLSHTTPClient("certificate.pem", "key.pem", "")
		require.EqualError(t, err, "mTLS requires an explicit HTTPS base URL")

		_, err = newMTLSHTTPClient("certificate.pem", "key.pem", "http://api.example.test")
		require.EqualError(t, err, "mTLS requires an explicit HTTPS base URL")

		_, err = newMTLSHTTPClient("certificate.pem", "key.pem", "https:///v1")
		require.EqualError(t, err, "mTLS requires an explicit HTTPS base URL")
	})

	t.Run("does not expose supplied values in file errors", func(t *testing.T) {
		t.Parallel()

		inlineCertificate := "-----BEGIN CERTIFICATE-----\nsensitive-certificate-value\n-----END CERTIFICATE-----"
		_, err := newMTLSHTTPClient(inlineCertificate, "key.pem", "https://api.example.test")
		require.EqualError(t, err, "mTLS client certificate file does not exist")
		assert.NotContains(t, err.Error(), inlineCertificate)

		certFile := filepath.Join(t.TempDir(), "certificate.pem")
		require.NoError(t, os.WriteFile(certFile, []byte("not a certificate"), 0o600))
		inlineKey := "-----BEGIN PRIVATE KEY-----\nsensitive-private-key-value\n-----END PRIVATE KEY-----"
		_, err = newMTLSHTTPClient(certFile, inlineKey, "https://api.example.test")
		require.EqualError(t, err, "mTLS client key file does not exist")
		assert.NotContains(t, err.Error(), inlineKey)
	})

	t.Run("rejects an invalid pair without exposing contents", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		certFile := filepath.Join(dir, "certificate.pem")
		keyFile := filepath.Join(dir, "key.pem")
		certContents := "sensitive invalid certificate"
		keyContents := "sensitive invalid private key"
		require.NoError(t, os.WriteFile(certFile, []byte(certContents), 0o600))
		require.NoError(t, os.WriteFile(keyFile, []byte(keyContents), 0o600))

		_, err := newMTLSHTTPClient(certFile, keyFile, "https://api.example.test")
		require.EqualError(t, err, "mTLS client certificate and key must be a valid matching PEM pair")
		assert.NotContains(t, err.Error(), certContents)
		assert.NotContains(t, err.Error(), keyContents)
	})
}

func TestNewMTLSHTTPClientPreservesDefaultTransport(t *testing.T) {
	pki := newMTLSTestPKI(t)
	certFile, keyFile := writeMTLSClientFiles(t, pki.clientChainPEM, pki.clientKeyPEM)

	proxyURL, err := url.Parse("http://proxy.example.test:8443")
	require.NoError(t, err)
	httpsProxyURL, err := url.Parse("https://proxy.example.test:8443")
	require.NoError(t, err)
	rootPool := x509.NewCertPool()
	require.True(t, rootPool.AppendCertsFromPEM(pki.rootPEM))
	baseTransport := &http.Transport{
		Proxy: func(request *http.Request) (*url.URL, error) {
			if request.URL.Host == "https-proxy-target.example.test" {
				return httpsProxyURL, nil
			}
			return proxyURL, nil
		},
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    rootPool,
			ServerName: "api.example.test",
		},
	}

	originalTransport := http.DefaultTransport
	http.DefaultTransport = baseTransport
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
	})

	client, err := newMTLSHTTPClient(certFile, keyFile, "https://api.example.test")
	require.NoError(t, err)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)

	assert.NotSame(t, baseTransport, transport)
	assert.NotSame(t, baseTransport.TLSClientConfig, transport.TLSClientConfig)
	assert.Empty(t, baseTransport.TLSClientConfig.Certificates)
	assert.Empty(t, transport.TLSClientConfig.Certificates)
	require.NotNil(t, transport.TLSClientConfig.GetClientCertificate)
	selectedCertificate, err := transport.TLSClientConfig.GetClientCertificate(&tls.CertificateRequestInfo{})
	require.NoError(t, err)
	assert.Len(t, selectedCertificate.Certificate, 2)
	assert.Same(t, rootPool, transport.TLSClientConfig.RootCAs)
	assert.Equal(t, uint16(tls.VersionTLS13), transport.TLSClientConfig.MinVersion)
	assert.Equal(t, "api.example.test", transport.TLSClientConfig.ServerName)
	assert.Equal(t, openAISDKResponseHeaderTimeout, transport.ResponseHeaderTimeout)

	request, err := http.NewRequest(http.MethodGet, "https://api.example.test", nil)
	require.NoError(t, err)
	actualProxyURL, err := transport.Proxy(request)
	require.NoError(t, err)
	assert.Equal(t, proxyURL, actualProxyURL)

	httpsProxyRequest, err := http.NewRequest(http.MethodGet, "https://https-proxy-target.example.test", nil)
	require.NoError(t, err)
	actualProxyURL, err = transport.Proxy(httpsProxyRequest)
	require.EqualError(t, err, "mTLS does not support HTTPS proxies")
	assert.Nil(t, actualProxyURL)

	sameOriginRedirect, err := http.NewRequest(http.MethodGet, "https://api.example.test/redirected", nil)
	require.NoError(t, err)
	require.NoError(t, client.CheckRedirect(sameOriginRedirect, []*http.Request{request}))

	crossOriginRedirect, err := http.NewRequest(http.MethodGet, "https://other.example.test", nil)
	require.NoError(t, err)
	assert.ErrorIs(t, client.CheckRedirect(crossOriginRedirect, []*http.Request{request}), http.ErrUseLastResponse)
}

func TestCLIUsesMTLSClientCertificateChain(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-api-key")
	pki := newMTLSTestPKI(t)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(pki.rootPEM))

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

	certFile, keyFile := writeMTLSClientFiles(t, pki.clientChainPEM, pki.clientKeyPEM)
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
	pki := newMTLSTestPKI(t)

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

	certFile, keyFile := writeMTLSClientFiles(t, pki.clientChainPEM, pki.clientKeyPEM)
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
	pki := newMTLSTestPKI(t)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(pki.rootPEM))

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

	certFile, keyFile := writeMTLSClientFiles(t, pki.clientChainPEM, pki.clientKeyPEM)
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
	pki := newMTLSTestPKI(t)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(pki.rootPEM))

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

	certFile, keyFile := writeMTLSClientFiles(t, pki.clientLeafPEM, pki.clientKeyPEM)
	require.Error(t, runMTLSTestCommand(t, server.URL, certFile, keyFile))
}

func findCommandFlag(t *testing.T, name string) cli.Flag {
	t.Helper()

	for _, flag := range Command.Flags {
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
		Name:   "openai-test",
		Before: configureMTLS,
		Flags: append([]cli.Flag{
			&cli.StringFlag{Name: "base-url"},
		}, mtlsClientFlags()...),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			client := openai.NewClient(getDefaultRequestOptions(cmd)...)
			_, err := client.Models.List(ctx)
			return err
		},
	}
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

type mtlsTestPKI struct {
	rootPEM        []byte
	clientLeafPEM  []byte
	clientChainPEM []byte
	clientKeyPEM   []byte
}

func newMTLSTestPKI(t *testing.T) mtlsTestPKI {
	t.Helper()

	now := time.Now()
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mTLS test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, rootCertificate, rootKey := createTestCertificate(t, rootTemplate, rootTemplate, nil)

	intermediateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "mTLS test intermediate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	intermediateDER, intermediateCertificate, intermediateKey := createTestCertificate(
		t,
		intermediateTemplate,
		rootCertificate,
		rootKey,
	)

	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "mTLS test client"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, _, clientKey := createTestCertificate(
		t,
		clientTemplate,
		intermediateCertificate,
		intermediateKey,
	)

	clientKeyDER, err := x509.MarshalECPrivateKey(clientKey)
	require.NoError(t, err)
	clientLeafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})
	intermediatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediateDER})

	return mtlsTestPKI{
		rootPEM:        pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}),
		clientLeafPEM:  clientLeafPEM,
		clientChainPEM: append(clientLeafPEM, intermediatePEM...),
		clientKeyPEM:   pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKeyDER}),
	}
}

func createTestCertificate(
	t *testing.T,
	template *x509.Certificate,
	parent *x509.Certificate,
	parentKey *ecdsa.PrivateKey,
) ([]byte, *x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	if parentKey == nil {
		parentKey = key
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return der, certificate, key
}

func writeMTLSClientFiles(t *testing.T, certPEM, keyPEM []byte) (string, string) {
	t.Helper()

	dir := t.TempDir()
	certFile := filepath.Join(dir, "client-chain.pem")
	keyFile := filepath.Join(dir, "client-key.pem")
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	return certFile, keyFile
}
