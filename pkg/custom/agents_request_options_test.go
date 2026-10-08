package custom

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAgentsSourceNoRetryPreservesMutationAndReadPolicies(t *testing.T) {
	for _, test := range []struct {
		resource, method string
		args             []string
		wantRequests     int32
	}{
		{"beta:agents", "create", []string{"beta", "agents", "create"}, 1},
		{"beta:agents:sessions", "create", []string{"beta", "agents", "sessions", "create"}, 1},
		{"beta:agents:sessions:events", "create", []string{"beta", "agents", "sessions", "events", "create"}, 1},
		{"beta:agents:sessions", "create", []string{"beta:agents:sessions", "create"}, 1},
		{"beta:agents:sessions", "retrieve", []string{"beta", "agents", "sessions", "retrieve"}, 3},
		{"beta:assistants", "create", []string{"beta", "assistants", "create"}, 3},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer sk-synthetic-agents-test" || r.Header.Get("OpenAI-Project") != "proj_synthetic" {
					t.Error("request context changed")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After-Ms", "1")
				w.Header().Set("X-Request-ID", "req_synthetic_retry")
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"error":{"message":"synthetic unavailable","type":"server_error"}}`)
			}))
			defer server.Close()
			t.Setenv("OPENAI_API_KEY", "sk-synthetic-agents-test")
			t.Setenv("OPENAI_BASE_URL", server.URL+"/")
			t.Setenv("OPENAI_ORG_ID", "org_synthetic")
			t.Setenv("OPENAI_PROJECT_ID", "proj_synthetic")
			root := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard,
				ExitErrHandler: func(context.Context, *cli.Command, error) {},
				Commands: []*cli.Command{{Name: test.resource, Category: "API RESOURCE", Commands: []*cli.Command{{
					Name: test.method, Action: func(ctx context.Context, command *cli.Command) error {
						options := append([]option.RequestOption{option.WithMaxRetries(2)}, GetDefaultRequestOptions(command)...)
						client := openai.NewClient(options...)
						var result any
						if test.method == "retrieve" {
							return client.Get(ctx, "synthetic", nil, &result)
						}
						return client.Post(ctx, "synthetic", nil, &result)
					},
				}}}},
			}
			configureCommandSubgroups(root)
			err := root.Run(context.Background(), append([]string{"openai"}, test.args...))
			var apiError *openai.Error
			require.ErrorAs(t, err, &apiError)
			require.Equal(t, http.StatusServiceUnavailable, apiError.StatusCode)
			require.Equal(t, "req_synthetic_retry", apiError.Response.Header.Get("X-Request-ID"))
			require.Equal(t, test.wantRequests, requests.Load())
		})
	}
}
