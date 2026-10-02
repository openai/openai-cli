package custom

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestDefaultRequestOptionsHTTPEndpoints(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-api-key")
	t.Setenv("OPENAI_ADMIN_KEY", "")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"local-model","object":"model"}]}`)
	}))
	defer server.Close()

	for _, source := range []string{"flag", "environment"} {
		for _, endpoint := range []struct {
			name string
			url  string
		}{
			{"loopback", server.URL},
			{"remote", "http://192.0.2.1:1/v1"},
		} {
			t.Run(source+"/"+endpoint.name, func(t *testing.T) {
				t.Setenv("OPENAI_BASE_URL", "")
				command := &cli.Command{
					Name:  "openai-test",
					Flags: []cli.Flag{&cli.StringFlag{Name: "base-url"}},
					Action: func(ctx context.Context, cmd *cli.Command) error {
						client := openai.NewClient(GetDefaultRequestOptions(cmd)...)
						page, err := client.Models.List(ctx)
						if err == nil {
							require.Len(t, page.Data, 1)
							assert.Equal(t, "local-model", page.Data[0].ID)
						}
						return err
					},
				}
				args := []string{command.Name}
				if source == "flag" {
					args = append(args, "--base-url", endpoint.url)
				} else {
					t.Setenv("OPENAI_BASE_URL", endpoint.url)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				err := command.Run(ctx, args)
				if endpoint.name == "remote" {
					require.ErrorContains(t, err, "HTTPS")
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}
