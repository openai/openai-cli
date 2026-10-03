package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAudioVoicesCreateCredentialSelection(t *testing.T) {
	for _, tc := range []struct {
		name, apiKey, adminKey, customHeaders, expectedAuth string
		flags                                               []string
	}{
		{"admin key alone", "", "synthetic-admin", "", "", nil},
		{"API key", "synthetic-api", "", "", "Bearer synthetic-api", nil},
		{"both keys", "synthetic-api", "synthetic-admin", "", "Bearer synthetic-api", nil},
		{"inherited header", "", "", "Authorization: Bearer synthetic-custom", "Bearer synthetic-custom", nil},
		{"inherited header and admin", "", "synthetic-admin", "Authorization: Bearer synthetic-custom", "Bearer synthetic-custom", nil},
		{"key overrides inherited header", "synthetic-api", "", "Authorization: Bearer synthetic-custom", "Bearer synthetic-api", nil},
		{"explicit header", "synthetic-api", "synthetic-admin", "", "Bearer synthetic-header",
			[]string{"--header", "Authorization: Bearer synthetic-header"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", tc.apiKey)
			t.Setenv("OPENAI_ADMIN_KEY", tc.adminKey)
			t.Setenv("OPENAI_CUSTOM_HEADERS", tc.customHeaders)
			t.Setenv("OPENAI_BASE_URL", "")
			received := make(chan string, 1)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"voice_synthetic","name":"Synthetic","type":"prompt"}`)
			}))
			defer server.Close()
			originalTransport := http.DefaultTransport
			http.DefaultTransport = server.Client().Transport
			defer func() { http.DefaultTransport = originalTransport }()

			create := runtimeTestCommand("audio:voices", "create")
			command := &cli.Command{
				Name: "openai",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "debug"},
					&cli.StringFlag{Name: "base-url"},
					// Match the real root's flag type and env sources: these affect precedence.
					&requestflag.Flag[string]{Name: "api-key", Sources: cli.EnvVars("OPENAI_API_KEY")},
					&requestflag.Flag[string]{Name: "admin-api-key", Sources: cli.EnvVars("OPENAI_ADMIN_KEY")},
					NewRequestHeaderFlag(),
					&cli.StringFlag{Name: "format", Value: "json"},
					&cli.StringFlag{Name: "transform"},
					&cli.BoolFlag{Name: "raw-output"},
				},
				Commands: []*cli.Command{{Name: "audio:voices", Commands: []*cli.Command{&create}}},
			}
			args := append([]string{"openai", "--base-url", server.URL + "/"}, tc.flags...)
			args = append(args, "audio:voices", "create", "--type", "prompt",
				"--name", "Synthetic", "--prompt", "A calm narrator")
			require.NoError(t, command.Run(t.Context(), args))
			select {
			case authorization := <-received:
				require.Equal(t, tc.expectedAuth, authorization)
			default:
				t.Fatal("voice creation sent no request")
			}
		})
	}
}
