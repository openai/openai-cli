package request

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

type defaultOptionsTransport func(*http.Request) (*http.Response, error)

func (transport defaultOptionsTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestDefaultOptionsPreserveOverridesMetadataAndMiddleware(t *testing.T) {
	for name, value := range map[string]string{
		"OPENAI_API_KEY":        "synthetic-env-key",
		"OPENAI_ADMIN_KEY":      "synthetic-env-admin",
		"OPENAI_ORG_ID":         "org-env",
		"OPENAI_PROJECT_ID":     "proj-env",
		"OPENAI_WEBHOOK_SECRET": "synthetic-env-webhook",
		"OPENAI_BASE_URL":       "https://env.invalid/v1",
		"OPENAI_CUSTOM_HEADERS": "",
	} {
		t.Setenv(name, value)
	}
	for _, test := range []struct {
		name, host, key, organization, project string
		args                                   []string
	}{
		{
			name: "SDK environment with endpoint project", host: "env.invalid", key: "synthetic-env-key", organization: "org-env", project: "proj-env",
			args: []string{"models", "list", "--project=proj-body"},
		},
		{
			name: "explicit root overrides", host: "cli.invalid", key: "synthetic-cli-key", organization: "org-cli", project: "proj-cli",
			args: []string{"--api-key=synthetic-cli-key", "--admin-api-key=synthetic-cli-admin", "--organization=org-cli", "--project=proj-cli", "--webhook-secret=synthetic-cli-webhook", "--base-url=https://cli.invalid/v1", "models", "list", "--project=proj-body"},
		},
		{
			name: "explicit empty credentials keep URL fallback", host: "env.invalid",
			args: []string{"--api-key=", "--admin-api-key=", "--organization=", "--project=", "--webhook-secret=", "--base-url=", "models", "list", "--project=proj-body"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var events []string
			var headers http.Header
			var host string
			transport := defaultOptionsTransport(func(request *http.Request) (*http.Response, error) {
				events = append(events, "transport")
				headers, host = request.Header.Clone(), request.URL.Host
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"object":"list","data":[]}`)),
					Request:    request,
				}, nil
			})
			middleware := func(name string) option.Middleware {
				return func(request *http.Request, next option.MiddlewareNext) (*http.Response, error) {
					events = append(events, name+" request")
					response, err := next(request)
					events = append(events, name+" response")
					return response, err
				}
			}
			root := &cli.Command{
				Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				Flags: []cli.Flag{
					&requestflag.Flag[string]{Name: "api-key"},
					&requestflag.Flag[string]{Name: "admin-api-key"},
					&requestflag.Flag[string]{Name: "organization"},
					&requestflag.Flag[string]{Name: "project"},
					&requestflag.Flag[string]{Name: "webhook-secret"},
					&cli.StringFlag{Name: "base-url"},
				},
				Metadata: map[string]any{MTLSHTTPClientMetadata: &http.Client{Transport: transport}},
				Commands: []*cli.Command{{Name: "models", Commands: []*cli.Command{{
					Name:  "list",
					Flags: []cli.Flag{&requestflag.Flag[string]{Name: "project", BodyPath: "project"}},
					Action: func(ctx context.Context, command *cli.Command) error {
						client := openai.NewClient(DefaultOptions(command, "synthetic-version", middleware("first"), middleware("second"))...)
						_, err := client.Models.List(ctx)
						return err
					},
				}}}},
			}
			ConfigureRootFlags(root)

			require.NoError(t, root.Run(t.Context(), append([]string{"openai"}, test.args...)))

			require.Equal(t, test.host, host)
			wantAuthorization := ""
			if test.key != "" {
				wantAuthorization = "Bearer " + test.key
			}
			require.Equal(t, wantAuthorization, headers.Get("Authorization"))
			require.Equal(t, test.organization, headers.Get("OpenAI-Organization"))
			require.Equal(t, test.project, headers.Get("OpenAI-Project"))
			require.Equal(t, "OpenAI/CLI synthetic-version", headers.Get("User-Agent"))
			require.Equal(t, "cli", headers.Get("X-Stainless-Lang"))
			require.Equal(t, "synthetic-version", headers.Get("X-Stainless-Package-Version"))
			require.Equal(t, "cli", headers.Get("X-Stainless-Runtime"))
			require.Equal(t, "openai models list", headers.Get("X-Stainless-CLI-Command"))
			require.Equal(t, []string{"first request", "second request", "transport", "second response", "first response"}, events)
		})
	}
}
