package custom

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestMTLSClientFlagEnvironmentDoesNotConsumeExplicitValue(t *testing.T) {
	t.Setenv("OPENAI_TEST_MTLS_FILE", "environment.pem")
	flag := &mtlsClientFlag{StringFlag: cli.StringFlag{
		Name: "certificate", Sources: cli.EnvVars("OPENAI_TEST_MTLS_FILE"), TakesFile: true,
	}}
	require.NoError(t, flag.PreParse())
	require.NoError(t, flag.PostParse())
	require.Equal(t, "environment.pem", flag.Get())
	require.True(t, flag.IsSet())
	require.NoError(t, flag.Set("certificate", ""))
	require.NoError(t, flag.PostParse())
	require.Equal(t, "", flag.Get())
	require.EqualError(t, flag.Set("certificate", "duplicate.pem"), "can't duplicate this flag")
	require.Equal(t, "", flag.Get())
	require.Same(t, &flag.StringFlag, flag.CLIStringFlag())
	require.True(t, flag.TakesFile)
	require.Equal(t, []string{"OPENAI_TEST_MTLS_FILE"}, flag.GetEnvVars())
	require.NoError(t, flag.PreParse())
	require.NoError(t, flag.Set("certificate", "next-run.pem"))
	require.Equal(t, "next-run.pem", flag.Get())
	require.EqualError(t, flag.Set("certificate", "duplicate.pem"), "can't duplicate this flag")
}

func TestMTLSClientFlagPreParsePreservesCurrentDefinition(t *testing.T) {
	var destination string
	var validated []string
	flag := &mtlsClientFlag{StringFlag: cli.StringFlag{
		Name: "certificate", Category: "Synthetic category", DefaultText: "Synthetic default",
		HideDefault: true, Usage: "Synthetic usage", Sources: cli.EnvVars("OPENAI_TEST_MTLS_METADATA"),
		Required: true, Hidden: true, Deprecated: "Synthetic deprecation", Local: true,
		Value: "synthetic-default.pem", Destination: &destination, Aliases: []string{"cert"}, TakesFile: true,
		Action: func(context.Context, *cli.Command, string) error { return nil },
		Config: cli.StringConfig{TrimSpace: true}, OnlyOnce: true,
		Validator: func(value string) error { validated = append(validated, value); return nil }, ValidateDefaults: true,
	}}
	definition := flag.CLIStringFlag()
	require.NoError(t, flag.PreParse())
	require.NoError(t, flag.Set("certificate", "  synthetic-explicit.pem  "))
	require.Equal(t, "synthetic-explicit.pem", destination)
	definition.Usage, definition.Value = "Updated synthetic usage", "synthetic-updated.pem"
	before := reflect.ValueOf(*definition)
	require.NoError(t, flag.PreParse())
	require.Same(t, definition, flag.CLIStringFlag())
	after := reflect.ValueOf(*definition)
	for index := 0; index < before.NumField(); index++ {
		field := before.Type().Field(index)
		if !field.IsExported() {
			continue
		}
		if field.Type.Kind() == reflect.Func {
			require.Equal(t, before.Field(index).Pointer(), after.Field(index).Pointer(), field.Name)
		} else {
			require.Equal(t, before.Field(index).Interface(), after.Field(index).Interface(), field.Name)
		}
	}
	require.False(t, flag.IsSet())
	require.False(t, flag.explicit)
	require.Zero(t, flag.Count())
	require.Equal(t, "synthetic-updated.pem", destination)
	require.Equal(t, []string{"synthetic-default.pem", "synthetic-explicit.pem", "synthetic-updated.pem"}, validated)
	require.NoError(t, flag.Set("cert", "  synthetic-next.pem  "))
	require.Equal(t, "synthetic-next.pem", destination)
	require.EqualError(t, flag.Set("cert", "duplicate.pem"), "can't duplicate this flag")
}

