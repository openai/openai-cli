package custom

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAdminSetupOriginDisplayPreservesDestination(t *testing.T) {
	for _, test := range []struct{ endpoint, display string }{
		{"https://api.openai.com/v1", "https://api.openai.com"},
		{"https://gateway.example.test:8443/v1", "https://gateway.example.test:8443"},
		{"http://[::1]:8080/v1", "http://[::1]:8080"},
		{"https://\u0430pi.example.test/v1", `https://\u0430pi.example.test`},
		{"https://caf\u00e9.example.test/v1", `https://caf\u00e9.example.test`},
		{"https://prefix\u202e.example.test/v1", `https://prefix\u202e.example.test`},
		{"https://prefix\u009b.example.test/v1", `https://prefix\u009b.example.test`},
	} {
		t.Run(test.display, func(t *testing.T) {
			endpoint, err := url.Parse(test.endpoint)
			require.NoError(t, err)
			requests := 0
			root := adminCredentialsTestCommand(t, func(ctx context.Context, command *cli.Command) error {
				options, display, err := adminSetupRequestOptions(command)
				if err != nil {
					return err
				}
				require.Equal(t, test.display, display)
				return verifyAdminSetup(ctx, options, []byte("synthetic-entered-admin-key"))
			})
			root.Metadata = map[string]any{mtlsHTTPClientMetadata: &http.Client{
				Transport: adminSetupOriginTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					require.Equal(t, endpoint.Scheme, request.URL.Scheme)
					require.Equal(t, endpoint.Host, request.URL.Host)
					require.Equal(t, "/v1/organization/projects?limit=1", request.URL.RequestURI())
					require.Equal(t, "Bearer synthetic-entered-admin-key", request.Header.Get("Authorization"))
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"object":"list","data":[],"has_more":false}`)), Request: request}, nil
				}),
			}}
			require.NoError(t, root.Run(t.Context(), []string{root.Name, "--base-url", test.endpoint, "admin:organization:projects", "list"}))
			require.Equal(t, 1, requests)
		})
	}
}

type adminSetupOriginTransport func(*http.Request) (*http.Response, error)

func (transport adminSetupOriginTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}
