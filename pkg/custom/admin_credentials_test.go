package custom

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	localcommands "github.com/openai/openai-cli/internal/commands"
	requestconfig "github.com/openai/openai-cli/internal/request"
	mtlstest "github.com/openai/openai-cli/internal/testutil/mtls"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func adminCredentialsTestCommand(t *testing.T, action cli.ActionFunc) *cli.Command {
	t.Helper()
	profileCommandTestEnvironment(t)
	for _, name := range []string{
		"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_CUSTOM_HEADERS", "OPENAI_BASE_URL",
		requestconfig.MTLSClientCertFileEnv, requestconfig.MTLSClientKeyFileEnv,
	} {
		t.Setenv(name, "")
	}
	return &cli.Command{
		Name: "openai-test", Writer: io.Discard, ErrWriter: io.Discard,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "api-key", Sources: cli.EnvVars("OPENAI_API_KEY")},
			&cli.StringFlag{Name: "admin-api-key", Sources: cli.EnvVars("OPENAI_ADMIN_KEY")},
			&cli.StringFlag{Name: "base-url"},
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "format-error", Value: "auto"},
			NewRequestHeaderFlag(),
		},
		Commands: []*cli.Command{{
			Name: "admin:organization:projects", Category: "API RESOURCE",
			Commands: []*cli.Command{{Name: "list", Action: func(ctx context.Context, command *cli.Command) error {
				// Generated actions validate request input before checking credentials.
				if _, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, EmptyBody, false); err != nil {
					return err
				}
				return action(ctx, command)
			}}},
		}},
	}
}

func TestAdminCredentialsCanonicalRequirementsSurviveRoutes(t *testing.T) {
	for _, route := range []string{"nested", "compatibility", "renamed alias"} {
		t.Run(route, func(t *testing.T) {
			calls := 0
			root := adminCredentialsTestCommand(t, func(context.Context, *cli.Command) error {
				calls++
				return nil
			})
			ConfigureCommand(root)
			ConfigureCommand(root)
			path := []string{"admin", "organization", "projects", "list"}
			switch route {
			case "compatibility":
				path = []string{"admin:organization:projects", "list"}
			case "renamed alias":
				alias := localcommands.CloneResourceCommand(root.Command("admin:organization:projects"))
				alias.Name, alias.Hidden = "workspaces", false
				alias.Aliases = []string{"workspace-alias"}
				root.Commands = append(root.Commands, alias)
				path = []string{"workspace-alias", "list"}
			}
			err := root.Run(t.Context(), append([]string{root.Name}, path...))
			var missing *adminCredentialsError
			require.ErrorAs(t, err, &missing)
			require.Zero(t, calls)
			var contextual *commandError
			require.ErrorAs(t, err, &contextual)
			require.Equal(t, "list", contextual.command.Name)
		})
	}
}

func TestAdminCredentialsRegistrationPreservesOtherActions(t *testing.T) {
	for _, tc := range []struct {
		name, resource, category string
		adminKey                 bool
	}{
		{name: "admin with key", resource: "admin:organization:projects", category: "API RESOURCE", adminKey: true},
		{name: "ordinary API", resource: "models", category: "API RESOURCE"},
		{name: "handwritten admin name", resource: "admin:local", category: "LOCAL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			expected := errors.New("synthetic action failure")
			root := adminCredentialsTestCommand(t, func(context.Context, *cli.Command) error {
				calls++
				return expected
			})
			root.Commands[0].Name = tc.resource
			root.Commands[0].Category = tc.category
			ConfigureCommand(root)
			ConfigureCommand(root)
			args := []string{root.Name, tc.resource, "list"}
			if tc.adminKey {
				args = append(args, "--admin-api-key", "synthetic-admin-key")
			}
			require.ErrorIs(t, root.Run(t.Context(), args), expected)
			require.Equal(t, 1, calls)
		})
	}
}

func TestAdminCredentialsStandardServiceAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name        string
		env         map[string]string
		args        []string
		mtlsClient  *http.Client
		wantAllowed bool
	}{
		{name: "missing"},
		{name: "ordinary flag only", args: []string{"--api-key", "synthetic-project-key"}},
		{name: "ordinary environment only", env: map[string]string{"OPENAI_API_KEY": "synthetic-project-key"}},
		{name: "admin flag", args: []string{"--admin-api-key", "synthetic-admin-key"}, wantAllowed: true},
		{name: "admin environment", env: map[string]string{"OPENAI_ADMIN_KEY": "synthetic-admin-key"}, wantAllowed: true},
		{name: "empty admin overrides environment", env: map[string]string{"OPENAI_ADMIN_KEY": "synthetic-admin-key"}, args: []string{"--admin-api-key", ""}},
		{name: "explicit authorization", args: []string{"-H", "authorization: Basic synthetic-token"}, wantAllowed: true},
		{name: "empty authorization overrides key", env: map[string]string{"OPENAI_ADMIN_KEY": "synthetic-admin-key"}, args: []string{"-H", "Authorization:"}},
		{name: "last flag authorization empty", args: []string{"-H", "Authorization: Bearer synthetic-token", "-H", "authorization:"}},
		{name: "environment authorization", env: map[string]string{"OPENAI_CUSTOM_HEADERS": " Authorization : Basic synthetic-token "}, wantAllowed: true},
		{name: "last environment authorization empty", env: map[string]string{"OPENAI_CUSTOM_HEADERS": "Authorization: Bearer synthetic-token\nauthorization:"}},
		{name: "last environment authorization present", env: map[string]string{"OPENAI_CUSTOM_HEADERS": "Authorization:\nauthorization: Basic synthetic-token"}, wantAllowed: true},
		{name: "environment header survives ordinary key", env: map[string]string{"OPENAI_CUSTOM_HEADERS": "Authorization: Basic synthetic-token"}, args: []string{"--api-key", "synthetic-project-key"}, wantAllowed: true},
		{name: "environment header survives empty admin", env: map[string]string{"OPENAI_CUSTOM_HEADERS": "Authorization: Basic synthetic-token", "OPENAI_ADMIN_KEY": "synthetic-admin-key"}, args: []string{"--admin-api-key", ""}, wantAllowed: true},
		{name: "flag header replaces environment", env: map[string]string{"OPENAI_CUSTOM_HEADERS": "Authorization: Basic synthetic-token"}, args: []string{"-H", "Authorization:"}},
		{name: "mtls client still needs admin authorization", mtlsClient: &http.Client{}},
		{name: "mtls client with admin key", mtlsClient: &http.Client{}, env: map[string]string{"OPENAI_ADMIN_KEY": "synthetic-admin-key"}, wantAllowed: true},
		{name: "mtls client with explicit authorization", mtlsClient: &http.Client{}, args: []string{"-H", "Authorization: Custom synthetic-token"}, wantAllowed: true},
		{name: "mtls client with empty authorization overrides key", mtlsClient: &http.Client{}, env: map[string]string{"OPENAI_ADMIN_KEY": "synthetic-admin-key"}, args: []string{"-H", "Authorization:"}},
		{name: "typed nil mtls client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			root := adminCredentialsTestCommand(t, func(context.Context, *cli.Command) error {
				calls++
				return nil
			})
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			ConfigureCommand(root)
			operation := root.Command("admin").Command("organization").Command("projects").Command("list")
			operation.Before = func(ctx context.Context, command *cli.Command) (context.Context, error) {
				command.Root().Metadata[requestconfig.MTLSHTTPClientMetadata] = tc.mtlsClient
				return ctx, nil
			}
			args := append([]string{root.Name, "admin", "organization", "projects", "list"}, tc.args...)
			err := root.Run(t.Context(), args)
			if tc.wantAllowed {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
			} else {
				var missing *adminCredentialsError
				require.ErrorAs(t, err, &missing)
				require.Zero(t, calls)
			}
		})
	}
}

func TestAdminCredentialsEndpointOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, envURL string
		args         []string
		wantAllowed  bool
	}{
		{name: "default"},
		{name: "standard flag", args: []string{"--base-url", "https://api.openai.com/v1"}},
		{name: "standard case and port", args: []string{"--base-url", "https://API.OpenAI.com:443/v1"}},
		{name: "standard environment", envURL: "https://api.openai.com/v1"},
		{name: "standard mtls flag", args: []string{"--base-url", "https://mtls.api.openai.com/v1"}},
		{name: "standard mtls case and port", args: []string{"--base-url", "https://MTLS.API.OpenAI.com:443/v1"}},
		{name: "standard mtls environment", envURL: "https://mtls.api.openai.com/v1"},
		{name: "custom flag", args: []string{"--base-url", "https://gateway.example.test/v1"}, wantAllowed: true},
		{name: "custom environment", envURL: "https://gateway.example.test/v1", wantAllowed: true},
		{name: "standard flag overrides custom environment", envURL: "https://gateway.example.test/v1", args: []string{"--base-url", "https://api.openai.com/v1"}},
		{name: "custom flag overrides standard environment", envURL: "https://api.openai.com/v1", args: []string{"--base-url", "https://gateway.example.test/v1"}, wantAllowed: true},
		{name: "empty flag retains custom environment", envURL: "https://gateway.example.test/v1", args: []string{"--base-url", ""}, wantAllowed: true},
		{name: "custom HTTP endpoint", args: []string{"--base-url", "http://api.openai.com/v1"}, wantAllowed: true},
		{name: "custom port", args: []string{"--base-url", "https://api.openai.com:8443/v1"}, wantAllowed: true},
		{name: "custom mtls port", args: []string{"--base-url", "https://mtls.api.openai.com:8443/v1"}, wantAllowed: true},
		{name: "custom mtls suffix", args: []string{"--base-url", "https://mtls.api.openai.com.example.test/v1"}, wantAllowed: true},
		{name: "URL basic authentication", args: []string{"--base-url", "https://synthetic-user:synthetic-password@api.openai.com/v1"}, wantAllowed: true},
		{name: "mtls URL basic authentication", args: []string{"--base-url", "https://synthetic-user:synthetic-password@mtls.api.openai.com/v1"}, wantAllowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			root := adminCredentialsTestCommand(t, func(context.Context, *cli.Command) error {
				calls++
				return nil
			})
			t.Setenv("OPENAI_BASE_URL", tc.envURL)
			ConfigureCommand(root)
			args := append([]string{root.Name, "admin", "organization", "projects", "list"}, tc.args...)
			err := root.Run(t.Context(), args)
			if tc.wantAllowed {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
			} else {
				var missing *adminCredentialsError
				require.ErrorAs(t, err, &missing)
				require.Zero(t, calls)
			}
		})
	}
}

