package request

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	mtlstest "github.com/openai/openai-cli/internal/testutil/mtls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMTLSHTTPClient(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		t.Parallel()

		client, err := NewMTLSHTTPClient("", "", "")
		require.NoError(t, err)
		assert.Nil(t, client)
	})

	t.Run("requires certificate and key together", func(t *testing.T) {
		t.Parallel()

		_, err := NewMTLSHTTPClient("certificate.pem", "", "")
		require.EqualError(t, err, "mTLS client certificate and key files must be configured together")

		_, err = NewMTLSHTTPClient("", "key.pem", "")
		require.EqualError(t, err, "mTLS client certificate and key files must be configured together")
	})

	t.Run("requires an explicit HTTPS base URL", func(t *testing.T) {
		t.Parallel()

		_, err := NewMTLSHTTPClient("certificate.pem", "key.pem", "")
		require.EqualError(t, err, "mTLS requires an explicit HTTPS base URL")

		_, err = NewMTLSHTTPClient("certificate.pem", "key.pem", "http://api.example.test")
		require.EqualError(t, err, "mTLS requires an explicit HTTPS base URL")

		_, err = NewMTLSHTTPClient("certificate.pem", "key.pem", "https:///v1")
		require.EqualError(t, err, "mTLS requires an explicit HTTPS base URL")
	})

	t.Run("does not expose supplied values in file errors", func(t *testing.T) {
		t.Parallel()

		inlineCertificate := "-----BEGIN CERTIFICATE-----\nsensitive-certificate-value\n-----END CERTIFICATE-----"
		_, err := NewMTLSHTTPClient(inlineCertificate, "key.pem", "https://api.example.test")
		require.EqualError(t, err, "mTLS client certificate file does not exist")
		assert.NotContains(t, err.Error(), inlineCertificate)

		certFile := filepath.Join(t.TempDir(), "certificate.pem")
		require.NoError(t, os.WriteFile(certFile, []byte("not a certificate"), 0o600))
		inlineKey := "-----BEGIN PRIVATE KEY-----\nsensitive-private-key-value\n-----END PRIVATE KEY-----"
		_, err = NewMTLSHTTPClient(certFile, inlineKey, "https://api.example.test")
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

		_, err := NewMTLSHTTPClient(certFile, keyFile, "https://api.example.test")
		require.EqualError(t, err, "mTLS client certificate and key must be a valid matching PEM pair")
		assert.NotContains(t, err.Error(), certContents)
		assert.NotContains(t, err.Error(), keyContents)
	})
}

func TestNewMTLSHTTPClientPreservesDefaultTransport(t *testing.T) {
	pki := mtlstest.NewPKI(t)
	certFile, keyFile := mtlstest.WriteClientFiles(t, pki.ClientChainPEM, pki.ClientKeyPEM)

	proxyURL, err := url.Parse("http://proxy.example.test:8443")
	require.NoError(t, err)
	httpsProxyURL, err := url.Parse("https://proxy.example.test:8443")
	require.NoError(t, err)
	rootPool := x509.NewCertPool()
	require.True(t, rootPool.AppendCertsFromPEM(pki.RootPEM))
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

	client, err := NewMTLSHTTPClient(certFile, keyFile, "https://api.example.test")
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
	sameOriginDefaultPortRedirect, err := http.NewRequest(http.MethodGet, "https://API.example.test:443/redirected", nil)
	require.NoError(t, err)
	require.NoError(t, client.CheckRedirect(sameOriginDefaultPortRedirect, []*http.Request{request}))

	crossOriginRedirect, err := http.NewRequest(http.MethodGet, "https://other.example.test", nil)
	require.NoError(t, err)
	assert.ErrorIs(t, client.CheckRedirect(crossOriginRedirect, []*http.Request{request}), http.ErrUseLastResponse)
	crossOriginPortRedirect, err := http.NewRequest(http.MethodGet, "https://api.example.test:8443", nil)
	require.NoError(t, err)
	assert.ErrorIs(t, client.CheckRedirect(crossOriginPortRedirect, []*http.Request{request}), http.ErrUseLastResponse)
}
