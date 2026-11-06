package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainAdminCredentialsMTLSRequiresAdminAuthorization(t *testing.T) {
	certFile, keyFile := adminCredentialsMTLSFiles(t)
	for _, host := range []string{"api.openai.com", "mtls.api.openai.com"} {
		for _, test := range []struct {
			name      string
			env, args []string
			allowed   bool
		}{
			{name: "missing"},
			{name: "project key only", env: []string{"OPENAI_API_KEY=synthetic-project-key"}},
			{name: "empty admin overrides environment", env: []string{"OPENAI_ADMIN_KEY=synthetic-admin-key"}, args: []string{"--admin-api-key="}},
			{name: "empty authorization overrides key", env: []string{"OPENAI_ADMIN_KEY=synthetic-admin-key"}, args: []string{"-H", "Authorization:"}},
			{name: "admin key", args: []string{"--admin-api-key", "synthetic-admin-key"}, allowed: true},
			{name: "custom authorization", args: []string{"-H", "Authorization: Custom synthetic-token"}, allowed: true},
		} {
			t.Run(host+"/"+test.name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodConnect || r.Host != host+":443" {
						t.Errorf("proxy received %s %s, want CONNECT %s:443", r.Method, r.Host, host)
					}
					// Do not establish a tunnel or contact the requested host.
					w.WriteHeader(http.StatusForbidden)
				}))
				t.Cleanup(server.Close)
				args := []string{"openai", "--base-url", "https://" + host + "/v1",
					"--mtls-client-cert-file", certFile, "--mtls-client-key-file", keyFile,
					"admin", "organization", "projects", "list"}
				args = append(args, test.args...)
				got := runMainDispatchWithEnv(t, "bash", adminCredentialsProxyEnv(server.URL, test.env), args...)
				if got.code != 1 || got.stdout != "" {
					t.Fatalf("mTLS admin command = %+v, want exit 1 and empty stdout", got)
				}
				want := missingAdminCredentialsMessage
				if test.allowed {
					want = "Could not connect to the API. Check your connection, proxy, and --base-url setting."
					if requests.Load() == 0 {
						t.Error("effective admin authorization did not reach the request transport")
					}
				} else if count := requests.Load(); count != 0 {
					t.Errorf("missing admin authorization sent %d proxy requests, want 0", count)
				}
				if strings.TrimSpace(got.stderr) != want {
					t.Errorf("mTLS diagnostic = %q, want %q", got.stderr, want)
				}
				for _, private := range []string{certFile, keyFile, "synthetic-project-key", "synthetic-admin-key", "synthetic-token"} {
					if strings.Contains(got.stdout+got.stderr, private) {
						t.Error("mTLS diagnostic exposed a private path or synthetic credential")
					}
				}
			})
		}
	}
}

// Keep synthetic private-key material in restricted temporary files only.
func adminCredentialsMTLSFiles(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Synthetic admin mTLS test"},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(keyDER)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	defer clear(keyPEM)
	directory := t.TempDir()
	certFile, keyFile := filepath.Join(directory, "client.pem"), filepath.Join(directory, "client-key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