func TestAdminCredentialsProfileEndpointsMatchRequests(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint string
		absentEndpoint bool
		env            map[string]string
		args           []string
		wantURL        string
		wantAuth       string
	}{
		{name: "saved gateway", endpoint: "https://gateway.example.test/v1", wantURL: "https://gateway.example.test/v1/organization/projects"},
		{name: "standard profile", endpoint: productionProfileURL},
		{name: "standard mtls profile", endpoint: "https://MTLS.API.OpenAI.com:443/v1"},
		{name: "empty profile endpoint"},
		{name: "absent profile endpoint", absentEndpoint: true},
		{name: "empty flag retains profile", endpoint: "https://gateway.example.test/v1", args: []string{"--base-url="}, wantURL: "https://gateway.example.test/v1/organization/projects"},
		{name: "environment replaces profile", endpoint: "https://gateway.example.test/v1", env: map[string]string{"OPENAI_BASE_URL": productionProfileURL}},
		{name: "environment gateway", endpoint: productionProfileURL, env: map[string]string{"OPENAI_BASE_URL": "https://environment.example.test/v1/"}, wantURL: "https://environment.example.test/v1/organization/projects"},
		{name: "empty environment retains credential guard", endpoint: "https://gateway.example.test/v1", env: map[string]string{"OPENAI_BASE_URL": ""}},
		{name: "flag replaces environment and profile", endpoint: "https://gateway.example.test/v1", env: map[string]string{"OPENAI_BASE_URL": "https://environment.example.test/v1/"}, args: []string{"--base-url=" + productionProfileURL}},
		{name: "flag gateway", endpoint: productionProfileURL, args: []string{"--base-url=https://flag.example.test/v1/"}, wantURL: "https://flag.example.test/v1/organization/projects"},
		{name: "disabled profile", endpoint: "https://gateway.example.test/v1", args: []string{"--profile="}},
		{name: "disabled environment selector", endpoint: "https://gateway.example.test/v1", env: map[string]string{"OPENAI_PROFILE": ""}},
		{name: "explicit selector", endpoint: "https://gateway.example.test/v1", args: []string{"--profile=work"}, wantURL: "https://gateway.example.test/v1/organization/projects"},
		{name: "standard profile admin key", endpoint: productionProfileURL, env: map[string]string{"OPENAI_ADMIN_KEY": "synthetic-admin-key"}, wantURL: "https://api.openai.com/v1/organization/projects", wantAuth: "Bearer synthetic-admin-key"},
		{name: "standard profile literal header", endpoint: productionProfileURL, args: []string{"-H", "Authorization: Basic synthetic-header"}, wantURL: "https://api.openai.com/v1/organization/projects", wantAuth: "Basic synthetic-header"},
		{name: "empty literal header overrides admin key", endpoint: productionProfileURL, env: map[string]string{"OPENAI_ADMIN_KEY": "synthetic-admin-key"}, args: []string{"-H", "Authorization:"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			root := adminCredentialsTestCommand(t, func(ctx context.Context, command *cli.Command) error {
				methodOptions, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, EmptyBody, false)
				if err != nil {
					return err
				}
				options := append(GetDefaultRequestOptions(command), option.WithHTTPClient(profileContextTransport(func(request *http.Request) {
					calls++
					require.Equal(t, tc.wantURL, request.URL.String())
					require.Equal(t, tc.wantAuth, request.Header.Get("Authorization"))
				})))
				client := openai.NewClient(options...)
				_, err = client.Admin.Organization.Projects.List(ctx, openai.AdminOrganizationProjectListParams{}, methodOptions...)
				return err
			})
			require.NoError(t, os.Unsetenv("OPENAI_BASE_URL"))
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			path, err := profileStorePath()
			require.NoError(t, err)
			_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
				values := profileValues{}
				if !tc.absentEndpoint {
					values.BaseURL = &tc.endpoint
				}
				registry.Profiles["work"] = values
				name := "work"
				registry.DefaultProfile = &name
				return nil
			})
			require.NoError(t, err)
			ConfigureCommand(root)
			args := append([]string{root.Name, "admin", "organization", "projects", "list"}, tc.args...)
			err = root.Run(t.Context(), args)
			if tc.wantURL != "" {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
			} else {
				var missing *adminCredentialsError
				require.ErrorAs(t, err, &missing)
				require.Zero(t, calls)
			}
		})
	}
}

func TestAdminCredentialsProfileResolutionFailures(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprintf("malformed=%t", malformed), func(t *testing.T) {
			root := adminCredentialsTestCommand(t, func(context.Context, *cli.Command) error {
				t.Fatal("profile resolution failure must stop the action")
				return nil
			})
			t.Setenv("OPENAI_PROFILE", "missing")
			if malformed {
				path, err := profileStorePath()
				require.NoError(t, err)
				writeProfileRegistryTestFile(t, path, `{"synthetic-sensitive-field":"synthetic-sensitive-value"}`)
			}
			ConfigureCommand(root)
			err := root.Run(t.Context(), []string{root.Name, "--base-url=https://gateway.example.test/v1/", "admin", "organization", "projects", "list"})
			var local *localcommands.LocalError
			require.ErrorAs(t, err, &local)
			require.NotContains(t, err.Error(), "synthetic-sensitive")
			var missing *adminCredentialsError
			require.False(t, errors.As(err, &missing))
		})
	}
}

func TestAdminCredentialsProfileEndpointSnapshot(t *testing.T) {
	endpoint := "https://gateway.example.test/v1"
	var path string
	requests := 0
	root := adminCredentialsTestCommand(t, func(ctx context.Context, command *cli.Command) error {
		_, err := updateProfiles(ctx, path, func(registry *profileRegistry) error {
			standard := productionProfileURL
			registry.Profiles["work"] = profileValues{BaseURL: &standard}
			return nil
		})
		if err != nil {
			return err
		}
		options := append(GetDefaultRequestOptions(command), option.WithHTTPClient(profileContextTransport(func(request *http.Request) {
			requests++
			require.Equal(t, endpoint+"/organization/projects", request.URL.String())
		})))
		client := openai.NewClient(options...)
		_, err = client.Admin.Organization.Projects.List(ctx, openai.AdminOrganizationProjectListParams{})
		return err
	})
	require.NoError(t, os.Unsetenv("OPENAI_BASE_URL"))
	var err error
	path, err = profileStorePath()
	require.NoError(t, err)
	_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
		registry.Profiles["work"] = profileValues{BaseURL: &endpoint}
		name := "work"
		registry.DefaultProfile = &name
		return nil
	})
	require.NoError(t, err)
	ConfigureCommand(root)
	args := []string{root.Name, "admin", "organization", "projects", "list"}
	require.NoError(t, root.Run(t.Context(), args))
	require.Equal(t, 1, requests)
	var missing *adminCredentialsError
	require.ErrorAs(t, root.Run(t.Context(), args), &missing)
	require.Equal(t, 1, requests)
}