func TestMTLSClientFlagsReuseEnvironmentForTLS(t *testing.T) {
	projectLinkTestEnvironment(t)
	authStatusTestEnvironment(t, map[string]string{
		"OPENAI_API_KEY": "synthetic-mtls-reuse-api", "OPENAI_BASE_URL": "https://api.openai.com/v1/",
		"OPENAI_ORG_ID": "org_mtls_reuse", "OPENAI_PROJECT_ID": "proj_mtls_reuse",
	})
	first, second := newMTLSTestPKI(t), newMTLSTestPKI(t)
	firstCert, firstKey := writeMTLSClientFiles(t, first.clientChainPEM, first.clientKeyPEM)
	secondCert, secondKey := writeMTLSClientFiles(t, second.clientChainPEM, second.clientKeyPEM)
	firstLeaf, _ := pem.Decode(first.clientLeafPEM)
	secondLeaf, _ := pem.Decode(second.clientLeafPEM)
	require.NotNil(t, firstLeaf)
	require.NotNil(t, secondLeaf)
	clientRoots := x509.NewCertPool()
	require.True(t, clientRoots.AppendCertsFromPEM(first.rootPEM))
	require.True(t, clientRoots.AppendCertsFromPEM(second.rootPEM))
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(102), DNSNames: []string{"api.openai.com"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, serverCertificate, serverKey := createTestCertificate(t, template, template, nil)
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	require.NoError(t, err)
	serverPair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyDER}))
	require.NoError(t, err)
	clear(serverKeyDER)
	type observation struct {
		path, serverName string
		headers          http.Header
		leaf             []byte
		verifiedChains   int
	}
	observed := make(chan observation, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		item := observation{path: request.URL.Path, serverName: request.TLS.ServerName,
			headers: request.Header.Clone(), verifiedChains: len(request.TLS.VerifiedChains)}
		if len(request.TLS.PeerCertificates) != 0 {
			item.leaf = bytes.Clone(request.TLS.PeerCertificates[0].Raw)
		}
		select {
		case observed <- item:
		default:
			t.Error("unexpected extra request")
		}
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v1/me" {
			_, _ = io.WriteString(w, `{"object":"user","id":"user-synthetic-mtls","name":null,"email":null}`)
		} else {
			_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
		}
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Config.ReadHeaderTimeout, server.Config.WriteTimeout, server.Config.IdleTimeout = 2*time.Second, 2*time.Second, 2*time.Second
	server.TLS = &tls.Config{Certificates: []tls.Certificate{serverPair}, ClientCAs: clientRoots,
		ClientAuth: tls.VerifyClientCertIfGiven, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(serverCertificate)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: serverRoots, MinVersion: tls.VersionTLS12}}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "api.openai.com:443" {
			return nil, errors.New("synthetic TLS fixture refuses any other destination")
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	original := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = original })
	for _, mode := range []string{"SDK", "identity"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			root := authStatusTestRoot(&output)
			root.Commands = append(root.Commands, &cli.Command{Name: "models", Action: func(ctx context.Context, command *cli.Command) error {
				if client, ok := command.Root().Metadata[mtlsHTTPClientMetadata].(*http.Client); ok {
					defer client.CloseIdleConnections()
				}
				defer transport.CloseIdleConnections()
				client := openai.NewClient(GetDefaultRequestOptions(command)...)
				_, err := client.Models.List(ctx)
				return err
			}})
			ConfigureCommand(root)
			for _, tc := range []struct {
				name, cert, key string
				args            []string
				leaf            []byte
				removed         bool
			}{
				{name: "initial environment", cert: firstCert, key: firstKey, leaf: firstLeaf.Bytes},
				{name: "reused environment", cert: firstCert, key: firstKey, leaf: firstLeaf.Bytes},
				{name: "changed environment", cert: secondCert, key: secondKey, leaf: secondLeaf.Bytes},
				{name: "explicit override", cert: secondCert, key: secondKey, leaf: firstLeaf.Bytes,
					args: []string{"--mtls-client-cert-file=" + firstCert, "--mtls-client-key-file=" + firstKey}},
				{name: "explicit empty", cert: secondCert, key: secondKey,
					args: []string{"--mtls-client-cert-file=", "--mtls-client-key-file="}},
				{name: "restored environment", cert: secondCert, key: secondKey, leaf: secondLeaf.Bytes},
				{name: "empty environment"},
				{name: "removed environment", removed: true},
				{name: "final environment", cert: firstCert, key: firstKey, leaf: firstLeaf.Bytes},
			} {
				t.Setenv(mtlsClientCertFileEnv, tc.cert)
				t.Setenv(mtlsClientKeyFileEnv, tc.key)
				if tc.removed {
					require.NoError(t, os.Unsetenv(mtlsClientCertFileEnv))
					require.NoError(t, os.Unsetenv(mtlsClientKeyFileEnv))
				}
				output.Reset()
				args := []string{"openai", "models"}
				path := "/v1/models"
				if mode == "identity" {
					args = []string{"openai", "auth", "status", "--verify", "--credential-kind=api", "--format=json"}
					path = "/v1/me"
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				err := root.Run(ctx, append(args, tc.args...))
				cancel()
				require.NoError(t, err, tc.name)
				select {
				case item := <-observed:
					require.Equal(t, path, item.path, tc.name)
					require.Equal(t, "api.openai.com", item.serverName, tc.name)
					require.Equal(t, tc.leaf, item.leaf, tc.name)
					require.Equal(t, len(tc.leaf) != 0, item.verifiedChains != 0, tc.name)
					require.Equal(t, "Bearer synthetic-mtls-reuse-api", item.headers.Get("Authorization"), tc.name)
					require.Equal(t, "org_mtls_reuse", item.headers.Get("OpenAI-Organization"), tc.name)
					require.Equal(t, "proj_mtls_reuse", item.headers.Get("OpenAI-Project"), tc.name)
				default:
					t.Fatalf("%s did not send a request", tc.name)
				}
				if mode == "identity" {
					var report authStatusReport
					require.NoError(t, json.Unmarshal(output.Bytes(), &report), tc.name)
					require.True(t, report.Verified, tc.name)
					require.Equal(t, "resolved", report.APIIdentity.Status, tc.name)
					require.Equal(t, "user-synthetic-mtls", report.APIIdentity.ID, tc.name)
				}
			}
		})
	}
}
