package images_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/openai/openai-cli/internal/imagemodels"
	mtlstest "github.com/openai/openai-cli/internal/testutil/mtls"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/urfave/cli/v3"
)

func TestImageModelsErrorClassification(t *testing.T) {
	const private = "synthetic-private-detail\x1b]52;c;synthetic-private-control\a"
	for _, extraArguments := range []bool{false, true} {
		diagnostic := images.ModelsErrorForTest("openai", extraArguments, []imagemodels.Result{
			{Entry: imagemodels.Entry{ID: private}, Status: imagemodels.StatusUnknown, Failure: imagemodels.Failure(private), ShutdownDate: private},
			{Status: imagemodels.StatusUnknown, Failure: imagemodels.FailureAuthentication},
		})
		wrapped := fmt.Errorf("%s: %w", private, diagnostic)
		want := diagnostic.Error()
		if got := imageErrorMessage(nil, wrapped); got != want || strings.Contains(got, "synthetic-private-") {
			t.Errorf("typed diagnostic = %q, want controlled guidance %q", got, want)
		}
		for _, arbitrary := range []error{errors.New(want), errors.New(want + private), errors.New(private)} {
			if got := imageErrorMessage(nil, arbitrary); got != "The command could not be completed. Check your arguments with --help." {
				t.Errorf("untyped diagnostic became trusted: %q", got)
			}
		}
		for _, format := range []string{"text", "json"} {
			root := readableErrorTestCommand(t, "--format-error", format)
			var out strings.Builder
			if err := custom.ShowCommandError(root, wrapped, &out); err != nil {
				t.Fatal(err)
			}
			message := strings.TrimSpace(out.String())
			if format == "json" {
				var payload struct{ Message string }
				if err := json.Unmarshal([]byte(message), &payload); err != nil {
					t.Fatal(err)
				}
				message = payload.Message
			}
			if message != want {
				t.Errorf("%s diagnostic = %q, want %q", format, message, want)
			}
		}
		if got := imageErrorMessage(nil, errors.Join(wrapped, context.Canceled)); got != "Request canceled." {
			t.Errorf("cancellation lost precedence: %q", got)
		}
	}
}

func TestImageModelsUsesConfiguredMTLSClient(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-models-key")
	pki := mtlstest.NewPKI(t)
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(pki.RootPEM) {
		t.Fatal("could not configure synthetic client CA")
	}
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) != 3 {
			t.Error("model check lost the configured client certificate chain")
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-models-key" || r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1/models/") {
			t.Error("model check changed the configured credentials, method or base path")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":%q,"object":"model"}`, strings.TrimPrefix(r.URL.Path, "/v1/models/"))
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	configureTestServerTrust(t, server)
	certFile, keyFile := mtlstest.WriteClientFiles(t, pki.ClientChainPEM, pki.ClientKeyPEM)
	var out, stderr strings.Builder
	root := &cli.Command{
		Name: "openai", Writer: &out, ErrWriter: &stderr,
		Flags:    []cli.Flag{&cli.StringFlag{Name: "base-url"}, &cli.StringFlag{Name: "format", Value: "json"}},
		Commands: []*cli.Command{{Name: "images"}},
	}
	custom.ConfigureCommand(root)
	if err := root.Run(t.Context(), []string{"openai", "--base-url", server.URL + "/v1/",
		"--mtls-client-cert-file", certFile, "--mtls-client-key-file", keyFile, "images", "models"}); err != nil {
		t.Fatal(err)
	}
	var report images.TestImageModelsReport
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 9 || !report.Complete || len(report.Models) != 9 || stderr.Len() != 0 {
		t.Fatalf("mTLS discovery did not complete all model checks: requests=%d; report=%+v; stderr=%q", requests.Load(), report, stderr.String())
	}
}

func TestImageModelsDataFormatsAndExtraction(t *testing.T) {
	report := images.TestImageModelsReport{Source: "offline", Models: []imagemodels.Result{images.TestImageModelTestRow("gpt-image-2.5-sunburst", imagemodels.StatusNotChecked, "")}}
	for _, format := range []string{"auto", "text", "JSON", "jsonl", "raw", "yaml", "explore"} {
		t.Run(format, func(t *testing.T) {
			var out, stderr strings.Builder
			command := imageModelsOutputCommand(&out, &stderr, report)
			if err := command.Run(context.Background(), []string{"openai", "--format", format}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "gpt-image-2.5-sunburst") || !strings.Contains(out.String(), "not_checked") || strings.Contains(out.String(), "default") {
				t.Fatalf("incorrect report: %q", out.String())
			}
			if format == "auto" || format == "text" {
				if json.Valid([]byte(out.String())) || !strings.Contains(out.String(), "Source: offline") {
					t.Fatalf("expected text: %q", out.String())
				}
			} else if format != "yaml" && !json.Valid([]byte(out.String())) {
				t.Fatalf("invalid JSON: %q", out.String())
			}
			if format == "explore" && !strings.Contains(stderr.String(), "falling back to 'json'") {
				t.Fatalf("missing existing explore warning: %q", stderr.String())
			}
		})
	}
	for _, format := range []string{"auto", "text", "json", "jsonl", "raw", "yaml"} {
		for _, raw := range []bool{false, true} {
			var out, stderr strings.Builder
			command := imageModelsOutputCommand(&out, &stderr, report)
			args := []string{"openai", "--format", format, "--transform", "models.0.id"}
			if raw {
				args = append(args, "--raw-output")
			}
			if err := command.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			want := "gpt-image-2.5-sunburst\n"
			if !raw && (format == "auto" || format == "json" || format == "jsonl" || format == "raw") {
				want = "\"gpt-image-2.5-sunburst\"\n"
			}
			if out.String() != want {
				t.Errorf("format=%s raw=%v got %q want %q", format, raw, out.String(), want)
			}
		}
	}
	var stderr strings.Builder
	want := errors.New("synthetic data output failure")
	if err := imageModelsOutputCommand(errorSink{want}, &stderr, report).Run(context.Background(), []string{"openai", "--format", "json"}); !errors.Is(err, want) {
		t.Errorf("lost output failure: %v", err)
	}
}

func imageModelsOutputCommand(out, stderr io.Writer, report images.TestImageModelsReport) *cli.Command {
	return &cli.Command{Name: "openai", Writer: out, Flags: []cli.Flag{
		&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"},
	}, Action: func(ctx context.Context, command *cli.Command) error {
		return images.TestWriteImageModelsData(ctx, command, report, stderr, custom.ImageModelOptions())
	}}
}
