package images_test

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
)

func configureTestServerTrust(t *testing.T, server *httptest.Server) {
	t.Helper()
	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(server.Certificate())
	originalTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: serverRoots}}
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
}