func TestAdminProfileEndpointDoesNotAuthorizeMTLS(t *testing.T) {
	for _, route := range [][]string{{"admin", "organization", "projects", "list"}, {"setup", "admin"}} {
		t.Run(route[0], func(t *testing.T) {
			root := adminCredentialsTestCommand(t, func(context.Context, *cli.Command) error {
				t.Fatal("mTLS must reject a profile-only endpoint before the action")
				return nil
			})
			require.NoError(t, os.Unsetenv("OPENAI_BASE_URL"))
			path, err := profileStorePath()
			require.NoError(t, err)
			_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
				endpoint, name := "https://mtls.api.openai.com/v1", "work"
				registry.Profiles[name] = profileValues{BaseURL: &endpoint}
				registry.DefaultProfile = &name
				return nil
			})
			require.NoError(t, err)
			ConfigureCommand(root)
			args := []string{root.Name, "--mtls-client-cert-file=synthetic-cert.pem", "--mtls-client-key-file=synthetic-key.pem"}
			err = root.Run(t.Context(), append(args, route...))
			require.ErrorContains(t, err, "mTLS requires an explicit HTTPS base URL")
		})
	}
}

func TestAdminCredentialsTypedErrorPresentation(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			root := adminCredentialsTestCommand(t, func(context.Context, *cli.Command) error {
				t.Fatal("missing credentials must stop the action")
				return nil
			})
			ConfigureCommand(root)
			err := root.Run(t.Context(), []string{root.Name, "--format-error", format, "admin", "organization", "projects", "list"})
			var missing *adminCredentialsError
			require.ErrorAs(t, err, &missing)
			var output bytes.Buffer
			require.NoError(t, ShowCommandError(root, fmt.Errorf("synthetic wrapper: %w", err), &output))
			if format == "text" {
				require.Equal(t, missing.Error()+"\n", output.String())
				return
			}
			var payload map[string]string
			require.NoError(t, json.Unmarshal(output.Bytes(), &payload))
			require.Equal(t, map[string]string{"message": missing.Error()}, payload)
		})
	}
}

func TestAdminCredentialsAcceptCertificateOnlyMTLS(t *testing.T) {
	root := adminCredentialsTestCommand(t, func(ctx context.Context, command *cli.Command) error {
		client := openai.NewClient(GetDefaultRequestOptions(command)...)
		_, err := client.Admin.Organization.Projects.List(ctx, openai.AdminOrganizationProjectListParams{})
		return err
	})
	pki := mtlstest.NewPKI(t)
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(pki.RootPEM))
	type requestDetails struct {
		authorization string
		chainLength   int
		path          string
	}
	requests := make(chan requestDetails, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		details := requestDetails{authorization: r.Header.Get("Authorization"), path: r.URL.Path}
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
	certFile, keyFile := mtlstest.WriteClientFiles(t, pki.ClientChainPEM, pki.ClientKeyPEM)
	ConfigureCommand(root)
	require.NoError(t, root.Run(t.Context(), []string{
		root.Name, "--base-url", server.URL,
		"--mtls-client-cert-file", certFile, "--mtls-client-key-file", keyFile,
		"admin", "organization", "projects", "list",
	}))
	select {
	case request := <-requests:
		require.Empty(t, request.authorization)
		require.Equal(t, 3, request.chainLength)
		require.Equal(t, "/organization/projects", request.path)
	default:
		t.Fatal("the admin request did not reach the mTLS server")
	}
}
