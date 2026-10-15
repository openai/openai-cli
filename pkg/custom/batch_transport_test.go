package custom

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

func TestBatchWorkflowUsesConfiguredMTLSTransport(t *testing.T) {
	for _, operation := range []string{"wait", "download"} {
		t.Run(operation, func(t *testing.T) {
			pki := newMTLSTestPKI(t)
			clientRoots := x509.NewCertPool()
			if !clientRoots.AppendCertsFromPEM(pki.rootPEM) {
				t.Fatal("could not load synthetic client root")
			}
			var paths []string
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer sk-fake-batches-mtls" || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) != 3 {
					t.Errorf("request did not preserve authenticated mTLS context: %s %s", r.Method, r.URL.Path)
				}
				if r.URL.Path == "/files/file_synthetic/content" {
					io.WriteString(w, "{\"custom_id\":\"synthetic\"}\n")
					return
				}
				status := "completed"
				if operation == "wait" && len(paths) == 1 {
					status = "in_progress"
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":"batch_synthetic","status":%q,"request_counts":{"total":1,"completed":1,"failed":0},"output_file_id":"file_synthetic"}`, status)
			}))
			server.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots, MinVersion: tls.VersionTLS12}
			server.StartTLS()
			defer server.Close()
			configureTestServerTrust(t, server)
			certFile, keyFile := writeMTLSClientFiles(t, pki.clientChainPEM, pki.clientKeyPEM)
			t.Setenv("OPENAI_API_KEY", "sk-fake-batches-mtls")
			for _, name := range []string{"OPENAI_ADMIN_KEY", "OPENAI_BASE_URL", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", "OPENAI_CUSTOM_HEADERS", "OPENAI_MTLS_CLIENT_CERT_FILE", "OPENAI_MTLS_CLIENT_KEY_FILE"} {
				t.Setenv(name, "")
			}
			var stdout bytes.Buffer
			root := &cli.Command{
				Name: "openai", Writer: &stdout, ErrWriter: io.Discard,
				Flags: []cli.Flag{&cli.StringFlag{Name: "base-url"}, &cli.StringFlag{Name: "format", Value: "json"}},
				Commands: []*cli.Command{{Name: "batches", Commands: []*cli.Command{{
					Name:  "retrieve",
					Flags: []cli.Flag{&requestflag.Flag[string]{Name: "batch-id", PathParam: "batch_id", Required: true}},
					Action: func(context.Context, *cli.Command) error {
						return fmt.Errorf("wait unexpectedly delegated to one-shot retrieval")
					},
				}}}},
			}
			ConfigureCommand(root)
			args := []string{"openai", "--base-url", server.URL, "--mtls-client-cert-file", certFile, "--mtls-client-key-file", keyFile, "batches"}
			if operation == "wait" {
				args = append(args, "retrieve", "batch_synthetic", "--wait", "--poll-interval", "1ms")
			} else {
				args = append(args, "download", "batch_synthetic", "--output", "-")
			}
			if err := root.Run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			want := []string{"/batches/batch_synthetic", "/batches/batch_synthetic"}
			if operation == "download" {
				want[1] = "/files/file_synthetic/content"
				if stdout.String() != "{\"custom_id\":\"synthetic\"}\n" {
					t.Fatalf("download bytes changed: %q", stdout.String())
				}
			}
			if !reflect.DeepEqual(paths, want) {
				t.Fatalf("unexpected requests: %v; want %v", paths, want)
			}
		})
	}
}
