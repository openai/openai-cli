package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestServiceAccountCreationRequestBody(t *testing.T) {
	for _, test := range []struct {
		name     string
		flags    []string
		wantBody string
	}{
		{
			name:     "service account only omits expiry",
			flags:    []string{"--create-service-account-only=true"},
			wantBody: `{"name":"synthetic","create_service_account_only":true}`,
		},
		{
			name:     "initial key with expiry",
			flags:    []string{"--create-service-account-only=false", "--expires-in-seconds", "86400"},
			wantBody: `{"name":"synthetic","create_service_account_only":false,"expires_in_seconds":86400}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			received := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				received <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"svc_synthetic","object":"organization.project.service_account","name":"synthetic"}`)
			}))
			t.Cleanup(server.Close)

			create := adminOrganizationProjectsServiceAccountsCreate
			command := &cli.Command{
				Name: "openai",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "base-url"},
					&cli.StringFlag{Name: "api-key"},
					&cli.StringFlag{Name: "admin-api-key"},
					&cli.StringFlag{Name: "format", Value: "json"},
					&cli.StringFlag{Name: "transform"},
					&cli.BoolFlag{Name: "raw-output"},
				},
				Commands: []*cli.Command{{
					Name:     "admin:organization:projects:service-accounts",
					Commands: []*cli.Command{&create},
				}},
			}
			args := []string{
				"openai",
				"--base-url", server.URL + "/",
				"--api-key", "synthetic-key",
				"--admin-api-key", "synthetic-admin-key",
				"admin:organization:projects:service-accounts", "create",
				"--project-id", "proj_synthetic",
				"--name", "synthetic",
			}
			require.NoError(t, command.Run(t.Context(), append(args, test.flags...)))

			select {
			case body := <-received:
				require.JSONEq(t, test.wantBody, string(body))
			default:
				t.Fatal("CLI did not send a request")
			}
		})
	}
}
