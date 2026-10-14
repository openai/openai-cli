package custom

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAdminSetupVerificationPreservesMTLS(t *testing.T) {
	const entered = "synthetic-entered-admin-key"
	root := adminCredentialsTestCommand(t, func(ctx context.Context, command *cli.Command) error {
		options, _, err := adminSetupRequestOptions(command)
		if err != nil {
			return err
		}
		return verifyAdminSetup(ctx, options, []byte(entered))
	})
	t.Setenv("OPENAI_ADMIN_KEY", "synthetic-environment-key")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "Authorization: Bearer synthetic-header-key\nX-Api-Key: synthetic-gateway-key")
	pki := newMTLSTestPKI(t)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(pki.rootPEM))
	type requestDetails struct {
		authorization, gateway, method, uri string
		chainLength                         int
	}
	requests := make(chan requestDetails, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		details := requestDetails{r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.Method, r.URL.RequestURI(), 0}
		if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 {
			details.chainLength = len(r.TLS.VerifiedChains[0])
		}
		requests <- details
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	configureTestServerTrust(t, server)
	certFile, keyFile := writeMTLSClientFiles(t, pki.clientChainPEM, pki.clientKeyPEM)
	ConfigureCommand(root)
	require.NoError(t, root.Run(t.Context(), []string{
		root.Name, "--base-url", server.URL, "--admin-api-key", "synthetic-flag-key",
		"--mtls-client-cert-file", certFile, "--mtls-client-key-file", keyFile,
		"admin", "organization", "projects", "list",
	}))
	select {
	case request := <-requests:
		require.Equal(t, "Bearer "+entered, request.authorization)
		require.Equal(t, "synthetic-gateway-key", request.gateway)
		require.Equal(t, 3, request.chainLength)
		require.Equal(t, "GET", request.method)
		require.Equal(t, "/organization/projects?limit=1", request.uri)
	default:
		t.Fatal("verification did not reach the mTLS server")
	}
}

func TestAdminSetupVerificationRejectsRedirectWithoutChangingClient(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/redirected")
		w.WriteHeader(http.StatusFound)
		_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
	}))
	t.Cleanup(server.Close)
	var originalRedirectCalls int
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		originalRedirectCalls++
		return nil
	}}
	root := adminCredentialsTestCommand(t, func(ctx context.Context, command *cli.Command) error {
		options, _, err := adminSetupRequestOptions(command)
		if err != nil {
			return err
		}
		return verifyAdminSetup(ctx, options, []byte("synthetic-entered-key"))
	})
	root.Metadata = map[string]interface{}{mtlsHTTPClientMetadata: client}
	root.ExitErrHandler = func(context.Context, *cli.Command, error) {}
	err := root.Run(t.Context(), []string{root.Name, "--base-url", server.URL, "admin:organization:projects", "list"})
	require.EqualError(t, err, "Verification stopped at a redirect. Check the configured base URL.")
	require.EqualValues(t, 1, calls.Load())
	require.Zero(t, originalRedirectCalls)
	require.NoError(t, client.CheckRedirect(nil, nil))
	require.Equal(t, 1, originalRedirectCalls)
}
