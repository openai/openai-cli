package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/commands/admin"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/openai/openai-go/v3/option"
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
				options, display, err := admin.TestRequestOptions(command, custom.GetDefaultRequestOptions)
				if err != nil {
					return err
				}
				require.Equal(t, test.display, display)
				return admin.TestVerifySetup(ctx, options, []byte("synthetic-entered-admin-key"))
			})
			root.Metadata = map[string]any{requestconfig.MTLSHTTPClientMetadata: &http.Client{
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

func TestAdminSetupProfileEndpointOptions(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, wantURL, wantDisplay string
		absentEndpoint                       bool
		env                                  map[string]string
		args                                 []string
	}{
		{name: "saved destination", endpoint: "https://gateway.example.test/custom/v1", wantURL: "https://gateway.example.test/custom/v1/organization/projects?limit=1", wantDisplay: "https://gateway.example.test"},
		{name: "empty saved endpoint", wantURL: "https://api.openai.com/v1/organization/projects?limit=1", wantDisplay: "https://api.openai.com"},
		{name: "absent saved endpoint", absentEndpoint: true, wantURL: "https://api.openai.com/v1/organization/projects?limit=1", wantDisplay: "https://api.openai.com"},
		{name: "empty flag retains profile", endpoint: "https://gateway.example.test/v1", args: []string{"--base-url="}, wantURL: "https://gateway.example.test/v1/organization/projects?limit=1", wantDisplay: "https://gateway.example.test"},
		{name: "environment overrides profile", endpoint: "https://gateway.example.test/v1", env: map[string]string{"OPENAI_BASE_URL": "https://environment.example.test/v1/"}, wantURL: "https://environment.example.test/v1/organization/projects?limit=1", wantDisplay: "https://environment.example.test"},
		{name: "flag overrides environment", endpoint: "https://gateway.example.test/v1", env: map[string]string{"OPENAI_BASE_URL": "https://environment.example.test/v1/"}, args: []string{"--base-url=https://flag.example.test/v1/"}, wantURL: "https://flag.example.test/v1/organization/projects?limit=1", wantDisplay: "https://flag.example.test"},
		{name: "environment selector", endpoint: "https://gateway.example.test/v1", env: map[string]string{"OPENAI_PROFILE": "work"}, wantURL: "https://gateway.example.test/v1/organization/projects?limit=1", wantDisplay: "https://gateway.example.test"},
		{name: "disabled default", endpoint: "https://gateway.example.test/v1", args: []string{"--profile="}, wantURL: "https://api.openai.com/v1/organization/projects?limit=1", wantDisplay: "https://api.openai.com"},
		{name: "escaped saved origin", endpoint: "https://prefix\u202e.example.test/v1", wantURL: "https://prefix\u202e.example.test/v1/organization/projects?limit=1", wantDisplay: `https://prefix\u202e.example.test`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateAdminProfileEnvironment(t)
			expected, err := url.Parse(tc.wantURL)
			require.NoError(t, err)
			requests := 0
			root := adminCredentialsTestCommand(t, nil)
			bridge := func(ctx context.Context, command *cli.Command) error {
				options, display, err := admin.TestRequestOptions(command, custom.GetDefaultRequestOptions)
				if err != nil {
					return err
				}
				require.Equal(t, tc.wantDisplay, display)
				return admin.TestVerifySetup(ctx, options, []byte("synthetic-entered-key"))
			}
			require.NoError(t, os.Unsetenv("OPENAI_BASE_URL"))
			t.Setenv("OPENAI_ADMIN_KEY", "synthetic-environment-key")
			t.Setenv("OPENAI_CUSTOM_HEADERS", "Authorization: Bearer synthetic-environment-header\nOpenAI-Project: proj_inherited\nX-Api-Key: synthetic-gateway-key")
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			values := map[string]string{}
			if !tc.absentEndpoint {
				values["base_url"] = tc.endpoint
			}
			writeAdminProfileFixture(t, values)
			root.Metadata = map[string]any{requestconfig.MTLSHTTPClientMetadata: &http.Client{
				Transport: adminSetupOriginTransport(func(request *http.Request) (*http.Response, error) {
					requests++
					require.Equal(t, expected.Scheme, request.URL.Scheme)
					require.Equal(t, expected.Host, request.URL.Host)
					require.Equal(t, expected.RequestURI(), request.URL.RequestURI())
					require.Equal(t, "Bearer synthetic-entered-key", request.Header.Get("Authorization"))
					require.Equal(t, "proj_literal", request.Header.Get("OpenAI-Project"))
					require.Equal(t, "synthetic-gateway-key", request.Header.Get("X-Api-Key"))
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"object":"list","data":[],"has_more":false}`)), Request: request}, nil
				}),
			}}
			bridgeCalls := configureAdminSetupProfileProbe(t, root, bridge, root.Metadata[requestconfig.MTLSHTTPClientMetadata].(*http.Client))
			args := []string{root.Name, "--admin-api-key=synthetic-flag-key", "-H", "Authorization: Bearer synthetic-literal-key", "-H", "OpenAI-Project: proj_literal"}
			args = append(args, tc.args...)
			require.NoError(t, root.Run(t.Context(), append(args, "setup", "admin")))
			require.Equal(t, 1, *bridgeCalls)
			require.Equal(t, 1, requests)
		})
	}
}

func TestAdminSetupProfileEndpointRejectsUnsafeOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, profileURL, environmentURL string
		useEnvironment                   bool
	}{
		{name: "profile HTTP", profileURL: "http://gateway.example.test/v1"},
		{name: "empty environment", profileURL: "https://gateway.example.test/v1", useEnvironment: true},
		{name: "environment HTTP", environmentURL: "http://gateway.example.test/v1", useEnvironment: true},
		{name: "embedded credential", environmentURL: "https://synthetic-user:synthetic-secret@gateway.example.test/v1", useEnvironment: true},
		{name: "query", environmentURL: "https://gateway.example.test/v1?key=synthetic-secret", useEnvironment: true},
		{name: "fragment", environmentURL: "https://gateway.example.test/v1#synthetic-secret", useEnvironment: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateAdminProfileEnvironment(t)
			root := adminCredentialsTestCommand(t, nil)
			helperCalls := 0
			probeOptions := func(command *cli.Command) ([]option.RequestOption, string, error) {
				helperCalls++
				return admin.TestRequestOptions(command, custom.GetDefaultRequestOptions)
			}
			bridge := func(_ context.Context, command *cli.Command) error {
				options, display, err := probeOptions(command)
				require.Nil(t, options)
				require.Empty(t, display)
				return err
			}
			require.NoError(t, os.Unsetenv("OPENAI_BASE_URL"))
			if tc.useEnvironment {
				t.Setenv("OPENAI_BASE_URL", tc.environmentURL)
			}
			writeAdminProfileFixture(t, map[string]string{"base_url": tc.profileURL})
			bridgeCalls := configureAdminSetupProfileProbe(t, root, bridge, nil)
			root.ExitErrHandler = func(context.Context, *cli.Command, error) {}
			publicError := root.Run(t.Context(), []string{root.Name, "setup", "admin"})
			require.Error(t, publicError)
			require.NotContains(t, publicError.Error(), "synthetic-secret")
			require.NotContains(t, publicError.Error(), "gateway.example.test")
			wantBridgeCalls := 1
			if tc.name == "environment HTTP" {
				wantBridgeCalls = 0
			}
			require.Equal(t, wantBridgeCalls, *bridgeCalls)
			require.Equal(t, wantBridgeCalls, helperCalls)
			options, display, err := probeOptions(root)
			require.Equal(t, wantBridgeCalls+1, helperCalls)
			require.Nil(t, options)
			require.Empty(t, display)
			var setup *admin.SetupError
			require.ErrorAs(t, err, &setup)
			require.Equal(t, 3, setup.ExitCode())
			require.NotContains(t, err.Error(), "synthetic-secret")
			require.NotContains(t, err.Error(), "gateway.example.test")
		})
	}
}

func TestAdminSetupProfileResolutionFailures(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "missing"
		if malformed {
			name = "malformed"
		}
		t.Run(name, func(t *testing.T) {
			isolateAdminProfileEnvironment(t)
			root := adminCredentialsTestCommand(t, nil)
			helperCalls := 0
			probeOptions := func(command *cli.Command) ([]option.RequestOption, string, error) {
				helperCalls++
				return admin.TestRequestOptions(command, custom.GetDefaultRequestOptions)
			}
			bridge := func(_ context.Context, command *cli.Command) error {
				options, display, err := probeOptions(command)
				require.Nil(t, options)
				require.Empty(t, display)
				return err
			}
			t.Setenv("OPENAI_PROFILE", "missing")
			if malformed {
				writeAdminProfileRegistry(t, []byte(`{"synthetic-sensitive-field":"synthetic-sensitive-value"}`))
			}
			bridgeCalls := configureAdminSetupProfileProbe(t, root, bridge, nil)
			err := root.Run(t.Context(), []string{root.Name, "--base-url=https://gateway.example.test/v1/", "setup", "admin"})
			require.Equal(t, 1, *bridgeCalls)
			require.Equal(t, 1, helperCalls)
			var local *localcommands.LocalError
			require.ErrorAs(t, err, &local)
			require.NotContains(t, err.Error(), "synthetic-sensitive")
			options, display, helperError := probeOptions(root)
			require.Equal(t, 2, helperCalls)
			require.Nil(t, options)
			require.Empty(t, display)
			require.ErrorAs(t, helperError, &local)
			require.NotContains(t, helperError.Error(), "synthetic-sensitive")
		})
	}
}

func TestAdminSetupProfileEndpointLoopback(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusFound, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			isolateAdminProfileEnvironment(t)
			var requests atomic.Int32
			type requestDetails struct{ uri, authorization, organization, project string }
			observed := make(chan requestDetails, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				select {
				case observed <- requestDetails{request.URL.RequestURI(), request.Header.Get("Authorization"), request.Header.Get("OpenAI-Organization"), request.Header.Get("OpenAI-Project")}:
				default:
				}
				w.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/redirected" {
					_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
					return
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
				} else {
					_, _ = io.WriteString(w, `{"error":{"message":"synthetic-entered-key"}}`)
				}
			}))
			t.Cleanup(server.Close)
			root := adminCredentialsTestCommand(t, nil)
			bridge := func(ctx context.Context, command *cli.Command) error {
				options, display, err := admin.TestRequestOptions(command, custom.GetDefaultRequestOptions)
				if err != nil {
					return err
				}
				require.Equal(t, server.URL, display)
				return admin.TestVerifySetup(ctx, options, []byte("synthetic-entered-key"))
			}
			require.NoError(t, os.Unsetenv("OPENAI_BASE_URL"))
			t.Setenv("OPENAI_ADMIN_KEY", "synthetic-environment-key")
			t.Setenv("OPENAI_CUSTOM_HEADERS", "Authorization: Bearer synthetic-environment-header\nOpenAI-Organization: org_inherited\nOpenAI-Project: proj_inherited")
			writeAdminProfileFixture(t, map[string]string{
				"base_url": server.URL + "/saved/v1", "organization": "org_profile", "project": "proj_profile",
			})
			client := server.Client()
			redirects := 0
			client.CheckRedirect = func(*http.Request, []*http.Request) error { redirects++; return nil }
			root.Metadata = map[string]any{requestconfig.MTLSHTTPClientMetadata: client}
			bridgeCalls := configureAdminSetupProfileProbe(t, root, bridge, root.Metadata[requestconfig.MTLSHTTPClientMetadata].(*http.Client))
			root.ExitErrHandler = func(context.Context, *cli.Command, error) {}
			err := root.Run(t.Context(), []string{root.Name, "setup", "admin"})
			require.Equal(t, 1, *bridgeCalls)
			if status == http.StatusOK {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "synthetic-entered-key")
				if status == http.StatusFound {
					require.ErrorContains(t, err, "stopped at a redirect")
				} else {
					require.ErrorContains(t, err, "denied admin access (403)")
				}
			}
			require.EqualValues(t, 1, requests.Load())
			select {
			case request := <-observed:
				require.Equal(t, requestDetails{"/saved/v1/organization/projects?limit=1", "Bearer synthetic-entered-key", "org_profile", "proj_profile"}, request)
			default:
				t.Fatal("verification did not reach the profile endpoint")
			}
			require.Zero(t, redirects)
			require.NoError(t, client.CheckRedirect(nil, nil))
			require.Equal(t, 1, redirects)
		})
	}
}

func TestAdminSetupRegisteredEndpointPolicy(t *testing.T) {
	for _, name := range []string{"empty endpoint", "callback error", "nil callback", "unrelated metadata"} {
		t.Run(name, func(t *testing.T) {
			root := adminCredentialsTestCommand(t, nil)
			t.Setenv("OPENAI_BASE_URL", "https://environment.example.test/v1")
			marker := &struct{ value string }{"synthetic-unrelated-metadata"}
			requests := 0
			client := &http.Client{Transport: adminSetupOriginTransport(func(request *http.Request) (*http.Response, error) {
				requests++
				require.Equal(t, "https://policy.example.test/v1/organization/projects?limit=1", request.URL.String())
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(`{"object":"list","data":[],"has_more":false}`)), Request: request}, nil
			})}
			root.Metadata = map[string]any{
				"synthetic-unrelated": marker, requestconfig.MTLSHTTPClientMetadata: client,
				admin.CredentialsRequiredMetadata: true,
			}
			callbackError := errors.New("synthetic safe endpoint resolution failure")
			resolverCalls, defaultsCalls := 0, 0
			var resolve func(*cli.Command) (string, error)
			if name != "nil callback" {
				resolve = func(*cli.Command) (string, error) {
					resolverCalls++
					if name == "callback error" {
						return "", callbackError
					}
					if name == "empty endpoint" {
						return "", nil
					}
					return "https://policy.example.test/v1", nil
				}
			}
			admin.ConfigureCredentials(root, resolve)
			admin.ConfigureCredentials(root, resolve)
			require.Same(t, marker, root.Metadata["synthetic-unrelated"])
			require.Same(t, client, root.Metadata[requestconfig.MTLSHTTPClientMetadata])
			root.Action = func(ctx context.Context, command *cli.Command) error {
				credentialError := admin.CheckCredentials(command)
				options, display, setupError := admin.TestRequestOptions(command, func(*cli.Command) []option.RequestOption {
					defaultsCalls++
					return nil
				})
				switch name {
				case "empty endpoint":
					var missing *admin.CredentialsError
					require.ErrorAs(t, credentialError, &missing)
					var invalid *admin.SetupError
					require.ErrorAs(t, setupError, &invalid)
					require.Equal(t, 3, invalid.ExitCode())
				case "callback error":
					require.ErrorIs(t, credentialError, callbackError)
					require.ErrorIs(t, setupError, callbackError)
				case "nil callback":
					require.EqualError(t, credentialError, "Admin endpoint selection is unavailable.")
					require.EqualError(t, setupError, "Admin endpoint selection is unavailable.")
				default:
					require.NoError(t, credentialError)
					require.NoError(t, setupError)
					require.Equal(t, "https://policy.example.test", display)
					require.NoError(t, admin.TestVerifySetup(ctx, options, []byte("synthetic-entered-key")))
				}
				if name != "unrelated metadata" {
					require.Nil(t, options)
					require.Empty(t, display)
				}
				return nil
			}
			require.NoError(t, root.Run(t.Context(), []string{root.Name, "--base-url=https://flag.example.test/v1"}))
			if name == "nil callback" {
				require.Zero(t, resolverCalls)
			} else {
				require.Equal(t, 2, resolverCalls)
			}
			if name == "unrelated metadata" {
				require.Equal(t, 1, defaultsCalls)
				require.Equal(t, 1, requests)
			} else {
				require.Zero(t, defaultsCalls)
				require.Zero(t, requests)
			}
			require.Same(t, marker, root.Metadata["synthetic-unrelated"])
			require.Same(t, client, root.Metadata[requestconfig.MTLSHTTPClientMetadata])
		})
	}
}

// Keep production root preparation. The bridge exercises setup request options after it.
func configureAdminSetupProfileProbe(t *testing.T, root *cli.Command, bridge cli.ActionFunc, client *http.Client) *int {
	t.Helper()
	custom.ConfigureCommand(root)
	setup := root.Command("setup")
	require.NotNil(t, setup)
	command := setup.Command("admin")
	require.NotNil(t, command)
	calls := 0
	command.Action = func(ctx context.Context, command *cli.Command) error {
		calls++
		if client != nil {
			// Root preparation can clear this request-scoped client. Restore only this entry.
			command.Root().Metadata[requestconfig.MTLSHTTPClientMetadata] = client
			require.Same(t, client, command.Root().Metadata[requestconfig.MTLSHTTPClientMetadata])
		}
		return bridge(ctx, command)
	}
	return &calls
}

// Isolate the public profile schema without importing private profile helpers.
func isolateAdminProfileEnvironment(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME"} {
		t.Setenv(name, home)
	}
	for _, name := range []string{"OPENAI_PROFILE", "OPENAI_PROJECT_ID", "OPENAI_ORG_ID"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
}

func writeAdminProfileFixture(t *testing.T, values map[string]string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"version": 1, "default_profile": "work", "profiles": map[string]any{"work": values},
	})
	require.NoError(t, err)
	writeAdminProfileRegistry(t, data)
}

func writeAdminProfileRegistry(t *testing.T, data []byte) {
	t.Helper()
	directory, err := os.UserConfigDir()
	require.NoError(t, err)
	path := filepath.Join(directory, "openai", "profiles.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, data, 0600))
}

type adminSetupOriginTransport func(*http.Request) (*http.Response, error)

func (transport adminSetupOriginTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}
